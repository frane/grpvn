package internal

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// The browser secret identifies an existing profile. Reauthorization still
// requires the browser owner's consent; the cookie never authorizes silently.
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
	return secret, credentialHash(secret), oauthAppKey(client, redirect), nil
}

func oauthAppKey(client, redirect string) string {
	u, _ := url.Parse(redirect)
	// Local clients use a new callback port on each login. Removing only
	// that port keeps their profile stable without grouping different apps.
	if u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" {
		u.Host = u.Hostname()
	}
	return credentialHash(strings.ToLower(strings.TrimSpace(client)) + "\n" + u.String())
}

func rememberedOAuthBrowser(db *sql.DB, r *http.Request, client, redirect, profile string) bool {
	cookie, err := r.Cookie("grpvn_profile")
	if err != nil {
		return false
	}
	p, err := existingOAuthProfile(db, credentialHash(cookie.Value), oauthAppKey(client, redirect))
	return err == nil && p != nil && p.ID == profile && trustedParticipant(db, p)
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
