package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

func attachClaudeChannel(service *server.MCPServer, hooks *server.Hooks, b Bootstrap, config MCPOptions) func() {
	var mu sync.Mutex
	var wg sync.WaitGroup
	cancels := map[string]context.CancelFunc{}
	runtimeID := config.RuntimeSession
	if runtimeID == "" {
		runtimeID = "mcp-" + NewMessage("", "", nil).ID
	}
	hooks.AddOnRegisterSession(func(_ context.Context, client server.ClientSession) {
		ctx, cancel := context.WithCancel(context.Background())
		mu.Lock()
		cancels[client.SessionID()] = cancel
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for !client.Initialized() {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
			name, st, err := b()
			if err != nil {
				fmt.Fprintln(os.Stderr, "grpvn channel:", err)
				return
			}
			db, err := OpenDB()
			if err != nil {
				fmt.Fprintln(os.Stderr, "grpvn channel:", err)
				return
			}
			defer db.Close()
			path, err := filepath.Abs(statePath())
			if err != nil {
				fmt.Fprintln(os.Stderr, "grpvn channel:", err)
				return
			}
			id, err := RegisterSession(db, AgentSession{Agent: name, Runtime: "claude-channel", RuntimeSession: runtimeID, Project: ProjectRoot(), StatePath: path, Channels: st.Follow, Mode: config.ActivationMode, KeepPolicy: true}, false, false)
			if err != nil {
				fmt.Fprintln(os.Stderr, "grpvn channel:", err)
				return
			}
			defer db.Exec("DELETE FROM activation_owners WHERE session_id=?", id)
			err = RunSessionBridge(ctx, db, id, false, func(a Activation) error {
				if err := service.SendNotificationToSpecificClient(client.SessionID(), "notifications/claude/channel", map[string]any{"content": a.Prompt, "meta": map[string]string{"delivery_id": a.ID, "session_id": id, "thread_id": a.Thread}}); err != nil {
					return err
				}
				return FinishActivation(db, a.ID, "accepted", "channel notification written; awaiting a real read receipt", "", "")
			})
			if err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "grpvn channel:", err)
			}
		}()
	})
	hooks.AddOnUnregisterSession(func(_ context.Context, client server.ClientSession) {
		mu.Lock()
		if cancel := cancels[client.SessionID()]; cancel != nil {
			cancel()
			delete(cancels, client.SessionID())
		}
		mu.Unlock()
	})
	return func() {
		mu.Lock()
		for _, cancel := range cancels {
			cancel()
		}
		mu.Unlock()
		wg.Wait()
	}
}
