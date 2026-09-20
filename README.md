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
| GET    | `/health`    | Liveness probe. Returns `{"status":"ok"}`.         |
| POST   | `/v1/check`  | Evaluate a tool call. Returns `allowed`, `level`, `reason`. |
| GET    | `/v1/audit`  | Last N audit entries (debug only).                 |

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
# {"status":"ok","version":"0.1.0"}

# Policy check (safe)
curl -X POST http://127.0.0.1:8787/v1/check \
  -H 'Content-Type: application/json' \
  -d '{"tool":"terminal","args":{"command":"echo hi"},"context":{}}'
# {"allowed":true,"level":"allow","reason":"allowed"}

# Policy check (blocked)
curl -X POST http://127.0.0.1:8787/v1/check \
  -H 'Content-Type: application/json' \
  -d '{"tool":"terminal","args":{"command":"rm -rf /"},"context":{}}'
# {"allowed":false,"level":"block","reason":"blocked"}
```

## Adapters

### Hermes

```python
# ~/.hermes/plugins/hermes-guard/__init__.py
ctx.register_hook("pre_tool_call", pre_tool_call_guard)
```

The hook fires before every tool call. If the guard blocks it, the tool is never
executed (fail-closed).

### OpenCode

```js
// plugins/opencode/jev-guard.js
export default function(client) {
  return {
    "tool.execute.before": async (input, output) => {
      const res = await fetch("http://localhost:8787/v1/check", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input)
      });
      const decision = await res.json();
      if (!decision.allowed) {
        throw new Error(`Blocked: ${decision.reason}`);
      }
    }
  };
}
```

## Configuration

### `configs/policy.yaml`

```yaml
deterministic:
  - id: block-rm-rf-root
    tool: terminal
    pattern: "rm -rf /"
    action: block
    reason: "blocked by deterministic policy"
```

### Jev Endpoint

Set via environment variable or flag:

```bash
./guard -listen 0.0.0.0:8787 -jev-endpoint https://jev.example.com/v1/evaluate
```

## Testing

```bash
go test ./internal/policy/ -v
```

## License

MIT