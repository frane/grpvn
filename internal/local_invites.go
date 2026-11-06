package internal

import (
	"database/sql"
	"fmt"
	"strings"
)

func (s *webServer) inviteLocalAgent(p *Principal, channel, address string) error {
	name := strings.TrimPrefix(strings.TrimSpace(address), "@")
	if validateChannel(channel) != nil || !s.can(p, channel, true) {
		return fmt.Errorf("Channel write access required")
	}
	var known bool
	if s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM messages m WHERE sender=? AND NOT EXISTS(SELECT 1 FROM remote_messages rm WHERE rm.local_id=m.id)) OR EXISTS(SELECT 1 FROM agent_sessions WHERE agent_name=?)", name, name).Scan(&known) != nil || !known {
		return fmt.Errorf("This agent is not connected on this computer yet. Copy the local agent setup prompt instead")
	}
	if _, err := CreateWorkspaceChannel(s.db, s.access, p, channel, false); err != nil {
		return err
	}
	tx, err := beginWrite(s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	enabled := true
	var restricted bool
	err = tx.QueryRow("SELECT enabled,restricted FROM local_participants WHERE name=?", name).Scan(&enabled, &restricted)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if !enabled && !principalPermission(tx, p, "*", true) {
		return fmt.Errorf("A workspace administrator must restore this agent")
	}
	var posting bool
	oldGrant := tx.QueryRow("SELECT can_write FROM local_channel_grants WHERE name=? AND channel=?", name, channel).Scan(&posting)
	notify := !enabled || oldGrant == sql.ErrNoRows || !posting
	if _, err = tx.Exec("INSERT INTO local_participants(name,enabled,restricted) VALUES(?,1,?) ON CONFLICT(name) DO UPDATE SET enabled=1", name, restricted); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO local_channel_grants(name,channel,can_write) VALUES(?,?,1) ON CONFLICT(name,channel) DO UPDATE SET can_write=1", name, channel); err != nil {
		return err
	}
	if notify {
		message, err := sendOnce(tx, principalAddress(p), "@"+name, "You have been invited to "+channel+" on this computer. Follow the channel and read its relevant recent messages; your existing identity is unchanged.", "", false)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO message_principals(message_id,principal_id) VALUES(?,?)", message.ID, p.ID); err != nil {
			return err
		}
	}
	if err = TouchChatMetadata(tx); err != nil {
		return err
	}
	return tx.Commit()
}
