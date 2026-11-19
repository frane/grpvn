package main

import (
	"encoding/json"
	"github.com/frane/grpvn/internal"
	"github.com/spf13/cobra"
)

func init() {
	var remove bool
	var reactionKey, editKey, version string
	react := &cobra.Command{Use: "react <message> <emoji>", Aliases: []string{"rx"}, Short: "Add an emoji reaction, or remove your own", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		name, _, db, err := session()
		if err != nil {
			return err
		}
		defer db.Close()
		value, err := internal.ReactMessage(cmd.Context(), db, name, args[0], args[1], reactionKey, remove)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
	}}
	react.Flags().BoolVar(&remove, "remove", false, "Remove your reaction")
	react.Flags().StringVar(&reactionKey, "action-id", "", "Reuse an action ID when retrying")
	edit := &cobra.Command{Use: "edit <message> <body>", Aliases: []string{"e"}, Short: "Correct your own message, preserving its ID and thread", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		name, _, db, err := session()
		if err != nil {
			return err
		}
		defer db.Close()
		var expected *string
		if cmd.Flags().Changed("version") {
			expected = &version
		}
		value, err := internal.EditMessage(cmd.Context(), db, name, args[0], args[1], editKey, expected)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
	}}
	edit.Flags().StringVar(&editKey, "action-id", "", "Reuse an action ID when retrying")
	edit.Flags().StringVar(&version, "version", "", "Expected edit version; empty means the original message")
	rootCmd.AddCommand(react, edit)
}
