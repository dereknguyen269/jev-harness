#!/bin/bash
# Kiro Plugin Generator (Kiro CLI + Kiro IDE)
# Usage: ./generate-kiro-plugin.sh [--global|--project] [--project-dir DIR] [--force]
#
# Installs the Jev guard hook from the source of truth:
#   plugins/kiro/jev-guard.py + plugins/kiro/hooks/jev-guard.json.template
# Never edit the installed copies directly — re-run this script after
# changing the sources.
set -e
usage() {
  echo "Usage: $0 [--global|--project] [--project-dir DIR] [--force]"
  echo ""
  echo "  --project          Install to project (.kiro/ under DIR, default)"
  echo "  --global           Install to Kiro global dir (~/.kiro/) for Kiro CLI + IDE"
  echo "  --project-dir DIR  Target project (default: current directory)"
  echo "  --force            Overwrite existing files without prompting"
}
SCOPE="--project"
PROJECT_DIR="."
FORCE=false
while [ $# -gt 0 ]; do
  case "$1" in
    --project|--global) SCOPE="$1"; shift ;;
    --project-dir) PROJECT_DIR="${2:?missing DIR}"; shift 2 ;;
    --force) FORCE=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1"; usage; exit 1 ;;
  esac
done
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_PY="$SCRIPT_DIR/plugins/kiro/jev-guard.py"
SRC_TPL="$SCRIPT_DIR/plugins/kiro/hooks/jev-guard.json.template"
if [ ! -f "$SRC_PY" ]; then echo "Error: source not found: $SRC_PY"; exit 1; fi
if [ ! -f "$SRC_TPL" ]; then echo "Error: template not found: $SRC_TPL"; exit 1; fi
if [ "$SCOPE" = "--global" ]; then
  BASE="$HOME/.kiro"
else
  mkdir -p "$PROJECT_DIR"
  BASE="$(cd "$PROJECT_DIR" && pwd)/.kiro"
fi
mkdir -p "$BASE/scripts" "$BASE/hooks"
DEST_PY="$BASE/scripts/jev-guard.py"
DEST_HOOK="$BASE/hooks/jev-guard.json"
install_file() {
  src="$1"; dest="$2"
  if [ -f "$dest" ] && [ "$FORCE" != "true" ]; then
    echo "Exists: $dest"
    printf "Overwrite? (y/N) "
    read -r REPLY < /dev/tty || REPLY=""  # no TTY (CI) -> skip, never abort under set -e
    case "$REPLY" in [Yy]*) ;; *) echo "Skipped $dest"; return 0 ;; esac
  fi
  cp "$src" "$dest"
  echo "Wrote $dest"
}
install_file "$SRC_PY" "$DEST_PY"
chmod +x "$DEST_PY"
render_template() {
  # python3 is required (it also validates the hook JSON below).
  command -v python3 >/dev/null 2>&1 || { echo "Error: python3 not found in PATH"; exit 1; }
  DEST_PY_ABS="$1" SRC="$2" OUT="$3" python3 -c \
    "import os; s=open(os.environ['SRC']).read().replace('__GUARD_SCRIPT__', os.environ['DEST_PY_ABS']); open(os.environ['OUT'],'w').write(s)"
}
if [ -f "$DEST_HOOK" ] && [ "$FORCE" != "true" ]; then
  echo "Exists: $DEST_HOOK"
  printf "Overwrite? (y/N) "
  read -r REPLY < /dev/tty || REPLY=""  # no TTY (CI) -> skip, never abort under set -e
  case "$REPLY" in [Yy]*) ;; *) echo "Skipped $DEST_HOOK"; HOOK_SKIPPED=1 ;; esac
fi
if [ "${HOOK_SKIPPED:-0}" != "1" ]; then
  TMP_HOOK="$DEST_HOOK.tmp"
  render_template "$DEST_PY" "$SRC_TPL" "$TMP_HOOK"
  mv "$TMP_HOOK" "$DEST_HOOK"
  echo "Wrote $DEST_HOOK"
fi
python3 -m json.tool "$DEST_HOOK" >/dev/null && echo "Hook JSON valid"
echo ""
echo "Scope: $SCOPE  Base: $BASE"
echo "Next steps:"
echo "1. Start the guard: go build -o guard ./cmd/harness && ./guard -listen 0.0.0.0:8787"
echo "2. Restart Kiro CLI / Kiro IDE so it picks up .kiro/hooks/jev-guard.json"
echo "3. Test: echo '{\"tool_name\":\"execute_bash\",\"tool_input\":{\"command\":\"git status\"}}' | python3 $DEST_PY; echo exit=\$?"
echo "   Tune with JEV_GUARD_BLOCK_MODE=ask|block JEV_GUARD_TIMEOUT_MS=5000 (see plugins/kiro/README.md)"
