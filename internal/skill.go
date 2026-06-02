package internal

import (
	"fmt"
	"os"
	"path/filepath"
)

func InstallSkill() error {
	h, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	content, err := os.ReadFile("skills/grpvn/SKILL.md")
	if err != nil {
		return err
	}
	paths := []string{
		filepath.Join(h, ".agents/skills/grpvn/SKILL.md"),
		filepath.Join(h, ".claude/skills/grpvn/SKILL.md"),
		filepath.Join(h, ".codex/skills/grpvn/SKILL.md"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			continue
		}
		if err := os.WriteFile(p, content, 0644); err != nil {
			continue
		}
		fmt.Printf("installed to %s\n", p)
	}
	return nil
}
