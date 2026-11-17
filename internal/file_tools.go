package internal

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type uploadFileArgs struct {
	Target string  `json:"target"`
	Name   string  `json:"name"`
	Text   *string `json:"text"`
	Data   *string `json:"data_base64"`
	Path   string  `json:"path"`
	Key    string  `json:"idempotency_key"`
}

func uploadFileTool(local bool) mcp.Tool {
	options := []mcp.ToolOption{
		mcp.WithDescription("Upload one file up to 10 MB to a conversation you can write. Provide name and exactly one of text or data_base64 (original binary bytes, including PDFs/images). Returns markdown: pass it to send/s, replying to the original message ID when threaded. Uploading alone does not post a message. Reuse idempotency_key on retries. Never fabricate unavailable file bytes."),
		mcp.WithReadOnlyHintAnnotation(false), mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("target", mcp.Required(), mcp.Description("Channel or DM; local tools also accept #source/channel or a reply ID.")),
		mcp.WithString("name", mcp.Description("File name, required with text/data_base64.")),
		mcp.WithString("text", mcp.Description("Plain UTF-8 file contents; mutually exclusive with data_base64.")),
		mcp.WithString("data_base64", mcp.Description("Standard base64 of the original file bytes; mutually exclusive with text.")),
		mcp.WithString("idempotency_key"),
	}
	if local {
		options = append(options, mcp.WithString("path", mcp.Description("Local file path instead of text/data_base64; bytes are transferred directly without copying them into the model context.")))
	}
	return mcp.NewTool("upload_file", options...)
}

func (a uploadFileArgs) bytes() ([]byte, error) {
	if a.Path != "" {
		return nil, fmt.Errorf("remote uploads cannot read filesystem paths; provide name and text or data_base64")
	}
	if a.Name == "" || (a.Text == nil) == (a.Data == nil) {
		return nil, fmt.Errorf("provide name and exactly one of text or data_base64")
	}
	if a.Text != nil {
		if len(*a.Text) > MaxFileBytes {
			return nil, fmt.Errorf("file exceeds 10 MB")
		}
		return []byte(*a.Text), nil
	}
	if len(*a.Data) > base64.StdEncoding.EncodedLen(MaxFileBytes) {
		return nil, fmt.Errorf("file exceeds 10 MB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(*a.Data)
	if err != nil {
		return nil, fmt.Errorf("data_base64 must contain standard base64 of the original file")
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("file exceeds 10 MB")
	}
	return data, nil
}

func fileUploadResult(file ChatFile, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	metadata, _ := json.Marshal(uploadedFileResult(file))
	return mcp.NewToolResultText(string(metadata)), nil
}

func registerFileTools(service *server.MCPServer, db *sql.DB, access *channelAccess) {
	service.AddTool(uploadFileTool(false), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args uploadFileArgs
		if err := req.BindArguments(&args); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		p := requestPrincipalContext(ctx)
		if p == nil || !ensureSendChannel(db, access, p, args.Target) {
			return mcp.NewToolResultError("Conversation write access required to upload a file"), nil
		}
		data, err := args.bytes()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		file, err := newChatFile(args.Target, p.ID, args.Name, data)
		if err == nil {
			file, err = storeFileUpload(db, file, args.Key)
		}
		return fileUploadResult(file, err)
	})
	service.AddTool(mcp.NewTool("read_file", mcp.WithDescription("Read a grpvn-file attachment from a permitted conversation. Images return image content; text/code files return text; other files return a downloadable embedded resource. Use the attachment ID from its message and the native channel or DM target."), mcp.WithReadOnlyHintAnnotation(true), mcp.WithString("id", mcp.Required()), mcp.WithString("target", mcp.Required())), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			ID     string `json:"id"`
			Target string `json:"target"`
		}
		if err := req.BindArguments(&args); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return attachmentResult(ctx, db, access, requestPrincipalContext(ctx), args.ID, args.Target)
	})
}

func attachmentResult(ctx context.Context, db *sql.DB, access *channelAccess, p *Principal, id, target string) (*mcp.CallToolResult, error) {
	file, err := loadChatFile(db, id, target)
	if access == nil && err == sql.ErrNoRows {
		file, err = ReadChatFile(ctx, db, id, target)
	}
	web := &webServer{db: db, access: access}
	if err != nil || (access != nil && (p == nil || !web.fileAllowed(p, file, false))) {
		return mcp.NewToolResultError("File not found in a conversation you can read"), nil
	}
	metadata, _ := json.Marshal(fileResult(file))
	if fileImage(file.MediaType) {
		return mcp.NewToolResultImage(string(metadata), base64.StdEncoding.EncodeToString(file.Data), file.MediaType), nil
	}
	if len(file.Data) <= 256*1024 && utf8.Valid(file.Data) {
		return mcp.NewToolResultText(string(metadata) + "\n\n" + string(file.Data)), nil
	}
	return mcp.NewToolResultResource(string(metadata), mcp.BlobResourceContents{URI: "grpvn-file:" + file.ID, MIMEType: file.MediaType, Blob: base64.StdEncoding.EncodeToString(file.Data)}), nil
}
