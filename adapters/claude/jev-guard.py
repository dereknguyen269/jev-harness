#!/usr/bin/env python3
"""Claude Code PreToolUse hook → Jev Guard /v1/check (thin shim, stdlib only).

Install: add to .claude/settings.json hooks PreToolUse.
Reads stdin JSON {tool_name, tool_input}, POSTs /v1/check.
Exit 0 allow; exit 2 block; stdout JSON with permissionDecision=ask for approval.
"""
import json
import os
import sys
import urllib.request

GUARD_URL = os.environ.get("JEV_GUARD_URL", "http://127.0.0.1:8787").rstrip("/")
try:
    TIMEOUT = float(os.environ.get("JEV_GUARD_TIMEOUT_MS", "2000")) / 1000.0
except ValueError:
    TIMEOUT = 2.0


def approval_ref(decision):
    """Short approval reference for ask messages, so the human can decide
    in the dashboard or terminal instead of (or to audit) this prompt.
    A host-prompt answer stays host-local — it does not write back to
    the guard record; dashboard/CLI decisions do."""
    aid = decision.get("approval_id") or ""
    if not aid:
        return ""
    short = str(aid)[:8]
    return f" [approval {short}: dashboard or `jev-guard approve|deny {short}`]"


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
        req = urllib.request.Request(GUARD_URL + "/v1/check", data=body,
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=TIMEOUT) as res:
            decision = json.loads(res.read().decode() or "{}")
    except Exception as e:
        print(f"Jev guard unavailable: {e}", file=sys.stderr)
        sys.exit(2)  # fail closed
    verdict = decision.get("decision", "block")
    if verdict == "block":
        print(f"Blocked by Jev guard: {decision.get('reason', '')}", file=sys.stderr)
        sys.exit(2)
    if verdict == "approval_required" or decision.get("request_approval"):
        json.dump({"hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "ask",
            "permissionDecisionReason": f"Jev guard: {decision.get('reason', '')}{approval_ref(decision)}"}}, sys.stdout)
        sys.exit(0)
    if verdict == "allow":
        sys.exit(0)
    if verdict == "ask":
        # Explicit advisory verdict: defer to the human like approval_required.
        json.dump({"hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "ask",
            "permissionDecisionReason": f"Jev guard: {decision.get('reason', '')}{approval_ref(decision)}"}}, sys.stdout)
        sys.exit(0)
    # Unknown verdict (or missing decision key, which defaults to block
    # above): fail closed with a hard block. `ask` would defer to the
    # client's prompt, which auto-accept modes can wave through.
    print(f"Blocked by Jev guard: unrecognized decision {verdict!r}", file=sys.stderr)
    sys.exit(2)


if __name__ == "__main__":
    main()
