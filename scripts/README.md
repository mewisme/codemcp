# Repository automation

The root `Makefile` is the public developer facade. Files in this directory are implementation details grouped by their canonical owner; CI may call them directly when platform-specific orchestration requires it.

| Directory | Owner | Retained purpose |
| --- | --- | --- |
| `dev/` | Developer facade implementation | Fast local checks and cross-platform source installation behind `make check` and `make install-local`. |
| `ci/security/` | CI security gate | Run gosec, compare the reviewed baseline, and intentionally regenerate it through `make security-baseline`. |
| `generate/` | Generator | Produce committed product-presentation metadata behind `make generate` / `make check-generated`. |
| `installer/` | Installer test subsystem | Native shell/PowerShell installer tests plus release-layout and malicious-archive fixture generators. |
| `release/` | Release boundary | Thin release verification entrypoint, Windows setup build/payload helpers, and the built-binary cross-process smoke. |

`internal/releaseverify` owns reusable release verification logic. The command under `release/verify` only parses flags and delegates to that package.

`install.sh` and `install.ps1` live at the repository root intentionally: they are public bootstrap product artifacts, not repository helper scripts.

Frontend-only development, tests, coverage, lint, typechecking, formatting, end-to-end tests, and mini-app development remain owned by `frontend/package.json`.

## Retained helper inventory

| Helper | Explicit caller / maintenance purpose |
| --- | --- |
| `dev/check.sh` | `make check`; fast local gate and manual pre-commit hook. |
| `dev/install-local.mjs` | `make install-local`; CI exercises the same target with `--skip-frontend`. |
| `ci/security/gosec-baseline.mjs` | `make security-gosec` in CI; `make security-baseline` is the reviewed regeneration path. |
| `ci/security/gosec-baseline.json` | Reviewed data consumed by the gosec baseline gate. |
| `generate/product-presentation/main.go` | `go generate ./internal/productadapter`, `make generate`, and the CI drift gate. |
| `installer/test-unix.sh` | Native Unix CI and `make check`; covers installer verification and malicious archives. |
| `installer/test-powershell.ps1` | Native Windows CI for both PowerShell editions. |
| `installer/test-windows-setup.ps1` | Native Windows Inno Setup compile/execute smoke for managed-direct installation, PATH ownership, and delegated failure handling. |
| `installer/release-layout-contract/main.go` | Shared installer-test lookup used by Unix and PowerShell installer tests. |
| `installer/archive-fixture/main.go` | Generates valid and malicious archive fixtures for installer security tests. |
| `release/verify/main.go` | Thin canonical release verifier used by CI and tagged release workflows. |
| `release/install-inno-setup.ps1` | Install and verify the pinned Inno Setup compiler used by Windows CI and tagged releases. |
| `release/build-windows-setup.ps1` | Compile the native Windows setup from canonical `cm.exe` and emit its payload SHA-256 provenance record. |
| `release/verify-windows-setup-payload.sh` | Verify staged setup payload provenance against the canonical GoReleaser Windows archive before publication. |
| `release/smoke.mjs` | `make release-smoke` and native CI; cross-process built-binary/runtime behavior only. |
