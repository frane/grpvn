package internal

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

//go:embed embedded/SKILL.md
var SkillContent []byte

var SkillTargets = []string{
	".agents/skills/grpvn/SKILL.md",
	".claude/skills/grpvn/SKILL.md",
	".codex/skills/grpvn/SKILL.md",
	".gemini/skills/grpvn/SKILL.md",
}

func InstallSkill(w io.Writer) error {
	h, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	var lastErr error
	written := 0
	for _, rel := range SkillTargets {
		p := filepath.Join(h, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			lastErr = err
			continue
		}
		if err := os.WriteFile(p, SkillContent, 0644); err != nil {
			lastErr = err
			continue
		}
		fmt.Fprintf(w, "installed to %s\n", p)
		written++
	}
	if written == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}
