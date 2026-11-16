package main

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"os/user"
	"runtime"

	"github.com/frane/grpvn/uihost"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func main() {
	name := "local-user"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	var app *application.App
	var window *application.WebviewWindow
	var tray *application.SystemTray
	var trayMenu *desktopTray
	notifier := newDesktopNotifications()
	server, err := uihost.Start(context.Background(), name, "")
	if err != nil {
		log.Fatal(err)
	}
	defer server.Close()
	app = application.New(application.Options{RawMessageHandler: func(_ application.Window, message string, origin *application.OriginInfo) {
		local, _ := url.Parse(server.URL)
		if origin == nil {
			return
		}
		caller, err := url.Parse(origin.Origin)
		// WebKitGTK supplies the document URL and has no native frame flag.
		if err != nil || caller.Scheme != local.Scheme || caller.Host != local.Host || (runtime.GOOS != "linux" && !origin.IsMainFrame) {
			return
		}
		if len(message) > 8192 {
			return
		}
		var command desktopCommand
		if json.Unmarshal([]byte(message), &command) != nil {
			return
		}
		if command.Type == "trayState" {
			application.InvokeAsync(func() {
				if trayMenu != nil && trayMenu.update(command) {
					tray.SetMenu(trayMenu.menu)
					tray.SetTooltip(trayMenu.tooltip())
				}
			})
			return
		}
		if notificationCommand(command.Type) {
			go notifier.handle(command, func(value any) { dispatchNotificationReply(window, value) })
			return
		}
		if !allowedExternalURL(command.Type, command.URL) {
			return
		}
		_ = app.Browser.OpenURL(command.URL)
	}, Name: "grpvn", Description: "grpvn chat", Icon: appIcon, Services: []application.Service{application.NewService(notifier)}, Linux: application.LinuxOptions{ApplicationID: "com.offbyte.grpvn", DisableQuitOnLastWindowClosed: true}, OnShutdown: func() { server.Close() }})
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "grpvn", Width: 1240, Height: 820, MinWidth: 760, MinHeight: 520, URL: server.URL, Linux: application.LinuxWindow{Icon: appIcon}})
	notifier.onOpen(func(target, root, action, workspace string) {
		window.Show()
		window.Focus()
		encoded, _ := json.Marshal(map[string]string{"target": target, "root": root, "action": action, "workspace": workspace})
		window.ExecJS("window.dispatchEvent(new CustomEvent('grpvn:open-notification',{detail:" + string(encoded) + "}));")
	})
	menu := app.NewMenu()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	menuAction := func(action string) func(*application.Context) {
		encoded, _ := json.Marshal(action)
		return func(*application.Context) {
			window.Show()
			window.Focus()
			window.ExecJS("window.dispatchEvent(new CustomEvent('grpvn:action',{detail:" + string(encoded) + "}));")
		}
	}
	file := menu.AddSubmenu("File")
	file.Add("New channel…").SetAccelerator("CmdOrCtrl+N").OnClick(menuAction("create"))
	file.Add("Add workspace…").SetAccelerator("CmdOrCtrl+Shift+N").OnClick(menuAction("connect"))
	file.AddSeparator()
	file.Add("Invite people or agents…").OnClick(menuAction("invite"))
	file.Add("Connect an agent…").OnClick(menuAction("agent"))
	file.AddSeparator()
	file.Add("Close window").SetAccelerator("CmdOrCtrl+W").OnClick(func(*application.Context) { window.Hide() })
	if runtime.GOOS != "darwin" {
		file.Add("Quit grpvn").SetAccelerator("Ctrl+Q").OnClick(func(*application.Context) { app.Quit() })
	}
	menu.AddRole(application.EditMenu)
	view := menu.AddSubmenu("View")
	view.Add("Find a conversation").SetAccelerator("CmdOrCtrl+K").OnClick(menuAction("search"))
	view.AddSeparator()
	view.Add("Conversations").SetAccelerator("CmdOrCtrl+1").OnClick(menuAction("chats"))
	view.Add("People & agents").SetAccelerator("CmdOrCtrl+2").OnClick(menuAction("people"))
	view.Add("Needs you").SetAccelerator("CmdOrCtrl+3").OnClick(menuAction("attention"))
	view.Add("Browse channels…").OnClick(menuAction("channels"))
	view.AddSeparator()
	view.Add("Notification settings…").OnClick(menuAction("notifications"))
	menu.AddRole(application.WindowMenu)
	help := menu.AddSubmenu("Help")
	help.Add("grpvn documentation").OnClick(func(*application.Context) {
		_ = app.Browser.OpenURL("https://github.com/frane/grpvn#documentation")
	})
	app.Menu.SetApplicationMenu(menu)
	tray = app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon)
	} else {
		tray.SetIcon(appIcon)
	}
	trayMenu = newDesktopTray(func(action trayAction) {
		window.Show()
		window.Focus()
		encoded, _ := json.Marshal(action)
		window.ExecJS("window.dispatchEvent(new CustomEvent('grpvn:action',{detail:" + string(encoded) + "}));")
	}, func() { window.Show(); window.Focus() }, func() { app.Quit() })
	tray.SetTooltip(trayMenu.tooltip())
	tray.SetMenu(trayMenu.menu)
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { window.Hide(); e.Cancel() })
	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
}
