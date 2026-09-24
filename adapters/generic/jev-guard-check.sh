#!/usr/bin/env sh
# Generic adapter: POST a tool call to Jev Guard /v2/check.
# Usage: jev-guard-check <tool> <command...>  (env: JEV_GUARD_URL, AGENT_NAME)
GUARD_URL="${JEV_GUARD_URL:-http://127.0.0.1:8787}"
AGENT="${AGENT_NAME:-generic}"
TOOL="$1"; shift || true
CMD="$*"
CMD_ESC=$(printf '%s' "$CMD" | sed 's/\\/\\\\/g; s/"/\\"/g')
curl -s -m 5 -X POST "$GUARD_URL/v2/check" -H 'Content-Type: application/json' -d "{
  \"agent\": {\"name\": \"$AGENT\"},
  \"tool\": {\"name\": \"$TOOL\", \"args\": {\"command\": \"$CMD_ESC\"}},
  \"context\": {}
}"
echo
