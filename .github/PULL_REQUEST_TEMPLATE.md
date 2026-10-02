## Summary

<!-- What changed and why. Link related issues. -->

## Test plan

- [ ] `make check` (or equivalent local gate)
- [ ] `CM_CONFIG_DIR="$(mktemp -d)" go test ./...` when Go code changed
- [ ] `pnpm --dir frontend test` / `lint` / `typecheck` when Admin UI changed
- [ ] Docs updated if behavior or UX changed
- [ ] Release smoke (services / tunnel / MCP / config) when those areas changed — see [docs/development.md](../docs/development.md)

## Notes

<!-- Optional: screenshots, migration notes, follow-ups. -->
