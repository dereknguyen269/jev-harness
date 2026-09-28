#!/bin/bash
# Jev Guard menu-bar notifier launcher (CFBundleExecutable).
#
# __REPO_ROOT__ is baked in by scripts/make-macos-menubar.sh. Override with
# JEV_GUARD_REPO (then re-run make macos-menubar). Gateway address/auth come
# from JEV_GUARD_URL / JEV_AUTH_TOKEN, same names as the agent plugins —
# set them in the app environment when the guard runs with --auth-token,
# otherwise every poll 401s and the menu shows an auth-mismatch state.
set -euo pipefail

REPO_ROOT="${JEV_GUARD_REPO:-__REPO_ROOT__}"
if [ ! -x "$REPO_ROOT/jev-guard" ]; then
  osascript -e "display alert \"Jev Guard Notifier\" message \"Binary not found at $REPO_ROOT/jev-guard — rebuild with make macos-menubar or set JEV_GUARD_REPO.\"" >/dev/null 2>&1 || true
  echo "jev-guard-menubar: binary not found at $REPO_ROOT/jev-guard" >&2
  exit 1
fi

# Strip legacy Finder ProcessSerialNumber args (see launcher.sh).
filtered=()
for a in "$@"; do
  [[ "$a" == -psn_* ]] || filtered+=("$a")
done

if [ "${#filtered[@]}" -gt 0 ]; then
  exec "$REPO_ROOT/jev-guard" menubar "${filtered[@]}"
else
  exec "$REPO_ROOT/jev-guard" menubar
fi
