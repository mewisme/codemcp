# Development

This guide covers source builds, verification, CI, release smoke tests, and release workflow expectations.

For how to propose changes, open issues/PRs, and community norms, start with [CONTRIBUTING.md](../CONTRIBUTING.md).

## Requirements

- Go 1.27+
- Node.js 24+
- pnpm 11+

Tagged release jobs additionally use GoReleaser OSS `v2.18.0`, Cosign, nFPM through GoReleaser, and the OSS NSIS/7-Zip toolchain used to build and inspect Windows setup executables. The pinned Linux release runner installs NSIS `3.09-4ubuntu1` and 7-Zip `23.01+dfsg-11`; the release path uses OSS-only GoReleaser features.

## Editor and local quality checks

`.editorconfig` sets **indent size 2** for all files. Go uses tabs at width 2 (required by `gofmt`); everything else uses 2-space indentation.

Fast local gate (subset of CI):

```bash
make check
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

The root `Makefile` is the developer command facade over the canonical Go, pnpm, and repository automation. Run `make` or `make help` to list the supported targets; helper paths under `scripts/` are implementation details for maintainers and CI.

Common workflows:

```bash
make bootstrap
make frontend-build
make check
make test
make test-race
make build
make generate
make check-generated
make install-local
make release-smoke
make security-gosec
make init
make uninit
make run ARGS="status --json"
make up
make status
make logs ARGS="-f"
make tui
make frontend-dev
```

`make bootstrap` is the explicit dependency-install step. `make frontend-build`, `run`, `up`, `restart`, and `serve` build the frontend using the existing installation.

`make init` and `make uninit` delegate directly to the canonical `cm` commands. `make init` does not add `--force`; pass it explicitly through `ARGS` only when token rotation is intended.

`make test` and `make test-race` always allocate a fresh `CM_CONFIG_DIR` and remove it after the test command. `make generate` owns committed product-presentation generation and `make check-generated` is the corresponding drift gate. `make security-gosec` checks the canonical security baseline; maintainers may intentionally regenerate that baseline with `make security-baseline` after reviewing the delta. The Makefile intentionally has no release/publish or destructive clean target.

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

## Build the embedded frontend

The Go binary embeds the built admin dashboard from `internal/interface/web/dist`. Vite writes directly to that directory:

```bash
pnpm --dir frontend build
```

There is no intermediate copy/sync step. Install dependencies separately with `pnpm --dir frontend install --frozen-lockfile` when needed.

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
- `config import`
- workspace registration/access changes
- tunnel configuration
- managed runtime tests

## Local source install

```bash
make install-local
```

The target builds the frontend directly into the embedded asset directory and runs the local Go installation flow.

Variants:

```bash
make install-local ARGS="--no-deps"
make install-local ARGS="--skip-frontend"
```

`--skip-frontend` requires an existing `internal/interface/web/dist/index.html` and is useful when those embedded assets were already built.

Managed installs expose only the `cm` executable.

## Release smoke

Build the canonical local binary:

```bash
make build
```

Run:

```bash
make release-smoke
```

Override `BINARY` when testing another built artifact, for example `make release-smoke BINARY=./dist-smoke/cm`.

The portable smoke intentionally focuses on built-binary and cross-process behavior such as:

- managed direct self-install and idempotent reinstall
- repeated managed install preserves the canonical `cm` command without creating legacy executable aliases
- `cm tui --help` is present and documents the Command Center
- `cm tui` refuses redirected/non-TTY execution without writing a full-screen UI
- isolated init/uninit
- config verify/export/import
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

Pure CLI rendering and JSON-shape contracts are owned by Go tests rather than duplicated in the release smoke.

## Automation ownership

Normal development uses the Makefile plus frontend-specific `pnpm --dir frontend ...` commands. The retained repository helpers are grouped by purpose under `scripts/`; see [scripts/README.md](../scripts/README.md) for the ownership map. Root `install.sh` and `install.ps1` remain public bootstrap product artifacts rather than developer helper scripts.

## Repository YAML files

YAML files committed to the repository are tooling or repository metadata, not CodeMCP runtime persistence. This includes GitHub workflows and issue templates, Dependabot, GoReleaser and pre-commit configuration, plus pnpm lock/workspace metadata. CodeMCP-owned runtime machine state uses JSON or JSONL only; released YAML/TOML state is handled only by migration readers.

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

The portable runtime smoke also checks that `request_control_approval` is present in the MCP catalog through a real MCP session. A dedicated native TUI release gate exercises route parsing, non-TTY refusal, Commands/resource navigation model integration, and public-command capability parity without attempting to drive a real alternate-screen terminal session inside CI.

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

Releases are produced by GoReleaser OSS after release-native checks pass. Release tags carry the version; published executable/package/setup filenames are intentionally versionless.

The canonical release matrix is:

| Kind | amd64 | arm64 |
| --- | --- | --- |
| Linux archive | `codemcp_linux_amd64.tar.gz` | `codemcp_linux_arm64.tar.gz` |
| macOS archive | `codemcp_darwin_amd64.tar.gz` | `codemcp_darwin_arm64.tar.gz` |
| Windows archive | `codemcp_windows_amd64.zip` | `codemcp_windows_arm64.zip` |
| Debian package | `codemcp_linux_amd64.deb` | `codemcp_linux_arm64.deb` |
| RPM package | `codemcp_linux_amd64.rpm` | `codemcp_linux_arm64.rpm` |
| Windows setup | `codemcp_windows_amd64_setup.exe` | `codemcp_windows_arm64_setup.exe` |

Archives contain the standalone binary with the embedded admin dashboard plus the release files configured by GoReleaser. Debian/RPM packaging is produced by GoReleaser's OSS nFPM integration. The two NSIS setup executables are built externally from the corresponding GoReleaser Windows `cm.exe`, then attached back to the same checksum/release contract as extra files.

`codemcp_checksums.txt` must contain exactly one SHA-256 entry for every archive, Debian/RPM package, and Windows setup artifact above. GoReleaser signs that final checksum manifest once with Cosign, producing `codemcp_checksums.txt.sigstore.json`.

The tagged workflow publishes through a GitHub **draft** first. It verifies the exact artifact matrix, checksums, checksum signature, package manifests, tagged telemetry boundary, and byte identity between each NSIS-embedded `cm.exe` and its GoReleaser source binary before converting the draft into the public release. This prevents an unverified native artifact from becoming the published release.

GoReleaser also produces package-manager manifests used by:

- `mewisme/scoop-mew`
- `mewisme/homebrew-mew`

The release workflow can dispatch package synchronization using the repository `PACKAGE_SYNC_TOKEN`. If the secret is not configured, release publishing can still succeed while package synchronization is skipped/warned according to workflow behavior.

Homebrew and Scoop manifests use exact tag URLs with the stable artifact filenames. Do not replace those manifest URLs with `releases/latest/download/...`; package-manager manifests must stay reproducible for the version they describe.

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
