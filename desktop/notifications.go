package main

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

type desktopCommand struct {
	Type       string
	URL        string
	RequestID  string
	ID         string
	Title      string
	Body       string
	Target     string
	Root       string
	Sound      bool
	Count      int
	Action     string
	Workspace  string
	Ready      bool
	Paused     bool
	Workspaces []trayWorkspace
}
type systemNotifications interface {
	ServiceStartup(context.Context, application.ServiceOptions) error
	ServiceShutdown() error
	RequestNotificationAuthorization() (bool, error)
	CheckNotificationAuthorization() (bool, error)
	SendNotification(notifications.NotificationOptions) error
	OnNotificationResponse(func(notifications.NotificationResult))
}
type desktopNotifications struct {
	service           systemNotifications
	dock              *dock.DockService
	notificationError error
	dockError         error
}

func newDesktopNotifications() *desktopNotifications {
	return &desktopNotifications{service: newSystemNotifications(), dock: dock.New()}
}
func (n *desktopNotifications) ServiceName() string { return "grpvn-notifications" }
func (n *desktopNotifications) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	n.notificationError = n.service.ServiceStartup(ctx, options)
	n.dockError = n.dock.ServiceStartup(ctx, options)
	// Missing desktop services must never prevent chat from opening.
	return nil
}
func (n *desktopNotifications) ServiceShutdown() error {
	if n.notificationError == nil {
		_ = n.service.ServiceShutdown()
	}
	if n.dockError == nil {
		_ = n.dock.ServiceShutdown()
	}
	return nil
}
func notificationCommand(kind string) bool {
	return kind == "notificationStatus" || kind == "requestNotifications" || kind == "notify" || kind == "notificationBadge"
}
func notificationText(value string, limit int) string {
	runes := []rune(strings.ReplaceAll(value, "\x00", ""))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}
func validNotificationTarget(target, root string) bool {
	if len(target) < 2 || len(target) > 300 || strings.ContainsAny(target, "\r\n\x00") || (!strings.HasPrefix(target, "#") && !strings.HasPrefix(target, "@")) {
		return false
	}
	if root != "" {
		if len(root) != 26 {
			return false
		}
		for _, c := range root {
			if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", c) {
				return false
			}
		}
	}
	return true
}
func (n *desktopNotifications) handle(command desktopCommand, reply func(any)) {
	if command.Type == "notificationBadge" {
		if n.dockError == nil {
			if command.Count <= 0 {
				_ = n.dock.RemoveBadge()
			} else {
				label := strconv.Itoa(command.Count)
				if command.Count > 99 {
					label = "99+"
				}
				_ = n.dock.SetBadge(label)
			}
		}
		return
	}
	result := map[string]any{"requestID": command.RequestID, "supported": n.notificationError == nil, "granted": false}
	var err error
	granted := false
	if n.notificationError != nil {
		err = n.notificationError
	} else {
		switch command.Type {
		case "notificationStatus":
			granted, err = n.service.CheckNotificationAuthorization()
		case "requestNotifications":
			granted, err = n.service.RequestNotificationAuthorization()
		case "notify":
			if !validNotificationTarget(command.Target, command.Root) || command.ID == "" || len(command.ID) > 500 || (command.Action != "" && command.Action != "requests") || len(command.Workspace) > 200 || strings.ContainsAny(command.Workspace, "\r\n\x00") {
				err = fmt.Errorf("invalid notification target")
				break
			}
			granted, err = n.service.CheckNotificationAuthorization()
			if err == nil && granted {
				threadID := command.Target + ":" + command.Root
				// Windows displays ThreadID as a visible toast header. Our own
				// burst grouping already prevents one toast per message.
				if runtime.GOOS == "windows" {
					threadID = ""
				}
				err = n.service.SendNotification(notifications.NotificationOptions{ID: command.ID, Title: notificationText(command.Title, 140), Body: notificationText(command.Body, 600), ThreadID: threadID, Sound: &notifications.NotificationSound{Silent: !command.Sound}, Data: map[string]interface{}{"target": command.Target, "root": command.Root, "action": command.Action, "workspace": command.Workspace}})
			}
		}
	}
	result["granted"] = granted
	if err != nil {
		result["error"] = err.Error()
	}
	reply(result)
}
func (n *desktopNotifications) onOpen(open func(string, string, string, string)) {
	n.service.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil || result.Response.ActionIdentifier != notifications.DefaultActionIdentifier {
			return
		}
		target, _ := result.Response.UserInfo["target"].(string)
		root, _ := result.Response.UserInfo["root"].(string)
		action, _ := result.Response.UserInfo["action"].(string)
		workspace, _ := result.Response.UserInfo["workspace"].(string)
		if validNotificationTarget(target, root) {
			if action != "" && action != "requests" {
				return
			}
			open(target, root, action, workspace)
		}
	})
}
func dispatchNotificationReply(window *application.WebviewWindow, value any) {
	encoded, err := json.Marshal(value)
	if err == nil && window != nil {
		window.ExecJS("window.dispatchEvent(new CustomEvent('grpvn:native-notification',{detail:" + string(encoded) + "}));")
	}
}
