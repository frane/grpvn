#!/usr/bin/env bash
# Copies the canonical SKILL.md and GEMINI.md into the plugin/ subtree so the
# Claude Code marketplace bundle, Codex plugin bundle, and Gemini extension
# all ship the same content as the standalone CLI.

set -euo pipefail
cd "$(dirname "$0")/.."

cp skills/grpvn/SKILL.md   plugin/skills/grpvn/SKILL.md
cp GEMINI.md               plugin/GEMINI.md

echo "synced:"
echo "  plugin/skills/grpvn/SKILL.md"
echo "  plugin/GEMINI.md"
