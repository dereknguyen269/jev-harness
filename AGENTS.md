# AGENTS.md — Jev Guard Harness

## Build & Run

```bash
go build -o guard ./cmd/harness
./guard -listen 0.0.0.0:8787
```

`go build` produces `guard` at root only. Rebuild after source changes.

Default listen is `0.0.0.0:8787`; override with `-listen` flag or `LISTEN` env var in `.env`.

Quick health check:
```bash
curl http://127.0.0.1:8787/health
```

## Testing

```bash
go test ./... -v
```

All 12 tests pass from the repository root. Tests use a `mockJevClient` — no network services needed.

## Key files

| Path | Purpose |
|------|---------|
| `cmd/harness/main.go` | HTTP server entrypoint, port 8787 |
| `internal/policy/engine.go` | Deterministic rules → Jev API → fail-closed |
| `internal/jev/client.go` | Model-type auto-detection, provider-specific request formats |
| `configs/policy.yaml` | YAML rules, file-order matching (priority field ignored) |
| `plugins/opencode/jev-guard.js` | OpenCode plugin source, hooks `tool.execute.before` |
| `.opencode/plugins/jev-guard.js` | Installed OpenCode plugin (generated copy) |
| `.opencode/opencode.json` | References plugin `"jev-guard"` |
| `.opencode/package.json` | Plugin dependency `@opencode-ai/plugin` v1.18.31 |
| `generate-opencode-plugin.sh` | Copies `plugins/opencode/jev-guard.js` to install target |

## Architecture

- **Entry:** `cmd/harness/main.go` — Go HTTP server (`-listen` flag).
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
- `Normalize()` maps tool names: `terminal`/`bash` → execute, `write_file`/`write` → write, `browser_navigate`/`browser` → navigate. Unknown tools fall through to generic extractor.
- HTTP client timeout: 10 seconds (500ms was too short for Vercel gateway).

## Dependencies

- `github.com/gorilla/mux v1.8.1` (routing)
- `github.com/google/uuid v1.6.0` (cache key hashing)
- `gopkg.in/yaml.v3 v3.0.1` (policy parsing)
- Go 1.23.4

## Gotchas

- `.gitignore` ignores `.env`, `guard`, `*.test.json`, `*.log` — don't commit env files or built binaries.
- `engine_test.go` uses `runtime.Caller` for portable policy path resolution — tests run from any directory.
- Policy `priority` field is parsed but **not used** — rules match in YAML file order.
- `.opencode/plugins/jev-guard.js` must match `plugins/opencode/jev-guard.js`. Run `./generate-opencode-plugin.sh my-jev-harness --project` to regenerate after changes.
- `generate-opencode-plugin.sh` takes `<plugin-name> [--global|--project]`; flag is `$2`, not `$1` (plugin name is ignored).
- `generate-opencode-plugin.sh` copies source to `.opencode/plugins/jev-guard.js` for project install; config path is `./.opencode/opencode.json`.
