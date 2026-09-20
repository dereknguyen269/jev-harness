# AGENTS.md — Jev Guard Harness

## Build & Run

```bash
go build -o guard ./cmd/harness
./guard -listen 0.0.0.0:8787
```

Binary outputs `guard` (root) and `harness` (also at `./harness` from a prior build) — both are stale ELF binaries; rebuild when changing source.

## Testing

```bash
go test ./internal/policy/ -v
```

All 12 tests pass on macOS after fixing the hardcoded policy path (`internal/policy/engine_test.go:9`).

No CI, no Makefile, no pre-commit hooks. Tests use a `mockJevClient` — no network services needed.

## Architecture

- **Entry:** `cmd/harness/main.go` — Go HTTP server on port 8787 (configurable via `-listen`).
- **Policy engine:** `internal/policy/engine.go` — deterministic rules first, then optional Jev API, fail-closed.
- **Jev client:** `internal/jev/client.go` — model-type auto-detection + provider-specific request formats:
  - Evaluation models (`typesafe-ai/jev`, `jev-latest`, etc.) → `{model, state, questions}` format to `/v1/systemone`
  - Chat LLMs → OpenAI-compatible `{model, messages}` format to `/v1/chat/completions`
  - Auto-detection: `strings.HasPrefix(model, "typesafe-ai/") || strings.Contains(model, "jev")` → evaluation type
  - API key priority: `-jev-api-key` flag > `JEV_API_KEY` > `TYPESAFE_API_KEY` > `OPENROUTER_API_KEY` env vars
  - **Evaluation model override:** if model is evaluation type, `-jev-endpoint`/`JEV_ENDPOINT` is ignored — forces `https://api.typesafe.ai/v1/systemone`
- **`.env` file:** `main.go` auto-loads `.env` from CWD at startup (stdlib only, no dependency). Key=value format, comments with `#`, quoted values stripped. Skipped if file missing.
- **`.env.example`:** template with `TYPESAFE_API_KEY`, `OPENROUTER_API_KEY`, `JEV_ENDPOINT`, `JEV_MODEL`, `JEV_API_KEY`, `AUDIT_PATH`, `LISTEN`.
- **Plugin:** `plugins/opencode/jev-guard.js` — OpenCode plugin hooking `tool.execute.before`. Fails closed on any guard error.
- **Config:** `configs/policy.yaml` — YAML rules with `id`, `tool`, `pattern` (regex), `action` (`block`/`approval_required`/`allow`), `priority`. Rules are evaluated in file order, not by priority — the first matching rule wins.

## Key behaviors

- `/v1/check` POST returns `{"decision": "allow"|"block"|"approval_required", ...}`. Unknown commands with no Jev client → `block` (fail-closed).
- Audit logging writes JSONL to `$HOME/.hermes/guard/audit.jsonl` by default, or `$AUDIT_PATH` if set.
- `/v1/audit` GET is a stub — returns "not yet implemented".
- `Normalize()` maps tool names: `terminal`/`bash` → execute, `write_file`/`write` → write, `browser_navigate`/`browser` → navigate. Unknown tools fall through to a generic extractor.
- HTTP client timeout: 10 seconds (500ms was too short for Vercel gateway).

## Dependencies

- `github.com/gorilla/mux v1.8.1` (routing)
- `github.com/google/uuid v1.6.0` (cache key hashing)
- `gopkg.in/yaml.v3 v3.0.1` (policy parsing)
- Go 1.23.4
