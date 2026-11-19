package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

func seedHumanReadOutbox(db *sql.DB, remote Remote, owner string) error {
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockHumanRead(db, tx, owner); err != nil {
		return err
	}
	var seeded bool
	_ = tx.QueryRow("SELECT bootstrapped FROM remote_read_positions WHERE remote_name=?", remote.Name).Scan(&seeded)
	if seeded {
		return nil
	}
	p, err := findPrincipal(tx, "id=?", owner)
	if err != nil {
		return err
	}
	service := &webServer{local: true}
	rows, err := tx.Query(`SELECT c.target FROM remote_channels c WHERE c.remote_name=? UNION SELECT d.target FROM remote_direct_threads d WHERE d.remote_name=? AND d.local_principal_id=?`, remote.Name, remote.Name, owner)
	if err != nil {
		return err
	}
	targets := []string{}
	for rows.Next() {
		var target string
		if err = rows.Scan(&target); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, target := range targets {
		var pending bool
		_ = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM human_read_outbox WHERE remote_name=? AND principal_id=? AND target=? AND kind='conversation')", remote.Name, owner, target).Scan(&pending)
		if pending {
			continue
		}
		change := conversationReadSnapshot(tx, owner, target, "read")
		if change.Held != nil {
			change.Mutation = "unread"
		}
		if change.Position > 0 || change.Held != nil {
			if err = service.recordHumanRead(tx, p, change); err != nil {
				return err
			}
		}
	}
	rows, err = tx.Query("SELECT t.root_id,t.target,t.position,t.following FROM human_threads t JOIN remote_messages rm ON rm.local_id=t.root_id WHERE t.principal_id=? AND rm.remote_name=?", owner, remote.Name)
	if err != nil {
		return err
	}
	threads := []humanReadChange{}
	for rows.Next() {
		change := humanReadChange{Kind: "thread", Mutation: "follow"}
		if err = rows.Scan(&change.RootID, &change.Target, &change.Position, &change.Following); err != nil {
			rows.Close()
			return err
		}
		threads = append(threads, change)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, change := range threads {
		var pending bool
		_ = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM human_read_outbox WHERE remote_name=? AND principal_id=? AND kind='thread' AND root_id=?)", remote.Name, owner, change.RootID).Scan(&pending)
		if !pending {
			if err = service.recordHumanRead(tx, p, change); err != nil {
				return err
			}
		}
	}
	rows, err = tx.Query("SELECT i.message_id FROM human_inbox i JOIN remote_messages rm ON rm.local_id=i.message_id WHERE i.principal_id=? AND i.read_at>0 AND rm.remote_name=?", owner, remote.Name)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for start := 0; start < len(ids); start += 100 {
		if err = service.recordHumanRead(tx, p, humanReadChange{Kind: "inbox", IDs: ids[start:min(start+100, len(ids))]}); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO remote_read_positions(remote_name,bootstrapped) VALUES(?,1) ON CONFLICT(remote_name) DO UPDATE SET bootstrapped=1", remote.Name); err != nil {
		return err
	}
	return tx.Commit()
}

