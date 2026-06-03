package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// All shipped JSON manifests must parse cleanly and carry the documented
// fields. Catches regressions like the literal `\"` bug that shipped in v0.1
// preview before this commit.
func TestManifestsAreValidJSON(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		path string
		want []string // top-level keys that must be present
	}{
		{".claude-plugin/manifest.json", []string{"name", "description", "mcp"}},
		{".claude-plugin/marketplace.json", []string{"name", "owner", "plugins"}},
		{"gemini-extension.json", []string{"name", "mcp"}},
		{"plugin/.claude-plugin/plugin.json", []string{"name", "version", "license"}},
		{"plugin/.codex-plugin/plugin.json", []string{"name", "version", "skills", "mcpServers", "interface"}},
		{"plugin/.mcp.json", []string{"mcpServers"}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, c.path))
			if err != nil {
				t.Fatalf("read %s: %v", c.path, err)
			}
			var doc map[string]interface{}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatalf("parse %s: %v", c.path, err)
			}
			for _, k := range c.want {
				if _, ok := doc[k]; !ok {
					t.Fatalf("%s missing top-level key %q", c.path, k)
				}
			}
		})
	}
}

func TestMarketplaceListsPluginSource(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".claude-plugin/marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Plugins) != 1 {
		t.Fatalf("marketplace should list exactly 1 plugin, got %d", len(doc.Plugins))
	}
	p := doc.Plugins[0]
	if p.Name != "grpvn" {
		t.Fatalf("plugin name should be grpvn, got %q", p.Name)
	}
	if p.Source != "./plugin" {
		t.Fatalf("plugin source should be ./plugin, got %q", p.Source)
	}
	// And the source actually has to exist.
	if _, err := os.Stat(filepath.Join(root, "plugin", ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("plugin source dir missing the canonical plugin.json: %v", err)
	}
}

// .mcp.json under plugin/ must declare grpvn as a server with command=grpvn
// and args=["serve"]. Mismatches would silently break Codex/marketplace
// installs.
func TestPluginMCPManifestShape(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "plugin/.mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		McpServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	srv, ok := doc.McpServers["grpvn"]
	if !ok {
		t.Fatal("plugin/.mcp.json must define mcpServers.grpvn")
	}
	if srv.Command != "grpvn" {
		t.Fatalf("expected command=grpvn, got %q", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "serve" {
		t.Fatalf("expected args=[\"serve\"], got %v", srv.Args)
	}
}

// plugin/skills/grpvn/SKILL.md and plugin/GEMINI.md exist and match the
// canonical sources at the repo root. sync-plugin.sh keeps them in lockstep;
// this test fails the moment they drift.
func TestPluginSkillContentMatchesCanonical(t *testing.T) {
	root := repoRoot(t)
	pairs := [][2]string{
		{"skills/grpvn/SKILL.md", "plugin/skills/grpvn/SKILL.md"},
		{"GEMINI.md", "plugin/GEMINI.md"},
	}
	for _, pair := range pairs {
		a, err := os.ReadFile(filepath.Join(root, pair[0]))
		if err != nil {
			t.Fatalf("read canonical %s: %v", pair[0], err)
		}
		b, err := os.ReadFile(filepath.Join(root, pair[1]))
		if err != nil {
			t.Fatalf("read plugin %s: %v", pair[1], err)
		}
		if string(a) != string(b) {
			t.Fatalf("plugin/%s drifted from canonical %s; run scripts/sync-plugin.sh", pair[1], pair[0])
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// Tests run from the tests/ directory; the repo root is one level up.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(wd)
}
