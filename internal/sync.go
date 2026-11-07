package internal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var syncHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("coordinator redirects are not supported; connect to its final URL")
	},
}

func remoteServerInfo(ctx context.Context, remote Remote, info *serverInfo) error {
	err := remoteRequest(ctx, remote, "GET", "/v1/server", nil, info)
	var failure *remoteHTTPError
	if errors.As(err, &failure) && (failure.Status == http.StatusNotFound || failure.Status == http.StatusUnauthorized) {
		// Older private coordinators authenticate unknown paths before returning
		// 404, hence the 401 compatibility case.
		return remoteRequest(ctx, remote, "GET", "/v1/info", nil, info)
	}
	return err
}

func remoteRequest(ctx context.Context, remote Remote, method, path string, input, output any) error {
	if err := remoteRequestSlot(ctx, remote); err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, remote.URL+path, body)
	if err != nil {
		return err
	}
	if remote.Token != "" {
		req.Header.Set("Authorization", "Bearer "+remote.Token)
	}
	if remote.ServerID != "" {
		req.Header.Set("X-Grpvn-Server", remote.ServerID)
	}
	req.Header.Set("Accept", "application/json")
	if path == "/mcp" {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := syncHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&failure)
		if len(failure.Error) > 512 {
			failure.Error = failure.Error[:512]
		}
		return &remoteHTTPError{Status: resp.StatusCode, Message: failure.Error, RetryAt: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	// The largest page is 100 fully escaped 64 KiB messages plus metadata.
	dec := json.NewDecoder(io.LimitReader(resp.Body, 6*MaxBodyBytes*syncPageSize+1024*1024))
	if err := dec.Decode(output); err != nil {
		return fmt.Errorf("invalid coordinator response: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid coordinator response: expected one JSON value")
	}
	return nil
}

type SyncStats struct {
	Pushed int
	Pulled int
}

// SyncRemotes syncs sources independently. No network request holds a DB
// transaction, and a slow/broken source cannot prevent another syncing.
func SyncRemotes(ctx context.Context, db *sql.DB, name string) (SyncStats, error) {
	return syncScheduled(ctx, db, name, time.Second, true)
}

// SyncTarget is an explicit history request, including a channel the human
// has left. It uses the same coordinator lease, pacing and delivery cursor.
func SyncTarget(ctx context.Context, db *sql.DB, target string) error {
	alias, _, ok := splitRemoteTarget(target)
	if !ok {
		return nil
	}
	remote, err := getRemote(db, alias)
	if err != nil {
		return err
	}
	remote.DB = db
	owner, claimed, err := claimSync(db, *remote, true, time.Now())
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	run, cancel := context.WithTimeout(ctx, maxSyncDuration)
	defer cancel()
	var stats SyncStats
	err = refreshHumanRemote(run, db, remote)
	if err == nil {
		channels, readErr := RemoteChannels(db, alias)
		err = readErr
		if err == nil {
			for _, channel := range channels {
				if channel.Target != target {
					continue
				}
				stats.Pulled, err = pullBatch(*remote, db, func(request syncBatchRequest, response *syncBatchResponse) error {
					return remoteRequest(run, *remote, "POST", "/v1/sync", request, response)
				}, []RemoteChannel{channel})
				break
			}
		}
	}
	if err == nil {
		err = syncRemoteFiles(run, db, *remote)
	}
	finishErr := finishSync(db, *remote, owner, time.Second, stats, err)
	return errors.Join(err, finishErr)
}

