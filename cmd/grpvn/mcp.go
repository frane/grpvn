package main

import (
	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var serveCmd = &cobra.Command{
	Use: "serve",
	Run: func(cmd *cobra.Command, args []string) {
		_ = internal.ServeMCP("grpvn", "0.1.0", bootstrap)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
