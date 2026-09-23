# Development

This guide covers source builds, verification, CI, release smoke tests, and release workflow expectations.

For how to propose changes, open issues/PRs, and community norms, start with [CONTRIBUTING.md](../CONTRIBUTING.md).

## Requirements

- Go 1.27+
- Node.js 24+
- pnpm 11+

## Editor and local quality checks

`.editorconfig` sets **indent size 2** for all files. Go uses tabs at width 2 (required by `gofmt`); everything else uses 2-space indentation.

Fast local gate (subset of CI):

```bash
./scripts/check.sh
```

Optional [pre-commit](https://pre-commit.com/) hooks (fmt/imports/vet/staticcheck + basic file hygiene):

```bash
pipx install pre-commit   # or: pip install pre-commit
go install golang.org/x/tools/cmd/goimports@latest
go install honnef.co/go/tools/cmd/staticcheck@v0.8.1
# ensure $(go env GOPATH)/bin is on PATH
pre-commit install
pre-commit run --all-files
```

CI remains the source of truth (`govulncheck`, `gosec` baseline, coverage, matrix tests).

## Makefile developer facade

The root `Makefile` is a thin convenience layer over the canonical Go, pnpm, and repository scripts. Run `make` or `make help` to list the supported developer targets.

Common workflows:

```bash
make bootstrap
make prepare
make check-embed
make check
make test
make test-race
make build
make run ARGS="status --json"
make up
make status
make logs ARGS="-f"
make tui
make frontend-dev
```

`make bootstrap` is the explicit dependency-install step. Ordinary `make prepare`, `run`, `up`, and `restart` reuse the existing frontend installation by default; override `PREPARE_ARGS` only when needed.

`make test` and `make test-race` always allocate a fresh `CM_CONFIG_DIR` and remove it after the test command. The Makefile intentionally has no CI, release/publish, or destructive clean target.

The direct commands below remain the underlying debugging interface.

## Install frontend dependencies

```bash
pnpm --dir frontend install --frozen-lockfile
```

## Frontend checks

```bash
pnpm --dir frontend test
pnpm --dir frontend lint
pnpm --dir frontend typecheck
pnpm --dir frontend build
```

## Prepare the embedded frontend

The Go binary embeds the built admin dashboard. The prepare script installs frontend dependencies with the frozen lockfile, builds the Admin UI, then copies `frontend/dist` into `internal/interface/web/dist`:

```bash
node scripts/prepare-frontend-embed.mjs
```

Use `--no-deps` to reuse the current frontend installation, or `--from-dist` to copy an already-built `frontend/dist` without running install/build. Use `--check` for a read-only verification that `frontend/dist` and `internal/interface/web/dist` are identical.

## Backend checks

Verify modules:

```bash
go mod verify
```

Run tests with an explicit isolated config root:

```bash
CM_CONFIG_DIR="$(mktemp -d)" go test ./...
```

Race detector:

```bash
CM_CONFIG_DIR="$(mktemp -d)" go test -race ./...
```

Vet:

```bash
go vet ./...
```

Build:

```bash
go build -trimpath ./
```

## Config isolation is a test invariant

Tests and smoke tests must not use the real default/global config directory.

Use either:

```text
--config-dir <isolated-temp>
```

or:

```text
CM_CONFIG_DIR=<isolated-temp>
```

The release smoke also creates a default-root sentinel and verifies that the selected test flow does not mutate it.

This rule applies especially to commands such as:

- `init`
- `uninit`
- `config set`
- `config convert`
- workspace registration/access changes
- tunnel configuration
- managed runtime tests

## Local source install

```bash
node scripts/install-local.mjs
```

The script prepares the frontend embed and runs the local Go installation flow.

Variants:

```bash
node scripts/install-local.mjs --no-deps
node scripts/install-local.mjs --from-dist
```

Managed installs expose only the `cm` executable.

## Release smoke

Build a native binary:

```bash
node scripts/prepare-frontend-embed.mjs
go build -trimpath -o cm ./
```

Run:

```bash
node scripts/smoke-release.mjs ./cm
```

The portable smoke verifies behavior such as:

- managed direct self-install and idempotent reinstall
- repeated managed install preserves the canonical `cm` command without creating legacy executable aliases
- `cm tui --help` is present and documents the Command Center
- `cm tui` refuses redirected/non-TTY execution without writing a full-screen UI
- stable workspace/request/upstream plain and JSON CLI output remains usable outside the TUI
- isolated init/uninit
- config verify/convert/transform
- config/status commands
- live config reload
- listener rebind and failed-bind rollback
- foreground runtime health
- managed runtime metadata through the portable hidden service entrypoint
- persistent runtime log replay/filter/follow/clear
- MCP discovery and tool listing, including workspace-container Agent tools
- live workspace-registry read-after-write consistency across CLI mutations: workspace register/unregister, access add/remove, container create/rename/delete, and membership add/remove without runtime restart or MCP reconnect
- integration coverage for Admin, TUI, and MCP workspace-registration mutations verifies each persistent registry change reloads runtime state before success is reported
- modern MCP error behavior
- clean stop/shutdown

Updater-specific native tests additionally verify:

- HTTPS archive/checksum download through the real downloader into managed activation
- checksum mismatch never stages or activates the target version
- same/latest no-op and explicit downgrade behavior
- install-method policy for direct/Homebrew/Scoop/Go/development/standalone installs
- managed restart/readiness, foreground preservation, and `--no-restart`
- automatic rollback plus previous-runtime restart on failed readiness

Control-approval native smoke additionally verifies:

- challenge creation requires a real approvable guard failure
- session/workspace binding and fake-challenge rejection
- challenge/request/retry/capability expiry behavior
- exact retry succeeds once; replay fails
- mismatch does not consume the valid grant/capability
- deny/cancel/expiry lifecycle
- hard-deny guards remain non-approvable
- MCP tool context cannot self-approve through `cm request approve/deny`
- CLI plain/JSON behavior and non-TTY safety
- Admin loopback and remote-auth policy

The portable runtime smoke also checks that `request_control_approval` is present in the MCP catalog and that `cm request list` reaches the running runtime. A dedicated native TUI release gate exercises route parsing, non-TTY refusal, Commands/resource navigation model integration, and public-command capability parity without attempting to drive a real alternate-screen terminal session inside CI.

Updater and control-approval integration gates run in every native Linux, macOS, and Windows CI/release job. Cross-build jobs continue to compile all six release OS/architecture targets.

## Cross-platform builds

Release targets:

```text
linux/amd64
linux/arm64
darwin/amd64
darwin/arm64
windows/amd64
windows/arm64
```

Example compile checks:

```bash
GOOS=linux GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...
```

Do not attempt to execute a cross-compiled test binary on the host OS; use native CI jobs for runtime tests and `go build` for cross-platform compile validation.

## CI

Pushes to `main` and pull requests run:

### Frontend checks

- test
- lint
- typecheck
- production build
- frontend artifact upload for native/cross-build jobs

### Native Linux

- installer validation
- module verification
- local install smoke
- managed install smoke
- control approval smoke
- TUI route/model/parity release gate
- Go tests
- race detector
- vet
- native build
- runtime/MCP smoke

### Native macOS

- Unix installer validation
- module verification
- local managed-install smoke
- control approval smoke
- TUI route/model/parity release gate
- Go tests
- vet
- native build
- runtime smoke

### Native Windows

- PowerShell installer validation
- module verification
- local managed-install smoke
- control approval smoke
- TUI route/model/parity release gate
- Go tests
- vet
- native build
- runtime smoke

### Cross-build matrix

All six release targets are compiled after the native/frontend prerequisites are available.

## Installer model

Unix installer layout uses immutable versions and stable current/command links:

```text
~/.cm/versions/<version>/...
~/.cm/current -> selected version
~/.local/bin/cm -> stable current path
```

Windows uses versioned directories plus a stable `current` directory junction so upgrades do not overwrite the executable currently held open by a managed runtime.

Managed service definitions therefore keep a stable launcher path instead of pinning one version-specific executable.

Direct updates stage an immutable target version, verify the downloaded release checksum before activation, switch `current`, and keep the previous version until runtime readiness succeeds. The install-global passive update cache lives at:

```text
<install-root>/state/update.json
```

Normal commands only read a fresh cache; explicit update checks bypass it and query the release source.

## Release workflow

Releases are produced by GoReleaser after release-native checks pass.

The release archive contains the standalone binary with the embedded admin dashboard plus release metadata/files configured by GoReleaser.

GoReleaser also produces package-manager manifests used by:

- `mewisme/scoop-mew`
- `mewisme/homebrew-mew`

The release workflow can dispatch package synchronization using the repository `PACKAGE_SYNC_TOKEN`. If the secret is not configured, release publishing can still succeed while package synchronization is skipped/warned according to workflow behavior.

## Tagging a release

After `main` is clean and CI is green:

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

Use the next semantic version appropriate for the release instead of copying this example unchanged.

## Useful checks before committing

```bash
git diff --check
CM_CONFIG_DIR="$(mktemp -d)" go test ./...
go vet ./...
pnpm --dir frontend test
pnpm --dir frontend lint
pnpm --dir frontend typecheck
pnpm --dir frontend build
```

For changes affecting service behavior, tunnel connectivity, runtime logs, configuration, or MCP protocol behavior, also run the release smoke.
