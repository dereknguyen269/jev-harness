#!/bin/bash
# Jev Guard macOS launcher (CFBundleExecutable for jev-guard.app).
#
# Finder-launched apps get CWD=/, but jev-guard resolves its default policy
# (configs/policy.yaml) relative to CWD — an empty policy plus fail-closed
# would block every tool call while looking healthy. So cd to the checkout
# first; this keeps the app identical to `./jev-guard serve` in a terminal.
#
# __REPO_ROOT__ is baked in by scripts/make-macos-app.sh. Override with
# JEV_GUARD_REPO (e.g. after moving the checkout — then re-run make macos-app).
# JEV_GUARD_OPEN_DASHBOARD=0 disables the auto-opened dashboard tab.
set -euo pipefail

REPO_ROOT="${JEV_GUARD_REPO:-__REPO_ROOT__}"
if [ ! -x "$REPO_ROOT/jev-guard" ]; then
  osascript -e "display alert \"Jev Guard\" message \"Binary not found at $REPO_ROOT/jev-guard — rebuild with make macos-app or set JEV_GUARD_REPO.\"" >/dev/null 2>&1 || true
  echo "jev-guard-launcher: binary not found at $REPO_ROOT/jev-guard" >&2
  exit 1
fi
cd "$REPO_ROOT"

# Strip legacy Finder ProcessSerialNumber args (-psn_*) — the Go flag
# parser would reject them and exit before serving. The length check keeps
# `set -u` happy on macOS bash 3.2, where "${empty[@]}" is unbound.
filtered=()
for a in "$@"; do
  [[ "$a" == -psn_* ]] || filtered+=("$a")
done

LISTEN_ADDR="${LISTEN:-127.0.0.1:8787}"
if [ "${JEV_GUARD_OPEN_DASHBOARD:-1}" = "1" ]; then
  (sleep 2; open "http://$LISTEN_ADDR/") >/dev/null 2>&1 &
fi

if [ "${#filtered[@]}" -gt 0 ]; then
  exec "$REPO_ROOT/jev-guard" serve "${filtered[@]}"
else
  exec "$REPO_ROOT/jev-guard" serve
fi
