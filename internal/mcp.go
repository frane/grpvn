package internal

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func ServeMCP(name, version string, b func() (string, *State, error)) error {
	s := server.NewMCPServer(name, version)
	s.AddTool(mcp.NewTool("c", mcp.WithDescription("Counts unread messages")), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n, st, err := b(); if err != nil { return mcp.NewToolResultError(err.Error()), nil }
		db, err := OpenDB(); if err != nil { return mcp.NewToolResultError(err.Error()), nil }; defer db.Close()
		var buf bytes.Buffer; code, err := Check(&buf, db, n, st.Cursor, st.Follow); if err != nil { return mcp.NewToolResultError(err.Error()), nil }
		if code == 2 { return mcp.NewToolResultText("no unread messages"), nil }; return mcp.NewToolResultText(buf.String()), nil
	})
	s.AddTool(mcp.NewTool("r", mcp.WithDescription("Consumes unread messages")), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n, st, err := b(); if err != nil { return mcp.NewToolResultError(err.Error()), nil }
		db, err := OpenDB(); if err != nil { return mcp.NewToolResultError(err.Error()), nil }; defer db.Close()
		var buf bytes.Buffer; next, code, err := Read(&buf, db, n, st.Cursor, st.Follow, 0, true, st.DefaultChannel, false, false, false, "auto"); if err != nil { return mcp.NewToolResultError(err.Error()), nil }
		if code == 2 { return mcp.NewToolResultText("no unread messages"), nil }
		if next != st.Cursor { st.Cursor = next; _ = st.Save(".grpvn/state.json") }; return mcp.NewToolResultText(buf.String()), nil
	})
	s.AddTool(mcp.NewTool("s", mcp.WithDescription("Sends a message"), mcp.WithString("target", mcp.Description("Channel, @user, or parent ULID")), mcp.WithString("body", mcp.Description("Message content"), mcp.Required())), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n, st, err := b(); if err != nil { return mcp.NewToolResultError(err.Error()), nil }
		db, err := OpenDB(); if err != nil { return mcp.NewToolResultError(err.Error()), nil }; defer db.Close()
		var args struct { Target string `json:"target"`; Body string `json:"body"` }
		data, _ := json.Marshal(req.Params.Arguments)
		json.Unmarshal(data, &args)
		if err := Send(db, n, args.Target, args.Body, st.DefaultChannel, false); err != nil { return mcp.NewToolResultError(err.Error()), nil }
		return mcp.NewToolResultText("sent"), nil
	})
	return server.ServeStdio(s)
}