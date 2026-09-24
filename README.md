# Jev Guard — Agent Safety Gateway

A local safety gateway for AI coding agents. Every tool call goes through
`jev-guard`, which normalizes it, applies deterministic policy, consults Jev
for ambiguous cases, and **fails closed** when Jev is unreachable.

> **Jev never decides.** Jev returns a structured judgment
> (`risk`/`confidence`/`action`); the Go policy engine turns it into the
> final `allow` / `approval_required` / `block`.

```
AI agent (Claude / OpenCode / Kiro / Codex / OpenClaw)
        │ tool call
        ▼
┌───────────────┐
│   jev-guard   │  Normalize → Policy → Cache → Jev → fail-closed → Audit
└───────┬───────┘
        ▼
  ALLOW / ASK / BLOCK
```

## Quick Start

```bash
# Build
go build -o jev-guard ./cmd/jev-guard

# Run (default profile, policy-only until a Jev key is set)
./jev-guard serve --listen 127.0.0.1:8787

# Health
curl http://127.0.0.1:8787/v2/health

# One-shot check (exit 0 allow / 2 block / 3 approval_required)
./jev-guard check --tool terminal --command "git status"
./jev-guard check --tool Bash --command "sudo systemctl restart nginx"
```

With a Jev key (`.env` is auto-loaded, or pass flags):

```bash
JEV_API_KEY=... ./jev-guard serve --profile strict
```

## CLI

| Command | Purpose |
|---------|---------|
| `jev-guard serve [--listen ADDR] [--policy FILE] [--profile NAME]` | Run the gateway. Policy: explicit `--policy`/`POLICY_PATH` wins, then `--profile` bundle, then `configs/policy.yaml`. |
| `jev-guard check --tool T --command C [--path P] [--env E]` | One-shot evaluation (no audit). |
| `jev-guard policy test [--policy FILE]` | Run bundled fixtures against policy. |
| `jev-guard eval <fixtures.yaml>` | Accuracy/latency report over eval fixtures. |
| `jev-guard audit [--decision D] [--min-risk F] [--json]` | Query the audit log. |
| `jev-guard doctor` | Readiness checklist (runtime, policy, Jev, adapters, audit). |
| `jev-guard version` | Print version. |

## HTTP API

### `POST /v2/check`

```bash
curl -X POST http://127.0.0.1:8787/v2/check \
  -H 'Content-Type: application/json' \
  -d '{"agent":{"name":"claude-code"},
       "tool":{"name":"Bash","args":{"command":"rm -rf /"}},
       "context":{"environment":"dev"}}'
```

```json
{
  "decision": "block",
  "risk": 1.0,
  "confidence": 1.0,
  "source": "policy",
  "policy_id": "root-delete",
  "reason_code": "CRITICAL_DELETE",
  "reason": "Matched policy root-delete: rm -rf /",
  "request_approval": false
}
```

