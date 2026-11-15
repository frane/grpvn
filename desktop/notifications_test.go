package main

import (
	"context"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"strings"
	"testing"
)

func TestNotificationTargetsAreChatRoutes(t *testing.T) {
	for _, target := range []string{"#dev", "#work/dev", "@rook@alice"} {
		if !validNotificationTarget(target, "") {
			t.Fatal(target)
		}
	}
	for _, target := range []string{"https://example.test", "file:///tmp/x", "", "#dev\nrun"} {
		if validNotificationTarget(target, "") {
			t.Fatal("accepted non-chat route", target)
		}
	}
	if validNotificationTarget("#dev", "not-a-thread") {
		t.Fatal("accepted arbitrary thread id")
	}
	if got := notificationText(strings.Repeat("界", 20), 10); got != strings.Repeat("界", 10)+"…" {
		t.Fatal("split Unicode notification text", got)
	}
}

type fakeSystemNotifications struct {
	granted  bool
	sent     []notifications.NotificationOptions
	callback func(notifications.NotificationResult)
}

func (*fakeSystemNotifications) ServiceStartup(context.Context, application.ServiceOptions) error {
	return nil
}
func (*fakeSystemNotifications) ServiceShutdown() error { return nil }
func (f *fakeSystemNotifications) RequestNotificationAuthorization() (bool, error) {
	return f.granted, nil
}
func (f *fakeSystemNotifications) CheckNotificationAuthorization() (bool, error) {
	return f.granted, nil
}
func (f *fakeSystemNotifications) SendNotification(o notifications.NotificationOptions) error {
	f.sent = append(f.sent, o)
	return nil
}
func (f *fakeSystemNotifications) OnNotificationResponse(fn func(notifications.NotificationResult)) {
	f.callback = fn
}

func TestNativeNotificationPreservesRouteAndRequiresExplicitClick(t *testing.T) {
	service := &fakeSystemNotifications{granted: true}
	n := &desktopNotifications{service: service}
	command := desktopCommand{Type: "notify", RequestID: "request", ID: "group", Title: "A reply", Body: "Thread reply", Target: "#work/dev", Root: "01M7F7WDATRKC6T3B9YT2HZR94"}
	var result map[string]any
	n.handle(command, func(value any) { result = value.(map[string]any) })
	if len(service.sent) != 1 || result["granted"] != true || result["requestID"] != "request" {
		t.Fatal(service.sent, result)
	}
	data := service.sent[0].Data
	if data["target"] != command.Target || data["root"] != command.Root || !service.sent[0].Sound.Silent {
		t.Fatal(service.sent)
	}
	opened := 0
	n.onOpen(func(target, root string) {
		opened++
		if target != command.Target || root != command.Root {
			t.Fatal(target, root)
		}
	})
	service.callback(notifications.NotificationResult{Response: notifications.NotificationResponse{ActionIdentifier: "dismiss", UserInfo: data}})
	if opened != 0 {
		t.Fatal("dismiss activated chat")
	}
	service.callback(notifications.NotificationResult{Response: notifications.NotificationResponse{ActionIdentifier: notifications.DefaultActionIdentifier, UserInfo: data}})
	if opened != 1 {
		t.Fatal("click lost destination")
	}
	service.granted = false
	n.handle(command, func(any) {})
	if len(service.sent) != 1 {
		t.Fatal("sent without system authorization")
	}
	n.notificationError = errors.New("no desktop service")
	n.handle(desktopCommand{Type: "notificationStatus"}, func(value any) { result = value.(map[string]any) })
	if result["supported"] != false || result["error"] == nil {
		t.Fatal(result)
	}
}
