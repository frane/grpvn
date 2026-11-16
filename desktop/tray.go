package main

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type trayWorkspace struct {
	ID    string
	Label string
}
type trayState struct {
	Ready      bool
	Count      int
	Paused     bool
	Workspace  string
	Workspaces []trayWorkspace
}
type trayAction struct {
	Action    string `json:"action"`
	Workspace string `json:"workspace"`
	Paused    bool   `json:"paused"`
}
type desktopTray struct {
	menu                                      *application.Menu
	attention, search, agent, pause, settings *application.MenuItem
	workspaces                                *application.Menu
	state                                     trayState
	action                                    func(trayAction)
}

func newDesktopTray(action func(trayAction), open, quit func()) *desktopTray {
	t := &desktopTray{menu: application.NewMenu(), action: action}
	t.menu.Add("Open grpvn").OnClick(func(*application.Context) { open() })
	t.attention = t.menu.Add("Needs you").OnClick(func(*application.Context) { action(trayAction{Action: "attention"}) })
	t.search = t.menu.Add("Find conversation…").OnClick(func(*application.Context) { action(trayAction{Action: "search"}) })
	t.workspaces = t.menu.AddSubmenu("Workspaces")
	t.menu.AddSeparator()
	t.agent = t.menu.Add("Connect an agent…").OnClick(func(*application.Context) { action(trayAction{Action: "agent"}) })
	t.pause = t.menu.AddCheckbox("Pause notifications", false)
	t.settings = t.menu.Add("Notification settings…").OnClick(func(*application.Context) { action(trayAction{Action: "notifications"}) })
	t.menu.AddSeparator()
	t.menu.Add("Quit grpvn").OnClick(func(*application.Context) { quit() })
	t.apply(trayState{})
	return t
}

func normalizeTrayState(command desktopCommand) trayState {
	state := trayState{Ready: command.Ready, Count: command.Count, Paused: command.Paused, Workspace: command.Workspace}
	if !state.Ready {
		return trayState{}
	}
	if state.Count < 0 {
		state.Count = 0
	}
	if state.Count > 999999 {
		state.Count = 999999
	}
	seen := map[string]bool{}
	for _, source := range command.Workspaces {
		if len(state.Workspaces) >= 32 {
			break
		}
		if seen[source.ID] || len(source.ID) > 200 || strings.ContainsAny(source.ID, "\r\n\x00") {
			continue
		}
		label := strings.TrimSpace(notificationText(source.Label, 100))
		if label == "" || strings.ContainsAny(label, "\r\n") {
			continue
		}
		seen[source.ID] = true
		state.Workspaces = append(state.Workspaces, trayWorkspace{ID: source.ID, Label: label})
	}
	return state
}

// Called on the UI thread. Reuse the menu so native open-menu callbacks remain
// valid; change it only when a count, preference or workspace actually changes.
func (t *desktopTray) update(command desktopCommand) bool {
	state := normalizeTrayState(command)
	if reflect.DeepEqual(state, t.state) {
		return false
	}
	t.apply(state)
	return true
}
func (t *desktopTray) apply(state trayState) {
	sourcesChanged := t.workspaces.ItemAt(0) == nil || state.Ready != t.state.Ready || state.Workspace != t.state.Workspace || !reflect.DeepEqual(state.Workspaces, t.state.Workspaces)
	t.state = state
	label := "Needs you"
	if state.Count > 0 {
		label = fmt.Sprintf("Needs you (%d)", state.Count)
	}
	t.attention.SetLabel(label)
	for _, item := range []*application.MenuItem{t.attention, t.search, t.agent, t.pause, t.settings} {
		item.SetEnabled(state.Ready)
	}
	t.pause.SetChecked(state.Paused)
	t.pause.OnClick(func(*application.Context) {
		// Keep the checkmark on the acknowledged preference until persistence
		// succeeds. A failed save must not leave the tray showing a false state.
		t.pause.SetChecked(state.Paused)
		t.action(trayAction{Action: "pause-notifications", Paused: !state.Paused})
	})
	if !sourcesChanged {
		return
	}
	t.workspaces.Clear()
	for _, source := range state.Workspaces {
		t.workspaces.AddRadio(source.Label, source.ID == state.Workspace).OnClick(func(*application.Context) { t.action(trayAction{Action: "workspace", Workspace: source.ID}) })
	}
	if len(state.Workspaces) > 0 {
		t.workspaces.AddSeparator()
	}
	t.workspaces.Add("Add workspace…").SetEnabled(state.Ready).OnClick(func(*application.Context) { t.action(trayAction{Action: "connect"}) })
}

func (t *desktopTray) tooltip() string {
	label := "grpvn"
	if t.state.Count > 0 {
		label += fmt.Sprintf(" — %d need your attention", t.state.Count)
	}
	if t.state.Paused {
		label += " · notifications paused"
	}
	return label
}
