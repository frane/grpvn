package internal

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validateWorkspaceName(name string) error {
	if name == "" || utf8.RuneCountInString(name) > 80 || !utf8.ValidString(name) {
		return fmt.Errorf("Use a workspace name of 1–80 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("Workspace names cannot contain control characters")
		}
	}
	return nil
}
func addressWorkspaceName(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" {
		return "grpvn"
	}
	if net.ParseIP(u.Hostname()) != nil || u.Hostname() == "localhost" {
		return u.Host
	}
	parts := strings.Split(u.Hostname(), ".")
	for len(parts) > 1 && (parts[0] == "grpvn" || parts[0] == "chat" || parts[0] == "www") {
		parts = parts[1:]
	}
	return parts[0]
}
func coordinatorName(db dbtx, base string) string {
	var name string
	if db.QueryRow("SELECT value FROM server_meta WHERE key='name'").Scan(&name) == nil && name != "" {
		return name
	}
	return addressWorkspaceName(base)
}
func workspaceDisplayName(db dbtx, owner string, remote Remote) string {
	var name string
	if db.QueryRow("SELECT value FROM human_preferences WHERE principal_id=? AND name=?", owner, "workspace:"+remote.ServerID).Scan(&name) == nil && name != "" {
		return name
	}
	if !strings.HasPrefix(remote.Name, "workspace-") {
		return remote.Name
	}
	return addressWorkspaceName(remote.URL)
}
func saveWorkspaceName(db *sql.DB, owner, server, name string) error {
	name = strings.TrimSpace(name)
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	_, err := db.Exec("INSERT INTO human_preferences(principal_id,name,value) VALUES(?,?,?) ON CONFLICT(principal_id,name) DO UPDATE SET value=excluded.value", owner, "workspace:"+server, name)
	if err == nil {
		err = touchPersonalMetadata(db, owner)
	}
	return err
}
