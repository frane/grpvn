package internal

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// targetsFor returns the channels this agent reads from (followed channels
// plus its own DM inbox), in the order the caller passed them.
func targetsFor(name string, follow []string) []string {
	out := append([]string{}, follow...)
	out = append(out, "@"+name)
	return out
}

// Check counts unread per followed target. Cursors are per-target seq
// positions in the cursors table, so a follow added today surfaces every
// message in that channel (absent cursor reads as 0 = everything unread).
func Check(w io.Writer, db *sql.DB, st *State) (int, error) {
	cursors, err := loadCursors(db, st.Name)
	if err != nil {
		return 0, err
	}
	counts := []string{}
	total := 0
	for _, target := range targetsFor(st.Name, st.Follow) {
		var n int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM messages WHERE target = ? AND seq > ? AND sender != ?",
			target, cursors[target], st.Name,
		).Scan(&n)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		label := target
		if target == "@"+st.Name {
			label = "@me"
		}
		counts = append(counts, fmt.Sprintf("%d %s", n, label))
		total += n
	}
	if total == 0 {
		return 2, nil
	}
	fmt.Fprintln(w, strings.Join(counts, " "))
	return 0, nil
}

// Read prints unread across all followed targets in commit (seq) order.
// Cursors are per-target rows in the cursors table: after a successful
// render each target's cursor advances to the last seq rendered FOR THAT
// TARGET, guarded to be monotonic. Delivery is at-least-once: two reads by
// the same agent racing each other may both print a message, but the
// commit-ordered seq guarantees neither can skip one.
// scanMessage reads one row into a Message. withSeq matches queries that
// select seq as the leading column.
func scanMessage(rows *sql.Rows, withSeq bool) (*Message, error) {
	var m Message
	var pID, corr sql.NullString
	var err error
	if withSeq {
		err = rows.Scan(&m.Seq, &m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &pID, &corr, &m.CreatedAt)
	} else {
		err = rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &pID, &corr, &m.CreatedAt)
	}
	if err != nil {
		return nil, err
	}
	if pID.Valid {
		m.ParentID = &pID.String
	}
	if corr.Valid {
		m.Correlation = &corr.String
	}
	return &m, nil
}

// AutoFollow subscribes the sender to a channel it just posted into.
// Posting says "I care about this conversation"; without the follow, the
// replies land where the sender never looks — the field failure was four
// posts into a channel and three answers the sender reported as "no
// reply". DMs and already-followed channels are no-ops. The send has
// already committed when this runs, so save failures are returned for the
// caller to warn about, never to fail the verb.
func AutoFollow(st *State, statePath, target string) (bool, error) {
	if !strings.HasPrefix(target, "#") {
		return false, nil
	}
	for _, f := range st.Follow {
		if f == target {
			return false, nil
		}
	}
	st.Follow = append(st.Follow, target)
	if err := st.Save(statePath); err != nil {
		return false, err
	}
	return true, nil
}

func Read(w io.Writer, db *sql.DB, st *State, limit int, advance bool, ts bool, full bool, human bool, color string) (int, error) {
	cursors, err := loadCursors(db, st.Name)
	if err != nil {
		return 0, err
	}
	targets := targetsFor(st.Name, st.Follow)

	var where strings.Builder
	args := []interface{}{}
	for i, t := range targets {
		if i > 0 {
			where.WriteString(" OR ")
		}
		where.WriteString("(target = ? AND seq > ?)")
		args = append(args, t, cursors[t])
	}
	q := "SELECT seq, id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE (" + where.String() + ") AND sender != ? ORDER BY seq ASC"
	args = append(args, st.Name)
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	newCursors := map[string]int64{}
	var msgs []*Message
	for rows.Next() {
		m, err := scanMessage(rows, true)
		if err != nil {
			return 0, err
		}
		newCursors[m.Target] = m.Seq
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 2, nil
	}
	RenderBatch(w, msgs, st.Name, st.DefaultChannel, ts, full, human, color)
	if advance {
		for target, pos := range newCursors {
			if err := advanceCursor(db, st.Name, target, pos); err != nil {
				return 0, err
			}
		}
	}
	return 0, nil
}

// MaxBodyBytes caps a single message body. Readers pay for every byte in
// tokens on every read, so a runaway body is everyone's problem; 64 KiB is
// generous for coordination traffic and small enough to keep `r` sane.
const MaxBodyBytes = 64 * 1024

// Send resolves the target, threads under a parent when the target is a
// ULID prefix, and saves. The returned message carries the assigned ID
// (the correlation ID when isAsk). The body arrives literal — stdin
// expansion of "-" happens in the CLI layer only: when this runs inside
// `grpvn serve`, stdin is the MCP JSON-RPC transport and reading it here
// would hang the server.
func Send(db *sql.DB, sender string, targetArg string, bodyArg string, defaultChannel string, isAsk bool) (*Message, error) {
	if len(bodyArg) > MaxBodyBytes {
		return nil, fmt.Errorf("body too large: %d bytes (max %d)", len(bodyArg), MaxBodyBytes)
	}
	target, parent, err := ResolveTarget(db, targetArg, defaultChannel)
	if err != nil {
		return nil, err
	}
	m := NewMessage(sender, target, []byte(bodyArg))
	if parent != nil {
		if parent.ChainDepth+1 > 8 {
			return nil, fmt.Errorf("chain depth limit reached (8)")
		}
		m.ChainRoot = parent.ChainRoot
		m.ChainDepth = parent.ChainDepth + 1
		m.ParentID = &parent.ID
	}
	if isAsk {
		m.Correlation = &m.ID
	}
	if err := m.Save(db); err != nil {
		return nil, err
	}
	return m, nil
}

