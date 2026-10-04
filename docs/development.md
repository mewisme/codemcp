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

The Windows amd64 setup is compiled natively with the pinned Inno Setup toolchain on a Windows runner. It embeds the canonical release `cm.exe` only as a temporary bootstrap, delegates durable installation to `cm install`, and is staged back into the GoReleaser release so the same checksum/signature authority covers it. The setup does not own a Program Files tree, Add/Remove Programs entry, updater, or independent uninstall path. Repository builds do not require Authenticode signing or an Inno license secret.

## ChatGPT Web live smoke

The browser-backed ChatGPT integration has an opt-in live smoke for maintainers. Never run it in CI and never point it at the default CodeMCP state root. Use a dedicated temporary `CM_CONFIG_DIR`, configure the browser, ChatGPT Web integration, and Secure MCP Tunnel there, then run:

```bash
export CM_CONFIG_DIR="$(mktemp -d)"
go run . integration chatgpt-web login
```

Complete authentication in the plain CodeMCP-owned browser window, including any identity-provider or CAPTCHA interaction, then close that browser completely. A successful handoff reopens the same isolated profile under managed private CDP, verifies the server-authenticated Temporary Chat composer, prints only the allowlisted account name/email when available, and closes the one-shot verification browser cleanly. The account summary is not written to the auth marker or shown by later status/doctor commands. Setting `integrations.browser.headless=true` makes this managed verification headless, but the preceding interactive login remains visible by design.

After login, use an MCP client connected to the same temporary CodeMCP state to spawn one `chatgpt-web` agent, then a second concurrent `chatgpt-web` agent. Confirm the first managed lease reuses the bootstrap page, the second agent adds exactly one sibling page, and completing either agent does not disconnect the other. The local browser fixture smoke validates both visible and headless managed tab lifecycle without a ChatGPT account:

```bash
CM_CONFIG_DIR="$(mktemp -d)" CM_BROWSER_SMOKE=1 go test ./internal/integrations/browser -run TestLocalBrowserManagerSmoke -count=1 -v
```

Classify live failures before changing the browser architecture:

- **unauthenticated session** — `/api/auth/session` does not prove a valid current user; rerun interactive login;
- **login redirect** — managed verification returns to a sign-in route; rerun interactive login and close the browser only after sign-in finishes;
- **challenge** — managed verification encounters a browser challenge that cannot reach the verified Temporary Chat composer; fail closed and never automate or bypass the challenge;
- **connector unavailable** — authentication is valid but the configured Secure MCP Tunnel route is unavailable; repair connector configuration without repeating login unless auth also failed;
- **UI drift** — the authenticated page loads but the expected Temporary Chat/composer contract is absent; update the ChatGPT Web adapter after confirming the upstream UI change.

Do not upload the temporary browser profile, auth marker, session data, screenshots containing account identity, or live prompt/response content as CI artifacts.
