#!/usr/bin/env bash
# Fast local quality checks (subset of CI). Prefer: make check
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"

check_config_dir="$(mktemp -d)"
trap 'rm -rf "${check_config_dir}"' EXIT
export CM_CONFIG_DIR="${check_config_dir}"

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
  shellcheck install.sh scripts/installer/test-unix.sh scripts/release/build-windows-setup.sh scripts/installer/test-windows-setup.sh scripts/release/verify-windows-setup-payload.sh
else
  echo "skip: shellcheck not installed"
fi

echo "==> frontend lint/typecheck (if pnpm available)"
if command -v pnpm >/dev/null 2>&1 && [[ -d frontend/node_modules ]]; then
  pnpm --dir frontend lint
  pnpm --dir frontend typecheck
else
  echo "skip: pnpm or frontend/node_modules missing"
fi

echo "OK: local checks passed"
