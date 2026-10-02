# Development

## Bootstrap and verify

```bash
make bootstrap
make frontend-build
make check
make test
make test-race
make build
make generate
make check-generated
make security-gosec
```

`make check`, `make test`, and `make test-race` build embedded frontend assets first. Go tests use an isolated `CM_CONFIG_DIR`.

## Run from source

```bash
make run ARGS="status"
make tui
```

Developer builds are endpointless for product telemetry by default. Opt in explicitly when needed:

```bash
make build LOCAL_TELEMETRY_ENDPOINT=https://telemetry.mewis.me/v1/products/codemcp/events
```

Production release builds inject their endpoint through release configuration.

## Frontend assets

`pnpm --dir frontend build` builds:

- Browser Admin → `internal/interface/web/dist/`
- Telegram Mini App → `internal/telegram/mini-app-dist/`

These are generated/ignored. CI Go-only jobs create deterministic minimal embed fixtures; the frontend job builds/tests the real apps.

## Static/security checks

```bash
go mod verify
go mod tidy -diff
staticcheck ./...
govulncheck ./...
CM_CONFIG_DIR="$(mktemp -d)" go test ./...
```

CI additionally runs race tests, frontend lint/typecheck/tests/E2E/build, installer checks, native/cross builds, and release contract verification.

## Architecture overview

`docs/architecture/overview.architecture.json` is the Archify source model for the system overview. `overview-light.svg` and `overview-dark.svg` are the README exports from that single topology, with theme-specific Archify blueprint palettes. Update the source model and regenerate both exports when system boundaries change.

## Release ownership

Release artifacts use stable versionless filenames; the Git tag is the version identity. `.goreleaser.yaml`, `.github/workflows/release.yml`, and `scripts/release/` own the artifact matrix, checksums, signatures, and package metadata checks.
