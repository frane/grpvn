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
	PublicURL       string
	Channels        []string
	Open            bool
	OpenChannels    []string
	PrivateChannels []string
}

type channelAccess struct {
	db        *sql.DB
	open      bool
	served    map[string]bool
	overrides map[string]bool // true = open, false = private
}

func newChannelAccess(options CoordinatorOptions) (*channelAccess, error) {
	a := &channelAccess{open: options.Open, served: map[string]bool{}, overrides: map[string]bool{}}
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
	if open, ok := a.overrides[channel]; ok {
		return open
	}
	return a.open
}

func (a *channelAccess) authenticated(r *http.Request) bool {
	return requestPrincipal(r) != nil
}

func (a *channelAccess) canAccess(r *http.Request, channel string) bool {
	return a.isServed(channel) && (a.isOpen(channel) || principalPermission(a.db, requestPrincipal(r), channel, false))
}

func (a *channelAccess) hasAnonymousAccess() bool {
	if len(a.served) > 0 {
		for channel := range a.served {
			if a.isOpen(channel) {
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
	if write && (p == nil || p.Kind != "agent") {
		apiError(w, 401, "a registered agent identity is required to post")
		return false
	}
	if write && p.scopes != nil && !p.scopes["grpvn.write"] {
		apiError(w, http.StatusForbidden, "credential does not grant write scope")
		return false
	}
	if !a.isOpen(channel) && !principalPermission(a.db, p, channel, write) {
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
