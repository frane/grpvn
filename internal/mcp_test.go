package internal

import (
	"strings"
	"testing"
)

func TestSkillAndMCPShareRelevancePolicy(t *testing.T) {
	phrases := []string{
		"relevant",
		"leave the rest unread",
		"relay",
		"@me",
	}
	skill := strings.ToLower(string(SkillContent))
	mcp := strings.ToLower(MCPInstructions + "\n" + mcpDescC + "\n" + mcpDescR + "\n" + mcpDescP + "\n" + mcpDescW)
	for _, p := range phrases {
		want := strings.ToLower(p)
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md missing %q", p)
		}
		if !strings.Contains(mcp, want) {
			t.Errorf("MCP instructions/tool descriptions missing %q", p)
		}
	}
	if !strings.Contains(mcpDescR, "target") {
		t.Error("r tool description must mention target")
	}
	if !strings.Contains(mcpDescP, "target") {
		t.Error("p tool description must mention target")
	}
	if !strings.Contains(mcpDescC, "per followed channel") {
		t.Error("c tool description must say unread is per channel")
	}
	if !strings.Contains(string(SkillContent), "MCP") {
		t.Error("SKILL.md must mention the MCP tools so CLI-skill and MCP stay one protocol")
	}
	if !strings.Contains(MCPInstructions, "Pass target on r/p") {
		t.Error("MCP initialize instructions must tell the host to pass target on r/p")
	}
}
