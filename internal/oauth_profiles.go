package internal

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// The browser secret proposes an existing profile; it never authorizes a
// client. Every new OAuth request still requires its own admin approval.
func browserProfile(r *http.Request, client, redirect string) (string, string, string, error) {
	secret := ""
	if cookie, err := r.Cookie("grpvn_profile"); err == nil && len(cookie.Value) >= 43 && len(cookie.Value) <= 256 {
		secret = cookie.Value
	}
	if secret == "" {
		var err error
		secret, err = newCredential()
		if err != nil {
			return "", "", "", err
		}
	}
	u, _ := url.Parse(redirect)
	// Local clients use a new callback port on each login. Removing only
	// that port keeps their profile stable without grouping different apps.
	if u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" {
		u.Host = u.Hostname()
	}
	key := credentialHash(strings.ToLower(strings.TrimSpace(client)) + "\n" + u.String())
	return secret, credentialHash(secret), key, nil
}

func existingOAuthProfile(db dbtx, browser, app string) (*Principal, error) {
	var id string
	err := db.QueryRow("SELECT principal_id FROM oauth_profiles WHERE browser_hash=? AND app_key=?", browser, app).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return findPrincipal(db, "id=?", id)
}
