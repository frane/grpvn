package internal

import (
	"database/sql"
	"fmt"
	"strings"
)

func generatedPrincipalName(db dbtx) (string, error) {
	rows, err := db.Query("SELECT name FROM principals UNION SELECT address FROM principal_aliases UNION SELECT sender FROM messages UNION SELECT substr(target,2) FROM messages WHERE target LIKE '@%' UNION SELECT agent_name FROM agent_sessions")
	if err != nil {
		return "", err
	}
	used, names := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return "", err
		}
		names[name] = true
		parts := strings.SplitN(name, "-", 3)
		if len(parts) == 3 {
			used[parts[0]], used[parts[1]] = true, true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for {
		name, err := generateIdentity(used)
		if err != nil || !names[name] {
			return name, err
		}
	}
}

func principalOwnsAddress(p *Principal, address string) bool {
	return address == principalAddress(p) || p.aliases[address]
}

func principalOwnsLocalName(p *Principal, name string) bool {
	if name == p.Name {
		return true
	}
	if p.parent != nil {
		return p.aliases[registeredSender(name, p.parent.Name, p.ParentID)]
	}
	return p.aliases[name]
}

func samePrincipalAddress(db dbtx, a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	var same bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM principal_aliases a JOIN principal_aliases b ON a.principal_id=b.principal_id WHERE a.address=? AND b.address=?)`, a, b).Scan(&same)
	return same, err
}

// RenamePrincipal changes only the current handle. Messages, grants, OAuth
// credentials, idempotency keys and read positions keep their stable IDs.
func RenamePrincipal(db *sql.DB, id, name string) (*Principal, error) {
	if name != "" && (len(name) > 100 || strings.ContainsAny(name, " \t\r\n\x00@/~#")) {
		return nil, fmt.Errorf("choose a name of at most 100 characters without whitespace, @, /, ~ or #")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := findPrincipal(tx, "id=?", id)
	if err != nil || p.Kind != "agent" || p.Status != "active" {
		return nil, fmt.Errorf("an active agent identity is required")
	}
	if err := cachePrincipal(tx, p); err != nil {
		return nil, err
	}
	if err := renamePrincipal(tx, p, name); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return p, cachePrincipal(db, p)
}

func renamePrincipal(tx dbtx, p *Principal, name string) error {
	if name != "" && (len(name) > 100 || strings.ContainsAny(name, " \t\r\n\x00@/~#")) {
		return fmt.Errorf("invalid agent name")
	}
	if name == "" {
		var err error
		name, err = generatedPrincipalName(tx)
		if err != nil {
			return err
		}
	}
	oldAddress := principalAddress(p)
	p.Name = name
	address := principalAddress(p)
	var taken bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM principals WHERE kind='agent' AND name=? AND COALESCE(parent_id,'')=? AND id<>?)
 OR EXISTS(SELECT 1 FROM principal_aliases WHERE address=? AND principal_id<>?)`, name, p.ParentID, p.ID, address, p.ID).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("that name is already assigned; choose another")
	}
	if address != oldAddress && !p.aliases[address] {
		// Trusted local identities may predate server registration. A remote
		// rename must not claim their inboxes or runtime activation owners.
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE sender=? OR target=?) OR EXISTS(SELECT 1 FROM agent_sessions WHERE agent_name=?)`, address, "@"+address, address).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("that name is already used by a local identity; choose another")
		}
	}
	for _, alias := range []string{oldAddress, address} {
		if _, err := tx.Exec("INSERT INTO principal_aliases(address,principal_id) VALUES(?,?) ON CONFLICT(address) DO NOTHING", alias, p.ID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE principals SET name=? WHERE id=?", name, p.ID); err != nil {
		return err
	}
	if oldAddress != address {
		if _, err := tx.Exec("UPDATE agent_sessions SET agent_name=? WHERE agent_name=?", address, oldAddress); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE activation_owners SET agent_name=? WHERE agent_name=?", address, oldAddress); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cursors(agent_name,target,position,updated_at)
 SELECT ?,target,position,updated_at FROM cursors WHERE agent_name=?
 ON CONFLICT(agent_name,target) DO UPDATE SET position=MAX(position,excluded.position)`, address, oldAddress); err != nil {
			return err
		}
	}
	return nil
}

func canonicalDMTarget(db dbtx, target string) (string, error) {
	if !strings.HasPrefix(target, "@") {
		return target, nil
	}
	var id string
	err := db.QueryRow("SELECT principal_id FROM principal_aliases WHERE address=?", strings.TrimPrefix(target, "@")).Scan(&id)
	if err == sql.ErrNoRows {
		return target, nil
	}
	if err != nil {
		return "", err
	}
	p, err := findPrincipal(db, "id=?", id)
	if err != nil {
		return "", err
	}
	if p.ParentID != "" {
		p.parent, err = findPrincipal(db, "id=?", p.ParentID)
		if err != nil {
			return "", err
		}
	}
	return "@" + principalAddress(p), nil
}
