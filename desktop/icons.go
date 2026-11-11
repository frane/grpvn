package main

import _ "embed"

//go:generate go run ./tools/resources

//go:embed icons/app.png
var appIcon []byte

//go:embed icons/tray.png
var trayIcon []byte
