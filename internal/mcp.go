package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type Bootstrap func() (string, *State, error)

func statePath() string { return ResolveStatePath("") }

type sendArgs struct {
	Target string `json:"target"`
	Body   string `json:"body"`
}

type targetArgs struct {
	Target string `json:"target"`
}

type patternArgs struct {
	Pattern string `json:"pattern"`
	Scope   string `json:"scope"`
}

type markArgs struct {
	ID     string `json:"id"`
	Delete bool   `json:"delete"`
}

type waitArgs struct {
	Timeout float64 `json:"timeout"`
}

func ServeMCP(name, version string, b Bootstrap) error {
	s := server.NewMCPServer(name, version)

	tool := func(n, desc string, opts ...mcp.ToolOption) mcp.Tool {
		return mcp.NewTool(n, append([]mcp.ToolOption{mcp.WithDescription(desc)}, opts...)...)
	}

	s.AddTool(tool("c", "Counts unread messages"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, st, err := b()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		db, err := OpenDB()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		defer db.Close()
		var buf bytes.Buffer
		code, err := Check(&buf, db, st)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if code == 2 {
			return mcp.NewToolResultText("no unread messages"), nil
		}
		return mcp.NewToolResultText(buf.String()), nil
	})

	s.AddTool(tool("r", "Reads unread messages and advances the cursor"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, st, err := b()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		db, err := OpenDB()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		defer db.Close()
		var buf bytes.Buffer
		code, err := Read(&buf, db, st, 0, true, false, false, false, "never")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if code == 2 {
			return mcp.NewToolResultText("no unread messages"), nil
		}
		if err := st.Save(statePath()); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("save state: %v", err)), nil
		}
		return mcp.NewToolResultText(buf.String()), nil
	})

	s.AddTool(tool("p", "Peeks at unread messages without advancing"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, st, err := b()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		db, err := OpenDB()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		defer db.Close()
		var buf bytes.Buffer
		code, err := Read(&buf, db, st, 0, false, false, false, false, "never")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if code == 2 {
			return mcp.NewToolResultText("no unread messages"), nil
		}
		return mcp.NewToolResultText(buf.String()), nil
	})

	s.AddTool(tool("s", "Sends a message",
		mcp.WithString("target", mcp.Description("Channel #name, @user, or parent ULID")),
		mcp.WithString("body", mcp.Description("Message content"), mcp.Required())),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n, st, err := b()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var args sendArgs
			data, _ := json.Marshal(req.Params.Arguments)
			_ = json.Unmarshal(data, &args)
			if err := Send(db, n, args.Target, args.Body, st.DefaultChannel, false); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("sent"), nil
		})

	s.AddTool(tool("q", "Asks a question and returns a correlation ID",
		mcp.WithString("target", mcp.Description("Channel #name, @user, or parent ULID"), mcp.Required()),
		mcp.WithString("body", mcp.Description("Message content"), mcp.Required())),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n, st, err := b()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var args sendArgs
			data, _ := json.Marshal(req.Params.Arguments)
			_ = json.Unmarshal(data, &args)
			target, parent, err := ResolveTarget(db, args.Target, st.DefaultChannel)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			m := NewMessage(n, target, []byte(args.Body))
			if parent != nil {
				if parent.ChainDepth+1 > 8 {
					return mcp.NewToolResultError("chain depth limit reached (8)"), nil
				}
				m.ChainRoot = parent.ChainRoot
				m.ChainDepth = parent.ChainDepth + 1
				m.ParentID = &parent.ID
			}
			m.Correlation = &m.ID
			if err := m.Save(db); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(m.ID), nil
		})

	s.AddTool(tool("g", "Greps message history with a regex pattern",
		mcp.WithString("pattern", mcp.Description("RE2 pattern"), mcp.Required()),
		mcp.WithString("scope", mcp.Description("#channel or @user; empty = followed channels and @me"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n, st, err := b()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var args patternArgs
			data, _ := json.Marshal(req.Params.Arguments)
			_ = json.Unmarshal(data, &args)
			var buf bytes.Buffer
			if err := Grep(&buf, db, n, st.Follow, args.Pattern, args.Scope, 0, st.DefaultChannel, false, false, false, "never"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(buf.String()), nil
		})

	s.AddTool(tool("l", "Logs history of a target (#channel/@user) or thread (ULID prefix)",
		mcp.WithString("target", mcp.Description("#channel, @user, or message ULID prefix"), mcp.Required())),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n, st, err := b()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var args targetArgs
			data, _ := json.Marshal(req.Params.Arguments)
			_ = json.Unmarshal(data, &args)
			var buf bytes.Buffer
			if err := Log(&buf, db, n, args.Target, 0, st.DefaultChannel, false, false, false, "never"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(buf.String()), nil
		})

	s.AddTool(tool("m", "Lists, adds, or removes message bookmarks",
		mcp.WithString("id", mcp.Description("Message ULID prefix; empty = list marks")),
		mcp.WithBoolean("delete", mcp.Description("Remove the mark"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n, st, err := b()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var args markArgs
			data, _ := json.Marshal(req.Params.Arguments)
			_ = json.Unmarshal(data, &args)
			var buf bytes.Buffer
			if err := Mark(&buf, db, n, args.ID, args.Delete, st.DefaultChannel, false, false, false, "never"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if buf.Len() == 0 {
				return mcp.NewToolResultText("ok"), nil
			}
			return mcp.NewToolResultText(buf.String()), nil
		})

	s.AddTool(tool("w", "Waits until unread messages arrive, then returns the counts. Long-poll alternative to calling c in a loop",
		mcp.WithNumber("timeout", mcp.Description("Seconds to wait before giving up (default 60, max 300)"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var args waitArgs
			data, _ := json.Marshal(req.Params.Arguments)
			_ = json.Unmarshal(data, &args)
			timeout := time.Duration(args.Timeout * float64(time.Second))
			if timeout <= 0 {
				timeout = 60 * time.Second
			}
			if timeout > 300*time.Second {
				timeout = 300 * time.Second
			}
			load := func() (*State, error) {
				_, st, err := b()
				return st, err
			}
			var buf bytes.Buffer
			code, err := Wait(ctx, &buf, db, load, timeout, 250*time.Millisecond)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if code == 2 {
				return mcp.NewToolResultText("no unread messages (timeout)"), nil
			}
			return mcp.NewToolResultText(buf.String()), nil
		})

	s.AddTool(tool("i", "Returns the current agent identity"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n, _, err := b()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		cwd, _ := os.Getwd()
		return mcp.NewToolResultText(fmt.Sprintf("%s@%s", n, cwd)), nil
	})

	return server.ServeStdio(s)
}
