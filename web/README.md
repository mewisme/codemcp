# Admin UI (`web/`)

Embedded React admin dashboard for CodeMCP. Built with React, TypeScript, Vite, Tailwind CSS, and shadcn/ui; packaged into the Go binary via `scripts/prepare-web-embed.mjs`.

## Requirements

- Node.js 24+
- pnpm 11+

## Commands

```bash
pnpm --dir web install
pnpm --dir web test
pnpm --dir web lint
pnpm --dir web typecheck
pnpm --dir web build
```

Local Vite dev server:

```bash
pnpm --dir web dev
```

## Embedding into the Go binary

From the repository root:

```bash
node scripts/prepare-web-embed.mjs
```

This installs frontend dependencies (frozen lockfile), builds the UI, and copies `web/dist` into `internal/web/dist` for `go:embed`. Use `--no-deps` to skip install, or `--from-dist` to copy an already-built `web/dist`.

Full backend/frontend workflow, CI gates, and release notes: [docs/development.md](../docs/development.md).

## Adding shadcn components

```bash
pnpm --dir web dlx shadcn@latest add button
```

UI primitives live under `src/components/ui/`.
