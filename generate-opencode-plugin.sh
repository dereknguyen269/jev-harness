#!/bin/bash
# OpenCode Plugin Generator
# Usage: ./generate-opencode-plugin.sh [plugin-name] [--global|--project] [--force]
#
# Installs the jev-guard plugin from the source of truth:
#   plugins/opencode/jev-guard.js
# Never edit the installed copies directly — re-run this script after
# changing the source.
set -e
usage() {
  echo "Usage: $0 [plugin-name] [--global|--project] [--force]"
  echo ""
  echo "  (plugin-name is accepted for historical reasons and ignored;"
  echo "   the installed file is always jev-guard.js)"
  echo "  --project  Install to project (.opencode/plugins/, default)"
  echo "  --global   Install to global config (~/.config/opencode/plugins/)"
  echo "  --force    Overwrite existing files without prompting"
}
PLUGIN_NAME=""
SCOPE="--project"
FORCE=false
for arg in "$@"; do
  case "$arg" in
    --project|--global) SCOPE="$arg" ;;
    --force) FORCE=true ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "Unknown argument: $arg"; usage; exit 1 ;;
    *) PLUGIN_NAME="$arg" ;;
  esac
done
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
if [ "$SCOPE" = "--global" ]; then
  TARGET_DIR="$HOME/.config/opencode/plugins"
  CONFIG_FILE="$HOME/.config/opencode/opencode.json"
else
  TARGET_DIR="$SCRIPT_DIR/.opencode/plugins"
  CONFIG_FILE="$SCRIPT_DIR/.opencode/opencode.json"
fi

mkdir -p "$TARGET_DIR"

PLUGIN_FILE="$TARGET_DIR/jev-guard.js"

if [ -f "$PLUGIN_FILE" ] && [ "$FORCE" != "true" ]; then
  echo "Exists: $PLUGIN_FILE"
  printf "Overwrite? (y/N) "
  read -r REPLY < /dev/tty || REPLY=""  # no TTY (CI) -> skip, never abort under set -e
  case "$REPLY" in [Yy]*) ;; *) echo "Skipped $PLUGIN_FILE"; exit 0 ;; esac
fi

SRC_PLUGIN="$SCRIPT_DIR/plugins/opencode/jev-guard.js"

if [ ! -f "$SRC_PLUGIN" ]; then
  echo "Error: Source plugin not found at $SRC_PLUGIN"
  exit 1
fi

cp "$SRC_PLUGIN" "$PLUGIN_FILE"
echo "Copied jev-guard plugin to $PLUGIN_FILE"

if command -v node >/dev/null 2>&1; then
  node --check "$PLUGIN_FILE" && echo "Plugin syntax OK"
else
  echo "Warning: node not found in PATH, skipping syntax check"
fi

echo ""
echo "Next steps:"
echo "1. Add plugin to config ($CONFIG_FILE):"
echo '   "plugin": ['
echo '     "jev-guard"'
echo "   ]"
echo ""
echo "2. Start the guard service: go build -o jev-guard ./cmd/jev-guard && ./jev-guard serve"
echo "3. Set JEV_AUTH_TOKEN in the opencode env to match the guard's token,"
echo "   then restart OpenCode to load the plugin"
