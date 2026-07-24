package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var versionCmd = &cobra.Command{
	Use: "version",
	Run: func(cmd *cobra.Command, args []string) {
		out := version
		if commit != "" {
			out += " (" + commit
			if date != "" {
				out += ", " + date
			}
			out += ")"
		}
		fmt.Println(out)
	},
}

var rootCmd = &cobra.Command{
	Use:           "grpvn",
	Short:         "Local-first peer chat for AI agents",
	SilenceUsage:  true,
	SilenceErrors: true,
	// --scope travels through the same env var the MCP configs set, so
	// every state-path resolution in the process — including ones that
	// never see the flag value — agrees on the active scope.
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if scopeFlag != "" {
			os.Setenv("GRPVN_SCOPE", scopeFlag)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		checkCmd.Run(cmd, args)
	},
}

var (
	statePathFlag, asFlag, colorFlag string
	scopeFlag                        string
	humanFlag, fullFlag, tsFlag      bool
	countFlag                        int

	// Overridden by goreleaser via -X main.version / main.commit / main.date.
	version = "0.7.1"
	commit  = ""
	date    = ""
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func init() {
	rootCmd.PersistentFlags().StringVarP(&statePathFlag, "state", "s", "", "state file path")
	rootCmd.PersistentFlags().StringVar(&scopeFlag, "scope", "", `identity scope: "project" keys the state file by the current project root; "host" forces the base file even when $GRPVN_SCOPE=project`)
	rootCmd.PersistentFlags().StringVarP(&asFlag, "as", "a", "", "agent name")
	rootCmd.PersistentFlags().BoolVarP(&humanFlag, "human", "H", false, "human mode")
	rootCmd.PersistentFlags().BoolVarP(&fullFlag, "full", "F", false, "full ULIDs")
	rootCmd.PersistentFlags().BoolVarP(&tsFlag, "ts", "t", false, "timestamps")
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVarP(&colorFlag, "color", "c", "auto", "color mode")
	rootCmd.PersistentFlags().IntVarP(&countFlag, "limit", "n", 0, "limit results")
}

func bootstrap() (string, *internal.State, error) {
	p := internal.ResolveStatePath(statePathFlag)
	base := internal.ResolveBaseStatePath(statePathFlag)
	var s *internal.State
	var err error
	if p != base {
		// Project scope: a first touch in a new project inherits the
		// runtime's follows and default channel instead of starting deaf.
		s, err = internal.LoadStateSeeded(p, base)
	} else {
		s, err = internal.LoadState(p)
	}
	if err != nil {
		return "", nil, err
	}
	n := asFlag
	if n == "" {
		n = os.Getenv("GRPVN_AS")
	}
	if n == "" {
		n = s.Name
	}
	minted := false
	if n == "" {
		n, err = internal.GenerateIdentity()
		if err != nil {
			return "", nil, fmt.Errorf("generate identity: %w", err)
		}
		s.Name = n
		minted = true
		// Surface save failures instead of silently regenerating the
		// identity on every subsequent call. Claude Desktop launches with
		// an unwritable cwd; the historic silent-swallow turned that into
		// an identity-mint loop.
		if err := s.Save(p); err != nil {
			return "", nil, fmt.Errorf("save state to %s: %w", p, err)
		}
	} else if s.Name == "" {
		// Identity came from --as or GRPVN_AS; persist it so subsequent
		// calls without the flag see the same name.
		s.Name = n
		minted = true
		if err := s.Save(p); err != nil {
			return "", nil, fmt.Errorf("save state to %s: %w", p, err)
		}
	}
	if minted {
		// A new participant starts reading from now; the host's history
		// stays reachable via l but must not arrive as unread. Best-effort:
		// on failure the identity works, just with a noisy first read.
		if db, err := internal.OpenDB(); err == nil {
			if err := internal.FastForwardCursors(db, s); err != nil {
				fmt.Fprintln(os.Stderr, "warn: fast-forward cursors:", err)
			}
			db.Close()
		}
	}
	return n, s, nil
}