func syncHumanReads(ctx context.Context, db *sql.DB, remote Remote, messagesChanged bool) error {
	var owner string
	if db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&owner) != nil {
		return nil
	}
	if err := seedHumanReadOutbox(db, remote, owner); err != nil {
		return err
	}
	type pendingRead struct{ id, kind, target, root, payload string }
	pending := []pendingRead{}
	rows, err := db.Query("SELECT id,kind,target,root_id,payload FROM human_read_outbox WHERE remote_name=? AND principal_id=? ORDER BY id LIMIT 4", remote.Name, owner)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item pendingRead
		if err = rows.Scan(&item.id, &item.kind, &item.target, &item.root, &item.payload); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range pending {
		var change humanReadChange
		if err = json.Unmarshal([]byte(item.payload), &change); err != nil {
			return err
		}
		path := "/v1/chat/read-markers"
		method := "POST"
		request := map[string]any{}
		switch change.Kind {
		case "conversation":
			request["Target"] = change.Target
			request["Position"] = change.Position
			if change.Mutation == "unread" {
				request["Unread"] = true
				if change.Held != nil {
					request["Position"] = *change.Held
				}
			}
			if change.Mutation == "resume" {
				request["Resume"] = true
				if change.ExpectedVersion != nil {
					request["ExpectedVersion"] = *change.ExpectedVersion
				}
			}
		case "thread":
			path = "/v1/chat/threads"
			method = "PATCH"
			request["RootID"] = change.RootID
			request["Position"] = change.Position
			if change.Mutation == "follow" {
				request["Following"] = change.Following
			}
		case "inbox":
			path = "/v1/chat/activity"
			method = "PATCH"
			request["IDs"] = change.IDs
		default:
			return fmt.Errorf("invalid queued human read state")
		}
		var result struct {
			State *humanReadChange `json:"read_state"`
		}
		if err = remoteRequest(ctx, remote, method, path, request, &result); err != nil {
			var failure *remoteHTTPError
			if errors.As(err, &failure) && (failure.Status == 400 || failure.Status == 403 || failure.Status == 404 || failure.Status == 405) {
				_, _ = db.Exec("DELETE FROM human_read_outbox WHERE id=? AND principal_id=?", item.id, owner)
				continue
			}
			return err
		}
		tx, err := beginWrite(db)
		if err != nil {
			return err
		}
		if err = lockHumanRead(db, tx, owner); err != nil {
			tx.Rollback()
			return err
		}
		removed, err := tx.Exec("DELETE FROM human_read_outbox WHERE id=? AND principal_id=?", item.id, owner)
		if err == nil {
			n, _ := removed.RowsAffected()
			if n > 0 && result.State != nil {
				err = applyRemoteHumanRead(tx, remote.Name, owner, *result.State)
			}
		}
		if err == nil {
			err = touchPersonalMetadata(tx, owner)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	mappingRows, err := db.Query("SELECT 'c:'||channel FROM remote_channels WHERE remote_name=? UNION SELECT 'd:'||peer_id||':'||peer_name FROM remote_direct_threads WHERE remote_name=? AND local_principal_id=? ORDER BY 1", remote.Name, remote.Name, owner)
	if err != nil {
		return err
	}
	mappingItems := []string{}
	for mappingRows.Next() {
		var item string
		if err = mappingRows.Scan(&item); err != nil {
			mappingRows.Close()
			return err
		}
		mappingItems = append(mappingItems, item)
	}
	err = mappingRows.Err()
	mappingRows.Close()
	if err != nil {
		return err
	}
	encodedMapping, _ := json.Marshal(mappingItems)
	mapping := string(encodedMapping)
	var previousMapping string
	var position int64
	_ = db.QueryRow("SELECT position,mapping FROM remote_read_positions WHERE remote_name=?", remote.Name).Scan(&position, &previousMapping)
	if previousMapping != mapping {
		position = 0
		if _, err = db.Exec("UPDATE remote_read_positions SET position=0,mapping=? WHERE remote_name=?", mapping, remote.Name); err != nil {
			return err
		}
	}

	for round := 0; round < 10; round++ {
		var page humanReadPage
		err = remoteRequest(ctx, remote, "GET", "/v1/chat/read-markers?after="+strconv.FormatInt(position, 10), nil, &page)
		var failure *remoteHTTPError
		if errors.As(err, &failure) && (failure.Status == http.StatusNotFound || failure.Status == http.StatusMethodNotAllowed) {
			return nil
		}
		if err != nil {
			return err
		}
		if page.Cursor < position || len(page.Changes) > 100 {
			return fmt.Errorf("invalid account read-state cursor")
		}
		if len(page.Changes) == 0 {
			break
		}
		tx, err := beginWrite(db)
		if err != nil {
			return err
		}
		if err = lockHumanRead(db, tx, owner); err != nil {
			tx.Rollback()
			return err
		}
		for _, change := range page.Changes {
			if change.Seq <= position || change.Seq > page.Cursor || change.Position < 0 {
				err = fmt.Errorf("invalid account read-state event")
				break
			}
			if err = applyRemoteHumanRead(tx, remote.Name, owner, change); err != nil {
				break
			}
		}
		if err == nil {
			_, err = tx.Exec("UPDATE remote_read_positions SET position=? WHERE remote_name=?", page.Cursor, remote.Name)
		}
		if err == nil {
			err = touchPersonalMetadata(tx, owner)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if page.Cursor == position && page.More {
			return fmt.Errorf("account read-state cursor did not advance")
		}
		position = page.Cursor
		if !page.More {
			break
		}
		if round == 9 {
			_ = wakeRemote(db, remote.Name)
		}
	}
	if messagesChanged {
		rows, err := db.Query("SELECT payload FROM remote_read_states WHERE remote_name=?", remote.Name)
		if err != nil {
			return err
		}
		states := []humanReadChange{}
		for rows.Next() {
			var payload string
			if err = rows.Scan(&payload); err != nil {
				rows.Close()
				return err
			}
			var state humanReadChange
			if err = json.Unmarshal([]byte(payload), &state); err != nil {
				rows.Close()
				return err
			}
			states = append(states, state)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(states) > 0 {
			tx, err := beginWrite(db)
			if err != nil {
				return err
			}
			if err = lockHumanRead(db, tx, owner); err != nil {
				tx.Rollback()
				return err
			}
			for _, state := range states {
				if err = applyRemoteHumanRead(tx, remote.Name, owner, state); err != nil {
					break
				}
			}
			if err == nil {
				err = touchPersonalMetadata(tx, owner)
			}
			if err != nil {
				tx.Rollback()
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}
