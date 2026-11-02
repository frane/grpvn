package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/frane/grpvn/internal"
	"github.com/spf13/cobra"
)

var daemonIntervalFlag time.Duration

var daemonCmd = &cobra.Command{
	Use:     "daemon",
	Aliases: []string{"d"},
	Short:   "Keep remote channels synced for every local agent using this database",
	Long: `Continuously pull joined remote channels and deliver queued sends.
Runs in the foreground by default; use start, stop, and status to manage
a background worker. One daemon owns each SQLite database. Normal commands
use the local cache while it is running; explicit sync can still force a pass.`,
	Args: cobra.NoArgs,
	RunE: runDaemon,
}

func runDaemon(cmd *cobra.Command, args []string) error {
	db, err := internal.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return internal.RunDaemon(ctx, db, daemonIntervalFlag, os.Stdout)
}

var daemonRunCmd = &cobra.Command{
	Use:     "run",
	Aliases: []string{"r"},
	Short:   "Run the local sync daemon in the foreground",
	Args:    cobra.NoArgs,
	RunE:    runDaemon,
}

var daemonStartCmd = &cobra.Command{
	Use:     "start",
	Aliases: []string{"s"},
	Short:   "Start the local sync daemon in the background",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if daemonIntervalFlag < 250*time.Millisecond {
			return fmt.Errorf("--interval must be at least 250ms")
		}
		db, err := internal.OpenDB()
		if err != nil {
			return err
		}
		defer db.Close()
		state, err := internal.ReadDaemonState(cmd.Context(), db)
		if err != nil {
			return err
		}
		if state.Running() {
			fmt.Printf("daemon already running pid=%d\n", state.PID)
			return nil
		}
		logPath, err := internal.DaemonLogPath(db)
		if err != nil {
			return err
		}
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer log.Close()
		null, err := os.Open(os.DevNull)
		if err != nil {
			return err
		}
		defer null.Close()
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		child := exec.Command(executable, "daemon", "run", "--interval", daemonIntervalFlag.String())
		child.Env, child.Stdin, child.Stdout, child.Stderr = os.Environ(), null, log, log
		detachDaemon(child)
		if err := child.Start(); err != nil {
			return err
		}
		exited := make(chan error, 1)
		go func() { exited <- child.Wait() }()
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			state, err := internal.ReadDaemonState(ctx, db)
			if err == nil && state.Running() {
				if state.PID != child.Process.Pid {
					_ = child.Process.Kill()
					<-exited
				}
				fmt.Printf("daemon running pid=%d log=%s\n", state.PID, logPath)
				return nil
			}
			select {
			case err := <-exited:
				// A concurrent starter may have acquired the lease instead.
				state, stateErr := internal.ReadDaemonState(ctx, db)
				if stateErr == nil && state.Running() {
					fmt.Printf("daemon already running pid=%d\n", state.PID)
					return nil
				}
				return fmt.Errorf("daemon exited during startup (%v); see %s", err, logPath)
			case <-ctx.Done():
				_ = child.Process.Kill()
				<-exited
				return fmt.Errorf("daemon startup: %w; see %s", ctx.Err(), logPath)
			case <-ticker.C:
			}
		}
	},
}

var daemonStopCmd = &cobra.Command{
	Use:     "stop",
	Aliases: []string{"x"},
	Short:   "Request a graceful stop of the local daemon",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := internal.OpenDB()
		if err != nil {
			return err
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		running, err := internal.StopDaemon(ctx, db)
		if err != nil {
			return err
		}
		if running {
			fmt.Println("daemon stopped")
		} else {
			fmt.Println("daemon not running")
		}
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"i"},
	Short:   "Show whether this database has a live daemon",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := internal.OpenDB()
		if err != nil {
			return err
		}
		defer db.Close()
		return printDaemonStatus(cmd.Context(), db)
	},
}

func printDaemonStatus(ctx context.Context, db *sql.DB) error {
	state, err := internal.ReadDaemonState(ctx, db)
	if err != nil {
		return err
	}
	if !state.Running() {
		fmt.Println("daemon not running")
		return nil
	}
	status := "running"
	if state.StopRequested {
		status = "stopping"
	}
	logPath, err := internal.DaemonLogPath(db)
	if err != nil {
		return err
	}
	fmt.Printf("daemon %s pid=%d interval=%s started=%s log=%s\n", status, state.PID, time.Duration(state.IntervalMS)*time.Millisecond, time.UnixMilli(state.StartedAt).Format(time.RFC3339), logPath)
	return nil
}

func init() {
	daemonCmd.PersistentFlags().DurationVarP(&daemonIntervalFlag, "interval", "i", time.Second, "delay between remote syncs")
	daemonCmd.AddCommand(daemonRunCmd, daemonStartCmd, daemonStopCmd, daemonStatusCmd)
	rootCmd.AddCommand(daemonCmd)
}
