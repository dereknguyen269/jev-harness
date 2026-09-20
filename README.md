# Jev Guard Harness

A policy enforcement layer for agent CLIs (Hermes, OpenCode) that runs a deterministic
check first, then consults a Jev API for ambiguous cases, and **fails closed** when the
upstream service is unreachable.

## Architecture

```
Agent CLI  →  plugin hook  →  guard HTTP service (port 8787)
                                     │
                     ┌───────────────┼───────────────┐
                     ▼               ▼               ▼
             deterministic   Jev API (opt)   fail-closed
              (policy.yaml)   (ambiguous)     (unreachable)
```

The guard service is a small Go HTTP server with three endpoints:

| Method | Path         | Purpose                                            |
|--------|--------------|----------------------------------------------------|
| GET    | `/health`    | Liveness probe. Returns `{"status":"ok","version","jev"}` (`jev` = Jev client configured). |
| POST   | `/v1/check`  | Evaluate a tool call. Returns `decision` (`allow`\|`block`\|`approval_required`), `risk`, `confidence`, `reason`, `policy`, `request_approval`. Engine failure → HTTP 500 with generic `"internal error"` (fail-closed, no details leaked). |
| GET    | `/v1/audit`  | Stub — returns "not yet implemented". Read the audit JSONL file instead (see below). |

## Policy Layers

1. **Deterministic** — `configs/policy.yaml` rules run first. Blocks dangerous patterns
   (`rm -rf /`, destructive shell, etc.) before any network call.
2. **Jev API** — if deterministic layer is unsure, forwards the request to a Jev
   endpoint for an LLM-based second opinion.
3. **Fail-closed** — if the Jev endpoint is unreachable, the request is blocked.

## Quick Start

```bash
# Build
go build -o guard ./cmd/harness

# Run
./guard -listen 0.0.0.0:8787

# Health check
curl http://127.0.0.1:8787/health
# {"status":"ok","version":"0.1.0","jev":false}

# Policy check (safe)
curl -X POST http://127.0.0.1:8787/v1/check \
  -H 'Content-Type: application/json' \
  -d '{"tool":"terminal","args":{"command":"echo hi"},"context":{}}'

# Policy check (blocked)
curl -X POST http://127.0.0.1:8787/v1/check \
  -H 'Content-Type: application/json' \
  -d '{"tool":"terminal","args":{"command":"rm -rf /"},"context":{}}'
```

## Environment Configuration

Create a `.env` file in the current directory:

```bash
# Copy the template
cp .env.example .env
# Edit with your credentials
```

Required: `JEV_API_KEY` (or `TYPESAFE_API_KEY` / `OPENROUTER_API_KEY`).

The guard auto-loads `.env` on startup (stdlib only). Supports `export KEY=value` prefix and inline `#` comments (a `#` must be preceded by whitespace; `#` inside an unquoted value like `KEY=abc#123` is preserved).

## Adapters

The guard is transport-agnostic: any agent CLI that can POST `{tool, args, context}`
to `/v1/check` before executing a tool — and block on `block` / non-2xx — can
integrate. Fail closed on any error.

| Adapter | Status | Notes |
|---------|--------|-------|
| OpenCode | ✅ Implemented | `plugins/opencode/jev-guard.js`, hooks `tool.execute.before`; install via `generate-opencode-plugin.sh` (see below). |
| Hermes | ⚠️ Example only | `pre_tool_call` hook sketch below; no adapter code in this repo yet. (The default audit path lives under `~/.hermes` for historical reasons.) |
| Claude Code | ❌ Not implemented | |
| OpenClaw | ❌ Not implemented | |
| Generic / any CLI | ✅ Via HTTP | POST to `/v1/check`; treat `block`, `approval_required`, and any error/5xx as deny. |

### Hermes

```python
# ~/.hermes/plugins/hermes-guard/__init__.py
ctx.register_hook("pre_tool_call", pre_tool_call_guard)
```

The hook fires before every tool call. If the guard blocks it, the tool is never
executed (fail-closed).

### OpenCode

Install the plugin from the source of truth (`plugins/opencode/jev-guard.js`) with the generator script:

```bash
# Project-local install (writes .opencode/plugins/jev-guard.js)
./generate-opencode-plugin.sh my-jev-harness --project

# Global install (writes ~/.config/opencode/plugins/jev-guard.js)
./generate-opencode-plugin.sh my-jev-harness --global
```

Then add `"jev-guard"` to the `"plugin"` array in `.opencode/opencode.json`
(project) or `~/.config/opencode/opencode.json` (global), and restart OpenCode.

The plugin hooks `tool.execute.before`, POSTs each tool call to `/v1/check`,
throws on `block`, and treats `approval_required` as a block (OpenCode has no
approval hook). Network/timeout errors fail closed. Guard endpoint and timeout
are configurable via `JEV_GUARD_URL` (default `http://127.0.0.1:8787`) and
`JEV_GUARD_TIMEOUT_MS` (default `2000`).

Re-run the generator after editing `plugins/opencode/jev-guard.js` — never edit
the installed copy directly.

## Configuration

### `configs/policy.yaml`

Rules evaluated in file order, not by priority — the first matching rule wins:

```yaml
rules:
  - id: root-delete
    tool: terminal
    pattern: "rm\\s+-rf\\s+/"
    action: block
    priority: 100
  - id: sudo-command
    tool: terminal
    pattern: "sudo\\s+"
    action: approval_required
    priority: 85
  - id: allow-git-status
    tool: terminal
    pattern: "git\\s+status"
    action: allow
    priority: 10
```

### Jev API (Vercel AI Gateway)

The guard supports TypeSafe AI evaluation models through Vercel AI Gateway:

```bash
./guard -listen 0.0.0.0:8787 \
  -jev-api-key vck_... \
  -jev-endpoint https://ai-gateway.vercel.sh/v1/evaluate
```

Or via `.env`:
```bash
JEV_ENDPOINT=https://ai-gateway.vercel.sh/v1/evaluate
JEV_MODEL=typesafe-ai/jev
JEV_API_KEY=vck_...
```

CLI flags:

| Flag | Default | Purpose |
|------|---------|---------|
| `-listen` | `127.0.0.1:8787` | HTTP listen address (or `LISTEN` in `.env`). |
| `-policy` | `configs/policy.yaml` | Path to policy file. |
| `-jev-endpoint` | auto-selected | Jev API endpoint. When set explicitly it is never overridden. |
| `-jev-api-key` | env fallback | Jev API key. When set explicitly (even to `""`) env vars are ignored. |

The Jev client auto-detects the provider and model type:
- **Evaluation models** (`typesafe-ai/jev`, `jev-latest`) → `/v1/evaluate` endpoint
- **Chat LLMs** → `/v1/chat/completions` endpoint

When `-jev-endpoint` is not set, an evaluation-model name remaps the endpoint to
the matching provider default (typesafe/openrouter/vercel) instead of using a
pinned non-eval endpoint.

API key priority: `-jev-api-key` flag > `JEV_API_KEY` > `TYPESAFE_API_KEY` > `OPENROUTER_API_KEY`.

### Audit log

Every `/v1/check` decision is appended as JSONL to
`$HOME/.hermes/guard/audit.jsonl` by default, or `$AUDIT_PATH` if set.

## Testing

```bash
go test ./internal/policy/ -v
```

All 12 tests pass on macOS. Tests use a `mockJevClient` — no network services needed.

## License

MIT
