package main

import (
	"strings"
	"testing"
)

func TestTrayFollowsSessionAttentionAndWorkspace(t *testing.T) {
	tray := newDesktopTray(func(trayAction) {}, func() {}, func() {})
	if tray.attention.Enabled() || tray.pause.Enabled() {
		t.Fatal("Actions must wait for an authenticated UI")
	}
	command := desktopCommand{Ready: true, Count: 7, Paused: true, Workspace: "work", Workspaces: []trayWorkspace{{ID: "", Label: "Local chat"}, {ID: "work", Label: "chat.example.test (work)"}}}
	if !tray.update(command) {
		t.Fatal("new state ignored")
	}
	if !tray.attention.Enabled() || !tray.pause.Checked() || !strings.Contains(tray.attention.Label(), "7") {
		t.Fatal("attention/preference not reflected")
	}
	if tray.workspaces.ItemAt(0).Checked() || !tray.workspaces.ItemAt(1).Checked() {
		t.Fatal("current workspace not selected")
	}
	if !strings.Contains(tray.tooltip(), "7") || !strings.Contains(tray.tooltip(), "paused") {
		t.Fatal("tooltip lost live state")
	}
	if tray.update(command) {
		t.Fatal("identical feed refresh should not rebuild native menus")
	}
	command.Count = 0
	command.Paused = false
	command.Workspace = ""
	tray.update(command)
	if tray.pause.Checked() || tray.attention.Label() != "Needs you" || !tray.workspaces.ItemAt(0).Checked() {
		t.Fatal("read/resume/switch did not update menu")
	}
	tray.update(desktopCommand{})
	if tray.attention.Enabled() || tray.workspaces.ItemAt(0).Enabled() || tray.tooltip() != "grpvn" {
		t.Fatal("sign-out retained profile state")
	}
}

func TestTrayRejectsMalformedSources(t *testing.T) {
	state := normalizeTrayState(desktopCommand{Ready: true, Count: -1, Workspaces: []trayWorkspace{{ID: "local", Label: "Local"}, {ID: "local", Label: "duplicate"}, {ID: "bad\n", Label: "bad"}, {ID: "bad", Label: "bad\nlabel"}, {ID: "empty"}}})
	if state.Count != 0 || len(state.Workspaces) != 1 {
		t.Fatalf("malformed state accepted: %+v", state)
	}
}
