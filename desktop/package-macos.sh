#!/bin/sh
set -eu
cd "$(dirname "$0")"
app="bin/grpvn.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
MACOSX_DEPLOYMENT_TARGET=13.0 go build -trimpath -tags production -ldflags '-s -w' -o "$app/Contents/MacOS/grpvn" .
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>grpvn</string>
<key>CFBundleDisplayName</key><string>grpvn</string>
<key>CFBundleIdentifier</key><string>com.offbyte.grpvn</string>
<key>CFBundleExecutable</key><string>grpvn</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.13.2</string>
<key>CFBundleVersion</key><string>15</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSSupportsAutomaticGraphicsSwitching</key><true/>
<key>NSHumanReadableCopyright</key><string>grpvn contributors</string>
</dict></plist>
PLIST
codesign --force --deep --sign - "$app"
ditto -c -k --sequesterRsrc --keepParent "$app" bin/grpvn-desktop-macos.zip
echo "Created desktop/bin/grpvn.app and desktop/bin/grpvn-desktop-macos.zip"