`approval_required` responses additionally carry `approval_id` + `expires_in` (30s).

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/health`, `/v2/health` | Liveness (`status`, `version`, `jev` = Jev configured). |
| POST | `/v2/check` | Full `DecisionResult` evaluation. |
| GET | `/v2/approvals` | List approvals. |
| POST | `/v2/approvals/:id/approve`, `.../deny` | Human decision. |
| GET | `/v2/audit[?decision=&min_risk=]` | Audit events. |
| GET | `/v2/stats` | Decision counts. |
| GET | `/v2/policies` | Policy version. |
| POST | `/v1/check` | **Deprecated compat** — same engine, legacy `{tool,args,context}` shape. |
| GET | `/v1/audit` | Stub — use `GET /v2/audit`. |

## Policy

Two formats, both file-order (first match wins):

**Legacy** `configs/policy.yaml` — `{id, tool, pattern, action, priority}` (priority parsed, not used). Still fully supported.

**V2** `configs/profiles/*.yaml` — match → decision with risk + reason codes:

```yaml
policies:
  - id: sudo
    match:
      tool: terminal
      command:
        regex: '\bsudo\b'
    decision:
      action: approval_required
      risk: 0.8
      reason_code: PRIVILEGED
  - id: production
    match:
      context:
        environment: production
    decision:
      action: approval_required
      risk: 0.9
```

### Profiles (`--profile`)

| Profile | Posture |
|---------|---------|
| `default` | Balanced: reads/git allow, `sudo`/production ask, destructive block. |
| `developer` | Permissive daily driver (production asks). |
| `strict` | Edits/installs ask, production blocks. |
| `permissive` | Minimal friction; critical deletes still block. |

### Risk levels & Jev resolution

`L0 READ → L1 LOW → L2 MUTATION → L3 PRIVILEGED → L4 CRITICAL`.
Jev judgments are confidence-gated per level (`0.5 / 0.7 / 0.85 / 0.95`);
low confidence → ask, `risk ≥ 0.95` → block. Cache holds reads (30s)
and low-risk allows (10s) only — mutations, criticals, and approvals
are never cached.

## Adapters

Thin shims — all canonicalize the tool name and `POST /v2/check`.
80% of logic lives in Go.

| Adapter | Status | Source |
|---------|--------|--------|
| OpenCode | ✅ | `plugins/opencode/jev-guard.js` (`tool.execute.before`; tries `/v2`, falls back to `/v1`) |
| Kiro CLI + IDE | ✅ | `plugins/kiro/jev-guard.py` (`PreToolUse`) |
| Claude Code | ✅ | `adapters/claude/jev-guard.py` (`PreToolUse`) |
| Codex | ✅ | `adapters/codex/jev-guard.py` |
| OpenClaw | ✅ | `adapters/openclaw/jev-guard.py` |
| Generic / any CLI | ✅ | `adapters/generic/jev-guard-check.sh`, or POST directly |

Install from source of truth with the generators (never edit installed copies):

```bash
./generate-opencode-plugin.sh my-jev-harness --project   # → .opencode/plugins/
./generate-kiro-plugin.sh --project --project-dir /path/to/repo --force  # → .kiro/
```

Guard endpoint/timeout: `JEV_GUARD_URL` (default `http://127.0.0.1:8787`),
`JEV_GUARD_TIMEOUT_MS` (default `2000`).

## Environment

`.env` in CWD is auto-loaded (supports `export` prefix, inline `#` comments):

```bash
cp .env.example .env   # then set a key below
JEV_API_KEY=...        # or TYPESAFE_API_KEY / OPENROUTER_API_KEY
JEV_MODEL=typesafe-ai/jev
JEV_ENDPOINT=https://ai-gateway.vercel.sh/v1/evaluate
LISTEN=127.0.0.1:8787
```

Key priority: `--jev-api-key` flag > `JEV_API_KEY` > `TYPESAFE_API_KEY` >
`OPENROUTER_API_KEY`. Model auto-detection: `typesafe-ai/*` or `*jev*` →
evaluation API (`{model, state, questions}`); otherwise OpenAI chat format.

Audit log: `$HOME/.hermes/guard/audit.jsonl` (or `$AUDIT_PATH`).

## Evals & Testing

```bash
go test ./... -v
./jev-guard policy test
./jev-guard eval evals/fixtures/policy.yaml
```

Fixtures (`evals/fixtures/policy.yaml`) cover safe/allow, destructive/block,
and privileged/approval cases across agent tool-name variants (`terminal`,
`Bash`, `shell`, `fs_write`). Current: 9/9, 100% accuracy, 0 false-allows.
Unit tests use mocks (`mockJevClient`, `judge.Mock`) — no network needed.

## Upgrading from v0.1

- Binary `guard` (`cmd/harness`, `/v1/check` only) is a deprecated shim — switch to `jev-guard serve`.
- Adapters work against both: new shims prefer `/v2/check` with `/v1` fallback.
- `configs/policy.yaml` keeps working unchanged; adopt `configs/profiles/*` + `--profile` when ready.

## License

MIT
