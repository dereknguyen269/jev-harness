# AGENTS.md — Jev Guard (Agent Safety Gateway v2)

## Build & Run

```bash
go build -o jev-guard ./cmd/jev-guard
./jev-guard serve --listen 0.0.0.0:8787
```

Or `make build|test|vet|fmt|run|clean|policy-reset` (`make check` = ui-build+fmt+vet+test).
`make build-go|test-go` skip the frontend (fail loudly if `web/dist/` is missing).

Dashboard frontend is React + Tailwind v4 + shadcn/ui in `web/` (Node 20+ required).
`make ui-build` runs `npm ci && npm run build` into `internal/server/web/dist/`,
which `go:embed` picks up — always build via `make`, never bare `go build` on a
fresh checkout. Backend API is unchanged (`/v1/*`).

`go build` produces binaries at root only (gitignored). Rebuild after source changes.

Default listen is `127.0.0.1:8787`; override with `--listen` flag or `LISTEN` env var in `.env`.
`--profile default|strict|developer|permissive` selects `configs/profiles/*.yaml` when `--policy` is missing.
`--db PATH` (`JEV_DB`, default `~/.hermes/guard/jev.db`) enables the SQLite policy store for the
dashboard; empty value or open failure → YAML-only mode. First run seeds the DB from YAML.
`--auth-token TOKEN` (`JEV_AUTH_TOKEN`, empty = auth disabled) gates the management API
via `Authorization: Bearer` (dashboard shell stays open so login can load);
`/v1/check|policies|health` stay open for agents.
Any active user's API key also works as a Bearer token (master token maps to admin);
`POST /v1/auth/login` (secret only — key identifies the user) and `GET /v1/auth/me` back the login screen.
Roles: viewer reads; operator also approves + reloads; admin manages users/rules/groups (+ sees API keys, masked otherwise).

Quick health check:
```bash
curl http://127.0.0.1:8787/health
curl http://127.0.0.1:8787/v1/health
```

Other CLI: `jev-guard check --tool terminal --command "git status"`,
`jev-guard policy test`, `jev-guard policy reseed [--mode merge|replace] [--force]`,
`jev-guard eval evals/fixtures/policy.yaml`,
`jev-guard audit [--decision D] [--min-risk F] [--json]`, `jev-guard doctor`.
`serve --reseed` merges bundled YAML defaults into the DB on startup (keeps custom rules).

## Testing

```bash
go test ./... -v
```

Tests use mocks (`mockJevClient`, `judge.Mock`) — no network services needed.

## Key files

| Path | Purpose |
|------|---------|
| `cmd/jev-guard/main.go` | CLI + HTTP server (`serve|check|policy <test\|reseed>|audit|eval|doctor|version`) |
| `internal/domain/` | V2 `ToolRequest`, `DecisionResult`, `CanonicalTool`, risk `L0..L4`, `Approval` |
| `internal/harness/` | Gateway pipeline `Normalize→Policy→Cache→Jev→fail-closed→ResolveJev→Audit` + resolver |
| `internal/normalize/` | First-class canonical mapping (OpenCode/Kiro/Claude/Codex/OpenClaw → terminal/write_file/...) |
| `internal/judge/` | `Judge` interface + Jev adapter (judgment only) + mock |
| `internal/policy/engine.go` | Deterministic rules → Jev API → fail-closed (`Reload` swaps rules without restart) |
| `internal/policy/v2.go` | `policies:` YAML format + `EngineV2` bridge to legacy rules (`Reload`) |
| `internal/store/` | SQLite `users` + `rules` + `groups` + `categories` (`--db`); DB wins at runtime, YAML seeds + backs CLI; `SyncDefaults`/`PruneStaleDefaults`/`ReplaceWith` power `policy reseed` (source=`yaml` vs `db` decides overwrite) |
| `internal/server/web/index.html` | REMOVED — replaced by React build (delete the file; history keeps it) |
| `web/` | React dashboard ("Jev Guard", see `src/lib/site.ts`): Vite + Tailwind v4 + shadcn/ui (`button|input|textarea|label|select|table|dialog|badge|tabs|checkbox|sonner` + radix), first-run onboarding tour |
| `internal/server/web/dist/` | Built dashboard, `go:embed` (gitignored, produced by `make ui-build`) |
| `internal/jev/client.go` | Model-type auto-detection, provider-specific request formats |
| `internal/cache/` | TTL decision cache (`read 30s, low 10s, mutation/critical 0s`) |
| `internal/approval/` | Async approval store (30s TTL, `pending→approved|denied|expired`) |
| `internal/audit/` | JSONL writer + reader/filter (`GET /v1/audit`, `stats`) |
| `internal/server/` | HTTP gateway (`/v1/check|approvals|audit|stats|policies|health`, no `/v2`) |
| `internal/server/` mgmt | Dashboard `GET /` + `/v1/users|rules|groups|categories` CRUD + `POST /v1/policy/reload` (503 when YAML-only) + `POST /v1/policy/reseed` (admin; `{"mode":"merge"|"replace","force":bool}`) + `GET /v1/jev/calls` (recent AI calls: status, latency, tokens). Gated by `--auth-token` when set (`GET /v1/auth/status` reports it) |
| `internal/config/` | Profiles (`default|strict|developer|permissive`) + thresholds |
| `configs/policy.yaml` | YAML rules, file-order matching (priority field ignored); 8 groups, every rule tagged `category: safety\|secrets\|productivity\|network`; git allows consolidated to `git-read`, `git-branch-workflow`, `git-sync` (dangerous git lives in `privileged-ops`) |
| `plugins/opencode/jev-guard.js` | OpenCode plugin source, hooks `tool.execute.before` |
| `.opencode/plugins/jev-guard.js` | Installed OpenCode plugin (generated copy) |
| `.opencode/opencode.json` | References plugin `"jev-guard"` |
| `.opencode/package.json` | Plugin dependency `@opencode-ai/plugin` v1.18.31 |
| `generate-opencode-plugin.sh` | Copies `plugins/opencode/jev-guard.js` to install target |
| `plugins/kiro/jev-guard.py` | Canonical Kiro PreToolUse hook (stdlib only; stdin JSON > flags > env) |
| `plugins/kiro/hooks/jev-guard.json.template` | Kiro hook template (`__GUARD_SCRIPT__` rendered at install) |
| `generate-kiro-plugin.sh` | Installs Kiro hook (`--project` to `.kiro/`, `--global` to `~/.kiro/`) |

