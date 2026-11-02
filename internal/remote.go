package internal

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

type Remote struct {
	DB        *sql.DB
	MemberID  string
	Name      string
	URL       string
	Token     string
	ServerID  string
	LastSync  int64
	LastError string
}

type RemoteChannel struct {
	Remote   string
	Channel  string
	Target   string
	Position int64
}

var remoteNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var channelPattern = regexp.MustCompile(`^#[a-zA-Z0-9_./-]{1,200}$`)

func validateChannel(channel string) error {
	if !channelPattern.MatchString(channel) {
		return fmt.Errorf("invalid channel %q: use # followed by letters, digits, _, ., / or -", channel)
	}
	return nil
}

func remoteTarget(name, channel string) string {
	return "#" + name + "/" + strings.TrimPrefix(channel, "#")
}

// ConnectRemote pins an alias to a coordinator's durable identity. Changing
// its URL or credentials is safe; silently pointing it at another log is not.
func ConnectRemote(ctx context.Context, db *sql.DB, name, rawURL, token string, channels []string) error {
	if !remoteNamePattern.MatchString(name) {
		return fmt.Errorf("invalid remote name %q: use letters, digits, _ or - (max 64)", name)
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || (u.Fragment != "" && !strings.HasPrefix(u.Fragment, "invite=")) {
		return fmt.Errorf("remote URL must be an http(s) URL without credentials, query, or fragment")
	}
	for _, channel := range channels {
		if err := validateChannel(channel); err != nil {
			return err
		}
	}
	invite := token
	if strings.HasPrefix(u.Fragment, "invite=") {
		invite = strings.TrimPrefix(u.Fragment, "invite=")
		u.Fragment = ""
	}
	r := Remote{DB: db, Name: name, URL: strings.TrimRight(u.String(), "/")}
	// Reconnecting without --token preserves the stored credential.
	old, err := getRemote(db, name)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var info serverInfo
	if err := remoteRequest(ctx, r, "GET", "/v1/info", nil, &info); err != nil {
		return err
	}
	if info.Protocol != syncProtocol || !validMessageID(info.ServerID) {
		return fmt.Errorf("incompatible grpvn coordinator")
	}
	if old != nil && old.ServerID != info.ServerID {
		return fmt.Errorf("remote %s points to a different coordinator; choose a new alias", name)
	}
	r.ServerID = info.ServerID
	if _, err := registerRemote(ctx, db, &r, invite, channels); err != nil {
		return err
	}
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Reserve the namespace only if it contains no existing local messages.
	var local int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM messages m WHERE substr(target, 1, ?) = ?
		AND NOT EXISTS (SELECT 1 FROM remote_messages rm WHERE rm.local_id = m.id)`, len(name)+2, "#"+name+"/").Scan(&local); err != nil {
		return err
	}
	if local > 0 {
		return fmt.Errorf("#%s/ already contains local messages; choose another remote name", name)
	}
	var pinned string
	err = tx.QueryRow("SELECT server_id FROM remotes WHERE name = ?", name).Scan(&pinned)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && pinned != info.ServerID {
		return fmt.Errorf("remote %s points to a different coordinator; choose a new alias", name)
	}
	if _, err := tx.Exec(`INSERT INTO remotes (name, url, token, server_id) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET url = excluded.url, token = excluded.token`, name, r.URL, r.Token, info.ServerID); err != nil {
		return err
	}
	for _, channel := range channels {
		if err := addRemoteChannel(tx, name, channel); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM sync_schedule WHERE remote_name = ?", name); err != nil {
		return err
	}
	return tx.Commit()
}

func addRemoteChannel(db dbtx, name, channel string) error {
	if err := validateChannel(channel); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO remote_channels (remote_name, channel, target) VALUES (?, ?, ?)`, name, channel, remoteTarget(name, channel))
	return err
}

// JoinRemoteTargets makes follow/send work on a configured remote namespace.
// It does not make unrelated local channels remotely visible.
func JoinRemoteTargets(db dbtx, targets ...string) error {
	for _, target := range targets {
		name, channel, ok := splitRemoteTarget(target)
		if !ok {
			continue
		}
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM remotes WHERE name = ?)", name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if err := addRemoteChannel(db, name, channel); err != nil {
				return err
			}
		}
	}
	return nil
}

func splitRemoteTarget(target string) (string, string, bool) {
	if !strings.HasPrefix(target, "#") {
		return "", "", false
	}
	name, channel, ok := strings.Cut(target[1:], "/")
	return name, "#" + channel, ok && channel != ""
}

func getRemote(db dbtx, name string) (*Remote, error) {
	var r Remote
	err := db.QueryRow("SELECT name, url, token, server_id, last_sync, last_error FROM remotes WHERE name = ?", name).
		Scan(&r.Name, &r.URL, &r.Token, &r.ServerID, &r.LastSync, &r.LastError)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func ListRemotes(db *sql.DB) ([]Remote, error) {
	rows, err := db.Query("SELECT name, url, token, server_id, last_sync, last_error FROM remotes ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Remote
	for rows.Next() {
		var r Remote
		if err := rows.Scan(&r.Name, &r.URL, &r.Token, &r.ServerID, &r.LastSync, &r.LastError); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func RemoteChannels(db *sql.DB, name string) ([]RemoteChannel, error) {
	rows, err := db.Query("SELECT remote_name, channel, target, position FROM remote_channels WHERE remote_name = ? ORDER BY channel", name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemoteChannel
	for rows.Next() {
		var ch RemoteChannel
		if err := rows.Scan(&ch.Remote, &ch.Channel, &ch.Target, &ch.Position); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

func PrintRemotes(w io.Writer, db *sql.DB) error {
	remotes, err := ListRemotes(db)
	if err != nil {
		return err
	}
	for _, remote := range remotes {
		channels, err := RemoteChannels(db, remote.Name)
		if err != nil {
			return err
		}
		pending, err := PendingRemoteCount(db, remote.Name)
		if err != nil {
			return err
		}
		targets := make([]string, 0, len(channels))
		for _, channel := range channels {
			targets = append(targets, channel.Target)
		}
		fmt.Fprintf(w, "%s %s %s pending=%d\n", remote.Name, remote.URL, strings.Join(targets, " "), pending)
		if remote.LastError != "" {
			fmt.Fprintf(w, "  sync error: %s\n", strings.ReplaceAll(remote.LastError, "\n", "; "))
		}
	}
	return nil
}

// Acquire SQLite's writer lock before any reads in a read/modify/write
// transaction. Competing CLI processes must wait, rather than fail a WAL
// read-to-write lock upgrade after a sibling commits.
func beginWrite(db *sql.DB) (*sql.Tx, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec("UPDATE schema_version SET applied_at = applied_at WHERE version = ?", CurrentSchemaVersion); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
