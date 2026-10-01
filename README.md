# Jev Guard — Agent Safety Gateway

A local safety gateway for AI coding agents. Every tool call goes through
`jev-guard`, which normalizes it, applies deterministic policy, consults Jev
for ambiguous cases, and **fails closed** when Jev is unreachable. A React
dashboard manages policy, users, approvals, and audit.

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
# Build everything (frontend + binary; needs Node 20+ and Go 1.23+)
make build

# Run (default profile, policy-only until a Jev key is set)
./jev-guard serve --listen 127.0.0.1:8787

# Dashboard
open http://127.0.0.1:8787/

# Health
curl http://127.0.0.1:8787/v1/health

# One-shot check (exit 0 allow / 2 block / 3 approval_required)
./jev-guard check --tool terminal --command "git status"
./jev-guard check --tool Bash --command "sudo systemctl restart nginx"
```

With a Jev key (`.env` is auto-loaded, or pass flags):

```bash
JEV_API_KEY=... ./jev-guard serve --profile strict
```

With dashboard auth (off by default):

```bash
./jev-guard serve --auth-token s3cret
```

## macOS app (unified: Dock + menu bar)

```bash
make app              # darwin-only; full pipeline: frontend + binary + bundle (needs rsvg-convert)
open dist/jev-guard.app # Dock tile + shield menu icon, auto-opens the dashboard
```

This wraps `./jev-guard serve --tray` as a single `jev-guard.app` (sources
in `packaging/macos/`, icon in `packaging/macos/icon.svg`): one app owns
the Dock tile, the shield menu icon with pending count, and the native
approval alerts (approve/deny right in the menu — same record as dashboard
+ `approve|deny` CLI). Quitting the app stops the gateway. Copy it to
`/Applications` to keep it; the app points at this checkout, so re-run
`make macos-app` after moving the repo (or set `JEV_GUARD_REPO`).
`JEV_GUARD_OPEN_DASHBOARD=0` disables auto-open, `JEV_GUARD_TRAY=0`
disables the tray (gateway only). `make macos-menubar` is a deprecated
alias for `make macos-app`. When the gateway runs with `--auth-token`,
the in-process tray reuses it automatically (no extra env needed).

Standalone notifier (gateway on another host/terminal):

```bash
./jev-guard menubar [--guard-url URL] [--poll SECS]  # needs JEV_AUTH_TOKEN when serve uses --auth-token
```

Polls the gateway for pending approvals (default every 2s), posts a
native notification per new arrival, and offers Approve/Deny right in the
menu (same record as dashboard + `approve|deny` CLI). Shows
"auth mismatch" / "Guard offline" states instead of failing silently.

## Dashboard

`GET /` serves the embedded React + shadcn/ui dashboard (built by
`make ui-build` into `internal/server/web/dist/`). First run seeds the
SQLite store (`~/.hermes/guard/jev.db`, override with `--db`/`JEV_DB`)
from YAML; afterwards the DB is the runtime policy source and YAML backs
the offline CLI commands. A first-run tour explains each tab.

| Tab | Purpose |
|-----|---------|
| Rules | Policy rules (tool + regex → allow/block/approval_required). Defined tool/group/category lists with custom fallback. Edits reload the live engine. |
| Groups | Group name + description definitions. Rules keep working if their group is deleted. |
| Categories | Category name + description definitions (seeded from rule usage). Same delete semantics as groups. |
| Users | Attribution-only users; each gets an API key that also works as a dashboard login. |
| Approvals | Pending `approval_required` decisions — approve/deny. Persisted to SQLite when `--db` is up (same record as the `approve|deny` CLI and the OpenCode plugin poll, so any channel unblocks the others); in-memory only in YAML-only mode. |
| Audit | Every decision with the exact command/path, risk, and rule. Filter + pagination. |
| Jev API | Recent outbound AI calls: status, latency, input/output tokens. SQLite-persisted when `--db` is up (survives restarts), otherwise in-memory. |
| Settings | Default approval TTL (5s–1h, admin only; applies live, no restart). |

Management writes validate input (bad regex/action → 400) and return 503
in YAML-only mode (no `--db`).

## CLI

| Command | Purpose |
|---------|---------|
| `jev-guard serve [--listen ADDR] [--policy FILE] [--profile NAME] [--group G] [--db PATH] [--auth-token TOKEN] [--approval-ttl SECS] [--reseed] [--tray] [--tray-poll SECS]` | Run the gateway. Policy: explicit `--policy`/`POLICY_PATH` wins, then `--profile` bundle, then `configs/policy.yaml`. `--reseed` merges bundled defaults into the DB on startup. `--tray` (macOS) also runs the menu-bar tray in-process: the unified `jev-guard.app` mode. |
| `jev-guard check --tool T --command C [--path P] [--env E] [--business B] [--task T] [--category C]` | One-shot evaluation (no audit). |
| `jev-guard policy test [--policy FILE]` | Run bundled fixtures against policy. |
| `jev-guard policy reseed [--mode merge|replace] [--force]` | Merge restructured YAML defaults into the DB (merge keeps custom rules; replace wipes policy tables and needs `--force`). Users survive both. |
| `jev-guard approvals [--status pending|all] [--limit N] [--json] [--db PATH]` | List approval history, newest first. Works with or without a running server (`--db` accepted anywhere in args). |
| `jev-guard approve|deny <id-prefix> [--db PATH]` | Decide one approval by ID prefix (same SQLite record as dashboard + plugin poll). |
| `jev-guard eval <fixtures.yaml>` | Accuracy/latency report over eval fixtures. |
| `jev-guard audit [--decision D] [--min-risk F] [--json]` | Query the audit log (now includes command/path detail). |
| `jev-guard doctor` | Readiness checklist (runtime, policy, Jev, adapters, audit). |
| `jev-guard version` | Print version. |

`make build|test|vet|fmt|run|clean`, `make check` (= ui-build + fmt + vet + test).
`make build-go|test-go` skip the frontend (fails loudly if `web/dist/` is missing).

## HTTP API

All `/v1/*`. When `--auth-token` is set, the management API requires
`Authorization: Bearer` — either the master token (admin identity) or any
active user's API key. The dashboard shell (`/`, `/assets/*`) stays open
so the login screen can load; the agent surface stays open too.

Roles (viewer < operator < admin): viewers read; operators also
approve/deny approvals and reload policy; admins additionally manage
users/rules/groups. API keys in the user list are masked for non-admins.
Unknown roles fail closed to read-only. With no `--auth-token`, everything
stays open (local-dev default).

```bash
curl -X POST http://127.0.0.1:8787/v1/check \
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
  "reason": "Blocked by rule root-delete: rm -rf /",
  "request_approval": false
}
```

`approval_required` responses additionally carry `approval_id` + `expires_in` (30s).

| Method | Path | Purpose | Auth |
|--------|------|---------|------|
| GET | `/health`, `/v1/health` | Liveness (`status`, `version`, `jev` = Jev configured). | open |
| POST | `/v1/check` | Full `DecisionResult` evaluation. | open (agents) |
| GET | `/v1/policies` | Policy version + groups. | open |
| GET | `/v1/auth/status` | `{"auth": bool}` — is dashboard auth on. | open |
| POST | `/v1/auth/login` | `{secret}` → `{name, role}` identity. | open |
| GET | `/v1/auth/me` | Caller identity from Bearer token. | dashboard |
| GET | `/` | Dashboard SPA. | dashboard |
| GET | `/v1/approvals` | List approvals (agent poll surface). | dashboard |
| GET | `/v1/approvals/page[?page=&per_page=]` | Paginated approval history + `pending` count for the tab badge. | dashboard |
| POST | `/v1/approvals/:id/approve`, `.../deny` | Human decision. | dashboard |
| GET/PUT | `/v1/settings` | Default `approval_ttl_seconds` (GET reads, PUT is admin-only, applies live). | dashboard |
| POST | `/v1/policy/reseed` | Merge (`{"mode":"merge"}`) or wipe+replace (`{"mode":"replace","force":true}`) bundled defaults into the DB. | dashboard |
| GET | `/v1/audit[?decision=]` | Audit events (with command/path/resource). | dashboard |
| GET | `/v1/stats` | Decision counts. | dashboard |
| GET | `/v1/rules` POST | List / create-or-replace rule. | dashboard |
| PUT/DELETE | `/v1/rules/:id` | Update / delete rule. | dashboard |
| GET | `/v1/groups` POST | List / create-or-replace group. | dashboard |
| PUT/DELETE | `/v1/groups/:name` | Update / delete group. | dashboard |
| GET | `/v1/categories` POST | List / create-or-replace category. | dashboard |
| PUT/DELETE | `/v1/categories/:name` | Update / delete category. | dashboard |
| GET/POST/PUT/DELETE | `/v1/users...` | User CRUD. | dashboard |
| POST | `/v1/policy/reload` | Re-read DB into the live engine. | dashboard |
| GET | `/v1/jev/calls[?limit=]` | Recent AI API calls (status, latency, tokens). Default 20, cap 100. | dashboard |

## Policy

Two formats, both file-order (first match wins):

**Legacy** `configs/policy.yaml` — `{id, tool, pattern, action, priority}` (priority parsed, not used). Still fully supported.

### Groups (categories / businesses / tasks)

Every rule accepts optional grouping fields — all empty = match all:

```yaml
groups:
  - name: secrets
    description: Credential file writes (ask)
  - name: code-allow
    description: Everyday programming edits (allow)

rules:
  - id: env-write
    tool: write_file
    group: secrets        # primary bucket
    category: safety      # dashboard taxonomy only — never match scope
    business: payments    # only matches context.business: payments
    task: deploy          # only matches context.task: deploy
    pattern: "\\.env"
    action: approval_required
    approval_timeout: 120  # per-rule approval TTL in seconds (0 = default)
```

`category` (and `group`) are dashboard taxonomy, never request scope:
adapters send no category, so every rule matches regardless of its tag.
`business`/`task` are genuine scopes — a rule carrying one only matches
requests that carry the same value.

`configs/profiles/*.yaml` supports the same fields (plus the V2 `policies:`
match → decision shape with explicit risk + reason codes):

```yaml
policies:
  - id: pay-deploy
    group: privileged-ops
    business: payments
    match:
      tool: terminal
      command: {regex: 'deploy'}
      context: {task: deploy}
    decision: {action: approval_required, risk: 0.8}
```

Request scope is carried in context:

```bash
./jev-guard check --tool terminal --command "deploy" \
  --business payments --task deploy --category safety
curl -X POST .../v1/check -d '{"agent":{"name":"x"},
  "tool":{"name":"Bash","args":{"command":"deploy"}},
  "context":{"business":"payments","task":"deploy"}}'
```

Filter to a subset of groups (CLI flag or env, comma-separated):

```bash
./jev-guard serve --group secrets,code-allow
POLICY_GROUPS=git,system-read ./jev-guard serve
./jev-guard check --tool terminal --command "git status" --group git
./jev-guard policy test --group secrets
./jev-guard eval --group git evals/fixtures/policy.yaml
```

Directory policies: `--policy configs/policies/` merges every `*.yaml`
sorted (rules concatenate, groups de-duplicate, first match wins).

`GET /v1/policies[?group=NAME]` returns `{version, groups}`.

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

### Approval TTL

`approval_required` decisions expire. Effective TTL: the matched rule's
`approval_timeout` when positive, else the default. Default chain:
`--approval-ttl` / `JEV_APPROVAL_TTL` seeds it, the DB `settings` row
(`PUT /v1/settings`, dashboard Settings tab) overrides it live at
runtime. Bounds everywhere: 5s–1h, default 30s. `approval_timeout`
is only valid on `approval_required` rules (validated on write, 400).

## Adapters

Thin shims — all canonicalize the tool name and `POST /v1/check`.
80% of logic lives in Go.

| Adapter | Status | Source |
|---------|--------|--------|
| OpenCode | ✅ | `plugins/opencode/jev-guard.js` (`tool.execute.before`) |
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

Approval flow: `approval_required` carries `approval_id` + `expires_in`.
The OpenCode plugin polls `GET /v1/approvals/page` until a human decides
via dashboard, `jev-guard approve|deny`, or the curl commands it prints —
any channel unblocks the others. When the guard runs with `--auth-token`,
set the same token as `JEV_AUTH_TOKEN` in the agent process env or every
approval poll 401s and the plugin fails closed on timeout. All adapters
fail closed on unknown verdicts or unreachable guard (Claude/Codex surface
`ask` + the approval short-id so the human can decide out of band).

## Environment

`.env` is auto-loaded from the CWD **and** `~/.config/jev-guard/.env`
(supports `export` prefix, inline `#` comments, quoted values; existing
environment wins):

```bash
cp .env.example .env   # then set values below
JEV_API_KEY=...        # or TYPESAFE_API_KEY / OPENROUTER_API_KEY
JEV_MODEL=typesafe-ai/jev
JEV_ENDPOINT=https://ai-gateway.vercel.sh/v1/evaluate
LISTEN=127.0.0.1:8787
JEV_DB=~/.hermes/guard/jev.db
JEV_AUTH_TOKEN=...
JEV_APPROVAL_TTL=30    # default approval TTL in seconds (5-3600)
```

Key priority: `--jev-api-key` flag > `JEV_API_KEY` > `TYPESAFE_API_KEY` >
`OPENROUTER_API_KEY`. Model auto-detection: `typesafe-ai/*` or `*jev*` →
evaluation API (`{model, state, questions}`); otherwise OpenAI chat format.

Audit log: `$HOME/.hermes/guard/audit.jsonl` (or `$AUDIT_PATH`).

## Evals & Testing

```bash
make check
./jev-guard policy test
./jev-guard eval evals/fixtures/policy.yaml
```

Fixtures (`evals/fixtures/policy.yaml`) cover safe/allow, destructive/block,
and privileged/approval cases across agent tool-name variants (`terminal`,
`Bash`, `shell`, `fs_write`). Unit tests use mocks (`mockJevClient`,
`judge.Mock`) — no network needed.

## License

MIT
