package internal

func configureGeneral(db dbtx, access *channelAccess) error {
	enabled := access.isServed("#general")
	if open, ok := access.overrides["#general"]; ok && !open {
		enabled = false
	}
	value := "0"
	if enabled {
		value = "1"
	}
	if _, err := db.Exec("INSERT INTO server_meta(key,value) VALUES('auto_general',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	rows, err := db.Query("SELECT id FROM principals WHERE status='active'")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = joinGeneral(db, id); err != nil {
			return err
		}
	}
	return nil
}
func joinGeneral(db dbtx, id string) error {
	var approved bool
	if db.QueryRow("SELECT EXISTS(SELECT 1 FROM principals p JOIN principal_approvals a ON a.principal_id=COALESCE(p.parent_id,p.id) WHERE p.id=? AND p.status='active' AND a.approved=1)", id).Scan(&approved) != nil || !approved {
		return nil
	}
	var enabled string
	if db.QueryRow("SELECT value FROM server_meta WHERE key='auto_general'").Scan(&enabled) != nil || enabled != "1" {
		return nil
	}
	var left bool
	_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM channel_subscriptions WHERE principal_id=? AND channel='#general' AND joined=0)", id).Scan(&left)
	if left {
		return nil
	}
	if _, err := db.Exec("INSERT INTO permissions(principal_id,channel,can_read,can_write) VALUES(?,'#general',1,1) ON CONFLICT(principal_id,channel) DO NOTHING", id); err != nil {
		return err
	}
	_, err := db.Exec("INSERT INTO channel_subscriptions(principal_id,channel,joined) VALUES(?,'#general',1) ON CONFLICT(principal_id,channel) DO NOTHING", id)
	return err
}

func joinPublicChannel(db dbtx, p *Principal, channel string) error {
	for _, id := range []string{p.ID, p.ParentID} {
		if id == "" {
			continue
		}
		var read, write bool
		if db.QueryRow("SELECT can_read,can_write FROM permissions WHERE principal_id=? AND channel=?", id, channel).Scan(&read, &write) == nil && read && !write {
			return setChannelJoined(db, p.ID, channel, true)
		}
	}
	return channelGrant(db, p, channel, true)
}
