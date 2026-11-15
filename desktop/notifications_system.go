//go:build darwin || windows

package main

import "github.com/wailsapp/wails/v3/pkg/services/notifications"

func newSystemNotifications() systemNotifications { return notifications.New() }
