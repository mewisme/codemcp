# Admin UI (`frontend/`)

Embedded React admin dashboard for CodeMCP. Built with React, TypeScript, Vite, Tailwind CSS, and shadcn/ui; packaged into the Go binary with `go:embed`.

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

The Vite build writes directly to `internal/interface/web/dist`, which is the directory embedded by the Go package:

```bash
pnpm --dir frontend build
```

Install dependencies separately with `pnpm --dir frontend install --frozen-lockfile` when needed. There is no intermediate frontend distribution directory or copy/sync step.

Full backend/frontend workflow, CI gates, and release notes: [docs/development.md](../docs/development.md).

## Adding shadcn components

```bash
pnpm --dir frontend dlx shadcn@latest add button
```

UI primitives live under `src/components/ui/`.
