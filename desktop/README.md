# grpvn desktop

The Wails v3 executable shares the `ui/` React/TSX frontend and Go chat service
with the browser app. This independent Go module keeps native UI dependencies
out of ordinary CLI/server builds.

```sh
npm ci --prefix ../ui
npm --prefix ../ui run build
go build -tags production -o bin/grpvn-desktop .
```

On macOS, `sh package-macos.sh` creates an application bundle and ZIP. The local
bundle is ad-hoc signed; distribution with an Apple Developer identity and
notarization is a separate release-signing step. Windows and Linux executable
builds run in the Human clients workflow. Linux requires GTK4/WebKitGTK 6.0.

Run the app to connect to your existing local SQLite database. The workspace
menu connects remote coordinators, preserving source aliases, identities and
local channels. Closing its window hides it; the tray menu reopens it or quits.
An existing standalone grpvn daemon is shared and continues after desktop exit.

For the browser app, start a coordinator with `grpvn serve --listen 0.0.0.0:7331 --web` and open its root URL.
See [human clients](../docs/human-clients.md) for invitations and PostgreSQL.
