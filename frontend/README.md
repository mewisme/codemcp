# Admin UI (`frontend/`)

Embedded React admin dashboard for CodeMCP. Built with React, TypeScript, Vite, Tailwind CSS, and shadcn/ui; packaged into the Go binary via `scripts/prepare-frontend-embed.mjs`.

## Requirements

- Node.js 24+
- pnpm 11+

## Commands

```bash
pnpm --dir frontend install
pnpm --dir frontend test
pnpm --dir frontend lint
pnpm --dir frontend typecheck
pnpm --dir frontend build
```

Local Vite dev server:

```bash
pnpm --dir frontend dev
```

## Embedding into the Go binary

From the repository root:

```bash
node scripts/prepare-frontend-embed.mjs
```

This installs frontend dependencies (frozen lockfile), builds the UI, and copies `frontend/dist` into `internal/interface/web/dist` for `go:embed`. Use `--no-deps` to skip install, or `--from-dist` to copy an already-built `frontend/dist`.

Full backend/frontend workflow, CI gates, and release notes: [docs/development.md](../docs/development.md).

## Adding shadcn components

```bash
pnpm --dir frontend dlx shadcn@latest add button
```

UI primitives live under `src/components/ui/`.
