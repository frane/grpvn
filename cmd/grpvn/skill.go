package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage agent skill manifests",
}

var skillInstallAllFlag bool

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install SKILL.md and wire MCP into detected agent configs",
	Long: `Detects which agents are present under HOME (Claude Code, Cursor,
Codex CLI, Gemini CLI, Claude Desktop on macOS, plus the generic ~/.agents)
and writes SKILL.md plus an mcpServers.grpvn entry into each one. Pass --all
to install into every known target regardless of detection.`,
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		if skillInstallAllFlag {
			err = internal.InstallSkillAll(os.Stdout)
		} else {
			err = internal.InstallSkill(os.Stdout)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

var skillPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print the embedded SKILL.md to stdout",
	Run: func(cmd *cobra.Command, args []string) {
		os.Stdout.Write(internal.SkillContent)
	},
}

func init() {
	skillInstallCmd.Flags().BoolVar(&skillInstallAllFlag, "all", false, "install into every known target, not just detected ones")
	skillCmd.AddCommand(skillInstallCmd, skillPrintCmd)
	rootCmd.AddCommand(skillCmd)
}
