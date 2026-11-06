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
		var command struct{ Type, URL string }
		if json.Unmarshal([]byte(message), &command) != nil || command.Type != "openExternal" {
			return
		}
		target, err := url.Parse(command.URL)
		if err != nil || target.User != nil || (target.Scheme != "https" && target.Scheme != "http") {
			return
		}
		_ = app.Browser.OpenURL(command.URL)
	}, Name: "grpvn", Description: "grpvn chat", Linux: application.LinuxOptions{DisableQuitOnLastWindowClosed: true}, OnShutdown: func() { server.Close() }})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "grpvn", Width: 1240, Height: 820, MinWidth: 760, MinHeight: 520, URL: server.URL})
	menu := app.NewMenu()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)
	view := menu.AddSubmenu("Conversations")
	view.Add("Show grpvn").SetAccelerator("CmdOrCtrl+1").OnClick(func(*application.Context) { window.Show(); window.Focus() })
	app.Menu.SetApplicationMenu(menu)
	tray := app.SystemTray.New()
	tray.SetLabel("grpvn")
	tray.SetTooltip("grpvn — conversations")
	trayMenu := app.NewMenu()
	trayMenu.Add("Open grpvn").OnClick(func(*application.Context) { window.Show(); window.Focus() })
	trayMenu.AddSeparator()
	trayMenu.Add("Quit grpvn").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(trayMenu)
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { window.Hide(); e.Cancel() })
	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
}
