package internal

import "encoding/json"

// Before v4, '*' combined workspace administration with implicit channel
// membership. Preserve people/agents who actually contributed, plus existing
// explicit grants, while removing that implicit enrollment from other rooms.
// This runs once; a later removal must never be undone by historical posts.
func preserveParticipatingMembers(db dbtx) error {
	if err := removeInheritedEnrollments(db); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT DISTINCT p.id,COALESCE(p.parent_id,''),m.target,g.can_write FROM message_principals mp
 JOIN principals p ON p.id=mp.principal_id JOIN messages m ON m.id=mp.message_id
 JOIN permissions g ON g.principal_id=p.id AND g.channel='*' AND g.can_read=1
 WHERE p.status='active' AND m.target LIKE '#%' AND NOT EXISTS(SELECT 1 FROM channel_subscriptions s WHERE s.principal_id=p.id AND s.channel=m.target AND s.joined=0)`)
	if err != nil {
		return err
	}
	type grant struct {
		id, parent, channel string
		write               bool
	}
	var list []grant
	for rows.Next() {
		var g grant
		if err = rows.Scan(&g.id, &g.parent, &g.channel, &g.write); err != nil {
			rows.Close()
			return err
		}
		list = append(list, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, g := range list {
		if _, err = db.Exec("INSERT INTO permissions(principal_id,channel,can_read,can_write) VALUES(?,?,1,?) ON CONFLICT(principal_id,channel) DO NOTHING", g.id, g.channel, g.write); err != nil {
			return err
		}
		if _, err = db.Exec("INSERT INTO channel_subscriptions(principal_id,channel,joined) VALUES(?,?,1) ON CONFLICT(principal_id,channel) DO NOTHING", g.id, g.channel); err != nil {
			return err
		}
		if g.parent != "" {
			if _, err = db.Exec(`INSERT INTO permissions(principal_id,channel,can_read,can_write)
   SELECT ?,?,1,can_write FROM permissions WHERE principal_id=? AND channel='*' AND can_read=1
   ON CONFLICT(principal_id,channel) DO NOTHING`, g.parent, g.channel, g.parent); err != nil {
				return err
			}
		}
	}
	return nil
}

// Old registrations copied every owner grant, without recording a join. Keep
// selected rooms, explicit joins/invitations and contributions; discard unused
// mirrored grants which silently enrolled unrelated delegated agents.
func removeInheritedEnrollments(db dbtx) error {
	rows, err := db.Query(`SELECT p.id,p.name,p.parent_id,owner.name,p.requested FROM principals p
 JOIN principals owner ON owner.id=p.parent_id JOIN permissions role ON role.principal_id=p.id AND role.channel='*' AND role.can_read=1
 WHERE p.kind='agent' AND p.status='active'`)
	if err != nil {
		return err
	}
	type candidate struct{ id, name, parent, owner, requested string }
	var people []candidate
	for rows.Next() {
		var p candidate
		if err = rows.Scan(&p.id, &p.name, &p.parent, &p.owner, &p.requested); err != nil {
			rows.Close()
			return err
		}
		people = append(people, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range people {
		var requested []string
		_ = json.Unmarshal([]byte(p.requested), &requested)
		selected := map[string]bool{}
		for _, channel := range requested {
			selected[channel] = true
		}
		rooms, err := db.Query(`SELECT g.channel FROM permissions g JOIN permissions owner ON owner.principal_id=? AND owner.channel=g.channel AND owner.can_read=g.can_read AND owner.can_write=g.can_write
   WHERE g.principal_id=? AND g.channel LIKE '#%' AND g.channel<>'#general'
   AND NOT EXISTS(SELECT 1 FROM workspace_channels c WHERE c.channel=g.channel AND c.is_open=1)
   AND NOT EXISTS(SELECT 1 FROM remote_channels r WHERE r.target=g.channel)
   AND NOT EXISTS(SELECT 1 FROM channel_subscriptions s WHERE s.principal_id=g.principal_id AND s.channel=g.channel)`, p.parent, p.id)
		if err != nil {
			return err
		}
		var channels []string
		for rooms.Next() {
			var channel string
			if err = rooms.Scan(&channel); err != nil {
				rooms.Close()
				return err
			}
			channels = append(channels, channel)
		}
		err = rooms.Err()
		rooms.Close()
		if err != nil {
			return err
		}
		for _, channel := range channels {
			if selected[channel] {
				continue
			}
			var contributed bool
			if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages m LEFT JOIN message_principals mp ON mp.message_id=m.id WHERE m.target=? AND (mp.principal_id=? OR (mp.principal_id IS NULL AND (m.sender=? OR m.sender IN(SELECT address FROM principal_aliases WHERE principal_id=?)))))`, channel, p.id, registeredSender(p.name, p.owner, p.parent), p.id).Scan(&contributed); err != nil {
				return err
			}
			if !contributed {
				if _, err = db.Exec("DELETE FROM permissions WHERE principal_id=? AND channel=?", p.id, channel); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
