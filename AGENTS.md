# AGENTS.md — Jev Guard (Agent Safety Gateway v2)

## Build & Run

```bash
go build -o jev-guard ./cmd/jev-guard
./jev-guard serve --listen 0.0.0.0:8787
```

`cmd/harness` (`guard` binary) is a deprecated compat shim — use `cmd/jev-guard`.
`go build` produces binaries at root only (both gitignored). Rebuild after source changes.

Default listen is `127.0.0.1:8787`; override with `--listen` flag or `LISTEN` env var in `.env`.
`--profile default|strict|developer|permissive` selects `configs/profiles/*.yaml` when `--policy` is missing.

Quick health check:
```bash
curl http://127.0.0.1:8787/health
curl http://127.0.0.1:8787/v2/health
```

Other CLI: `jev-guard check --tool terminal --command "git status"`,
`jev-guard policy test`, `jev-guard eval evals/fixtures/policy.yaml`,
`jev-guard audit [--decision D] [--min-risk F] [--json]`, `jev-guard doctor`.

## Testing

```bash
go test ./... -v
```

Tests use mocks (`mockJevClient`, `judge.Mock`) — no network services needed.

## Key files

| Path | Purpose |
|------|---------|
| `cmd/jev-guard/main.go` | V2 CLI + HTTP server (`serve|check|policy test|audit|eval|doctor|version`) |
| `cmd/harness/main.go` | Deprecated compat shim (serves `/v1/*`, prints warning) |
| `internal/domain/` | V2 `ToolRequest`, `DecisionResult`, `CanonicalTool`, risk `L0..L4`, `Approval` |
| `internal/harness/` | Gateway pipeline `Normalize→Policy→Cache→Jev→fail-closed→ResolveJev→Audit` + resolver |
| `internal/normalize/` | First-class canonical mapping (OpenCode/Kiro/Claude/Codex/OpenClaw → terminal/write_file/...) |
| `internal/judge/` | `Judge` interface + Jev adapter (judgment only) + mock |
| `internal/policy/engine.go` | Deterministic rules → Jev API → fail-closed |
| `internal/policy/v2.go` | `policies:` YAML format + `EngineV2` bridge to legacy rules |
| `internal/jev/client.go` | Model-type auto-detection, provider-specific request formats |
| `internal/cache/` | TTL decision cache (`read 30s, low 10s, mutation/critical 0s`) |
| `internal/approval/` | Async approval store (30s TTL, `pending→approved|denied|expired`) |
| `internal/audit/` | JSONL writer + reader/filter (`GET /v2/audit`, `stats`) |
| `internal/server/` | HTTP gateway (`/v2/check|approvals|audit|stats|policies|health` + `/v1/check` compat) |
| `internal/config/` | Profiles (`default|strict|developer|permissive`) + thresholds |
| `configs/policy.yaml` | YAML rules, file-order matching (priority field ignored) |
| `plugins/opencode/jev-guard.js` | OpenCode plugin source, hooks `tool.execute.before` |
| `.opencode/plugins/jev-guard.js` | Installed OpenCode plugin (generated copy) |
| `.opencode/opencode.json` | References plugin `"jev-guard"` |
| `.opencode/package.json` | Plugin dependency `@opencode-ai/plugin` v1.18.31 |
| `generate-opencode-plugin.sh` | Copies `plugins/opencode/jev-guard.js` to install target |
| `plugins/kiro/jev-guard.py` | Canonical Kiro PreToolUse hook (stdlib only; stdin JSON > flags > env) |
| `plugins/kiro/hooks/jev-guard.json.template` | Kiro hook template (`__GUARD_SCRIPT__` rendered at install) |
| `generate-kiro-plugin.sh` | Installs Kiro hook (`--project` to `.kiro/`, `--global` to `~/.kiro/`) |

## Architecture

- **Entry:** `cmd/jev-guard/main.go` — V2 CLI + HTTP server (`serve` flag `--listen`, `--profile`). `cmd/harness` is a deprecated shim.
- **Policy engine:** `internal/policy/engine.go` — deterministic rules first, then optional Jev API, fail-closed.
- **Jev client:** `internal/jev/client.go` — model-type auto-detection:
  - Evaluation models (`typesafe-ai/jev`, `jev-latest`) → `{model, state, questions}` to `/v1/evaluate`
  - Chat LLMs → OpenAI `{model, messages}` to `/v1/chat/completions`
  - Auto-detection: `strings.HasPrefix(model, "typesafe-ai/") || strings.Contains(model, "jev")` → evaluation type
  - Vercel AI Gateway: `https://ai-gateway.vercel.sh/v1/evaluate`
  - API key priority: `-jev-api-key` flag > `JEV_API_KEY` > `TYPESAFE_API_KEY` > `OPENROUTER_API_KEY`
  - Evaluation model forces endpoint to `/v1/evaluate`
- **`.env` auto-load:** `main.go` loads `.env` from CWD at startup (stdlib only). Supports `export KEY=value` prefix and inline `#` comments. Skipped if missing.
- **Config:** `configs/policy.yaml` — rules have `id`, `tool`, `pattern` (regex), `action` (`block`/`approval_required`/`allow`), `priority`. Rules evaluated in file order, **not** by priority — first match wins.

## Key behaviors

- `/v1/check` POST returns `{"decision": "allow"|"block"|"approval_required", ...}`. Unknown commands with no Jev client → `block` (fail-closed).
- Audit logging writes JSONL to `$HOME/.hermes/guard/audit.jsonl` by default, or `$AUDIT_PATH` if set.
- `/v1/audit` GET is a stub — returns "not yet implemented".
- `/v2/*` gateway: `POST /v2/check` (full `DecisionResult` + `approval_id`), approvals, `GET /v2/audit|stats|policies|health`.
- `Normalize()` maps tool names incl. Kiro: `execute_bash` → `terminal`/execute, `fs_write`/`fs_append`/`str_replace`/`delete_file`/`smart_relocate` → `write_file`/write (delete flags destructive, relocate uses destination path). Unknown tools fall through to generic extractor.
- `internal/normalize` canonicalizes all agents first: `Bash/shell/execute→terminal`, `Write/Edit/apply_patch→write_file`, `Read→read_file`. `EngineV2` evaluates via canonical name so variants hit the same rules.
- HTTP client timeout: 10 seconds (500ms was too short for Vercel gateway).

## Dependencies

- `github.com/gorilla/mux v1.8.1` (routing)
- `github.com/google/uuid v1.6.0` (cache key hashing)
- `gopkg.in/yaml.v3 v3.0.1` (policy parsing)
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