func syncScheduled(ctx context.Context, db *sql.DB, name string, base time.Duration, force bool) (SyncStats, error) {
	remotes, err := ListRemotes(db)
	if err != nil {
		return SyncStats{}, err
	}
	for i := range remotes {
		credential, err := localCredential(db, remotes[i].ServerID, "")
		if err != nil {
			return SyncStats{}, err
		}
		remotes[i].Token, remotes[i].MemberID = credential.Secret, credential.ID
	}
	if name != "" {
		found := false
		for _, remote := range remotes {
			if remote.Name == name {
				remotes, found = []Remote{remote}, true
				break
			}
		}
		if !found {
			return SyncStats{}, fmt.Errorf("unknown remote %q", name)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var total SyncStats
	var failures []error
	slots := make(chan struct{}, 4)
	for _, remote := range remotes {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			remote.DB = db
			owner, acquired, claimErr := claimSync(db, remote, force, time.Now())
			if claimErr != nil {
				mu.Lock()
				failures = append(failures, claimErr)
				mu.Unlock()
				return
			}
			if !acquired {
				return
			}
			runCtx, cancel := context.WithTimeout(ctx, maxSyncDuration)
			stats, syncErr := syncRemote(runCtx, db, remote)
			cancel()
			saveErr := finishSync(db, remote, owner, base, stats, syncErr)
			mu.Lock()
			defer mu.Unlock()
			total.Pushed += stats.Pushed
			total.Pulled += stats.Pulled
			if err := errors.Join(syncErr, saveErr); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", remote.Name, err))
			}
		})
	}
	wg.Wait()
	sort.Slice(failures, func(i, j int) bool { return failures[i].Error() < failures[j].Error() })
	return total, errors.Join(failures...)
}

// SyncAvailable bounds automatic command/hook sync. Failure leaves the
// cached log usable and queued sends intact; explicit sync reports errors.
func SyncAvailable(ctx context.Context, db *sql.DB) (SyncStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if running, err := DaemonRunning(ctx, db); err != nil || running {
		return SyncStats{}, err
	}
	return SyncDue(ctx, db, "", time.Second)
}

func syncRemote(ctx context.Context, db *sql.DB, remote Remote) (SyncStats, error) {
	var stats SyncStats
	if err := refreshHumanRemote(ctx, db, &remote); err != nil {
		return stats, err
	}
	credential, err := localCredential(db, remote.ServerID, "")
	if err != nil {
		return stats, err
	}
	if credential.Status == "pending" {
		channels, err := RemoteChannels(db, remote.Name)
		if err != nil {
			return stats, err
		}
		var requested []string
		for _, channel := range channels {
			requested = append(requested, channel.Channel)
		}
		p, err := registerRemote(ctx, db, &remote, "", requested)
		if err != nil {
			return stats, err
		}
		if p.Status != "active" {
			return stats, &remoteHTTPError{Status: 403, Message: "Waiting for workspace admission"}
		}
	}
	var failures []error
	if err := refreshWorkspaceMetadata(ctx, db, remote); err != nil {
		failures = append(failures, err)
	}
	if err := syncWorkspacePreferences(ctx, db, remote); err != nil {
		failures = append(failures, err)
	}
	stats.Pushed, err = pushOutgoing(ctx, db, remote)
	if err != nil {
		failures = append(failures, err)
	}
	channels, err := RemoteChannels(db, remote.Name)
	if err != nil {
		return stats, errors.Join(append(failures, err)...)
	}
	var humanOwner string
	if db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&humanOwner) == nil {
		active := channels[:0]
		for _, channel := range channels {
			var read bool
			err := db.QueryRow("SELECT can_read FROM remote_channel_details WHERE remote_name=? AND channel=?", remote.Name, channel.Channel).Scan(&read)
			if (err == sql.ErrNoRows || read) && channelJoined(db, humanOwner, channel.Target) {
				active = append(active, channel)
			}
		}
		channels = active
	}
	stats.Pulled, err = pullBatch(remote, db, func(request syncBatchRequest, response *syncBatchResponse) error {
		return remoteRequest(ctx, remote, "POST", "/v1/sync", request, response)
	}, channels)
	if err != nil {
		failures = append(failures, err)
	}
	if err := syncRemoteFiles(ctx, db, remote); err != nil {
		failures = append(failures, err)
	}
	return stats, errors.Join(failures...)
}

func pushOutgoing(ctx context.Context, db *sql.DB, remote Remote) (int, error) {
	pushed := 0
	// Push in local commit order so an offline thread's parents arrive first.
	for {
		wire, localID, err := nextOutgoing(db, remote.Name)
		if err != nil {
			return pushed, err
		}
		if wire == nil {
			break
		}
		var ack wireMessage
		agent, err := agentRemote(ctx, db, remote, wire.Sender, wire.Target)
		if err != nil {
			return pushed, err
		}
		if err := remoteRequest(ctx, agent, "POST", "/v1/messages", wire, &ack); err != nil {
			return pushed, err
		}
		var humanID string
		if db.QueryRow("SELECT principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&humanID) == nil && ack.PrincipalID == humanID {
			wire.Sender = ack.Sender
		}
		if ack.Seq <= 0 || !sameWire(*wire, ack) {
			return pushed, fmt.Errorf("coordinator acknowledged a different message")
		}
		if _, err := db.Exec("UPDATE remote_messages SET remote_seq = ? WHERE remote_name = ? AND local_id = ? AND remote_seq IS NULL", ack.Seq, remote.Name, localID); err != nil {
			return pushed, err
		}
		pushed++
	}
	return pushed, nil
}

