package verbs

import (
	"database/sql"
	"fmt"
	"io"
	"os"

	"grpvn/internal/msg"
)

func Send(db *sql.DB, sender string, targetArg string, bodyArg string, defaultChannel string, isAsk bool) error {
	target, parent, err := msg.ResolveTarget(db, targetArg, defaultChannel)
	if err != nil {
		return err
	}

	var body []byte
	if bodyArg == "-" {
		body, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	} else {
		body = []byte(bodyArg)
	}

	m := msg.New(sender, target, body)

	if parent != nil {
		if parent.ChainDepth+1 > 8 {
			return fmt.Errorf("chain depth limit reached (8). start a new thread instead.")
		}
		m.ChainRoot = parent.ChainRoot
		m.ChainDepth = parent.ChainDepth + 1
		m.ParentID = &parent.ID
	}

	if isAsk {
		m.Correlation = &m.ID
	}

	if err := m.Save(db); err != nil {
		return fmt.Errorf("save message: %w", err)
	}

	if isAsk {
		fmt.Println(m.ID)
	}

	return nil
}
