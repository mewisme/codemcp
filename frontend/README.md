# Frontend (`frontend/`)

Embedded React interfaces for CodeMCP. The Browser Admin and Telegram Mini App share the same React, TypeScript, Tailwind CSS and shadcn/ui component layer while keeping separate HTML/Vite entrypoints and embedded asset bundles. Logs are the first Mini App surface, not the identity of the bundle itself.

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

Telegram Mini App development entrypoint:

```bash
pnpm --dir frontend dev:mini-app
```

## Embedding into the Go binary

The default Vite config writes Browser Admin directly to `internal/interface/web/dist`. `vite.mini-app.config.ts` writes the dedicated Telegram Mini App bundle to `internal/telegram/mini-app-dist`. The Browser Admin package and Telegram adapter embed their respective generated assets with `go:embed`:

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
