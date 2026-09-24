#!/usr/bin/env python3
"""OpenClaw tool interception shim → Jev Guard /v2/check (thin shim, stdlib only)."""
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
    tool = payload.get("tool") or payload.get("tool_name") or "unknown"
    args = payload.get("args") or payload.get("tool_input") or {}
    body = json.dumps({
        "agent": {"name": "openclaw"},
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
    verdict = decision.get("decision", "allow")
    if verdict == "block":
        print(f"Blocked by Jev guard: {decision.get('reason', '')}", file=sys.stderr)
        sys.exit(2)
    if verdict == "approval_required":
        print(f"Approval required by Jev guard: {decision.get('reason', '')}", file=sys.stderr)
        sys.exit(1)
    sys.exit(0)


if __name__ == "__main__":
    main()
