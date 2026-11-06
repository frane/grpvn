package internal

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerFileTools(service *server.MCPServer, db *sql.DB, access *channelAccess) {
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
