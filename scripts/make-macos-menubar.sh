#!/bin/bash
# Deprecated: the standalone jev-guard-menubar.app bundle is retired —
# dist/jev-guard.app is now the single unified build (gateway + menu-bar
# tray via `serve --tray`). This stub forwards to the unified builder so
# direct invocations and old scripts keep working.
# Legacy sources packaging/macos/{menubar.sh,Info.menubar.plist.in} are
# unused and can be deleted.
set -euo pipefail

echo "make-macos-menubar.sh is deprecated: building the unified jev-guard.app instead" >&2
exec "$(cd "$(dirname "$0")" && pwd)/make-macos-app.sh"
