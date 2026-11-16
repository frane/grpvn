package internal

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errFileUploadKey = errors.New("upload idempotency key was already used for a different file")

func newChatFile(target, uploader, name string, data []byte) (ChatFile, error) {
	if strings.HasPrefix(target, "#") {
		if err := validateChannel(target); err != nil {
			return ChatFile{}, err
		}
	} else if !strings.HasPrefix(target, "@") || len(target) < 2 || strings.ContainsAny(target, "\r\n\x00") {
		return ChatFile{}, fmt.Errorf("channel or DM target required")
	}
	if name == "" {
		return ChatFile{}, fmt.Errorf("file name is required")
	}
	if len(data) > MaxFileBytes {
		return ChatFile{}, fmt.Errorf("file exceeds 10 MB")
	}
	return ChatFile{ID: NewMessage("", "", nil).ID, Target: target, Uploader: uploader, Name: fileName(name), MediaType: http.DetectContentType(data), Data: data, CreatedAt: time.Now().UnixMilli()}, nil
}

// Commit bytes and their retry key together. Uploading does not post a message.
func storeFileUpload(db *sql.DB, f ChatFile, key string) (ChatFile, error) {
	if len(key) > MaxIdempotencyKeyBytes {
		return f, fmt.Errorf("upload idempotency key too long")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return f, err
	}
	defer tx.Rollback()
	if key != "" {
		var id string
		err = tx.QueryRow("SELECT file_id FROM chat_file_uploads WHERE uploader=? AND target=? AND upload_key=?", f.Uploader, f.Target, key).Scan(&id)
		if err == nil {
			previous, err := loadChatFile(tx, id, f.Target)
			if err != nil {
				return f, err
			}
			if previous.Name != f.Name || !bytes.Equal(previous.Data, f.Data) {
				return f, errFileUploadKey
			}
			return previous, tx.Commit()
		}
		if err != sql.ErrNoRows {
			return f, err
		}
	}
	if err = storeChatFile(tx, f); err != nil {
		return f, err
	}
	if key != "" {
		if _, err = tx.Exec("INSERT INTO chat_file_uploads(uploader,target,upload_key,file_id) VALUES(?,?,?,?)", f.Uploader, f.Target, key, f.ID); err != nil {
			return f, err
		}
	}
	return f, tx.Commit()
}

// FileMarkdown is the same attachment reference rendered by human chat clients.
func FileMarkdown(f ChatFile) string {
	name := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "`", "\\`", "*", "\\*", "!", "\\!").Replace(f.Name)
	prefix := ""
	if fileImage(f.MediaType) {
		prefix = "!"
	}
	return prefix + "[" + name + "](grpvn-file:" + f.ID + ")"
}

func uploadedFileResult(f ChatFile) map[string]any {
	result := fileResult(f)
	result["target"] = f.Target
	result["reference"] = "grpvn-file:" + f.ID
	result["markdown"] = FileMarkdown(f)
	return result
}

// UploadChatFile is the local CLI/stdio path. Remote transfers use the agent's
// existing identity and request budget; local bytes remain cached in SQLite.
func UploadChatFile(ctx context.Context, db *sql.DB, sender, target, name string, data []byte, key string) (ChatFile, error) {
	target, _, err := ResolveTarget(db, target, "")
	if err != nil {
		return ChatFile{}, err
	}
	f, err := newChatFile(target, sender, name, data)
	if err != nil {
		return f, err
	}
	if len(key) > MaxIdempotencyKeyBytes {
		return f, fmt.Errorf("upload idempotency key too long")
	}
	if alias, native, ok := splitRemoteTarget(target); ok {
		remote, err := getRemote(db, alias)
		if err != nil {
			return f, err
		}
		remote.DB = db
		agent, err := agentRemote(ctx, db, *remote, sender, native)
		if err != nil {
			return f, err
		}
		f, err = uploadRemoteFile(ctx, agent, native, f, key)
		if err != nil {
			return f, fmt.Errorf("file upload was not acknowledged; retry with the same idempotency key when the workspace is available: %w", err)
		}
		return f, storeChatFile(db, f)
	}
	if !localSenderAllowed(db, sender, target) || channelArchived(db, target) {
		return f, fmt.Errorf("conversation write access required")
	}
	return storeFileUpload(db, f, key)
}

// UploadChatFilePath is only used by local tools. Remote MCP never reads paths
// from the coordinator's filesystem on behalf of a connecting agent.
func UploadChatFilePath(ctx context.Context, db *sql.DB, sender, target, path, key string) (ChatFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return ChatFile{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ChatFile{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return ChatFile{}, fmt.Errorf("choose a regular file up to 10 MB")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return ChatFile{}, err
	}
	return UploadChatFile(ctx, db, sender, target, filepath.Base(path), data, key)
}
