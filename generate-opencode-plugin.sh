#!/bin/bash
# OpenCode Plugin Generator
# Usage: ./generate-opencode-plugin.sh <plugin-name> [--global|--project]

set -e

PLUGIN_NAME=$1
TARGET_DIR=""
INSTALL_GLOBAL=true

if [ -z "$1" ]; then
  echo "Usage: $0 <plugin-name> [--global|--project]"
  echo ""
  echo "Options:"
  echo "  --global   Install to global config (~/.config/opencode/plugins/)"
  echo "  --project  Install to project (.opencode/plugins/)"
  echo ""
  echo "Generates the jev-guard plugin from plugins/opencode/jev-guard.js"
  exit 1
fi

# Parse optional target flag
if [ "$2" = "--project" ]; then
  INSTALL_GLOBAL=false
elif [ "$2" = "--global" ]; then
  INSTALL_GLOBAL=true
fi

# Determine output directory
if [ "$INSTALL_GLOBAL" = true ]; then
  TARGET_DIR="$HOME/.config/opencode/plugins"
else
  TARGET_DIR=".opencode/plugins"
fi

# Create output directory
mkdir -p "$TARGET_DIR"

PLUGIN_FILE="$TARGET_DIR/jev-guard.js"

# Check if file already exists
if [ -f "$PLUGIN_FILE" ]; then
  echo "Error: Plugin file already exists at $PLUGIN_FILE"
  read -p "Overwrite? (y/N) " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    exit 1
  fi
fi

SRC_PLUGIN="$(dirname "$0")/plugins/opencode/jev-guard.js"

if [ ! -f "$SRC_PLUGIN" ]; then
  echo "Error: Source plugin not found at $SRC_PLUGIN"
  exit 1
fi

cp "$SRC_PLUGIN" "$PLUGIN_FILE"
echo "Copied jev-guard plugin to $PLUGIN_FILE"

# Determine opencode.json path
if [ "$INSTALL_GLOBAL" = true ]; then
  CONFIG_FILE="$HOME/.config/opencode/opencode.json"
else
  CONFIG_FILE="./.opencode/opencode.json"
fi

echo "Generated OpenCode plugin: $PLUGIN_FILE"
echo ""
echo "Next steps:"
echo "1. Add plugin to config ($CONFIG_FILE):"
echo '   "plugin": ['
echo '     "jev-guard"'
echo "   ]"
echo ""
echo "2. Start the guard service: go build -o guard ./cmd/harness && ./guard"
echo "3. Restart OpenCode to load the plugin"