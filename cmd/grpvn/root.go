package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var versionCmd = &cobra.Command{
	Use: "version",
	Run: func(cmd *cobra.Command, args []string) { fmt.Println(version) },
}

var rootCmd = &cobra.Command{
	Use:           "grpvn",
	Short:         "Local-first peer chat for AI agents",
	SilenceUsage:  true,
	SilenceErrors: true,
	Run: func(cmd *cobra.Command, args []string) {
		checkCmd.Run(cmd, args)
	},
}

var (
	statePathFlag, asFlag, colorFlag string
	humanFlag, fullFlag, tsFlag      bool
	countFlag                        int
	version                          = "0.1.0"
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func init() {
	rootCmd.PersistentFlags().StringVarP(&statePathFlag, "state", "s", "", "state file path")
	rootCmd.PersistentFlags().StringVarP(&asFlag, "as", "a", "", "agent name")
	rootCmd.PersistentFlags().BoolVarP(&humanFlag, "human", "H", false, "human mode")
	rootCmd.PersistentFlags().BoolVarP(&fullFlag, "full", "F", false, "full ULIDs")
	rootCmd.PersistentFlags().BoolVarP(&tsFlag, "ts", "t", false, "timestamps")
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVarP(&colorFlag, "color", "c", "auto", "color mode")
	rootCmd.PersistentFlags().IntVarP(&countFlag, "limit", "n", 0, "limit results")
}

func bootstrap() (string, *internal.State, error) {
	p := statePathFlag
	if p == "" {
		p = os.Getenv("GRPVN_STATE")
	}
	if p == "" {
		p = ".grpvn/state.json"
	}
	s, err := internal.LoadState(p)
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
	if n == "" {
		n, _ = internal.GenerateIdentity()
		s.Name = n
		_ = s.Save(p)
	}
	return n, s, nil
}
