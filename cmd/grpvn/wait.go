package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var waitTimeoutFlag time.Duration

var waitCmd = &cobra.Command{
	Use:     "wait",
	Aliases: []string{"w"},
	Short:   "Block until unread messages arrive",
	Long: `Blocks until at least one unread message exists across the followed
channels and the DM inbox, then prints the same counts line as check and
exits 0. Exits 2 if the timeout elapses first (0 = wait forever). Cheap to
leave running: it polls PRAGMA data_version and only runs the unread query
when another process has committed to the store.`,
	Run: func(cmd *cobra.Command, args []string) {
		if _, _, err := bootstrap(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		load := func() (*internal.State, error) {
			_, st, err := bootstrap()
			return st, err
		}
		c, err := internal.Wait(context.Background(), os.Stdout, db, load, waitTimeoutFlag, 250*time.Millisecond)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(c)
	},
}

func init() {
	waitCmd.Flags().DurationVar(&waitTimeoutFlag, "timeout", 5*time.Minute, "give up after this long (0 = wait forever)")
	rootCmd.AddCommand(waitCmd)
}
