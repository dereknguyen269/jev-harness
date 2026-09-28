# AGENTS.md — Jev Guard (Agent Safety Gateway)

## Build & test

```bash
make build          # frontend + binary; needs Node 20+ and Go 1.23+
make check          # full gate: ui-build + fmt + vet + test
make build-go|test-go  # Go-only escape hatch (no frontend)
make macos-app      # darwin-only: dist/jev-guard.app Dock bundle (needs rsvg-convert)
go test ./internal/<pkg>/ -run <TestName> -v  # single test
```

- `make ui-build` = `npm install && npm run build` in `web/` → `internal/server/web/dist/`, consumed by `go:embed` in `internal/server/server.go`. Bare `go build` on a fresh checkout fails at compile time when `dist/` is missing — always build via `make`.
- `make fmt` is `gofmt -l internal cmd` with a non-empty check; keep it clean.
- CI (`.github/workflows/ci.yml`) runs only `go test ./... -v` — run `make check` locally before pushing.
- Tests use mocks (`mockJevClient`, `judge.Mock`), no network. `engine_test.go` resolves policy via `runtime.Caller`, so tests run from any directory.
- Binaries build at repo root (`jev-guard`, gitignored). Frontend `package-lock.json` is now committed in `web/`.
- `make macos-app` wraps `./jev-guard serve` as `dist/jev-guard.app` (gitignored; sources in `packaging/macos/`, builder in `scripts/make-macos-app.sh`). The launcher `cd`s to the checkout because Finder-launched apps get CWD=`/`, and the default policy path is CWD-relative (`configs/policy.yaml`) — without the `cd` the engine runs empty and fail-closes everything. Repo path is baked at build time: re-run after moving the checkout (or set `JEV_GUARD_REPO`). macOS bash is 3.2: no `"${empty[@]}"` under `set -u`.

## Run

```bash
./jev-guard serve --listen 127.0.0.1:8787   # default listen; also LISTEN env / .env
./jev-guard check --tool terminal --command "git status"  # exit 0 allow / 2 block / 3 approval_required
./jev-guard policy test                     # run bundled fixtures after editing policy
./jev-guard eval evals/fixtures/policy.yaml
./jev-guard doctor
./jev-guard approvals [--status pending|all] [--json] [--db PATH]  # --db accepted anywhere in args
./jev-guard approve|deny <id-prefix> [--db PATH]
jev-guard policy reseed [--mode merge|replace] [--force]  # replace destroys customs without --force
```

- Policy source: `--policy PATH` (or `POLICY_PATH`) > `--profile default|strict|developer|permissive` (`configs/profiles/`) > `configs/policy.yaml`. `--policy` accepts a directory (merges `*.yaml` sorted). `--group A,B` / `POLICY_GROUPS` filters groups.
- `--db PATH` (`JEV_DB`, default `~/.hermes/guard/jev.db`): DB seeded from YAML on first run, then DB wins at runtime and YAML backs offline CLI. Empty/unopenable → YAML-only mode (management API returns 503). `serve --reseed` merges bundled defaults on startup (keeps custom rules).
- `--auth-token` (`JEV_AUTH_TOKEN`, empty = open): gates management API via `Authorization: Bearer`. Open always: `/`, `/assets/*`, `/health`, `/v1/health|check|policies|auth/*`. Any active user's API key also works as Bearer (master token = admin). Roles: viewer reads; operator +approve/reload; admin +users/rules/groups (keys masked for non-admins).
- `.env` auto-loaded from CWD **and** `~/.config/jev-guard/.env` (supports `export` prefix, inline `#`, quoted values; existing env wins). Key priority: `--jev-api-key` > `JEV_API_KEY` > `TYPESAFE_API_KEY` > `OPENROUTER_API_KEY`. Model auto-detect: `typesafe-ai/*` or `*jev*` → evaluation API, else OpenAI chat format.
- Audit log: `$HOME/.hermes/guard/audit.jsonl` (or `AUDIT_PATH`).

## Architecture (pipeline order matters)

`internal/normalize` → `internal/policy` → `internal/cache` → `internal/judge` → fail-closed → `internal/approval` + `internal/audit`. Entry: `cmd/jev-guard/main.go`; HTTP routes: `internal/server/server.go` (`/v1/*` only, no `/v2`).

- **Normalize first:** `Bash/shell/execute→terminal`, `Write/Edit/apply_patch→write_file`, `Read→read_file` (plus Kiro `execute_bash`/`fs_write`/… variants). Rules match canonical names, so tool-name variants hit the same rule.
- **Policy:** rules match in YAML **file order, first wins — `priority` is parsed but ignored**. Keep blocks → approvals → allows ordered. Request scope: rule `business`/`task` only matches when `context.business`/`task` is set; `category`/`group` are dashboard taxonomy, never match scope. Run `policy test` after editing; writes via API validate regex+action (400) and auto-reload.
- **Fail-closed:** unknown commands with no Jev key → `block`. Jev returns judgment only (`risk`/`confidence`/`action`); the Go engine makes the final decision (confidence-gated per risk level, `risk ≥ 0.95` → block).
- **Cache:** reads 30s, low-risk allows 10s; mutations/criticals/approvals never cached.
- **Approvals:** `pending→approved|denied|expired`. TTL = per-rule `approval_timeout` else default (`--approval-ttl`/`JEV_APPROVAL_TTL`/DB `settings.approval_ttl_seconds`, 5s–1h, default 30s). Persisted to SQLite when `--db` is up (same record as dashboard buttons, plugin poll, and `approve|deny` CLI — either channel unblocks), memory-only in YAML-only mode.
- `configs/policy.yaml`: 8 groups, every rule tagged `category: safety|secrets|productivity|network`.

## Adapters (never edit installed copies)

Sources of truth: `plugins/opencode/jev-guard.js` (`tool.execute.before`), `plugins/kiro/jev-guard.py`, `adapters/{claude,codex,openclaw}/*.py`, `adapters/generic/jev-guard-check.sh`. Regenerate after changes:

```bash
./generate-opencode-plugin.sh [name] [--global|--project] [--force]  # → .opencode/plugins/ (project default)
./generate-kiro-plugin.sh [--global|--project] [--project-dir DIR] [--force]  # scope flag first
```

Guard endpoint for plugins: `JEV_GUARD_URL` (default `http://127.0.0.1:8787`), `JEV_GUARD_TIMEOUT_MS` (default 2000).

## Gotchas

- `.gitignore` covers `.env`, built binaries, `*.test.json`, `*.log` — don't commit env or binaries.
- `make policy-reset` (`policy reseed --mode replace --force`) destroys dashboard custom rules; users survive. Restart server afterwards.
- HTTP client timeout is 10s (Vercel gateway needs it); don't shorten.
- Management writes need `--db`; without it they 503 even though reads work.
