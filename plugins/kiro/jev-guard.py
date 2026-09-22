#!/usr/bin/env python3
"""Kiro PreToolUse guard — canonical source: jev-harness/plugins/kiro."""
import argparse
import json
import os
import sys
import urllib.error
import urllib.request
GUARD_URL = os.environ.get("JEV_GUARD_URL", "http://127.0.0.1:8787").rstrip("/")
try:
    TIMEOUT_S = float(os.environ.get("JEV_GUARD_TIMEOUT_MS", "2000")) / 1000.0
except ValueError:
    TIMEOUT_S = 2.0
BLOCK_MODE = os.environ.get("JEV_GUARD_BLOCK_MODE", "ask").strip().lower()
FAIL_OPEN = os.environ.get("JEV_GUARD_FAIL_OPEN", "") == "1"
LOG_PATH = os.environ.get("JEV_GUARD_LOG", "")
try:
    HARD_BLOCK_RISK = float(os.environ.get("JEV_GUARD_HARD_BLOCK_RISK", "0.9"))
except ValueError:
    HARD_BLOCK_RISK = 0.9
ALLOW, BLOCK = 0, 2
TOOL_KEYS = ("tool_name", "toolName", "tool", "name")
ARG_KEYS = ("tool_input", "toolInput", "args", "arguments", "input", "parameters")
DIR_KEYS = ("cwd", "workspace_folder", "workspaceFolder", "working_dir", "project_dir")
def pick(payload, keys, default=None):
    for key in keys:
        if key in payload and payload[key] not in (None, ""):
            return payload[key]
    return default


def parse_flags(argv=None):
    p = argparse.ArgumentParser(description="Jev guard Kiro hook")
    p.add_argument("--tool", default="")
    p.add_argument("--command", default="")
    p.add_argument("--cwd", default="")
    p.add_argument("--guard-url", default="")
    p.add_argument("--timeout-ms", default="")
    args, _ = p.parse_known_args(argv)
    return args


def read_payload():
    if not sys.stdin.isatty():
        raw = sys.stdin.read()
    else:
        raw = ""
    if LOG_PATH and raw:
        try:
            with open(LOG_PATH, "a") as fh:
                fh.write(raw.rstrip() + "\n")
        except OSError:
            pass
    if not raw.strip():
        return {}
    try:
        return json.loads(raw)
    except ValueError:
        return {"_raw": raw}
def tool_name(payload):
    value = pick(payload, TOOL_KEYS, "")
    if isinstance(value, dict):
        return str(pick(value, ("name", "tool_name", "toolName"), "unknown"))
    return str(value or "unknown")


def tool_args(payload):
    value = pick(payload, ARG_KEYS)
    if isinstance(value, dict):
        return value
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
            return parsed if isinstance(parsed, dict) else {"value": parsed}
        except ValueError:
            return {"value": value}
    return {}


def apply_fallbacks(payload, flags):
    if tool_name(payload) in ("", "unknown"):
        flag_tool = flags.tool or os.environ.get("KIRO_TOOL", "")
        if flag_tool:
            payload["tool_name"] = flag_tool
    if not tool_args(payload):
        flag_cmd = flags.command or os.environ.get("KIRO_COMMAND", "")
        if flag_cmd:
            payload["tool_input"] = {"command": flag_cmd, "path": flag_cmd}
    if not pick(payload, DIR_KEYS, ""):
        flag_cwd = flags.cwd or os.environ.get("KIRO_CWD", "")
        if flag_cwd:
            payload["cwd"] = flag_cwd
    return payload
def guard_url_and_timeout(flags):
    url = (flags.guard_url or os.environ.get("JEV_GUARD_URL", GUARD_URL)).rstrip("/") or GUARD_URL
    try:
        timeout_ms = float(flags.timeout_ms or os.environ.get("JEV_GUARD_TIMEOUT_MS", TIMEOUT_S * 1000))
    except ValueError:
        timeout_ms = TIMEOUT_S * 1000
    return url, timeout_ms / 1000.0


def call_guard(tool, args, payload, url, timeout):
    body = json.dumps(
        {
            "tool": tool,
            "args": args,
            "context": {
                "user_request": str(pick(payload, ("user_request", "prompt"), "")),
                "working_dir": str(pick(payload, DIR_KEYS, os.getcwd())),
                "platform": "kiro",
                "agent": "kiro",
            },
        }
    ).encode("utf-8")
    req = urllib.request.Request(
        url + "/v1/check",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as res:
        if res.status != 200:
            raise RuntimeError("guard returned HTTP %s" % res.status)
        return json.loads(res.read().decode("utf-8") or "{}")


def block(reason):
    sys.stderr.write("Blocked by Jev guard: %s\n" % reason)
    sys.exit(BLOCK)


def ask(reason):
    json.dump(
        {"hookSpecificOutput": {"permissionDecision": "ask",
                                "permissionDecisionReason": "Jev guard: %s" % reason}},
        sys.stdout,
    )
    sys.exit(ALLOW)


def as_float(value, default=0.0):
    try:
        return float(value)
    except (TypeError, ValueError):
        return default
def describe(decision, reason, tool):
    risk = decision.get("risk")
    conf = decision.get("confidence")
    rule = (decision.get("policy") or {}).get("rule_id")
    bits = ["%s on %s" % (reason, tool)]
    if risk is not None:
        bits.append("risk %.2f" % as_float(risk))
    if conf is not None:
        bits.append("confidence %.2f" % as_float(conf))
    if rule:
        bits.append("rule %s" % rule)
    return " | ".join(bits) + " — approve only if you expected this"


def main(argv=None):
    flags = parse_flags(argv)
    payload = apply_fallbacks(read_payload(), flags)
    tool = tool_name(payload)
    args = tool_args(payload)
    url, timeout = guard_url_and_timeout(flags)
    try:
        decision = call_guard(tool, args, payload, url, timeout)
    except (urllib.error.URLError, urllib.error.HTTPError, RuntimeError, ValueError, OSError) as err:
        detail = getattr(err, "reason", err)
        if FAIL_OPEN:
            sys.exit(ALLOW)
        if BLOCK_MODE == "ask":
            ask("guard unreachable (%s) on %s — cannot assess risk" % (detail, tool))
        block("guard unavailable (%s)" % detail)
        return
    verdict = str(decision.get("decision", "allow")).lower()
    reason = decision.get("reason") or "risk detected"
    if verdict in ("approval_required", "ask") or decision.get("request_approval"):
        ask(reason)
    if verdict == "block" or decision.get("allow") is False:
        risk = as_float(decision.get("risk"), 1.0)
        if BLOCK_MODE == "ask" and risk < HARD_BLOCK_RISK:
            ask(describe(decision, reason, tool))
        block(describe(decision, reason, tool))
    if verdict == "allow" or decision.get("allow") is True:
        sys.exit(ALLOW)
    if BLOCK_MODE == "ask":
        ask("unrecognized guard decision %r on %s" % (verdict, tool))
    block("unrecognized guard decision %r on %s" % (verdict, tool))


if __name__ == "__main__":
    main()
