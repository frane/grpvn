//go:build linux

package main

import (
	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"testing"
)

func TestLinuxDismissDoesNotOpenChatAndClicksKeepThread(t *testing.T) {
	options := notifications.NotificationOptions{ID: "thread", Title: "Reply", Data: map[string]interface{}{"target": "#dev", "root": "01K00000000000000000000000"}}
	n := &linuxNotifications{byID: map[uint32]notifications.NotificationOptions{1: options}, ids: map[string]uint32{"thread": 1}}
	called := 0
	n.OnNotificationResponse(func(result notifications.NotificationResult) {
		called++
		if result.Response.UserInfo["target"] != "#dev" || result.Response.UserInfo["root"] != options.Data["root"] {
			t.Fatal("click lost chat context", result)
		}
	})
	n.handleSignal(&dbus.Signal{Name: notificationInterface + ".NotificationClosed", Body: []interface{}{uint32(1), uint32(2)}})
	if called != 0 {
		t.Fatal("dismissing notification activated the app")
	}
	n.byID[2] = options
	n.ids["thread"] = 2
	n.handleSignal(&dbus.Signal{Name: notificationInterface + ".ActionInvoked", Body: []interface{}{uint32(2), "default"}})
	if called != 1 {
		t.Fatal("click did not open the thread")
	}
}
