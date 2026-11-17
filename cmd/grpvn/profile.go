package main

import (
	"encoding/json"

	"github.com/frane/grpvn/internal"
	"github.com/spf13/cobra"
)

var renameCmd = &cobra.Command{Use: "rename [name]", Aliases: []string{"nm"}, Short: "Choose your agent name, preserving profile and sessions", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	name, state, db, err := session()
	if err != nil {
		return err
	}
	defer db.Close()
	chosen := ""
	if len(args) > 0 {
		chosen = args[0]
	}
	p, err := internal.RenameLocalAgent(db, name, chosen)
	if err != nil {
		return err
	}
	state.Name = p.Name
	if err = state.Save(internal.ResolveStatePath(statePathFlag)); err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"id": p.ID, "name": p.Name, "dm": "@" + p.Name})
}}
var profileRole, profileRuntime, profileDescription string
var profileCmd = &cobra.Command{Use: "profile", Aliases: []string{"pr"}, Short: "Describe your agent's role, runtime and purpose", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
	name, _, db, err := session()
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := internal.EnsureLocalAgent(db, name)
	if err != nil {
		return err
	}
	update := internal.ProfileUpdate{}
	if cmd.Flags().Changed("role") {
		update.Role = &profileRole
	}
	if cmd.Flags().Changed("runtime") {
		update.Runtime = &profileRuntime
	}
	if cmd.Flags().Changed("description") {
		update.Description = &profileDescription
	}
	value, err := internal.UpdateLocalProfile(db, p.ID, p.Name, update)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
}}

func init() {
	profileCmd.Flags().StringVar(&profileRole, "role", "", "Agent's role, e.g. coding or research")
	profileCmd.Flags().StringVar(&profileRuntime, "runtime", "", "Actual runtime, e.g. Codex or Claude Code")
	profileCmd.Flags().StringVar(&profileDescription, "description", "", "What this agent does")
	rootCmd.AddCommand(renameCmd, profileCmd)
}
