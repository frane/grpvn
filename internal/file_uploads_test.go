package internal

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestMCPUploadsOriginalFilesWithPermissionsAndRetryKeys(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "writer", "writer", []string{"#project", "@*"})
	seedTestAgent(t, db, "reader", "reader", []string{"#project", "@*"})
	seedTestAgent(t, db, "outsider", "outsider", []string{"#elsewhere", "@*"})
	if _, err := db.Exec("UPDATE permissions SET can_write=0 WHERE principal_id=? AND channel='#project'", credentialHash("reader")[:32]); err != nil {
		t.Fatal(err)
	}
	h, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Larger than the message-body limit: files are transferred separately.
	prefix := []byte("%PDF-1.7\n")
	pdf := append(prefix, bytes.Repeat([]byte("p"), MaxFileBytes-len(prefix))...)
	args := map[string]any{"target": "#project", "name": "../deck.pdf", "data_base64": base64.StdEncoding.EncodeToString(pdf), "idempotency_key": "deck-v1"}
	first, failed := remoteTool(t, h, "writer", "upload_file", args)
	if failed {
		t.Fatal(first)
	}
	id, ok := first["id"].(string)
	if !ok || first["name"] != "deck.pdf" || first["type"] != "application/pdf" || first["markdown"] != "[deck.pdf](grpvn-file:"+id+")" {
		t.Fatal(first)
	}
	stored, err := loadChatFile(db, id, "#project")
	if err != nil || !bytes.Equal(stored.Data, pdf) {
		t.Fatal("original bytes changed", err)
	}
	var messages int
	_ = db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages)
	if messages != 0 {
		t.Fatal("upload alone posted a message")
	}
	retry, failed := remoteTool(t, h, "writer", "upload_file", args)
	if failed || retry["id"] != id {
		t.Fatal("upload retry duplicated the file", retry)
	}
	if _, failed := remoteTool(t, h, "reader", "upload_file", args); !failed {
		t.Fatal("read-only member uploaded")
	}
	if _, failed := remoteTool(t, h, "outsider", "upload_file", args); !failed {
		t.Fatal("unrelated member uploaded")
	}
	if _, failed := remoteTool(t, h, "writer", "upload_file", map[string]any{"target": "#project", "path": "/etc/passwd", "name": "x", "text": "x"}); !failed {
		t.Fatal("remote tool accepted a server filesystem path")
	}
	if _, failed := remoteTool(t, h, "writer", "upload_file", map[string]any{"target": "#project", "name": "x", "data_base64": "not base64"}); !failed {
		t.Fatal("malformed binary data accepted")
	}
	if _, failed := remoteTool(t, h, "writer", "upload_file", map[string]any{"target": "#project", "name": "deck.pdf", "text": "changed", "idempotency_key": "deck-v1"}); !failed {
		t.Fatal("retry key silently changed the original file")
	}
	root, failed := remoteTool(t, h, "writer", "send", map[string]string{"target": "#project", "body": "Please review this deck", "idempotency_key": "deck-question"})
	if failed {
		t.Fatal(root)
	}
	posted, failed := remoteTool(t, h, "writer", "send", map[string]string{"target": root["id"].(string), "body": first["markdown"].(string), "idempotency_key": "deck-reply"})
	if failed {
		t.Fatal(posted)
	}
	message, err := findMessageByID(db, posted["id"].(string))
	if err != nil || message.ParentID == nil || *message.ParentID != root["id"] {
		t.Fatal("attachment lost thread context", err)
	}
	if file, failed := remoteTool(t, h, "reader", "read_file", map[string]string{"target": "#project", "id": id}); failed || file["id"] != id {
		t.Fatal("reader could not retrieve the deck", file)
	}
	if _, failed := remoteTool(t, h, "outsider", "read_file", map[string]string{"target": "#project", "id": id}); !failed {
		t.Fatal("private attachment leaked")
	}
}

func TestMCPFilePayloadLimitsAndTextUploads(t *testing.T) {
	text := "report\n"
	data, err := (uploadFileArgs{Name: "report.txt", Text: &text}).bytes()
	if err != nil || string(data) != text {
		t.Fatal(data, err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	if _, err := (uploadFileArgs{Name: "x", Text: &text, Data: &encoded}).bytes(); err == nil {
		t.Fatal("ambiguous file payload accepted")
	}
	oversized := strings.Repeat("x", MaxFileBytes+1)
	if _, err := (uploadFileArgs{Name: "x", Text: &oversized}).bytes(); err == nil {
		t.Fatal("oversized text file accepted")
	}
	oversized = strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxFileBytes)+4)
	if _, err := (uploadFileArgs{Name: "x", Data: &oversized}).bytes(); err == nil {
		t.Fatal("oversized encoded file accepted")
	}
}

func TestAttachmentAccessRespectsOAuthScopesForDMs(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "dm-agent", "dm-agent", []string{"@*"})
	p, err := findPrincipal(db, "id=?", credentialHash("dm-agent")[:32])
	if err != nil {
		t.Fatal(err)
	}
	access, _ := newChannelAccess(CoordinatorOptions{})
	access.db = db
	s := &webServer{db: db, access: access}
	f := ChatFile{Target: "@peer", Uploader: p.ID}
	p.scopes = map[string]bool{"grpvn.write": true}
	if s.fileAllowed(p, f, false) || !s.fileAllowed(p, f, true) {
		t.Fatal("attachment access ignored write-only token scope")
	}
	p.scopes = map[string]bool{"grpvn.read": true}
	if !s.fileAllowed(p, f, false) || s.fileAllowed(p, f, true) {
		t.Fatal("attachment access ignored read-only token scope")
	}
}
