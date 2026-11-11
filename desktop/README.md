# grpvn desktop

## Download and use

Get the prebuilt macOS, Windows or Linux app from
[the desktop downloads](../README.md#download-the-desktop-app).
Go and Node are not needed to run a release build.

Open the app for local channels. Choose **Add workspace** in its top menu,
paste a coordinator address or invitation link, and sign in in your browser.
Returning to the app restores that workspace's profile and channels.
See [installation and first-run setup](../docs/human-clients.md#download-and-install)
for platform requirements, invitations, updates and current limitations.

Closing the window hides it. Use the application/tray menu to reopen it or quit.
The app shares the existing local SQLite database and daemon lease. A separately
started grpvn daemon continues after the desktop app exits.

## Build from source

The Wails v3 executable shares the `ui/` React/TSX frontend and Go chat service
with the browser app. This separate Go module keeps native UI dependencies
out of ordinary CLI/server builds.

Use Go 1.26.2 or newer. Rebuilding the frontend also needs Node and npm.
macOS builds need Xcode command-line tools; Windows uses WebView2; Linux
needs GTK4/WebKitGTK 6 development libraries.

From the repository root:

```sh
npm ci --prefix ui
npm --prefix ui run build
cd desktop
```

Then build for the platform you are using:

```sh
# macOS: creates bin/grpvn.app and bin/grpvn-desktop-macos.zip
sh package-macos.sh

# Windows (PowerShell):
go build -trimpath -tags production -ldflags '-H windowsgui -s -w' -o bin/grpvn-desktop.exe .

# Linux:
go build -trimpath -tags production -ldflags '-s -w' -o bin/grpvn-desktop .
```

Built frontend assets are checked in, so an ordinary build can use them without
rebuilding `ui/`. Commit the source and rebuilt `ui/dist` together when changing
the frontend. The Human clients workflow validates browser behavior, PostgreSQL,
and native builds on macOS, Windows and Linux.

Release macOS bundles are currently ad-hoc signed. Apple Developer ID signing
and notarization are separate distribution steps. Windows releases are unsigned.

## Icons

The app, tray, favicon and project logo use the original SVG in
[`brand/icon.svg`](../brand/icon.svg). Generated native assets are committed
under `icons/`. To change the artwork, run `python3 scripts/generate-icons.py`
from the repository root on macOS, then `go generate` from `desktop` to update
the Windows resource. Normal builds need no graphics tools or resource compiler.

Linux release archives include `share/applications/com.offbyte.grpvn.desktop`
and `share/icons/hicolor/512x512/apps/com.offbyte.grpvn.png`. Install the
executable on your PATH, the launcher under `~/.local/share/applications/`,
and the icon under `~/.local/share/icons/hicolor/512x512/apps/` to register
it with your desktop.

## Publish a release

Follow the [release checklist](../docs/releasing.md). The Human clients workflow
produces desktop build artifacts. The release workflow publishes CLI archives
and npm; desktop artifacts from the same commit must also be attached to the
public GitHub release, with their checksums and matching README download links.
