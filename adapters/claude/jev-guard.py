#!/usr/bin/env python3
"""Claude Code PreToolUse hook → Jev Guard /v2/check (thin shim, stdlib only).

Install: add to .claude/settings.json hooks PreToolUse.
Reads stdin JSON {tool_name, tool_input}, POSTs /v2/check.
Exit 0 allow; exit 2 block; stdout JSON with permissionDecision=ask for approval.
"""
import json
import os
import sys
import urllib.request

GUARD_URL = os.environ.get("JEV_GUARD_URL", "http://127.0.0.1:8787").rstrip("/")
TIMEOUT = float(os.environ.get("JEV_GUARD_TIMEOUT_MS", "2000")) / 1000.0


def main():
    try:
        payload = json.load(sys.stdin)
    except Exception:
        payload = {}
    tool = payload.get("tool_name") or payload.get("tool") or "unknown"
    args = payload.get("tool_input") or payload.get("args") or {}
    body = json.dumps({
        "agent": {"name": "claude-code"},
        "tool": {"name": tool, "args": args},
        "context": {"workspace": os.getcwd()},
    }).encode()
    try:
        req = urllib.request.Request(GUARD_URL + "/v2/check", data=body,
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=TIMEOUT) as res:
            decision = json.loads(res.read().decode() or "{}")
    except Exception as e:
        print(f"Jev guard unavailable: {e}", file=sys.stderr)
        sys.exit(2)  # fail closed
    verdict = decision.get("decision", "allow")
    if verdict == "block":
        print(f"Blocked by Jev guard: {decision.get('reason', '')}", file=sys.stderr)
        sys.exit(2)
    if verdict == "approval_required" or decision.get("request_approval"):
        json.dump({"hookSpecificOutput": {
            "permissionDecision": "ask",
            "permissionDecisionReason": f"Jev guard: {decision.get('reason', '')}"}}, sys.stdout)
        sys.exit(0)
    sys.exit(0)


if __name__ == "__main__":
    main()
