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
	Target         string `json:"target"`
	Body           string `json:"body"`
	IdempotencyKey string `json:"idempotency_key"`
}

type targetArgs struct {
	Target string `json:"target"`
}

type logArgs struct {
	Target string  `json:"target"`
	Limit  float64 `json:"limit"`
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

// MCPInstructions is returned in the MCP initialize handshake so hosts that
// never open SKILL.md still get the relevance rule. Keep it in lockstep with
// skills/grpvn/SKILL.md ("Read only what's relevant").
const MCPInstructions = "Unread is listed per followed channel. See every channel's count; only r a target relevant to your current work, a DM (@me), or a mention. Leave the rest unread. Do not drain every channel, do not reply there, and do not relay unrelated traffic to the human. Pass target on r/p (#chan or @me). Bare r with no target dumps everything — don't unless every listed channel is yours right now."

const (
	mcpDescC = "Counts unread per followed channel, listed separately (e.g. 1 @me 2 #dev 5 #ops). This is a board, not a to-do: see every channel, do not r them all. Only read a target relevant to your current work, a DM (@me), or a mention. Remote channels use the local daemon's cache when running, or sync here with a bounded timeout. Cached messages stay available offline."
	mcpDescR = "Reads unread and advances the cursor. Pass target to read one #channel or @me; omit to read every followed channel — don't omit unless every listed channel is relevant to your current work. Leave unrelated channels unread. Do not reply there or relay them to the human. If a previous r call's response was lost by the transport, its messages were still marked read — recover them with l (full history, ignores read state). On a flaky connection prefer p (peek) first."
	mcpDescP = "Peeks at unread without advancing. Pass target to peek one #channel or @me; omit to peek every followed channel. Prefer a relevant target — same rule as r. Leave unrelated channels alone."
	mcpDescW = "Waits until unread arrives that needs you, or until a new message commits, then returns the per-channel counts. Leftover unread on unrelated channels does not wake a re-armed waiter. See the counts; r only a relevant target. Returns \"no unread messages (timeout)\" otherwise. To wait longer than your host allows a single tool call to run, call w again each time it times out — do not pass a timeout larger than your host's tool-call limit."

	// MCPLogDefault / MCPLogMax keep l responses inside a context window and
	// off a 4-minute transport timeout. A busy channel dumped in full is how
	// Claude Desktop's stdio bridge went silent; tail-50 is the safe default.
	MCPLogDefault = 50
	MCPLogMax     = 500
)

type MCPOptions struct {
	ClaudeChannel                  bool
	RuntimeSession, ActivationMode string
}

func ServeMCP(name, version string, b Bootstrap, options ...MCPOptions) error {
	var config MCPOptions
	if len(options) > 0 {
		config = options[0]
	}
	opts := []server.ServerOption{server.WithInstructions(MCPInstructions)}
	hooks := &server.Hooks{}
	if config.ClaudeChannel {
		opts = append(opts, server.WithExperimental(map[string]any{"claude/channel": map[string]any{}}), server.WithHooks(hooks))
	}
	s := server.NewMCPServer(name, version, opts...)
	if config.ClaudeChannel {
		cleanup := attachClaudeChannel(s, hooks, b, config)
		defer cleanup()
	}

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
		return text + "\n[grpvn] unread: " + line + " — r only a relevant target (r with target #chan or @me); leave the rest. Do not relay unrelated channels to the human."
	}

	// open is the shared preamble: identity, DB, and the one-time move of
	// pre-v2 cursors from state.json into the cursors table. The caller
	// owns closing the DB.
	open := func(ctx context.Context) (string, *State, *sql.DB, error) {
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
		_, _ = SyncAvailable(ctx, db)
		return n, st, db, nil
	}

	s.AddTool(tool("c", mcpDescC), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, st, db, err := open(ctx)
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

	s.AddTool(tool("r", mcpDescR,
		mcp.WithString("target", mcp.Description("One #channel or @me to read; empty = every followed channel (rarely what you want — pick a relevant target). Leave unrelated channels unread."))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args targetArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			_, st, db, err := open(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var buf bytes.Buffer
			var only []string
			if args.Target != "" {
				only = []string{args.Target}
			}
			code, err := Read(&buf, db, st, 0, true, false, false, false, "never", only...)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if code == 2 {
				return mcp.NewToolResultText("no unread messages"), nil
			}
			return mcp.NewToolResultText(buf.String()), nil
		})

	s.AddTool(tool("p", mcpDescP,
		mcp.WithString("target", mcp.Description("One #channel or @me to peek; empty = every followed channel. Prefer a relevant target; same rule as r."))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args targetArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			_, st, db, err := open(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var buf bytes.Buffer
			var only []string
			if args.Target != "" {
				only = []string{args.Target}
			}
			code, err := Read(&buf, db, st, 0, false, false, false, false, "never", only...)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if code == 2 {
				return mcp.NewToolResultText("no unread messages"), nil
			}
			return mcp.NewToolResultText(buf.String()), nil
		})

	s.AddTool(tool("s", "Sends a message and returns \"<id> <target>\" so you can tell whether the write landed. Pass idempotency_key on anything you might retry: a second s with the same key is a no-op that returns the original id (marked replayed) instead of posting a duplicate. There is no delete, so a duplicate long post is permanent.",
		mcp.WithString("target", mcp.Description("Channel #name, @user, or parent ULID")),
		mcp.WithString("body", mcp.Description("Message content"), mcp.Required()),
		mcp.WithString("idempotency_key", mcp.Description("Retry key unique to this sender. Same key → same message, no duplicate. Use whenever the previous s may have timed out."))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args sendArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			m, replayed, err := SendSynced(ctx, db, n, args.Target, args.Body, st.DefaultChannel, false, args.IdempotencyKey)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Posting into a channel subscribes the sender; replies to this
			// message must be able to reach it. Fail open — the send is
			// already committed.
			_, _ = AutoFollow(db, st, statePath(), m.Target)
			return mcp.NewToolResultText(notice(db, st, FormatSendAck(m, replayed))), nil
		})

	s.AddTool(tool("q", "Asks a question and returns a correlation ID. Same idempotency_key behaviour as s.",
		mcp.WithString("target", mcp.Description("Channel #name, @user, or parent ULID"), mcp.Required()),
		mcp.WithString("body", mcp.Description("Message content"), mcp.Required()),
		mcp.WithString("idempotency_key", mcp.Description("Retry key unique to this sender. Same key → same question, no duplicate."))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args sendArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			m, _, err := SendSynced(ctx, db, n, args.Target, args.Body, st.DefaultChannel, true, args.IdempotencyKey)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			_, _ = AutoFollow(db, st, statePath(), m.Target)
			ack := m.ID
			if m.Pending {
				ack += " queued"
			}
			return mcp.NewToolResultText(notice(db, st, ack)), nil
		})

	s.AddTool(tool("g", "Greps message history with a regex pattern",
		mcp.WithString("pattern", mcp.Description("RE2 pattern"), mcp.Required()),
		mcp.WithString("scope", mcp.Description("Search scope: one #channel or @user; empty = followed channels and @me. On the CLI this is grep's second positional argument, not the --scope flag (that one selects an identity)"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args patternArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open(ctx)
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

	s.AddTool(tool("l", "Logs history of a target (#channel/@user) or thread (ULID prefix). Defaults to the 50 most recent messages so a busy channel does not blow the context window or hang the transport; pass limit (max 500) for more, or use the CLI for the full log. With no target, lists every channel that exists — name, message count, age of the last message, and whether you follow it. Listing channels is how you see the whole board; it does not mark anything read.",
		mcp.WithString("target", mcp.Description("#channel, @user, or message ULID prefix; empty = list every channel that exists, followed or not")),
		mcp.WithNumber("limit", mcp.Description("Max messages to return, most recent. Default 50, cap 500. Omit for the default."))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args logArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			n, st, db, err := open(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var buf bytes.Buffer
			if args.Target == "" {
				if err := Channels(&buf, db, st.Follow); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(notice(db, st, buf.String())), nil
			}
			limit := MCPLogDefault
			if args.Limit > 0 {
				limit = int(args.Limit)
			}
			if limit > MCPLogMax {
				limit = MCPLogMax
			}
			if err := Log(&buf, db, n, args.Target, limit, st.DefaultChannel, false, false, false, "never"); err != nil {
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
			n, st, db, err := open(ctx)
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

	s.AddTool(tool("w", mcpDescW,
		mcp.WithNumber("timeout", mcp.Description("Seconds to wait before giving up (default 45, max 240; keep at or below 45 if your MCP host kills long tool calls)"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args waitArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			_, _, db, err := open(ctx)
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
		n, st, db, err := open(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		defer db.Close()
		cwd, _ := os.Getwd()
		return mcp.NewToolResultText(notice(db, st, fmt.Sprintf("%s@%s", n, cwd))), nil
	})

	s.AddTool(tool("connect", "Connect selected channels from a coordinator. The named source is stored in SQLite; channels appear as #name/channel and are followed by this identity. Reconnecting adds channels and preserves sync positions.",
		mcp.WithString("name", mcp.Description("Local alias for this coordinator"), mcp.Required()),
		mcp.WithString("url", mcp.Description("Coordinator http(s) URL"), mcp.Required()),
		mcp.WithArray("channels", mcp.Description("Remote channels, e.g. [\"#dev\", \"#ops\"]"), mcp.WithStringItems(), mcp.Required()),
		mcp.WithString("invite", mcp.Description("Scoped invitation; omitted reuses an existing membership or requests registration"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Name     string   `json:"name"`
				URL      string   `json:"url"`
				Channels []string `json:"channels"`
				Invite   string   `json:"invite"`
			}
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if len(args.Channels) == 0 {
				return mcp.NewToolResultError("pass at least one channel"), nil
			}
			_, st, err := b()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			token := args.Invite
			if token == "" {
				token = os.Getenv("GRPVN_INVITE")
			}
			if err := ConnectRemote(ctx, db, args.Name, args.URL, token, args.Channels); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			for _, channel := range args.Channels {
				target := remoteTarget(args.Name, channel)
				found := false
				for _, f := range st.Follow {
					found = found || f == target
				}
				if !found {
					st.Follow = append(st.Follow, target)
				}
			}
			if err := st.Save(statePath()); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			registration, err := RemoteRegistration(db, args.Name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if registration.Status != "active" {
				return mcp.NewToolResultText(fmt.Sprintf("connected %s; registration %s; ask the coordinator admin to approve %s with explicit channel grants", args.Name, registration.Status, registration.ApprovalCode)), nil
			}
			stats, err := SyncRemotes(ctx, db, args.Name)
			if err != nil {
				return mcp.NewToolResultError("connected; initial sync: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("connected %s; pushed %d, pulled %d", args.Name, stats.Pushed, stats.Pulled)), nil
		})

	s.AddTool(tool("sync", "Push durable queued sends and pull remote channels into the local SQLite log. Safe to retry; remote messages and thread IDs are deduplicated.",
		mcp.WithString("remote", mcp.Description("Source alias; omitted syncs all sources"))),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Remote string `json:"remote"`
			}
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			stats, err := SyncRemotes(ctx, db, args.Remote)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("pushed %d, pulled %d", stats.Pushed, stats.Pulled)), nil
		})

	s.AddTool(tool("remotes", "List configured coordinator URLs, joined channels, pending sends, and last sync errors. Tokens are never included."),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			db, err := OpenDB()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer db.Close()
			var buf bytes.Buffer
			if err := PrintRemotes(&buf, db); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(buf.String()), nil
		})

	return server.ServeStdio(s)
}
