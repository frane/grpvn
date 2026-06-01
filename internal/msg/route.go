package msg

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)
var ulidPrefixRegex = regexp.MustCompile("^[0-9A-HJKMNP-TV-Z]{6,26}$")

func ResolveTarget(db *sql.DB, input string, defaultChannel string) (target string, parent *Message, err error) {
	if input == "" {
		if defaultChannel == "" {
			return "", nil, errors.New("no target provided and no default_channel set")
		}
		return defaultChannel, nil, nil
	}

	if strings.HasPrefix(input, "#") {
		return input, nil, nil
	}

	if strings.HasPrefix(input, "@") {
		return input, nil, nil
	}

	if ulidPrefixRegex.MatchString(strings.ToUpper(input)) {
		parent, err = FindByPrefix(db, input)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Not a ULID prefix, or at least not one we know.
				// If it doesn't start with # or @ and isn't a known ULID, 
				// we treat it as an error per spec: "anything else... error"
				return "", nil, fmt.Errorf("ambiguous or unknown target: %s", input)
			}
			return "", nil, err
		}
		// If it's a reply, target is parent.target
		return parent.Target, parent, nil
	}

	return "", nil, fmt.Errorf("invalid target prefix: %s", input)
}

func FindByPrefix(db *sql.DB, prefix string) (*Message, error) {
	prefix = strings.ToUpper(prefix)
	rows, err := db.Query("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE id LIKE ?", prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var m Message
	var parentID, correlation sql.NullString
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	err = rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &parentID, &correlation, &m.CreatedAt)
	if err != nil {
		return nil, err
	}

	if rows.Next() {
		return nil, fmt.Errorf("ambiguous prefix: %s matches multiple messages", prefix)
	}

	if parentID.Valid {
		m.ParentID = &parentID.String
	}
	if correlation.Valid {
		m.Correlation = &correlation.String
	}

	return &m, nil
}
