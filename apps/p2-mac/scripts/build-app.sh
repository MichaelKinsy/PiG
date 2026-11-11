#!/bin/sh
# Ports apps/p2-mac/scripts/build-app.sh
# Builds build/PiG.app from the Swift package: the binary, Info.plist, the icons, an ad-hoc signature (no Developer ID
# yet). CONFIG=release for an optimized build.
set -eu
cd "$(dirname "$0")/.."
config="${CONFIG:-debug}"
swift build -c "$config"
bin="$(swift build -c "$config" --show-bin-path)/P2Mac"
app=build/PiG.app
rm -rf "$app" build/P2.app
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
# SwiftPM records the deployment target (14.0) as the SDK the binary was built with, and AppKit then draws the
# window chrome of that release: small, closely spaced window buttons and a 52-point toolbar instead of the current
# ones. Record the real SDK.
vtool -set-build-version macos 14.0 "$(xcrun --sdk macosx --show-sdk-version)" -replace -output "$app/Contents/MacOS/PiG" "$bin" 2>/dev/null
cp Resources/Info.plist "$app/Contents/Info.plist"
# The icons are the desktop app's (apps/p2desktop, the PiG pixel pig): the Dock icon and the monochrome menu bar template.
cp ../p2desktop/icons/AppIcon.icns "$app/Contents/Resources/AppIcon.icns"
cp ../p2desktop/cmd/p2-desktop/resources/tray-template.png "$app/Contents/Resources/tray-template.png"
codesign --force --sign - "$app"
echo "$app"
