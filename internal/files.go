package internal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const MaxFileBytes = 10 * 1024 * 1024

var fileLinks = regexp.MustCompile(`grpvn-file:([0-9A-HJKMNP-TV-Z]{26})`)
var fileHTTPClient = &http.Client{Timeout: 18 * time.Second, CheckRedirect: syncHTTPClient.CheckRedirect}

type ChatFile struct {
	ID, Target, Uploader, Name, MediaType string
	Data                                  []byte
	CreatedAt                             int64
}

func fileImage(media string) bool {
	return media == "image/png" || media == "image/jpeg" || media == "image/gif" || media == "image/webp"
}
func fileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." {
		name = "file"
	}
	if len(name) > 180 {
		name = string([]rune(name)[:min(60, len([]rune(name)))])
	}
	return name
}
func storeChatFile(db dbtx, f ChatFile) error {
	_, err := db.Exec("INSERT INTO chat_files(id,target,uploader,name,media_type,data,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id,target) DO NOTHING", f.ID, f.Target, f.Uploader, f.Name, f.MediaType, f.Data, f.CreatedAt)
	return err
}
func loadChatFile(db dbtx, id, target string) (ChatFile, error) {
	var f ChatFile
	f.ID = id
	f.Target = target
	err := db.QueryRow("SELECT uploader,name,media_type,data,created_at FROM chat_files WHERE id=? AND target=?", id, target).Scan(&f.Uploader, &f.Name, &f.MediaType, &f.Data, &f.CreatedAt)
	return f, err
}

