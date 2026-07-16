package internal

import (
	"bytes"
	"context"
	"database/sql"
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

	// notice appends the unread counts to a non-reading tool's result, so
	// every grpvn touch doubles as a check. Most MCP hosts have no hook
	// surface, which makes this the one cross-runtime path to proactivity:
	// an agent that only ever sends still finds out something is waiting.
	// Errors are swallowed — a broken count must not fail the verb that ran.
	notice := func(db *sql.DB, st *State, text string) string {
		line, err := UnreadLine(db, st)
		if err != nil || line == "" {
			return text
		}
		return text + "\n[grpvn] unread: " + line + " — call the r tool"
	}

	// open is the shared preamble: identity, DB, and the one-time move of
	// pre-v2 cursors from state.json into the cursors table. The caller
	// owns closing the DB.
	open := func() (string, *State, *sql.DB, error) {
		n, st, err := b()
		if err != nil {
			return "", nil, nil, err
		}
		db, err := OpenDB()
		if err != nil {
			return "", nil, nil, err
		}
		if err := MigrateLegacyCursors(db, st, statePath()); err != nil {
			db.Close()
			return "", nil, nil, err
		}
		return n, st, db, nil
	}

	s.AddTool(tool("c", "Counts unread messages"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, st, db, err := open()
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

	s.AddTool(tool("r", "Reads unread messages and advances the cursor. If a previous r call's response was lost by the transport, its messages were still marked read — recover them with l (full history, ignores read state). On a flaky connection prefer p (peek) first"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, st, db, err := open()
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
		return mcp.NewToolResultText(buf.String()), nil
	})

	s.AddTool(tool("p", "Peeks at unread messages without advancing"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, st, db, err := open()
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
			var args sendArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			m, err := Send(db, n, args.Target, args.Body, st.DefaultChannel, false)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Posting into a channel subscribes the sender; replies to this
			// message must be able to reach it. Fail open — the send is
			// already committed.
			_, _ = AutoFollow(db, st, statePath(), m.Target)
			return mcp.NewToolResultText(notice(db, st, "sent")), nil
		})

	s.AddTool(tool("q", "Asks a question and returns a correlation ID",
		mcp.WithString("target", mcp.Description("Channel #name, @user, or parent ULID"), mcp.Required()),
		mcp.WithString("body", mcp.Description("Message content"), mcp.Required())),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args sendArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			m, err := Send(db, n, args.Target, args.Body, st.DefaultChannel, true)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			_, _ = AutoFollow(db, st, statePath(), m.Target)
			return mcp.NewToolResultText(notice(db, st, m.ID)), nil
		})

	s.AddTool(tool("g", "Greps message history with a regex pattern",
		mcp.WithString("pattern", mcp.Description("RE2 pattern"), mcp.Required()),
		mcp.WithString("scope", mcp.Description("#channel or @user; empty = followed channels and @me"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args patternArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var buf bytes.Buffer
			if err := Grep(&buf, db, n, st.Follow, args.Pattern, args.Scope, 0, st.DefaultChannel, false, false, false, "never"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(notice(db, st, buf.String())), nil
		})

	s.AddTool(tool("l", "Logs history of a target (#channel/@user) or thread (ULID prefix)",
		mcp.WithString("target", mcp.Description("#channel, @user, or message ULID prefix"), mcp.Required())),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args targetArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var buf bytes.Buffer
			if err := Log(&buf, db, n, args.Target, 0, st.DefaultChannel, false, false, false, "never"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(notice(db, st, buf.String())), nil
		})

	s.AddTool(tool("m", "Lists, adds, or removes message bookmarks",
		mcp.WithString("id", mcp.Description("Message ULID prefix; empty = list marks")),
		mcp.WithBoolean("delete", mcp.Description("Remove the mark"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args markArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var buf bytes.Buffer
			if err := Mark(&buf, db, n, args.ID, args.Delete, st.DefaultChannel, false, false, false, "never"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if buf.Len() == 0 {
				return mcp.NewToolResultText(notice(db, st, "ok")), nil
			}
			return mcp.NewToolResultText(notice(db, st, buf.String())), nil
		})

	s.AddTool(tool("w", "Waits until unread messages arrive, then returns the counts; returns \"no unread messages (timeout)\" otherwise. To wait longer than your host allows a single tool call to run, call w again each time it times out — do not pass a timeout larger than your host's tool-call limit",
		mcp.WithNumber("timeout", mcp.Description("Seconds to wait before giving up (default 45, max 240; keep at or below 45 if your MCP host kills long tool calls)"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args waitArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			_, _, db, err := open()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			// Default and cap sized for MCP hosts, not for grpvn: Claude
			// Desktop kills tool calls around the one-minute mark and
			// remote bridges around four, and a wait that dies at the
			// transport surfaces as "Failed to call tool" instead of a
			// clean timeout the model can loop on. 45s slips under the
			// strictest common limit; the tool description tells the model
			// to re-call w rather than stretch a single call.
			timeout := time.Duration(args.Timeout * float64(time.Second))
			if timeout <= 0 {
				timeout = 45 * time.Second
			}
			if timeout > 240*time.Second {
				timeout = 240 * time.Second
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
		n, st, db, err := open()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		defer db.Close()
		cwd, _ := os.Getwd()
		return mcp.NewToolResultText(notice(db, st, fmt.Sprintf("%s@%s", n, cwd))), nil
	})

	return server.ServeStdio(s)
}
