package internal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func uploadTestFile(t *testing.T, client *http.Client, base, target, token, name string, data []byte) (int, map[string]any) {
	t.Helper()
	var payload bytes.Buffer
	form := multipart.NewWriter(&payload)
	_ = form.WriteField("target", target)
	part, _ := form.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = form.Close()
	r, _ := http.NewRequest("POST", base+"/v1/files", &payload)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Origin", base)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(response.Body).Decode(&result)
	return response.StatusCode, result
}
func TestFileAccessFollowsChannelAndDMParticipants(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "alice-token", "alice", []string{"#project", "@*"})
	seedTestAgent(t, db, "bob-token", "bob", []string{"#project", "@*"})
	seedTestAgent(t, db, "outsider-token", "outsider", []string{"#other", "@*"})
	h, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a2ioAAAAASUVORK5CYII=")
	code, metadata := uploadTestFile(t, http.DefaultClient, server.URL, "#project", "alice-token", "pixel.png", png)
	if code != 201 {
		t.Fatal(code, metadata)
	}
	id := metadata["id"].(string)
	if file, failed := remoteTool(t, h, "bob-token", "read_file", map[string]string{"id": id, "target": "#project"}); failed || file["id"] != id {
		t.Fatal("MCP could not read a permitted image", file)
	}
	if _, failed := remoteTool(t, h, "outsider-token", "read_file", map[string]string{"id": id, "target": "#project"}); !failed {
		t.Fatal("MCP exposed another channel's file")
	}
	get := func(id, target, token string) (int, []byte, http.Header) {
		t.Helper()
		r, _ := http.NewRequest("GET", server.URL+"/v1/files/"+id+"?"+url.Values{"target": {target}}.Encode(), nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, data, response.Header
	}
	if status, data, headers := get(id, "#project", "bob-token"); status != 200 || !bytes.Equal(data, png) || headers.Get("Content-Type") != "image/png" || !strings.HasPrefix(headers.Get("Content-Disposition"), "inline") {
		t.Fatal("channel image did not download correctly", status, headers)
	}
	if status, _, _ := get(id, "#project", "outsider-token"); status != 404 {
		t.Fatal("file leaked to an unrelated member", status)
	}
	if status, _, _ := get(id, "#other", "outsider-token"); status != 404 {
		t.Fatal("file escaped its conversation", status)
	}
	if status, _, _ := get(id, "#project", ""); status != 401 {
		t.Fatal("anonymous file read", status)
	}
	code, html := uploadTestFile(t, http.DefaultClient, server.URL, "#project", "alice-token", "example.html", []byte("<!doctype html><script>alert(1)</script>"))
	if code != 201 {
		t.Fatal(code, html)
	}
	if status, _, headers := get(html["id"].(string), "#project", "bob-token"); status != 200 || headers.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment") {
		t.Fatal("active document rendered inline", status, headers)
	}
	code, dm := uploadTestFile(t, http.DefaultClient, server.URL, "@bob", "alice-token", "private.txt", []byte("Between Alice and Bob"))
	if code != 201 {
		t.Fatal(code, dm)
	}
	for _, token := range []string{"alice-token", "bob-token"} {
		if status, _, _ := get(dm["id"].(string), "@bob", token); status != 200 {
			t.Fatal("DM participant cannot read file", status)
		}
	}
	if status, _, _ := get(dm["id"].(string), "@bob", "outsider-token"); status != 404 {
		t.Fatal("DM grant exposed another conversation's file", status)
	}
	if code, _ := uploadTestFile(t, http.DefaultClient, server.URL, "#other", "alice-token", "x.txt", []byte("denied")); code != 403 {
		t.Fatal("unauthorized upload", code)
	}
	if code, _ := uploadTestFile(t, http.DefaultClient, server.URL, "#project", "alice-token", "large.bin", make([]byte, MaxFileBytes+1)); code != 413 {
		t.Fatal("oversized upload", code)
	}
}

func TestFilesSyncIntoLocalDatabaseAndStayAvailableOffline(t *testing.T) {
	remoteDB := newTestDB(t)
	server := testCoordinator(t, remoteDB, "file-invite", "#project")
	localDB := newTestDB(t)
	if err := ConnectRemote(context.Background(), localDB, "work", server.URL, "file-invite", []string{"#project"}); err != nil {
		t.Fatal(err)
	}
	data := []byte("package main\n\nfunc main() {}\n")
	status, metadata := uploadTestFile(t, http.DefaultClient, server.URL, "#project", "file-invite", "example.go", data)
	if status != 201 {
		t.Fatal(status, metadata)
	}
	id := metadata["id"].(string)
	if _, err := Send(remoteDB, "alice", "#project", "[example.go](grpvn-file:"+id+")", "", false); err != nil {
		t.Fatal(err)
	}
	mustSync(t, localDB, "work")
	f, err := loadChatFile(localDB, id, "#work/project")
	if err != nil || !bytes.Equal(f.Data, data) {
		t.Fatal("file was not cached with the message", f, err)
	}
	var count int
	_ = localDB.QueryRow("SELECT COUNT(*) FROM remote_file_queue").Scan(&count)
	if count != 0 {
		t.Fatal("file queue was not acknowledged")
	}
	server.Close()
	seedTestAgent(t, localDB, "local-token", "local-person", []string{"*"})
	h, err := CoordinatorHandler(localDB, CoordinatorOptions{LocalUI: true})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(h)
	defer local.Close()
	r, _ := http.NewRequest("GET", local.URL+"/v1/files/"+id+"?target=%23work%2Fproject", nil)
	r.Header.Set("Authorization", "Bearer local-token")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || !bytes.Equal(got, data) {
		t.Fatal("cached file requires a live coordinator", response.StatusCode, string(got))
	}
}