## Architecture

- **Entry:** `cmd/jev-guard/main.go` — CLI + HTTP server (`serve` flag `--listen`, `--profile`).
- **Policy engine:** `internal/policy/engine.go` — deterministic rules first, then optional Jev API, fail-closed.
- **Jev client:** `internal/jev/client.go` — model-type auto-detection:
  - Evaluation models (`typesafe-ai/jev`, `jev-latest`) → `{model, state, questions}` to `/v1/evaluate`
  - Chat LLMs → OpenAI `{model, messages}` to `/v1/chat/completions`
  - Auto-detection: `strings.HasPrefix(model, "typesafe-ai/") || strings.Contains(model, "jev")` → evaluation type
  - Vercel AI Gateway: `https://ai-gateway.vercel.sh/v1/evaluate`
  - API key priority: `-jev-api-key` flag > `JEV_API_KEY` > `TYPESAFE_API_KEY` > `OPENROUTER_API_KEY`
  - Evaluation model forces endpoint to `/v1/evaluate`
- **`.env` auto-load:** `main.go` loads `.env` from CWD at startup (stdlib only). Supports `export KEY=value` prefix and inline `#` comments. Skipped if missing.
- **Config:** `configs/policy.yaml` — rules have `id`, `tool`, `pattern` (regex), `action` (`block`/`approval_required`/`allow`), `priority`, plus `group`/`category`/`business`/`task` scopes. Rules evaluated in file order, **not** by priority — first match wins.

## Key behaviors

- `/v1/check` POST returns `{"decision": "allow"|"block"|"approval_required", ...}`. Unknown commands with no Jev client → `block` (fail-closed).
- Audit logging writes JSONL to `$HOME/.hermes/guard/audit.jsonl` by default, or `$AUDIT_PATH` if set.
- `/v1/audit` GET is a stub — returns "not yet implemented".
- `/v2/*` gateway: `POST /v2/check` (full `DecisionResult` + `approval_id`), approvals, `GET /v2/audit|stats|policies|health`.
- Dashboard management: `GET|POST /v1/users`, `PUT|DELETE /v1/users/{id}`, `GET|POST /v1/rules`,
  `PUT|DELETE /v1/rules/{id}`, `POST /v1/policy/reload` (503 when YAML-only).
- Rule writes validate action + regex (400 on invalid) and auto-reload the live engine.
- `Normalize()` maps tool names incl. Kiro: `execute_bash` → `terminal`/execute, `fs_write`/`fs_append`/`str_replace`/`delete_file`/`smart_relocate` → `write_file`/write (delete flags destructive, relocate uses destination path). Unknown tools fall through to generic extractor.
- `internal/normalize` canonicalizes all agents first: `Bash/shell/execute→terminal`, `Write/Edit/apply_patch→write_file`, `Read→read_file`. `EngineV2` evaluates via canonical name so variants hit the same rules.
- HTTP client timeout: 10 seconds (500ms was too short for Vercel gateway).

## Dependencies

- `github.com/gorilla/mux v1.8.1` (routing)
- `github.com/google/uuid v1.6.0` (cache key hashing)
- `gopkg.in/yaml.v3 v3.0.1` (policy parsing)
- `modernc.org/sqlite` (pure-Go SQLite for the dashboard store; run `go mod tidy` after checkout)
- Go 1.23.4

## Gotchas

- `.gitignore` ignores `.env`, `guard`, `jev-guard`, `*.test.json`, `*.log` — don't commit env files or built binaries.
- `engine_test.go` uses `runtime.Caller` for portable policy path resolution — tests run from any directory.
- Policy `priority` field is parsed but **not used** — rules match in YAML file order.
- `.opencode/plugins/jev-guard.js` must match `plugins/opencode/jev-guard.js`. Run `./generate-opencode-plugin.sh my-jev-harness --project` to regenerate after changes.
- `generate-opencode-plugin.sh` takes `<plugin-name> [--global|--project]`; flag is `$2`, not `$1` (plugin name is ignored).
- `generate-opencode-plugin.sh` copies source to `.opencode/plugins/jev-guard.js` for project install; config path is `./.opencode/opencode.json`.
- `.kiro/hooks/jev-guard.json` + `.kiro/scripts/jev-guard.py` are generated copies of `plugins/kiro/*`. Run `./generate-kiro-plugin.sh --project --force` (or `--global`) to regenerate after changes.
- `generate-kiro-plugin.sh` usage is `./generate-kiro-plugin.sh [--global|--project] [--project-dir DIR] [--force]` (scope flag first, unlike the opencode script).
