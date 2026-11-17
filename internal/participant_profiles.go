package internal

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type ParticipantProfile struct {
	Role        string `json:"role"`
	Runtime     string `json:"runtime"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"`
	OwnerName   string `json:"owner_name"`
	LastSeen    int64  `json:"last_seen"`
}
type ProfileUpdate struct {
	Name        *string
	Role        *string
	Runtime     *string
	Description *string
	OwnerID     *string
}

func readParticipantProfile(db dbtx, id, name string) ParticipantProfile {
	profile := ParticipantProfile{}
	_ = db.QueryRow("SELECT role,runtime,description,owner_id FROM participant_profiles WHERE identity=?", id).Scan(&profile.Role, &profile.Runtime, &profile.Description, &profile.OwnerID)
	if profile.OwnerID == "" {
		_ = db.QueryRow("SELECT owner.id FROM principals agent JOIN principals owner ON owner.id=agent.parent_id WHERE agent.id=? AND owner.kind='human'", id).Scan(&profile.OwnerID)
	}
	if profile.OwnerID != "" {
		_ = db.QueryRow("SELECT name FROM principals WHERE id=? AND kind='human'", profile.OwnerID).Scan(&profile.OwnerName)
	}
	var runtime string
	var last int64
	_ = db.QueryRow("SELECT runtime,heartbeat_at FROM agent_sessions WHERE agent_name=? ORDER BY heartbeat_at DESC LIMIT 1", name).Scan(&runtime, &last)
	if profile.Runtime == "" {
		profile.Runtime = runtime
	}
	if profile.Runtime == "" {
		_ = db.QueryRow("SELECT c.name FROM oauth_requests r JOIN oauth_clients c ON c.id=r.client_id WHERE r.principal_id=? ORDER BY r.expires_at DESC LIMIT 1", id).Scan(&profile.Runtime)
	}
	_ = db.QueryRow("SELECT last_seen FROM participant_activity WHERE identity=?", id).Scan(&profile.LastSeen)
	if last > profile.LastSeen {
		profile.LastSeen = last
	}
	return profile
}
func enrichParticipant(db dbtx, person map[string]any, viewer *Principal) {
	id, _ := person["id"].(string)
	name, _ := person["name"].(string)
	profile := readParticipantProfile(db, id, name)
	if p, err := findPrincipal(db, "id=?", id); err == nil && cachePrincipal(db, p) == nil {
		aliases := []string{}
		for alias := range p.aliases {
			aliases = append(aliases, alias)
		}
		person["aliases"] = aliases
	}
	person["role"], person["runtime"], person["description"] = profile.Role, profile.Runtime, profile.Description
	person["owner_id"], person["owner_name"], person["last_seen"] = profile.OwnerID, profile.OwnerName, profile.LastSeen
	person["can_edit"] = viewer != nil && person["kind"] == "agent" && (viewer.ID == id || profile.OwnerID == viewer.ID || principalPermission(db, viewer, "*", true))
}
func updateParticipantProfile(db *sql.DB, id string, update ProfileUpdate, mayAssignOwner bool) error {
	if update.Role == nil && update.Runtime == nil && update.Description == nil && update.OwnerID == nil {
		return nil
	}
	for _, field := range []struct {
		value *string
		limit int
	}{{update.Role, 80}, {update.Runtime, 80}, {update.Description, 1200}} {
		if field.value != nil && (!utf8.ValidString(*field.value) || len(*field.value) > field.limit || strings.ContainsRune(*field.value, 0)) {
			return fmt.Errorf("profile field is too long or invalid")
		}
	}
	if update.OwnerID != nil && !mayAssignOwner {
		return fmt.Errorf("only a human administrator or the current owner can assign ownership")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if update.OwnerID != nil && *update.OwnerID != "" {
		var valid bool
		if tx.QueryRow("SELECT EXISTS(SELECT 1 FROM principals WHERE id=? AND kind='human' AND status='active')", *update.OwnerID).Scan(&valid) != nil || !valid {
			return fmt.Errorf("choose an existing human owner")
		}
	}
	if _, err = tx.Exec("INSERT INTO participant_profiles(identity) VALUES(?) ON CONFLICT(identity) DO NOTHING", id); err != nil {
		return err
	}
	for _, field := range []struct {
		column string
		value  *string
	}{{"role", update.Role}, {"runtime", update.Runtime}, {"description", update.Description}, {"owner_id", update.OwnerID}} {
		if field.value != nil {
			if _, err = tx.Exec("UPDATE participant_profiles SET "+field.column+"=? WHERE identity=?", strings.TrimSpace(*field.value), id); err != nil {
				return err
			}
		}
	}
	if err = TouchChatMetadata(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Local profiles retain the ID the UI already used before named profiles:
// local:<original handle>. Aliases keep that ID through later name changes.
func EnsureLocalAgent(db *sql.DB, name string) (*Principal, error) {
	if p, err := participantByAddress(db, name); err == nil {
		if p.Kind != "agent" {
			return nil, fmt.Errorf("this identity is a human profile")
		}
		return p, nil
	}
	id := "local:" + name
	tx, err := beginWrite(db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO principals(id,credential_hash,name,kind,status,approval_code,created_at) VALUES(?,?,?,'agent','active',?,?) ON CONFLICT(id) DO NOTHING", id, credentialHash(NewMessage("", "", nil).ID), name, "local-"+NewMessage("", "", nil).ID, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("INSERT INTO principal_aliases(address,principal_id) VALUES(?,?) ON CONFLICT(address) DO NOTHING", name, id); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO permissions(principal_id,channel,can_read,can_write)
 SELECT ?,m.target,1,COALESCE((SELECT g.can_write FROM local_channel_grants g WHERE g.name=? AND g.channel=m.target),1)
 FROM messages m WHERE m.sender=? AND m.target LIKE '#%' AND NOT EXISTS(SELECT 1 FROM remote_messages r WHERE r.local_id=m.id) GROUP BY m.target
 ON CONFLICT(principal_id,channel) DO NOTHING`, id, name, name); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("INSERT INTO message_principals(message_id,principal_id) SELECT m.id,? FROM messages m WHERE m.sender=? AND NOT EXISTS(SELECT 1 FROM remote_messages r WHERE r.local_id=m.id) ON CONFLICT(message_id) DO NOTHING", id, name); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("INSERT INTO idempotency(agent_name,key,message_id,created_at) SELECT ?,key,message_id,created_at FROM idempotency WHERE agent_name=? ON CONFLICT(agent_name,key) DO NOTHING", id, name); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	p, err := findPrincipal(db, "id=?", id)
	if err == nil {
		err = cachePrincipal(db, p)
	}
	return p, err
}
func RenameLocalAgent(db *sql.DB, name, chosen string) (*Principal, error) {
	p, err := EnsureLocalAgent(db, name)
	if err != nil {
		return nil, err
	}
	return RenamePrincipal(db, p.ID, chosen)
}

func UpdateLocalProfile(db *sql.DB, id, name string, update ProfileUpdate) (map[string]any, error) {
	if err := updateParticipantProfile(db, id, update, false); err != nil {
		return nil, err
	}
	value := map[string]any{"id": id, "name": name, "kind": "agent"}
	enrichParticipant(db, value, nil)
	return value, nil
}
func RefreshLocalStateIdentity(db *sql.DB, state *State, path, name string) (string, error) {
	var id string
	if db.QueryRow("SELECT principal_id FROM principal_aliases WHERE address=?", name).Scan(&id) != nil {
		return name, nil
	}
	p, err := findPrincipal(db, "id=?", id)
	if err != nil {
		return name, err
	}
	if err = cachePrincipal(db, p); err != nil {
		return name, err
	}
	current := principalAddress(p)
	if current != name && state.Name == name {
		state.Name = current
		if err = state.Save(path); err != nil {
			return name, err
		}
	}
	return current, nil
}

func inboxAliases(db dbtx, name string) []string {
	list := []string{"@" + name}
	rows, err := db.Query("SELECT address FROM principal_aliases WHERE principal_id=(SELECT principal_id FROM principal_aliases WHERE address=?) AND address<>? ORDER BY address", name, name)
	if err != nil {
		return list
	}
	defer rows.Close()
	for rows.Next() {
		var alias string
		if rows.Scan(&alias) == nil {
			list = append(list, "@"+alias)
		}
	}
	return list
}
func (s *webServer) participantProfile(w http.ResponseWriter, r *http.Request, viewer *Principal) bool {
	if !strings.HasPrefix(r.URL.Path, "/v1/chat/participants/") || r.Method != "PATCH" {
		return false
	}
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/chat/participants/"))
	if err != nil {
		apiError(w, 400, "Invalid profile")
		return true
	}
	var update ProfileUpdate
	if !webDecode(w, r, &update) {
		return true
	}
	person, err := findPrincipal(s.db, "id=?", id)
	if err == nil {
		err = cachePrincipal(s.db, person)
	}
	localName := strings.TrimPrefix(id, "local:")
	if err != nil && s.local && strings.HasPrefix(id, "local:") {
		var known bool
		_ = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE sender=?) OR EXISTS(SELECT 1 FROM agent_sessions WHERE agent_name=?)", localName, localName).Scan(&known)
		if !known {
			apiError(w, 404, "Agent not found")
			return true
		}
	} else if err != nil || person.Kind != "agent" {
		apiError(w, 404, "Agent not found")
		return true
	}
	name := localName
	if person != nil {
		name = principalAddress(person)
	}
	profile := readParticipantProfile(s.db, id, name)
	admin := viewer.Kind == "human" && principalPermission(s.db, viewer, "*", true)
	owner := viewer.Kind == "human" && profile.OwnerID == viewer.ID
	if viewer.ID != id && !admin && !owner {
		apiError(w, 403, "Only this agent, its owner or an administrator can edit its profile")
		return true
	}
	if update.Name != nil {
		if person == nil {
			person, err = RenameLocalAgent(s.db, localName, *update.Name)
		} else {
			person, err = RenamePrincipal(s.db, id, *update.Name)
		}
		if err != nil {
			apiError(w, 400, err.Error())
			return true
		}
		name = principalAddress(person)
	}
	if err = updateParticipantProfile(s.db, id, update, admin || owner); err != nil {
		apiError(w, 400, err.Error())
		return true
	}
	result := map[string]any{"id": id, "name": name, "kind": "agent", "status": "active"}
	enrichParticipant(s.db, result, viewer)
	writeJSON(w, 200, result)
	return true
}
