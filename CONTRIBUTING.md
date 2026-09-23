# Contributing to CodeMCP

Thanks for contributing. This guide covers how to propose changes; detailed build, test, CI, and release steps live in [docs/development.md](docs/development.md).

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Before you start

- Search [existing issues](https://github.com/mewisme/codemcp/issues) and PRs to avoid duplicates.
- For security vulnerabilities, do **not** open a public issue. Follow [SECURITY.md](SECURITY.md).
- Prefer a focused PR that does one thing well over a large mixed change.

## Development setup

Requirements:

- Go 1.27+
- Node.js 24+
- pnpm 11+

Quick path:

```bash
pnpm --dir web install
node scripts/prepare-web-embed.mjs
CM_CONFIG_DIR="$(mktemp -d)" go test ./...
go build -trimpath ./
```

Fast local gate (subset of CI):

```bash
./scripts/check.sh
```

Tests must never use the real default/global config directory. Always isolate with `CM_CONFIG_DIR` or `--config-dir`. See [docs/development.md](docs/development.md).

## Pull requests

1. Fork and branch from `main`.
2. Keep changes scoped; update docs when behavior or UX changes.
3. Fill out the PR template.
4. Ensure CI is green.

Suggested local checks before opening a PR:

```bash
./scripts/check.sh
CM_CONFIG_DIR="$(mktemp -d)" go test ./...
go vet ./...
pnpm --dir web test
pnpm --dir web lint
pnpm --dir web typecheck
pnpm --dir web build
```

For changes that affect services, tunnel connectivity, runtime logs, configuration, or MCP protocol behavior, also run the release smoke described in [docs/development.md](docs/development.md).

## Issues

Use the GitHub issue templates:

- **Bug report** — include OS, `cm version`, repro steps, expected vs actual
- **Feature request** — describe the problem, proposal, and alternatives

Questions about product security boundaries belong in discussion or docs issues; vulnerability reports belong in [SECURITY.md](SECURITY.md).

## Releases and changelog

Releases are cut from tags on `main` via GoReleaser. Release notes live on [GitHub Releases](https://github.com/mewisme/codemcp/releases); there is no separate root `CHANGELOG.md`.

## License

Contributions are licensed under the project [MIT License](LICENSE).
