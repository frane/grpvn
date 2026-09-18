package internal

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// ulidEntropy is a process-local, mutex-guarded monotonic reader backed by
// crypto/rand. ulid.Make() in oklog/ulid v2 seeds its default entropy with
// math/rand from time.Now().UnixNano(), which on Windows (where the clock
// often advances in 100ns ticks) can give two freshly-started processes the
// same seed and therefore the same ULID sequence. Reading from crypto/rand
// instead makes a cross-process collision a 2^-80 event.
var ulidEntropy = &ulid.LockedMonotonicReader{
	MonotonicReader: ulid.Monotonic(rand.Reader, 0),
}

type Message struct {
	Seq         int64 // commit-ordered sequence; 0 until saved/loaded
	ID          string
	Sender      string
	Target      string
	Body        []byte
	ChainRoot   string
	ChainDepth  int
	ParentID    *string
	Correlation *string
	CreatedAt   int64
}

func NewMessage(sender, target string, body []byte) *Message {
	now := time.Now()
	id := ulid.MustNew(ulid.Timestamp(now), ulidEntropy).String()
	return &Message{
		ID:         id,
		Sender:     sender,
		Target:     target,
		Body:       body,
		ChainRoot:  id,
		ChainDepth: 0,
		CreatedAt:  now.UnixMilli(),
	}
}

func (m *Message) Save(db *sql.DB) error {
	return m.save(db)
}

func (m *Message) save(db dbtx) error {
	res, err := db.Exec("INSERT INTO messages (id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.Sender, m.Target, m.Body, m.ChainRoot, m.ChainDepth, m.ParentID, m.Correlation, m.CreatedAt)
	if err != nil {
		return err
	}
	if seq, err := res.LastInsertId(); err == nil {
		m.Seq = seq
	}
	return nil
}

// dbtx is *sql.DB or *sql.Tx. Send uses a transaction when an idempotency
// key is set so a retry cannot insert a second row.
type dbtx interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...interface{}) *sql.Row
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

var ulidPrefixRegex = regexp.MustCompile("^[0-9A-HJKMNP-TV-Z]{6,26}$")

func ResolveTarget(db dbtx, input string, defaultChannel string) (target string, parent *Message, err error) {
	if input == "" {
		if defaultChannel == "" {
			return "", nil, errors.New("no target and no default")
		}
		return defaultChannel, nil, nil
	}
	if strings.HasPrefix(input, "#") || strings.HasPrefix(input, "@") {
		return input, nil, nil
	}
	if ulidPrefixRegex.MatchString(strings.ToUpper(input)) {
		parent, err = FindMessageByPrefix(db, input)
		switch {
		case err == nil:
			return parent.Target, parent, nil
		case errors.Is(err, sql.ErrNoRows):
			// No such message: fall through to "invalid target", which also
			// covers input that is not an ID at all.
		default:
			// An ambiguous prefix must not be reported as a bad target: the
			// caller has a real message in mind and needs to be told how to
			// name it unambiguously.
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("invalid target: %s", input)
}

// FindMessageByPrefix resolves an ID prefix to exactly one message, and
// fails when the prefix is ambiguous rather than picking a match.
//
// The leading 10 characters of a ULID are a millisecond timestamp, so the
// 6-character prefix a read prints is shared by every message minted in the
// same ~4-minute window. Prefixes are rendered unique only within the batch
// they appear in (see UniquePrefixLen), while this lookup scans the whole
// store: on a busy host the same prefix legitimately matches messages in
// several channels. Returning an arbitrary one silently retargeted a reply
// into whichever channel that message happened to live in — the reply left
// the thread it was written for, and its author only found out from the
// after-the-fact auto-follow line.
func FindMessageByPrefix(db dbtx, prefix string) (*Message, error) {
	prefix = strings.ToUpper(prefix)
	matches, err := findMessagesByPrefix(db, prefix)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, sql.ErrNoRows
	case 1:
		return matches[0], nil
	}
	return nil, ambiguousPrefixError(prefix, matches)
}

// findMessagesByPrefix returns every message whose ID starts with prefix,
// oldest first, capped so a 1-character prefix cannot load the store.
func findMessagesByPrefix(db dbtx, prefix string) ([]*Message, error) {
	rows, err := db.Query("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE id LIKE ? ORDER BY id ASC LIMIT ?", prefix+"%", maxPrefixMatches)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMessageRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// maxPrefixMatches bounds the ambiguity probe. Any count above one is an
// error, so the exact total past a handful does not matter.
const maxPrefixMatches = 10

// ErrAmbiguousPrefix marks a prefix that names more than one message.
// Callers that guess whether an argument is a target or a body must treat it
// as a target the user got wrong, never as prose — demoting it to a body is
// how an intended reply ended up posted in the default channel.
var ErrAmbiguousPrefix = errors.New("ambiguous message prefix")

// ambiguousPrefixError names the shortest prefix that separates the
// candidates, so the caller can retry without guessing a length. Every
// message printed with --full, or with the id a send acknowledged, carries
// enough characters already.
func ambiguousPrefixError(prefix string, matches []*Message) error {
	need := UniquePrefixLen(matches)
	if need <= len(prefix) {
		need = len(prefix) + 1
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q: %d messages match. Use at least %d characters", prefix, len(matches), need)
	for _, m := range matches {
		id := m.ID
		if len(id) > need {
			id = id[:need]
		}
		fmt.Fprintf(&b, "\n  %s  %s  %s", id, m.Target, m.Sender)
	}
	return fmt.Errorf("%w %s", ErrAmbiguousPrefix, b.String())
}

func findMessageByID(db dbtx, id string) (*Message, error) {
	return scanOneMessage(db.QueryRow("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE id = ?", id))
}

// rowScanner is *sql.Row or *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanOneMessage(row *sql.Row) (*Message, error) {
	return scanMessageRow(row)
}

func scanMessageRow(row rowScanner) (*Message, error) {
	var m Message
	var parentID, correlation sql.NullString
	if err := row.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &parentID, &correlation, &m.CreatedAt); err != nil {
		return nil, err
	}
	if parentID.Valid {
		m.ParentID = &parentID.String
	}
	if correlation.Valid {
		m.Correlation = &correlation.String
	}
	return &m, nil
}
