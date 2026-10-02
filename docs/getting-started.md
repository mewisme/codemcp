# Getting started

This is the default `CodeMCP` path: install one local runtime, register the projects ChatGPT may use, connect it through OpenAI Secure MCP Tunnel, and keep the runtime running as a managed service.

```text
install → init → register workspace → configure tunnel → cm up → connect ChatGPT
```

## Requirements

- Linux, macOS, or Windows on `amd64` or `arm64`.
- A normal user account. The MCP runtime should not run as root.
- For ChatGPT: an OpenAI Secure MCP Tunnel and a restricted runtime API key with **Tunnels Read + Use**.
- Outbound HTTPS to OpenAI. The default ChatGPT setup does **not** require a public inbound MCP port.

Docker is not required. Git is optional unless you want to use Git tools.

## 1. Install

Choose one installation owner and keep using that owner for upgrades.

### Managed direct install

This is the recommended developer-friendly path. It installs immutable versions under `~/.cm`, exposes a stable `cm` command, and lets `cm upgrade` stage, verify, activate, and roll back updates.

### Linux / macOS

```bash
curl -fsSL get.mewis.me/codemcp.sh | sh
```

### Windows PowerShell

```powershell
irm https://get.mewis.me/codemcp.ps1 | iex
```

On Windows, the native setup executable is an alternative bootstrap into the **same** managed direct layout. It does not create a separate Program Files installation:

- [latest Windows amd64 setup](https://github.com/mewisme/codemcp/releases/latest/download/codemcp_windows_amd64_setup.exe)
- [latest Windows arm64 setup](https://github.com/mewisme/codemcp/releases/latest/download/codemcp_windows_arm64_setup.exe)

The setup runs the embedded `cm.exe install` flow and, for the default layout, adds `%USERPROFILE%\.cm\current` to the current user's `PATH`.

### Homebrew and Scoop

These installations remain owned by their package managers. Their generated manifests resolve stable artifact filenames through an exact release tag rather than a moving `latest` asset.

```bash
brew tap mewisme/mew
brew install --cask codemcp
```

```powershell
scoop bucket add mew https://github.com/mewisme/scoop-mew
scoop install mew/codemcp
```

### Native Linux packages

CodeMCP publishes Debian and RPM packages as GitHub Release assets; there is no CodeMCP APT or YUM repository. Download the package for your architecture, then install that local file with your normal system package tool.

| Format | amd64 | arm64 |
| --- | --- | --- |
| Debian | [`codemcp_linux_amd64.deb`](https://github.com/mewisme/codemcp/releases/latest/download/codemcp_linux_amd64.deb) | [`codemcp_linux_arm64.deb`](https://github.com/mewisme/codemcp/releases/latest/download/codemcp_linux_arm64.deb) |
| RPM | [`codemcp_linux_amd64.rpm`](https://github.com/mewisme/codemcp/releases/latest/download/codemcp_linux_amd64.rpm) | [`codemcp_linux_arm64.rpm`](https://github.com/mewisme/codemcp/releases/latest/download/codemcp_linux_arm64.rpm) |

For example, after downloading the matching asset:

```bash
sudo apt install ./codemcp_linux_amd64.deb
# or
sudo dnf install ./codemcp_linux_amd64.rpm
```

Use the `arm64` filename on ARM64 systems. These packages install the package-owned system binary `/usr/bin/cm`. Removing the package does not remove your per-user CodeMCP config, registered workspaces, or workspace-local state.

### Manual release downloads

Published filenames are stable; the Git tag is the release-version identity.

| Platform | amd64 | arm64 |
| --- | --- | --- |
| Linux archive | `codemcp_linux_amd64.tar.gz` | `codemcp_linux_arm64.tar.gz` |
| macOS archive | `codemcp_darwin_amd64.tar.gz` | `codemcp_darwin_arm64.tar.gz` |
| Windows archive | `codemcp_windows_amd64.zip` | `codemcp_windows_arm64.zip` |
| Debian package | `codemcp_linux_amd64.deb` | `codemcp_linux_arm64.deb` |
| RPM package | `codemcp_linux_amd64.rpm` | `codemcp_linux_arm64.rpm` |
| Windows setup | `codemcp_windows_amd64_setup.exe` | `codemcp_windows_arm64_setup.exe` |

Use `releases/latest/download/<filename>` only when you intentionally want the current release. For an exact version, pin the tag while keeping the same stable filename, for example:

```text
https://github.com/mewisme/codemcp/releases/download/vX.Y.Z/codemcp_linux_amd64.tar.gz
```

All executable/package/setup release assets are covered by `codemcp_checksums.txt`, whose published Sigstore bundle is `codemcp_checksums.txt.sigstore.json`.

The installed executable is `cm`.

## 2. Initialize

```bash
cm init
```

The default config/state root is:

```text
~/.cm/
```

For isolated instances, tests, or development runs, select another root with `--config-dir` or `CM_CONFIG_DIR`. See [Configuration](configuration.md#config-root).

## 3. Register a workspace

Register only project roots you want ChatGPT to reach:

```bash
cm workspace register ~/projects/my-project
```

The command returns a stable `ws_*` workspace ID and records its ownership marker in `<workspace>/.cm/workspace.json`. Filesystem, shell, Git, process, context, memory, rules, skills, and checkpoint operations use explicit workspace targets.

```bash
cm workspace list
```

Read [Workspaces](workspaces.md) before adding extra filesystem roots or using workspace containers.

## 4. Configure OpenAI Secure MCP Tunnel

Create a tunnel in OpenAI Platform and a restricted runtime API key with **Tunnels Read + Use**, then configure them locally:

```bash
cm tunnel configure --enabled --id tunnel_...
cm tunnel key set
```

Check the local configuration:

```bash
cm tunnel status
```

The tunnel ID is an identifier. The runtime API key is a secret used only to authenticate the tunnel client; do not use a Platform Admin API key as the long-lived runtime key.

For tunnel creation, associations, permissions, Developer Mode, and ChatGPT app setup, follow [OpenAI + ChatGPT](openai-chatgpt.md).

## 5. Start the runtime

For normal use, start the managed background runtime:

```bash
cm up
```

Inspect it:

```bash
cm status
cm tunnel status
```

For one-off foreground testing, use:

```bash
cm serve
```

`serve` stays attached to the current terminal. On a remote server, prefer a managed service instead of relying on an SSH session. See [Runtime and operations](runtime.md).

## 6. Connect ChatGPT

In ChatGPT:

1. Enable Developer Mode if required for your workspace/account.
2. Create a custom app.
3. Choose **Tunnel** as the connection type.
4. Select the same `tunnel_...` configured locally.
5. Run **Scan Tools**.
6. Review the discovered tools and create/enable the app.

Then test with a read-only action such as listing registered workspaces or reading runtime status.

## 7. Verify and operate

Useful commands:

```bash
cm status
cm tunnel status
cm logs -f
cm config verify
```

For interactive operation:

```bash
cm tui
```

## Next steps

- [OpenAI + ChatGPT](openai-chatgpt.md) — complete tunnel/app setup
- [Workspaces](workspaces.md) — scope, extra roots, relocation, containers
- [Runtime and operations](runtime.md) — services, logs, updates
- [TUI Command Center](tui.md) — interactive operation
- [Security](security.md) — trust boundaries and recommended posture
- [Configuration](configuration.md) — config roots, auth, exposure, storage
- [MCP and upstreams](mcp.md) — generic MCP clients and upstream servers

## Advanced installation and development

For update behavior after installation, including package-manager ownership, see [Runtime and operations](runtime.md#updates). Source builds, release verification, and CI details are in [Development](development.md).