func nextOutgoing(db *sql.DB, name string) (*wireMessage, string, error) {
	var id, channel string
	err := db.QueryRow(`SELECT m.id, rc.channel FROM remote_messages rm
		JOIN messages m ON m.id = rm.local_id JOIN remote_channels rc ON rc.target = m.target
		WHERE rm.remote_name = ? AND rm.remote_seq IS NULL ORDER BY m.seq LIMIT 1`, name).Scan(&id, &channel)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	m, err := findMessageByID(db, id)
	if err != nil {
		return nil, "", err
	}
	wire := attributedWire(db, m)
	wire.Seq, wire.Target = 0, channel
	translate := func(local string) (string, error) {
		var remoteID string
		err := db.QueryRow("SELECT remote_id FROM remote_messages WHERE remote_name = ? AND local_id = ?", name, local).Scan(&remoteID)
		return remoteID, err
	}
	if wire.ID, err = translate(m.ID); err != nil {
		return nil, "", err
	}
	if wire.ChainRoot, err = translate(m.ChainRoot); err != nil {
		return nil, "", err
	}
	if m.ParentID != nil {
		parent, err := translate(*m.ParentID)
		if err != nil {
			return nil, "", err
		}
		wire.ParentID = &parent
	}
	if m.Correlation != nil {
		wire.Correlation = &wire.ID
	}
	return &wire, id, nil
}

func queueRemoteMessage(db dbtx, m *Message) error {
	if err := JoinRemoteTargets(db, m.Target); err != nil {
		return err
	}
	var name, channel string
	err := db.QueryRow("SELECT remote_name, channel FROM remote_channels WHERE target = ?", m.Target).Scan(&name, &channel)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	wire := toWire(m)
	wire.Target = channel
	if err := validateWire(wire); err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO remote_messages (remote_name, remote_id, local_id) VALUES (?, ?, ?)", name, m.ID, m.ID)
	if err == nil {
		err = wakeRemote(db, name)
	}
	m.Pending = err == nil
	return err
}

