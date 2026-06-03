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

func Check(w io.Writer, db *sql.DB, name string, cursor string, follow []string) (int, error) {
	v := append([]string{}, follow...)
	v = append(v, "@"+name)
	p := make([]string, len(v))
	args := make([]interface{}, len(v)+1)
	args[0] = cursor
	for i, x := range v {
		p[i] = "?"
		args[i+1] = x
	}
	q := fmt.Sprintf("SELECT target, COUNT(*) FROM messages WHERE id > ? AND target IN (%s) GROUP BY target", strings.Join(p, ", "))
	rows, err := db.Query(q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	counts := []string{}
	total := 0
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err != nil {
			return 0, err
		}
		if t == "@"+name {
			t = "@me"
		}
		counts = append(counts, fmt.Sprintf("%d %s", c, t))
		total += c
	}
	if total == 0 {
		return 2, nil
	}
	fmt.Fprintln(w, strings.Join(counts, " "))
	return 0, nil
}

func Read(w io.Writer, db *sql.DB, name string, cursor string, follow []string, limit int, advance bool, defaultChannel string, ts bool, full bool, human bool, color string) (string, int, error) {
	v := append([]string{}, follow...)
	v = append(v, "@"+name)
	p := make([]string, len(v))
	args := make([]interface{}, len(v)+1)
	args[0] = cursor
	for i, x := range v {
		p[i] = "?"
		args[i+1] = x
	}
	q := "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE id > ? AND target IN (" + strings.Join(p, ", ") + ") ORDER BY id ASC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return cursor, 0, err
	}
	defer rows.Close()
	cur := cursor
	count := 0
	for rows.Next() {
		var m Message
		var pID, corr sql.NullString
		if err := rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &pID, &corr, &m.CreatedAt); err != nil {
			return cursor, 0, err
		}
		if pID.Valid {
			m.ParentID = &pID.String
		}
		if corr.Valid {
			m.Correlation = &corr.String
		}
		if human {
			if count == 0 {
				HumanHeader(w, ShouldColor(color))
			}
			RenderHuman(w, &m, name, ShouldColor(color))
		} else {
			RenderAI(w, &m, name, defaultChannel, ts, full)
		}
		cur = m.ID
		count++
	}
	if count == 0 {
		return cursor, 2, nil
	}
	if !advance {
		cur = cursor
	}
	return cur, 0, nil
}

func Send(db *sql.DB, sender string, targetArg string, bodyArg string, defaultChannel string, isAsk bool) error {
	target, parent, err := ResolveTarget(db, targetArg, defaultChannel)
	if err != nil {
		return err
	}
	var body []byte
	if bodyArg == "-" {
		body, _ = io.ReadAll(os.Stdin)
	} else {
		body = []byte(bodyArg)
	}
	m := NewMessage(sender, target, body)
	if parent != nil {
		if parent.ChainDepth+1 > 8 {
			return fmt.Errorf("chain depth limit reached (8)")
		}
		m.ChainRoot = parent.ChainRoot
		m.ChainDepth = parent.ChainDepth + 1
		m.ParentID = &parent.ID
	}
	if isAsk {
		m.Correlation = &m.ID
	}
	if err := m.Save(db); err != nil {
		return err
	}
	if isAsk {
		fmt.Println(m.ID)
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
	re, _ := regexp.Compile(pattern)
	count := 0
	for rows.Next() {
		var m Message
		var pID, corr sql.NullString
		if err := rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &pID, &corr, &m.CreatedAt); err != nil {
			return err
		}
		if !re.Match(m.Body) {
			continue
		}
		if pID.Valid {
			m.ParentID = &pID.String
		}
		if corr.Valid {
			m.Correlation = &corr.String
		}
		if human {
			if count == 0 {
				HumanHeader(w, ShouldColor(color))
			}
			RenderHuman(w, &m, name, ShouldColor(color))
		} else {
			RenderAI(w, &m, name, defaultChannel, ts, full)
		}
		count++
		if limit > 0 && count >= limit {
			break
		}
	}
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
	count := 0
	for rows.Next() {
		var m Message
		var pID, corr sql.NullString
		if err := rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &pID, &corr, &m.CreatedAt); err != nil {
			return err
		}
		if pID.Valid {
			m.ParentID = &pID.String
		}
		if corr.Valid {
			m.Correlation = &corr.String
		}
		if human {
			if count == 0 {
				HumanHeader(w, ShouldColor(color))
			}
			RenderHuman(w, &m, name, ShouldColor(color))
		} else {
			RenderAI(w, &m, name, defaultChannel, ts, full)
		}
		count++
	}
	return nil
}

func Mark(w io.Writer, db *sql.DB, name string, msgArg string, delete bool, defaultChannel string, ts bool, full bool, human bool, color string) error {
	if msgArg == "" && !delete {
		rows, err := db.Query("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages JOIN marks ON messages.id = marks.message_id WHERE marks.agent_name = ? ORDER BY id ASC", name)
		if err != nil {
			return err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var m Message
			var pID, corr sql.NullString
			if err := rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &pID, &corr, &m.CreatedAt); err != nil {
				return err
			}
			if pID.Valid {
				m.ParentID = &pID.String
			}
			if corr.Valid {
				m.Correlation = &corr.String
			}
			if human {
				if count == 0 {
					HumanHeader(w, ShouldColor(color))
				}
				RenderHuman(w, &m, name, ShouldColor(color))
			} else {
				RenderAI(w, &m, name, defaultChannel, ts, full)
			}
			count++
		}
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
