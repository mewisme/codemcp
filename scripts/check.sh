#!/usr/bin/env bash
# Fast local quality checks (subset of CI). Usage: ./scripts/check.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

export CM_CONFIG_DIR="${CM_CONFIG_DIR:-$(mktemp -d)}"
trap 'rm -rf "${CM_CONFIG_DIR}"' EXIT

echo "==> gofmt"
test -z "$(gofmt -l . | tee /dev/stderr)"

echo "==> go vet"
go vet ./...

echo "==> staticcheck"
if ! command -v staticcheck >/dev/null 2>&1; then
  go install honnef.co/go/tools/cmd/staticcheck@v0.8.1
fi
staticcheck ./...

echo "==> go mod tidy -diff"
go mod tidy -diff

echo "==> go test (short packages smoke)"
go test ./internal/outboundpolicy/ ./internal/approval/ ./internal/config/ ./internal/secretstore/ -count=1

echo "==> shellcheck install.sh"
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck install.sh scripts/test-install-sh.sh
else
  echo "skip: shellcheck not installed"
fi

echo "==> installer verification policy"
sh scripts/test-install-sh.sh

echo "==> web lint/typecheck (if pnpm available)"
if command -v pnpm >/dev/null 2>&1 && [[ -d web/node_modules ]]; then
  pnpm --dir web lint
  pnpm --dir web typecheck
else
  echo "skip: pnpm or web/node_modules missing"
fi

echo "OK: local checks passed"