// SendSynced commits locally first, then attempts delivery. A network
// failure is a queued acknowledgement, not a failed/ambiguous local write.
func SendSynced(ctx context.Context, db *sql.DB, sender, target, body, defaultChannel string, ask bool, key string) (*Message, bool, error) {
	m, replayed, err := SendIdempotent(db, sender, target, body, defaultChannel, ask, key)
	if err != nil {
		return nil, false, err
	}
	var name string
	err = db.QueryRow("SELECT remote_name FROM remote_messages WHERE local_id = ? AND remote_seq IS NULL", m.ID).Scan(&name)
	if err == sql.ErrNoRows {
		return m, replayed, nil
	}
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	running, err := DaemonRunning(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if !running {
		_, _ = SyncDue(ctx, db, name, time.Second)
	} // status/error retained in remotes
	if err := db.QueryRow("SELECT remote_seq IS NULL FROM remote_messages WHERE local_id = ?", m.ID).Scan(&m.Pending); err != nil {
		return nil, false, err
	}
	return m, replayed, nil
}

func importPage(db *sql.DB, remote string, channel RemoteChannel, page messagePage) (int, error) {
	if len(page.Messages) > syncPageSize {
		return 0, fmt.Errorf("coordinator page exceeds message limit")
	}
	last := channel.Position
	for _, wire := range page.Messages {
		if err := validateWire(wire); err != nil {
			return 0, err
		}
		if wire.Target != channel.Channel || wire.Seq <= last {
			return 0, fmt.Errorf("coordinator returned a different channel or unordered sequence")
		}
		last = wire.Seq
	}
	if page.Cursor != last {
		return 0, fmt.Errorf("coordinator cursor does not match its page")
	}
	if len(page.Messages) == 0 {
		return 0, nil
	}
	tx, err := beginWrite(db)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	pulled := 0
	for _, wire := range page.Messages {
		id, err := localRemoteID(tx, remote, wire.ID)
		if err != nil {
			return 0, err
		}
		var seq sql.NullInt64
		if err := tx.QueryRow("SELECT remote_seq FROM remote_messages WHERE remote_name = ? AND remote_id = ?", remote, wire.ID).Scan(&seq); err != nil {
			return 0, err
		}
		// A delivered mapping survives gc: do not resurrect retained history
		// when another process imports a stale/retried page.
		if seq.Valid {
			continue
		}
		m := wire.message()
		m.ID, m.Target = id, channel.Target
		if err := queueMessageFiles(tx, remote, channel.Target, wire.Body); err != nil {
			return 0, err
		}
		if wire.OwnerID != "" {
			var own string
			_ = tx.QueryRow("SELECT c.principal_id FROM local_credentials c JOIN remotes r ON r.server_id=c.server_id WHERE r.name=? AND c.agent_name=''", remote).Scan(&own)
			if own != wire.OwnerID {
				m.Sender = registeredSender(wire.Sender, wire.OwnerName, wire.OwnerID)
			}
			if _, err := tx.Exec("INSERT INTO remote_attributions(local_id,principal_id,owner_id,approved) VALUES(?,?,?,?) ON CONFLICT(local_id) DO UPDATE SET principal_id=excluded.principal_id,owner_id=excluded.owner_id,approved=excluded.approved", id, wire.PrincipalID, wire.OwnerID, wire.OwnerApproved); err != nil {
				return 0, err
			}
		}
		if wire.Automated {
			if _, err := tx.Exec("INSERT OR IGNORE INTO automated_messages(message_id,activation_id,hops) VALUES(?,?,1)", id, "remote:"+wire.ID); err != nil {
				return 0, err
			}
		}
		m.ChainRoot, err = localRemoteID(tx, remote, wire.ChainRoot)
		if err != nil {
			return 0, err
		}
		if wire.ParentID != nil {
			parent, err := localRemoteID(tx, remote, *wire.ParentID)
			if err != nil {
				return 0, err
			}
			m.ParentID = &parent
		}
		if wire.Correlation != nil {
			m.Correlation = &m.ID
		}
		existing, err := findMessageByID(tx, id)
		if err == sql.ErrNoRows {
			if err := m.save(tx); err != nil {
				return 0, err
			}
			pulled++
		} else if err != nil {
			return 0, err
		} else if !sameWire(toWire(existing), toWire(m)) {
			return 0, fmt.Errorf("coordinator changed an immutable message")
		}
		if _, err := tx.Exec("UPDATE remote_messages SET remote_seq = ? WHERE remote_name = ? AND remote_id = ?", wire.Seq, remote, wire.ID); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec("UPDATE remote_channels SET position = MAX(position, ?) WHERE remote_name = ? AND channel = ?", page.Cursor, remote, channel.Channel); err != nil {
		return 0, err
	}
	return pulled, tx.Commit()
}

// Reserve mappings for thread references even when gc removed an ancestor
// from the coordinator. Mapping rows deliberately outlive log retention.
func localRemoteID(db dbtx, remote, id string) (string, error) {
	var local string
	err := db.QueryRow("SELECT local_id FROM remote_messages WHERE remote_name = ? AND remote_id = ?", remote, id).Scan(&local)
	if err == nil {
		return local, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	local = NewMessage("", "", nil).ID
	_, err = db.Exec("INSERT INTO remote_messages (remote_name, remote_id, local_id) VALUES (?, ?, ?)", remote, id, local)
	return local, err
}

func PendingRemoteCount(db *sql.DB, name string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM remote_messages rm JOIN messages m ON m.id = rm.local_id
		WHERE rm.remote_name = ? AND rm.remote_seq IS NULL`, name).Scan(&count)
	return count, err
}

func RemoteChannelTarget(name, channel string) string { return remoteTarget(name, channel) }

// SyncErrorLine keeps automatic CLI warnings on one line.
func SyncErrorLine(err error) string { return strings.ReplaceAll(err.Error(), "\n", "; ") }
