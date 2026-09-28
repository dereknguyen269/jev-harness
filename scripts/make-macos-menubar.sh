#!/bin/bash
# Build jev-guard-menubar.app (LSUIElement menu-bar notifier) into dist/.
# Run via `make macos-menubar` (guards platform, builds the binary first).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG="$REPO_ROOT/packaging/macos"
APP="$REPO_ROOT/dist/jev-guard-menubar.app"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "make-macos-menubar.sh: macOS only (uname $(uname -s))" >&2
  exit 1
fi
if [ ! -x "$REPO_ROOT/jev-guard" ]; then
  echo "make-macos-menubar.sh: $REPO_ROOT/jev-guard missing — run \`make build\` (or \`make ui-build && make build-go\`) first" >&2
  exit 1
fi
command -v rsvg-convert >/dev/null 2>&1 || { echo "make-macos-menubar.sh: rsvg-convert not found (brew install librsvg)" >&2; exit 1; }
command -v iconutil >/dev/null 2>&1 || { echo "make-macos-menubar.sh: iconutil not found (Xcode CLT?)" >&2; exit 1; }

VERSION="$(grep -E '^const version = ' "$REPO_ROOT/cmd/jev-guard/main.go" | sed -E 's/.*"([^"]+)".*/\1/')"
VERSION="${VERSION:-0.0.0}"

ICONSET="$(mktemp -d /tmp/jev-iconset.XXXXXX.iconset)"
trap 'rm -rf "$ICONSET"' EXIT

render() { # $1=size $2=filename
  rsvg-convert -w "$1" -h "$1" "$PKG/icon.svg" -o "$ICONSET/$2"
}
render 16   icon_16x16.png
render 32   icon_16x16@2x.png
render 32   icon_32x32.png
render 64   icon_32x32@2x.png
render 128  icon_128x128.png
render 256  icon_128x128@2x.png
render 256  icon_256x256.png
render 512  icon_256x256@2x.png
render 512  icon_512x512.png
render 1024 icon_512x512@2x.png

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

sed -e "s/__VERSION__/$VERSION/g" "$PKG/Info.menubar.plist.in" > "$APP/Contents/Info.plist"
sed -e "s|__REPO_ROOT__|$REPO_ROOT|g" "$PKG/menubar.sh" > "$APP/Contents/MacOS/jev-guard-menubar"
chmod +x "$APP/Contents/MacOS/jev-guard-menubar"

# Ad-hoc signature is required: unsigned arm64 bundles are killed on launch.
codesign --force --deep -s - "$APP" >/dev/null
touch "$APP"  # nudge Finder icon cache

echo "Built $APP (v$VERSION, repo=$REPO_ROOT)"
echo "Run: open $APP   (needs the gateway up; set JEV_AUTH_TOKEN to match serve --auth-token)"
