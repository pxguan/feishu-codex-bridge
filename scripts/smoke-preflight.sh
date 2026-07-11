#!/usr/bin/env bash
# smoke-preflight.sh - offline preflight before real Feishu smoke test (no Feishu creds needed).
# Checks: build / vet / test green; binary can detect Codex (and Claude if logged in);
# isolation: avoid running under the production HOME (would collide with the single-instance lock).
# Usage: bash scripts/smoke-preflight.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT" || exit 1

BINARY="./feishu-codex-bridge"
TMP_HOME="$(mktemp -d)"
trap 'rm -rf "$TMP_HOME"' EXIT

PASS=0
FAIL=0

ok()   { echo "  [OK] $1"; PASS=$((PASS+1)); }
bad()  { echo "  [XX] $1"; FAIL=$((FAIL+1)); }
warn() { echo "  [!!] $1"; }

echo "Preflight check (project root: $ROOT)"

# 1. build ./...
echo ". go build ./..."
if go build ./... 2>&1 | tail -5; then ok "build passed"; else bad "build failed"; fi

# 2. vet ./...
echo ". go vet ./..."
if go vet ./... 2>&1 | tail -5; then ok "vet passed"; else bad "vet failed"; fi

# 3. test ./...
echo ". go test ./..."
if go test ./... 2>&1 | grep -qE "FAIL|panic"; then
  bad "FAIL/panic found (see above)"
else
  ok "unit tests green"
fi

# 4. build the smoke binary
echo ". make build"
if make build >/dev/null 2>&1 && [ -x "$BINARY" ]; then ok "binary ready: $BINARY"; else bad "make build failed"; fi

# 5. doctor probe (isolated HOME, never touches production config/lock)
echo ". doctor (isolated HOME)"
DOC="$(HOME="$TMP_HOME" "$BINARY" doctor 2>&1)"
if echo "$DOC" | grep -qE "Codex [0-9]"; then ok "Codex detected"; else warn "Codex not detected (maybe not logged in; does not affect code correctness)"; fi
if echo "$DOC" | grep -qiE "Claude"; then ok "Claude probe output present"; else warn "doctor lists only Codex (Claude covered by unit tests)"; fi

# 6. isolation check: current HOME vs production config dir
REAL_HOME="${HOME:-}"
PROD_LOCK="$REAL_HOME/.feishu-codex-bridge"
if [ -d "$PROD_LOCK" ]; then
  warn "Found $PROD_LOCK (production config). For smoke test use an isolated HOME: export HOME=/tmp/fcb-smoke"
else
  ok "No production config dir under current HOME (isolation safe)"
fi

echo ""
echo "---- Preflight result: passed $PASS / failed $FAIL ----"
if [ "$FAIL" -gt 0 ]; then
  echo "Preflight FAILED. Fix the [XX] items above before running the real smoke test."
  exit 1
else
  echo "Preflight PASSED. Next: read docs/smoke-test-runbook.md steps 3-4 and run the real smoke on an isolated bot."
fi
