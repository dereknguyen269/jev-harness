#!/usr/bin/env python3
"""Codex tool interception shim → Jev Guard /v2/check (thin shim, stdlib only)."""
import json
import os
import sys
import urllib.request

GUARD_URL = os.environ.get("JEV_GUARD_URL", "http://127.0.0.1:8787").rstrip("/")
TIMEOUT = float(os.environ.get("JEV_GUARD_TIMEOUT_MS", "2000")) / 1000.0


def main():
    tool = os.environ.get("CODEX_TOOL", sys.argv[1] if len(sys.argv) > 1 else "shell")
    args = {"command": os.environ.get("CODEX_COMMAND", " ".join(sys.argv[2:]))}
    body = json.dumps({
        "agent": {"name": "codex"},
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
        sys.exit(2)
    if decision.get("decision") == "block":
        print(f"Blocked by Jev guard: {decision.get('reason', '')}", file=sys.stderr)
        sys.exit(2)
    if decision.get("decision") == "approval_required":
        print(f"Approval required by Jev guard: {decision.get('reason', '')}", file=sys.stderr)
        sys.exit(1)
    sys.exit(0)


if __name__ == "__main__":
    main()
