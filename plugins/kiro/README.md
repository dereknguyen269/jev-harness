# Jev Guard — Kiro CLI + Kiro IDE

Source of truth for the Kiro integration. Install with the generator
(see below); never edit installed copies directly.

## Files

| Path | Purpose |
|------|---------|
| `plugins/kiro/jev-guard.py` | Canonical PreToolUse hook (stdlib only) |
| `plugins/kiro/hooks/jev-guard.json.template` | Hook definition, `__GUARD_SCRIPT__` rendered at install |
| `generate-kiro-plugin.sh` | Installer for `--project` and `--global` scopes |

## Install

```bash
# Project scope: <project>/.kiro/{scripts,hooks}/ (Kiro IDE per-repo,
# also picked up by Kiro CLI when that repo is the CWD)
./generate-kiro-plugin.sh --project --project-dir /path/to/repo --force

# Global scope: ~/.kiro/{scripts,hooks}/ (Kiro CLI everywhere + IDE global)
./generate-kiro-plugin.sh --global --force
```

Then restart Kiro CLI / Kiro IDE so it loads `.kiro/hooks/jev-guard.json`.
## How it works

The hook fires on `PreToolUse` for mutating tools
(`execute_bash`, `fs_write`, `str_replace`, `fs_append`, `delete_file`,
`smart_relocate`). It POSTs `{tool, args, context}` to
`$JEV_GUARD_URL/v1/check` (default `http://127.0.0.1:8787`) and maps the
verdict to the Kiro hook contract:

| Guard decision | Hook result |
|----------------|-------------|
| `allow` | exit 0, tool proceeds |
| `approval_required` / `ask` | exit 0 + `permissionDecision: ask` (human approves) |
| `block`, risk below ceiling | exit 0 + `ask` with risk/confidence/rule details |
| `block`, risk >= ceiling | exit 2, tool refused |
| anything else | fail-closed: exit 0 + `ask` (default) or exit 2 (`block` mode) |

Guard unreachable: `ask` in default mode, hard `block` in
`JEV_GUARD_BLOCK_MODE=block` mode, silent allow with `JEV_GUARD_FAIL_OPEN=1`.

## Tuning

| Env | Default | Purpose |
|-----|---------|---------|
| `JEV_GUARD_URL` | `http://127.0.0.1:8787` | Guard address |
| `JEV_GUARD_TIMEOUT_MS` | `2000` | HTTP timeout (hook wrapper passes `5000`) |
| `JEV_GUARD_BLOCK_MODE` | `ask` | `ask` = blocks become prompts; `block` = OpenCode parity |
| `JEV_GUARD_HARD_BLOCK_RISK` | `0.9` | Risk at/above this always hard-blocks in `ask` mode |
| `JEV_GUARD_FAIL_OPEN` | unset | `1` = allow when guard unreachable |
| `JEV_GUARD_LOG` | unset | Append raw stdin payloads (may contain secrets) |

## Manual test

```bash
echo '{"tool_name":"execute_bash","tool_input":{"command":"git status"}}' \
  | python3 .kiro/scripts/jev-guard.py; echo exit=$?
```
