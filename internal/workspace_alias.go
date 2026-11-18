package internal

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

func chooseWorkspaceAlias(db dbtx, base, serverID, requested string) (string, error) {
	if requested != "" && !remoteNamePattern.MatchString(requested) {
		return "", fmt.Errorf("Use a workspace alias of up to 64 letters, digits, - or _")
	}
	var existing string
	if db.QueryRow("SELECT name FROM remotes WHERE server_id=? ORDER BY name LIMIT 1", serverID).Scan(&existing) == nil {
		return existing, nil // Reconnecting preserves existing routing and profiles.
	}
	alias := requested
	if alias == "" {
		address, err := url.Parse(base)
		if err != nil {
			return "", err
		}
		parts := strings.Split(strings.ToLower(address.Hostname()), ".")
		for len(parts) > 1 && (parts[0] == "grpvn" || parts[0] == "chat" || parts[0] == "www") {
			parts = parts[1:]
		}
		alias = parts[0]
		if net.ParseIP(address.Hostname()) != nil {
			alias = "server-" + address.Hostname()
		}
		alias = strings.Map(func(ch rune) rune {
			if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' {
				return ch
			}
			return '-'
		}, alias)
		alias = strings.Trim(alias, "-_")
		if alias == "" {
			alias = "server"
		}
		if len(alias) > 58 {
			alias = alias[:58]
		}
	}
	for attempt := 1; ; attempt++ {
		candidate := alias
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", alias, attempt)
		}
		var used bool
		err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM remotes WHERE name=? AND server_id<>?) OR EXISTS(SELECT 1 FROM workspace_signins WHERE alias=? AND server_id<>? AND status='waiting' AND expires_at>?)`, candidate, serverID, candidate, serverID, time.Now().UnixMilli()).Scan(&used)
		if err != nil {
			return "", err
		}
		if !used {
			return candidate, nil
		}
		if requested != "" {
			return "", fmt.Errorf("That alias already belongs to another server. Choose another name")
		}
	}
}
