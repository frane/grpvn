package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type actionToolContext func(context.Context) (*webServer, *Principal, func(), error)

func registerMessageActionTools(service *server.MCPServer, open actionToolContext, result func(any) *mcp.CallToolResult) {
	for _, kind := range []string{"reaction", "edit"} {
		name, description := "react", "Add an emoji reaction to an existing message, or remove your own with remove=true. Repeating the same desired state does not add a duplicate reaction. Reactions do not post messages or activate agents. Supply action_id for a retried request. Message reads include counts and who reacted."
		options := []mcp.ToolOption{mcp.WithString("message", mcp.Required()), mcp.WithString("emoji", mcp.Required()), mcp.WithBoolean("remove"), mcp.WithString("action_id")}
		if kind == "edit" {
			name = "edit"
			description = "Correct your own message without changing its ID, thread or original audit record. You cannot edit another participant's messages. Use edit_version from read as version to detect concurrent changes. Supply action_id for a retried request."
			options = []mcp.ToolOption{mcp.WithString("message", mcp.Required()), mcp.WithString("body", mcp.Required()), mcp.WithString("version"), mcp.WithString("action_id")}
		}
		service.AddTool(mcp.NewTool(name, append([]mcp.ToolOption{mcp.WithDescription(description), mcp.WithReadOnlyHintAnnotation(false), mcp.WithDestructiveHintAnnotation(false)}, options...)...), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Message  string  `json:"message"`
				Body     string  `json:"body"`
				Emoji    string  `json:"emoji"`
				ActionID string  `json:"action_id"`
				Remove   bool    `json:"remove"`
				Version  *string `json:"version"`
			}
			if err := request.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			s, p, close, err := open(ctx)
			defer close()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			value, err := changeMessage(ctx, s, p, args.Message, actionRequest{ID: args.ActionID, Kind: kind, Body: args.Body, Emoji: args.Emoji, Added: !args.Remove, Version: args.Version})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return result(value), nil
		})
	}
}
func jsonToolResult(value any) *mcp.CallToolResult {
	data, _ := json.Marshal(value)
	return mcp.NewToolResultText(string(data))
}

func changeMessage(ctx context.Context, s *webServer, p *Principal, target string, args actionRequest) (map[string]any, error) {
	_, message, err := ResolveTarget(s.db, target, "")
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, fmt.Errorf("message not found")
	}
	if args.ID == "" {
		args.ID = NewMessage("", "", nil).ID
	}
	args.MessageID = message.ID
	action, err := s.applyMessageAction(ctx, p, args)
	if err != nil {
		return nil, err
	}
	return map[string]any{"action_id": action.ID, "message": presentedWire(s.db, message, p.ID)}, nil
}

func localActionServer(db *sql.DB, name string) (*webServer, *Principal, error) {
	p, err := EnsureLocalAgent(db, name)
	if err != nil {
		return nil, nil, err
	}
	access, err := newChannelAccess(CoordinatorOptions{LocalUI: true})
	if err != nil {
		return nil, nil, err
	}
	access.db = db
	return &webServer{db: db, access: access, local: true}, p, nil
}
func ReactMessage(ctx context.Context, db *sql.DB, name, message, emoji, key string, remove bool) (map[string]any, error) {
	s, p, err := localActionServer(db, name)
	if err != nil {
		return nil, err
	}
	return changeMessage(ctx, s, p, message, actionRequest{ID: key, Kind: "reaction", Emoji: emoji, Added: !remove})
}
func EditMessage(ctx context.Context, db *sql.DB, name, message, body, key string, version *string) (map[string]any, error) {
	s, p, err := localActionServer(db, name)
	if err != nil {
		return nil, err
	}
	return changeMessage(ctx, s, p, message, actionRequest{ID: key, Kind: "edit", Body: body, Added: true, Version: version})
}
