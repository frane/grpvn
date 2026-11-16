package internal

import (
	"database/sql"
	"fmt"
	"net/http"
)

// CoordinatorOptions controls the served channel set and anonymous access.
// Private is the default. Open changes the default; channel overrides can
// mix a public lobby with private conversations on the same coordinator.
type CoordinatorOptions struct {
	Web             bool // serve the human web application
	LocalUI         bool // trusted localhost human interface; never enable on a public listener
	PublicURL       string
	Channels        []string
	Open            bool
	OpenChannels    []string
	PrivateChannels []string
}

type channelAccess struct {
	db        *sql.DB
	open      bool
	local     bool
	served    map[string]bool
	overrides map[string]bool // true = open, false = private
}

func newChannelAccess(options CoordinatorOptions) (*channelAccess, error) {
	a := &channelAccess{open: options.Open, local: options.LocalUI, served: map[string]bool{}, overrides: map[string]bool{}}
	for _, channel := range options.Channels {
		if err := validateChannel(channel); err != nil {
			return nil, err
		}
		a.served[channel] = true
	}
	for _, channel := range options.OpenChannels {
		if err := validateChannel(channel); err != nil {
			return nil, err
		}
		a.overrides[channel] = true
	}
	for _, channel := range options.PrivateChannels {
		if err := validateChannel(channel); err != nil {
			return nil, err
		}
		if _, exists := a.overrides[channel]; exists {
			return nil, fmt.Errorf("channel %s cannot be both open and private", channel)
		}
		a.overrides[channel] = false
	}
	for channel := range a.overrides {
		if !a.isServed(channel) {
			return nil, fmt.Errorf("channel %s is outside --channel; add it to the served channel set", channel)
		}
	}
	return a, nil
}

func (a *channelAccess) isServed(channel string) bool { return len(a.served) == 0 || a.served[channel] }

func (a *channelAccess) isOpen(channel string) bool {
	if a.db == nil {
		return a.isOpenWith(nil, channel)
	}
	return a.isOpenWith(a.db, channel)
}

func (a *channelAccess) isOpenWith(db dbtx, channel string) bool {
	if open, ok := a.overrides[channel]; ok {
		return open
	}
	if db != nil {
		var open bool
		if db.QueryRow("SELECT is_open FROM workspace_channels WHERE channel=?", channel).Scan(&open) == nil {
			return open && a.open
		}
	}
	return a.open
}

// Public channels belong to the workspace. Anonymous internet access requires
// an explicit serve --open/--open-channel setting independently of visibility.
func (a *channelAccess) isPublicWith(db dbtx, channel string) bool {
	if open, ok := a.overrides[channel]; ok {
		return open
	}
	if db != nil {
		if channel == "#general" {
			var enabled string
			if db.QueryRow("SELECT value FROM server_meta WHERE key='auto_general'").Scan(&enabled) == nil && enabled == "1" {
				return true
			}
		}
		var open bool
		if db.QueryRow("SELECT is_open FROM workspace_channels WHERE channel=?", channel).Scan(&open) == nil {
			return open
		}
	}
	return a.open
}
func (a *channelAccess) isPublic(channel string) bool { return a.isPublicWith(a.db, channel) }
func (a *channelAccess) publicReadable(db dbtx, p *Principal, channel string) bool {
	return p != nil && (p.scopes == nil || p.scopes["grpvn.read"]) && a.isPublicWith(db, channel) && trustedWorkspaceIdentity(db, p)
}

func (a *channelAccess) authenticated(r *http.Request) bool {
	return requestPrincipal(r) != nil
}

func (a *channelAccess) canAccess(r *http.Request, channel string) bool {
	p := requestPrincipal(r)
	return a.isServed(channel) && (a.isOpen(channel) || a.publicReadable(a.db, p, channel) || principalPermission(a.db, p, channel, false))
}

func (a *channelAccess) hasAnonymousAccess() bool {
	if a.db == nil {
		return a.hasAnonymousAccessWith(nil)
	}
	return a.hasAnonymousAccessWith(a.db)
}

func (a *channelAccess) hasAnonymousAccessWith(db dbtx) bool {
	if len(a.served) > 0 {
		for channel := range a.served {
			if a.isOpenWith(db, channel) {
				return true
			}
		}
		return false
	}
	if a.open {
		return true
	}
	for channel, open := range a.overrides {
		if open && a.isServed(channel) {
			return true
		}
	}
	return false
}

func (a *channelAccess) authorize(w http.ResponseWriter, r *http.Request, channel string) bool {
	if !a.isServed(channel) {
		apiError(w, http.StatusForbidden, "channel is not served")
		return false
	}
	write := r.Method == "POST" && r.URL.Path == "/v1/messages"
	p := requestPrincipal(r)
	if write && !isChatParticipant(p) {
		apiError(w, 401, "a registered agent identity is required to post")
		return false
	}
	if write && p.scopes != nil && !p.scopes["grpvn.write"] {
		apiError(w, http.StatusForbidden, "credential does not grant write scope")
		return false
	}
	if write && principalPermission(a.db, p, channel, false) && !principalPermission(a.db, p, channel, true) {
		apiError(w, 403, "Channel is read-only for this member")
		return false
	}
	if !a.isOpen(channel) && !principalPermission(a.db, p, channel, write) && !(!write && a.publicReadable(a.db, p, channel)) {
		if write && ensureSendChannel(a.db, a, p, channel) {
			return true
		}
		if p == nil {
			authenticationRequired(w)
		} else {
			apiError(w, http.StatusForbidden, "channel permission required")
		}
		return false
	}
	return true
}

func authenticationRequired(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	apiError(w, http.StatusUnauthorized, "private access: approved membership and channel permissions required")
}
