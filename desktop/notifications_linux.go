//go:build linux

package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

const notificationInterface = "org.freedesktop.Notifications"
const notificationPath = dbus.ObjectPath("/org/freedesktop/Notifications")

type linuxNotifications struct {
	mu       sync.Mutex
	send     sync.Mutex
	conn     *dbus.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	byID     map[uint32]notifications.NotificationOptions
	ids      map[string]uint32
	callback func(notifications.NotificationResult)
}

func newSystemNotifications() systemNotifications {
	return &linuxNotifications{byID: map[uint32]notifications.NotificationOptions{}, ids: map[string]uint32{}}
}
func (n *linuxNotifications) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("desktop notification service is unavailable: %w", err)
	}
	var capabilities []string
	if err = conn.Object(notificationInterface, notificationPath).CallWithContext(ctx, notificationInterface+".GetCapabilities", 0).Store(&capabilities); err != nil {
		conn.Close()
		return fmt.Errorf("desktop notification service is unavailable: %w", err)
	}
	if err = conn.AddMatchSignal(dbus.WithMatchInterface(notificationInterface), dbus.WithMatchObjectPath(notificationPath)); err != nil {
		conn.Close()
		return err
	}
	n.conn = conn
	n.ctx, n.cancel = context.WithCancel(ctx)
	n.done = make(chan struct{})
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go func() {
		defer close(n.done)
		defer conn.RemoveSignal(signals)
		for {
			select {
			case <-n.ctx.Done():
				return
			case signal, ok := <-signals:
				if !ok {
					return
				}
				n.handleSignal(signal)
			}
		}
	}()
	return nil
}
func (n *linuxNotifications) ServiceShutdown() error {
	if n.cancel != nil {
		n.cancel()
	}
	if n.conn != nil {
		_ = n.conn.Close()
	}
	if n.done != nil {
		<-n.done
	}
	return nil
}
func (n *linuxNotifications) CheckNotificationAuthorization() (bool, error) {
	return n.conn != nil, nil
}
func (n *linuxNotifications) RequestNotificationAuthorization() (bool, error) {
	return n.CheckNotificationAuthorization()
}
func (n *linuxNotifications) OnNotificationResponse(callback func(notifications.NotificationResult)) {
	n.mu.Lock()
	n.callback = callback
	n.mu.Unlock()
}
func (n *linuxNotifications) SendNotification(options notifications.NotificationOptions) error {
	if n.conn == nil {
		return fmt.Errorf("desktop notification service is unavailable")
	}
	n.send.Lock()
	defer n.send.Unlock()
	n.mu.Lock()
	replaces := n.ids[options.ID]
	n.mu.Unlock()
	hints := map[string]dbus.Variant{"desktop-entry": dbus.MakeVariant("com.offbyte.grpvn")}
	if options.Sound != nil {
		hints["suppress-sound"] = dbus.MakeVariant(options.Sound.Silent)
	}
	var id uint32
	err := n.conn.Object(notificationInterface, notificationPath).CallWithContext(n.ctx, notificationInterface+".Notify", 0, "grpvn", replaces, "com.offbyte.grpvn", options.Title, options.Body, []string{"default", "Open chat"}, hints, int32(-1)).Store(&id)
	if err != nil {
		return err
	}
	n.mu.Lock()
	if replaces != 0 && replaces != id {
		delete(n.byID, replaces)
	}
	n.byID[id] = options
	n.ids[options.ID] = id
	n.mu.Unlock()
	return nil
}
func (n *linuxNotifications) handleSignal(signal *dbus.Signal) {
	if signal == nil || len(signal.Body) < 2 {
		return
	}
	id, ok := signal.Body[0].(uint32)
	if !ok {
		return
	}
	n.mu.Lock()
	options, exists := n.byID[id]
	callback := n.callback
	if !exists {
		n.mu.Unlock()
		return
	}
	// Closing or dismissing a notification only removes it. It must never
	// activate the app; only an explicit ActionInvoked signal opens a thread.
	if signal.Name == notificationInterface+".NotificationClosed" {
		delete(n.byID, id)
		delete(n.ids, options.ID)
		n.mu.Unlock()
		return
	}
	action, ok := signal.Body[1].(string)
	if signal.Name != notificationInterface+".ActionInvoked" || !ok || action != "default" {
		n.mu.Unlock()
		return
	}
	delete(n.byID, id)
	delete(n.ids, options.ID)
	n.mu.Unlock()
	if callback != nil {
		callback(notifications.NotificationResult{Response: notifications.NotificationResponse{ID: options.ID, ActionIdentifier: notifications.DefaultActionIdentifier, Title: options.Title, Body: options.Body, UserInfo: options.Data}})
	}
}
