package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxSyncChannels = 256

type syncPosition struct {
	Channel string `json:"channel"`
	After   int64  `json:"after"`
}
type syncBatchRequest struct {
	Channels []syncPosition `json:"channels"`
}
type syncChannelError struct {
	Channel string `json:"channel"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}
type syncBatchResponse struct {
	ServerID string                 `json:"server_id"`
	Pages    map[string]messagePage `json:"pages"`
	Errors   []syncChannelError     `json:"errors,omitempty"`
	More     bool                   `json:"more"`
}

func batchHandler(db *sql.DB, access *channelAccess, serverID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request syncBatchRequest
		if err := decoder.Decode(&request); err != nil {
			apiError(w, 400, "invalid sync JSON")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			apiError(w, 400, "expected one sync request")
			return
		}
		if len(request.Channels) == 0 || len(request.Channels) > maxSyncChannels {
			apiError(w, 400, "sync takes 1 to 256 channels")
			return
		}
		result := syncBatchResponse{ServerID: serverID, Pages: map[string]messagePage{}}
		var predicates []string
		var args []any
		for _, position := range request.Channels {
			if err := validateChannel(position.Channel); err != nil || position.After < 0 {
				apiError(w, 400, "invalid channel or position")
				return
			}
			if _, duplicate := result.Pages[position.Channel]; duplicate {
				apiError(w, 400, "duplicate channel")
				return
			}
			if !access.isServed(position.Channel) {
				result.Errors = append(result.Errors, syncChannelError{position.Channel, 403, "channel is not served"})
				continue
			}
			if !access.canAccess(r, position.Channel) {
				if prospectiveChannel(db, access, requestPrincipal(r), position.Channel) {
					result.Pages[position.Channel] = messagePage{Messages: []wireMessage{}, Cursor: position.After}
					continue
				}
				result.Errors = append(result.Errors, syncChannelError{position.Channel, 401, "private channel requires authorization"})
				continue
			}
			result.Pages[position.Channel] = messagePage{Messages: []wireMessage{}, Cursor: position.After}
			predicates = append(predicates, "(target = ? AND seq > ?)")
			args = append(args, position.Channel, position.After)
		}
		if len(predicates) > 0 {
			query := `SELECT seq, id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at
			FROM messages m WHERE (` + strings.Join(predicates, " OR ") + `) AND NOT EXISTS
			(SELECT 1 FROM remote_messages rm WHERE rm.local_id = m.id) ORDER BY seq LIMIT ?`
			args = append(args, syncPageSize)
			rows, err := db.QueryContext(r.Context(), query, args...)
			if err != nil {
				apiError(w, 500, "cannot read sync log")
				return
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				m, err := scanMessage(rows, true)
				if err != nil {
					apiError(w, 500, "cannot read message")
					return
				}
				page := result.Pages[m.Target]
				page.Messages, page.Cursor = append(page.Messages, toWire(m)), m.Seq
				result.Pages[m.Target] = page
				count++
			}
			if err := rows.Err(); err != nil {
				apiError(w, 500, "cannot read sync log")
				return
			}
			result.More = count == syncPageSize
		}
		for channel, page := range result.Pages {
			for i := range page.Messages {
				page.Messages[i] = attributedWire(db, page.Messages[i].message())
			}
			result.Pages[channel] = page
		}
		writeJSON(w, 200, result)
	}
}

func pullBatch(ctxRemote Remote, db *sql.DB, ctxRequest func(syncBatchRequest, *syncBatchResponse) error, channels []RemoteChannel) (int, error) {
	total := 0
	for start := 0; start < len(channels); start += maxSyncChannels {
		end := start + maxSyncChannels
		if end > len(channels) {
			end = len(channels)
		}
		group := channels[start:end]
		for {
			request := syncBatchRequest{}
			for _, ch := range group {
				request.Channels = append(request.Channels, syncPosition{ch.Channel, ch.Position})
			}
			var response syncBatchResponse
			if err := ctxRequest(request, &response); err != nil {
				return total, err
			}
			if response.ServerID != ctxRemote.ServerID {
				return total, fmt.Errorf("coordinator identity changed; refusing to mix logs")
			}
			var failures []error
			for i, ch := range group {
				if page, ok := response.Pages[ch.Channel]; ok {
					pulled, err := importPage(db, ctxRemote.Name, ch, page)
					if err != nil {
						return total, err
					}
					total += pulled
					group[i].Position = page.Cursor
				} else {
					found := false
					for _, failure := range response.Errors {
						if failure.Channel == ch.Channel {
							failures = append(failures, &remoteHTTPError{Status: failure.Status, Message: ch.Channel + ": " + failure.Message})
							found = true
							break
						}
					}
					if !found {
						return total, fmt.Errorf("coordinator omitted channel %s", ch.Channel)
					}
				}
			}
			if len(failures) > 0 {
				return total, failures[0]
			}
			if !response.More {
				break
			}
		}
	}
	return total, nil
}
