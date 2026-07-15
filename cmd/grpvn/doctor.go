package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose setups where notifications would be silently dead",
	Long: `Checks every identity on this host for the mistakes that make grpvn look
installed but never deliver: state files that follow no channels, multiple
identities other agents don't know to address, and Claude Code settings
missing the notification hooks or permissions. Warnings come with the
command that fixes them.`,
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if err := internal.Doctor(os.Stdout, home, internal.ResolveStatePath(statePathFlag)); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
