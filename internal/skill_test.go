package internal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillContentEmbedded(t *testing.T) {
	if len(SkillContent) == 0 {
		t.Fatal("SKILL.md should be embedded at build time")
	}
	if !bytes.Contains(SkillContent, []byte("grpvn")) {
		t.Fatal("SKILL.md should mention grpvn")
	}
}

func TestInstallSkillWritesToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, rel := range SkillTargets {
		p := filepath.Join(home, rel)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected file %s: %v", p, err)
		}
		if !bytes.Equal(got, SkillContent) {
			t.Fatalf("content mismatch at %s", p)
		}
	}
	if !strings.Contains(buf.String(), "installed to") {
		t.Fatalf("expected install log lines, got %q", buf.String())
	}
}
