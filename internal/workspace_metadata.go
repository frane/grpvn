package internal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// One metadata request per workspace every thirty seconds updates membership,
// archive and write rights. It shares the daemon's existing budget and backoff.
func refreshWorkspaceMetadata(ctx context.Context, db *sql.DB, remote Remote) error {
	var owner string
	if db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&owner) != nil {
		return nil
	}
	var next int64
	_ = db.QueryRow("SELECT value FROM chat_metadata WHERE key=?", "remote:"+remote.ServerID).Scan(&next)
	if next > time.Now().UnixMilli() {
		return nil
	}
	return discoverHumanChannels(ctx, db, remote, owner)
}
func discoverHumanChannels(ctx context.Context, db *sql.DB, remote Remote, owner string) error {
	var result struct {
		Channels []struct {
			Name     string `json:"name"`
			Write    bool   `json:"write"`
			Open     bool   `json:"open"`
			Manage   bool   `json:"manage"`
			Joined   *bool  `json:"joined"`
			Archived bool   `json:"archived"`
		} `json:"channels"`
	}
	if err := remoteRequest(ctx, remote, "GET", "/v1/chat/channels?browse=1", nil, &result); err != nil {
		return err
	}
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE remote_channel_details SET can_read=0,can_write=0 WHERE remote_name=?", remote.Name); err != nil {
		return err
	}
	for _, channel := range result.Channels {
		if validateChannel(channel.Name) != nil {
			return fmt.Errorf("Invalid workspace channel")
		}
		target := "#" + remote.Name + "/" + strings.TrimPrefix(channel.Name, "#")
		_, err = tx.Exec("INSERT INTO remote_channel_details(remote_name,channel,can_read,can_write,is_open,can_manage) VALUES(?,?,1,?,?,?) ON CONFLICT(remote_name,channel) DO UPDATE SET can_read=1,can_write=excluded.can_write,is_open=excluded.is_open,can_manage=excluded.can_manage", remote.Name, channel.Name, channel.Write, channel.Open, channel.Manage)
		if err != nil {
			return err
		}
		joined := channel.Joined == nil || *channel.Joined
		if err = setChannelJoined(tx, owner, target, joined); err != nil {
			return err
		}
		if channel.Archived {
			_, err = tx.Exec("INSERT INTO channel_archives(channel,principal_id,created_at) VALUES(?,?,?) ON CONFLICT(channel) DO NOTHING", target, owner, time.Now().UnixMilli())
		} else {
			_, err = tx.Exec("DELETE FROM channel_archives WHERE channel=?", target)
		}
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec("INSERT INTO chat_metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "remote:"+remote.ServerID, time.Now().Add(30*time.Second).UnixMilli())
	if err != nil {
		return err
	}
	if err = TouchChatMetadata(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for _, channel := range result.Channels {
		if err = addRemoteChannel(db, remote.Name, channel.Name); err != nil {
			return err
		}
	}
	return nil
}
func activeRemoteTarget(db *sql.DB, alias, owner string) string {
	channels, err := RemoteChannels(db, alias)
	if err != nil {
		return ""
	}
	for _, c := range channels {
		var readable bool
		err := db.QueryRow("SELECT can_read FROM remote_channel_details WHERE remote_name=? AND channel=?", alias, c.Channel).Scan(&readable)
		if (err == sql.ErrNoRows || readable) && channelJoined(db, owner, c.Target) && !channelArchived(db, c.Target) {
			return c.Target
		}
	}
	return ""
}