// ReadChatFile reads one explicit attachment without moving message cursors.
// A missing remote file is fetched with the source's existing credential and
// pacing, then retained in the same local database for offline reads.
func ReadChatFile(ctx context.Context, db *sql.DB, id, target string) (ChatFile, error) {
	f, err := loadChatFile(db, id, target)
	if err != sql.ErrNoRows {
		return f, err
	}
	alias, native, ok := splitRemoteTarget(target)
	if !ok {
		return f, err
	}
	remote, err := getRemote(db, alias)
	if err != nil {
		return f, err
	}
	remote.DB = db
	f, err = downloadRemoteFile(ctx, *remote, id, native)
	if err != nil {
		return f, err
	}
	f.Target = target
	return f, storeChatFile(db, f)
}
func fileResult(f ChatFile) map[string]any {
	return map[string]any{"id": f.ID, "name": f.Name, "type": f.MediaType, "size": len(f.Data), "image": fileImage(f.MediaType), "url": "/v1/files/" + f.ID + "?" + url.Values{"target": {f.Target}}.Encode()}
}
func (s *webServer) fileAllowed(p *Principal, f ChatFile, write bool) bool {
	if p == nil {
		return !write && strings.HasPrefix(f.Target, "#") && s.access.isServed(f.Target) && s.access.isOpen(f.Target)
	}
	if strings.HasPrefix(f.Target, "@") {
		return (f.Uploader == p.ID || principalOwnsAddress(p, strings.TrimPrefix(f.Target, "@"))) && principalPermission(s.db, p, "@*", write)
	}
	if p.Kind == "member" && !write {
		return s.access.isServed(f.Target) && (s.access.isOpen(f.Target) || principalPermission(s.db, p, f.Target, false))
	}
	return s.can(p, f.Target, write)
}
func mountFiles(mux *http.ServeMux, db *sql.DB, access *channelAccess, base string, local bool) {
	s := &webServer{db: db, access: access, base: base, local: local, attempts: map[string][]time.Time{}}
	var id string
	if db.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&id) == nil {
		s.cookieName = webSessionCookie + "_" + credentialHash(id)[:12]
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := s.actor(r)
		public := r.Method == "GET" && strings.HasPrefix(r.URL.Query().Get("target"), "#") && s.access.isServed(r.URL.Query().Get("target")) && s.access.isOpen(r.URL.Query().Get("target"))
		if (!public && (p == nil || p.Status != "active")) || (r.Method != "GET" && !isChatParticipant(p)) {
			apiError(w, 401, "Sign in to access files")
			return
		}
		if r.Method == "POST" && r.Header.Get("Authorization") == "" {
			origin := base
			if origin == "" {
				origin = "http://" + r.Host
			}
			if r.Header.Get("Origin") != origin {
				apiError(w, 403, "Open grpvn in its own window")
				return
			}
		}
		if r.Method == "POST" && r.URL.Path == "/v1/files" {
			s.uploadFile(w, r, p)
			return
		}
		if r.Method == "GET" {
			s.downloadFile(w, r, p)
			return
		}
		apiError(w, 405, "Method not allowed")
	})
	mux.Handle("/v1/files", handler)
	mux.Handle("/v1/files/", handler)
}
func (s *webServer) uploadFile(w http.ResponseWriter, r *http.Request, p *Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxFileBytes+64*1024)
	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
		apiError(w, 413, "Choose a file up to 10 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	target := r.FormValue("target")
	file, header, err := r.FormFile("file")
	if err != nil {
		apiError(w, 400, "Choose a file")
		return
	}
	defer file.Close()
	if !(strings.HasPrefix(target, "#") || strings.HasPrefix(target, "@")) || !s.can(p, target, true) {
		apiError(w, 403, "Channel write access required")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil || len(data) > MaxFileBytes {
		apiError(w, 413, "Choose a file up to 10 MB")
		return
	}
	f := ChatFile{ID: NewMessage("", "", nil).ID, Target: target, Uploader: p.ID, Name: fileName(header.Filename), MediaType: http.DetectContentType(data), Data: data, CreatedAt: time.Now().UnixMilli()}
	if remote, native, ok, err := s.remoteChannel(r.Context(), p, target); ok {
		if err != nil {
			apiError(w, 403, err.Error())
			return
		}
		uploaded, err := uploadRemoteFile(r.Context(), remote, native, f)
		if err != nil {
			apiError(w, 502, "File upload failed. Try again when the workspace is available.")
			return
		}
		f.ID = uploaded.ID
	}
	if err = storeChatFile(s.db, f); err != nil {
		apiError(w, 500, "Could not save file")
		return
	}
	writeJSON(w, 201, fileResult(f))
}
func (s *webServer) downloadFile(w http.ResponseWriter, r *http.Request, p *Principal) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	target := r.URL.Query().Get("target")
	if !validMessageID(id) || target == "" {
		apiError(w, 400, "File and conversation required")
		return
	}
	f, err := loadChatFile(s.db, id, target)
	if err == sql.ErrNoRows && s.local && s.can(p, target, false) {
		if remote, native, ok, accessErr := s.remoteChannel(r.Context(), p, target); ok && accessErr == nil {
			f, err = downloadRemoteFile(r.Context(), remote, id, native)
			if err == nil {
				f.Target = target
				f.Uploader = p.ID
				err = storeChatFile(s.db, f)
			}
		}
	}
	if err != nil || !s.fileAllowed(p, f, false) {
		apiError(w, 404, "File not found in this conversation")
		return
	}
	disposition := "attachment"
	if fileImage(f.MediaType) && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	}
	media := f.MediaType
	if !fileImage(media) {
		media = "application/octet-stream"
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	w.Header().Set("X-Grpvn-File-Name", base64.RawURLEncoding.EncodeToString([]byte(f.Name)))
	w.Header().Set("X-Grpvn-File-Type", f.MediaType)
	http.ServeContent(w, r, f.Name, time.UnixMilli(f.CreatedAt), bytes.NewReader(f.Data))
}
func fileRequest(ctx context.Context, remote Remote, method, path, contentType string, body io.Reader) (*http.Response, error) {
	if err := remoteRequestSlot(ctx, remote); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, method, remote.URL+path, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+remote.Token)
	r.Header.Set("X-Grpvn-Server", remote.ServerID)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	response, err := fileHTTPClient.Do(r)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, &remoteHTTPError{Status: response.StatusCode, Message: "File transfer failed", RetryAt: retryAfter(response.Header.Get("Retry-After"), time.Now())}
	}
	return response, nil
}
func uploadRemoteFile(ctx context.Context, remote Remote, target string, f ChatFile) (ChatFile, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("target", target)
	part, err := form.CreateFormFile("file", f.Name)
	if err != nil {
		return f, err
	}
	_, _ = part.Write(f.Data)
	_ = form.Close()
	response, err := fileRequest(ctx, remote, "POST", "/v1/files", form.FormDataContentType(), &body)
	if err != nil {
		return f, err
	}
	defer response.Body.Close()
	var metadata struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&metadata)
	if err != nil || !validMessageID(metadata.ID) {
		return f, fmt.Errorf("Invalid file acknowledgement")
	}
	f.ID = metadata.ID
	return f, nil
}
func downloadRemoteFile(ctx context.Context, remote Remote, id, target string) (ChatFile, error) {
	f := ChatFile{ID: id, Target: target, CreatedAt: time.Now().UnixMilli()}
	response, err := fileRequest(ctx, remote, "GET", "/v1/files/"+id+"?"+url.Values{"target": {target}}.Encode(), "", nil)
	if err != nil {
		return f, err
	}
	defer response.Body.Close()
	f.Data, err = io.ReadAll(io.LimitReader(response.Body, MaxFileBytes+1))
	if err != nil || len(f.Data) > MaxFileBytes {
		return f, fmt.Errorf("File exceeds 10 MB")
	}
	name, _ := base64.RawURLEncoding.DecodeString(response.Header.Get("X-Grpvn-File-Name"))
	f.Name = fileName(string(name))
	f.MediaType = http.DetectContentType(f.Data)
	return f, nil
}
func queueMessageFiles(db dbtx, remote, target, body string) error {
	for _, match := range fileLinks.FindAllStringSubmatch(body, 5) {
		if _, err := db.Exec("INSERT INTO remote_file_queue(remote_name,file_id,target) VALUES(?,?,?) ON CONFLICT(remote_name,file_id,target) DO NOTHING", remote, match[1], target); err != nil {
			return err
		}
	}
	return nil
}
func syncRemoteFiles(ctx context.Context, db *sql.DB, remote Remote) error {
	rows, err := db.Query("SELECT file_id,target FROM remote_file_queue WHERE remote_name=? ORDER BY file_id LIMIT 3", remote.Name)
	if err != nil {
		return err
	}
	type queued struct{ id, target string }
	var queue []queued
	for rows.Next() {
		var q queued
		if rows.Scan(&q.id, &q.target) == nil {
			queue = append(queue, q)
		}
	}
	rows.Close()
	for _, q := range queue {
		var exists bool
		_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM chat_files WHERE id=? AND target=?)", q.id, q.target).Scan(&exists)
		if !exists {
			_, native, _ := splitRemoteTarget(q.target)
			f, err := downloadRemoteFile(ctx, remote, q.id, native)
			if err != nil {
				var failure *remoteHTTPError
				if errors.As(err, &failure) && (failure.Status == 403 || failure.Status == 404) {
					_, _ = db.Exec("DELETE FROM remote_file_queue WHERE remote_name=? AND file_id=? AND target=?", remote.Name, q.id, q.target)
					continue
				}
				return err
			}
			f.Target = q.target
			if err = storeChatFile(db, f); err != nil {
				return err
			}
		}
		if _, err = db.Exec("DELETE FROM remote_file_queue WHERE remote_name=? AND file_id=? AND target=?", remote.Name, q.id, q.target); err != nil {
			return err
		}
	}
	return nil
}
