package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/frane/grpvn/internal"
	"github.com/spf13/cobra"
)

var connectTokenFlag string
var syncWatchFlag bool
var syncIntervalFlag time.Duration

var connectCmd = &cobra.Command{
	Use:     "connect <name> <url> <#channel>...",
	Aliases: []string{"j", "co"},
	Short:   "Join selected channels from a coordinator as #name/channel",
	Args:    cobra.MinimumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := bootstrap()
		if err != nil {
			return err
		}
		db, err := internal.OpenDB()
		if err != nil {
			return err
		}
		defer db.Close()
		token := connectTokenFlag
		if token == "" {
			token = os.Getenv("GRPVN_INVITE")
		}
		if err := internal.ConnectRemote(cmd.Context(), db, args[0], args[1], token, args[2:]); err != nil {
			return err
		}
		for _, channel := range args[2:] {
			target := internal.RemoteChannelTarget(args[0], channel)
			if !containsString(st.Follow, target) {
				st.Follow = append(st.Follow, target)
			}
			fmt.Println(target)
		}
		if err := st.Save(internal.ResolveStatePath(statePathFlag)); err != nil {
			return err
		}
		registration, err := internal.RemoteRegistration(db, args[0])
		if err != nil {
			return err
		}
		if registration.Status != "active" {
			fmt.Printf("registration %s; ask the coordinator admin to approve %s with explicit channel grants\n", registration.Status, registration.ApprovalCode)
			return nil
		}
		_, err = internal.SyncRemotes(cmd.Context(), db, args[0])
		return err
	},
}

var remotesCmd = &cobra.Command{
	Use:     "remotes",
	Aliases: []string{"re"},
	Short:   "List configured sources, their channels, pending sends, and sync errors",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := internal.OpenDB()
		if err != nil {
			return err
		}
		defer db.Close()
		return internal.PrintRemotes(os.Stdout, db)
	},
}

var syncCmd = &cobra.Command{
	Use:     "sync [remote]",
	Aliases: []string{"sy"},
	Short:   "Push queued sends and pull joined channels; optionally keep syncing",
	Long:    "Push queued sends and pull joined channels. Use --watch for a foreground loop,\nor grpvn daemon start for one shared background worker per SQLite database.",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if syncIntervalFlag <= 0 {
			return fmt.Errorf("--interval must be positive")
		}
		db, err := internal.OpenDB()
		if err != nil {
			return err
		}
		defer db.Close()
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		if name != "" {
			remotes, err := internal.ListRemotes(db)
			if err != nil {
				return err
			}
			found := false
			for _, remote := range remotes {
				found = found || remote.Name == name
			}
			if !found {
				return fmt.Errorf("unknown remote %q", name)
			}
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		for {
			var stats internal.SyncStats
			var err error
			if syncWatchFlag {
				stats, err = internal.SyncDue(ctx, db, name, syncIntervalFlag)
			} else {
				stats, err = internal.SyncRemotes(ctx, db, name)
			}
			if !syncWatchFlag {
				fmt.Printf("pushed %d, pulled %d\n", stats.Pushed, stats.Pulled)
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "sync:", internal.SyncErrorLine(err))
			} else if stats.Pushed+stats.Pulled > 0 {
				fmt.Printf("pushed %d, pulled %d\n", stats.Pushed, stats.Pulled)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(syncIntervalFlag):
			}
		}
	},
}

func init() {
	connectCmd.Flags().StringVarP(&connectTokenFlag, "token", "k", "", "deprecated alias for --invite")
	connectCmd.Flags().StringVar(&connectTokenFlag, "invite", "", "redeem a person/device invitation (also accepted in the URL fragment)")
	_ = connectCmd.Flags().MarkDeprecated("token", "use --invite; shared server tokens do not grant membership")
	syncCmd.Flags().BoolVar(&syncWatchFlag, "watch", false, "keep syncing until interrupted")
	syncCmd.Flags().DurationVar(&syncIntervalFlag, "interval", time.Second, "delay between syncs in watch mode")
	rootCmd.AddCommand(connectCmd, remotesCmd, syncCmd)
}