// Gc prunes messages (and their marks) older than the cutoff. Retention is
// an operator decision, so this is CLI-only — it is deliberately NOT
// exposed over MCP, keeping the agent-facing surface append-only. Cursor
// positions survive pruning: seq is AUTOINCREMENT and never reused, so a
// position simply has fewer rows behind it. Pruned thread parents render
// as unresolvable prefixes, which is the documented trade.
func Gc(w io.Writer, db *sql.DB, olderThan time.Duration, vacuum bool) error {
	if olderThan <= 0 {
		return fmt.Errorf("--older-than must be positive")
	}
	cutoff := time.Now().Add(-olderThan).UnixMilli()
	resMarks, err := db.Exec(
		"DELETE FROM marks WHERE message_id IN (SELECT id FROM messages WHERE created_at < ?)", cutoff)
	if err != nil {
		return fmt.Errorf("prune marks: %w", err)
	}
	resMsgs, err := db.Exec("DELETE FROM messages WHERE created_at < ?", cutoff)
	if err != nil {
		return fmt.Errorf("prune messages: %w", err)
	}
	nMsgs, _ := resMsgs.RowsAffected()
	nMarks, _ := resMarks.RowsAffected()
	fmt.Fprintf(w, "pruned %d messages, %d marks\n", nMsgs, nMarks)
	if vacuum {
		if _, err := db.Exec("VACUUM"); err != nil {
			return fmt.Errorf("vacuum: %w", err)
		}
	}
	return nil
}

func Grep(w io.Writer, db *sql.DB, name string, follow []string, pattern string, scope string, limit int, defaultChannel string, ts bool, full bool, human bool, color string) error {
	var v []string
	if scope != "" {
		v = []string{scope}
	} else {
		v = append([]string{}, follow...)
		v = append(v, "@"+name)
	}
	p := make([]string, len(v))
	args := make([]interface{}, len(v))
	for i, x := range v {
		p[i] = "?"
		args[i] = x
	}
	rows, err := db.Query("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE target IN ("+strings.Join(p, ", ")+") ORDER BY id DESC", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}
	var msgs []*Message
	for rows.Next() {
		m, err := scanMessage(rows, false)
		if err != nil {
			return err
		}
		if !re.Match(m.Body) {
			continue
		}
		msgs = append(msgs, m)
		if limit > 0 && len(msgs) >= limit {
			break
		}
	}
	RenderBatch(w, msgs, name, defaultChannel, ts, full, human, color)
	return nil
}

func Log(w io.Writer, db *sql.DB, name string, arg string, limit int, defaultChannel string, ts bool, full bool, human bool, color string) error {
	var q string
	var args []interface{}
	if strings.HasPrefix(arg, "#") || strings.HasPrefix(arg, "@") {
		q = "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE target = ? ORDER BY id ASC"
		args = []interface{}{arg}
	} else {
		parent, err := FindMessageByPrefix(db, arg)
		if err != nil {
			return err
		}
		q = "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE chain_root = ? ORDER BY id ASC"
		args = []interface{}{parent.ChainRoot}
	}
	if limit > 0 {
		if strings.HasPrefix(arg, "#") || strings.HasPrefix(arg, "@") {
			q = "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM (SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE target = ? ORDER BY id DESC LIMIT ?) ORDER BY id ASC"
			args = append(args, limit)
		} else {
			q += " LIMIT ?"
			args = append(args, limit)
		}
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var msgs []*Message
	for rows.Next() {
		m, err := scanMessage(rows, false)
		if err != nil {
			return err
		}
		msgs = append(msgs, m)
	}
	RenderBatch(w, msgs, name, defaultChannel, ts, full, human, color)
	return nil
}

func Mark(w io.Writer, db *sql.DB, name string, msgArg string, delete bool, defaultChannel string, ts bool, full bool, human bool, color string) error {
	if msgArg == "" && !delete {
		rows, err := db.Query("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages JOIN marks ON messages.id = marks.message_id WHERE marks.agent_name = ? ORDER BY id ASC", name)
		if err != nil {
			return err
		}
		defer rows.Close()
		var msgs []*Message
		for rows.Next() {
			m, err := scanMessage(rows, false)
			if err != nil {
				return err
			}
			msgs = append(msgs, m)
		}
		RenderBatch(w, msgs, name, defaultChannel, ts, full, human, color)
		return nil
	}
	m, err := FindMessageByPrefix(db, msgArg)
	if err != nil {
		return err
	}
	if delete {
		_, err := db.Exec("DELETE FROM marks WHERE agent_name = ? AND message_id = ?", name, m.ID)
		return err
	}
	_, err = db.Exec("INSERT OR REPLACE INTO marks (agent_name, message_id, marked_at) VALUES (?, ?, ?)", name, m.ID, time.Now().UnixMilli())
	return err
}

func ID(w io.Writer, name string) {
	cwd, _ := os.Getwd()
	fmt.Fprintf(w, "%s@%s\n", name, cwd)
}

func Init(path string, as string, force bool) (string, error) {
	if _, err := os.Stat(path); err == nil && !force {
		return "", fmt.Errorf("state file already exists")
	}
	name := as
	if name == "" {
		var err error
		name, err = GenerateIdentity()
		if err != nil {
			return "", err
		}
	}
	s := &State{Name: name, Follow: []string{}}
	if err := s.Save(path); err != nil {
		return "", err
	}
	return name, nil
}
