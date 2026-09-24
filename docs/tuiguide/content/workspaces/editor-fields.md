# Workspace Editor Fields

## Register Workspace

### Workspace path

Required directory path that becomes the registered workspace root. The field is directory-picker aware and validates that a directory value is supplied. Registration stores the workspace identity/access metadata; it does not modify project files.

## Relocate Workspace

### New workspace path

Required directory path for the same project after its directory has already been renamed or moved. `Enter` on this final field performs the relocate action. It preserves the stable workspace ID and requires the destination to contain the same local `.cm/workspace.json` identity. It never moves or renames project files itself.

## Add Access Directory

### Additional directory

Required directory path granted as an additional filesystem root for the current workspace. It extends the workspace's accessible roots without changing the workspace root itself.

## Remove Access Directory

### Directory to remove

Select field populated from the workspace's currently configured additional access directories. The selected directory is revoked from the workspace when the editor is saved.

## Create Workspace Container

### Container name

Required human-readable name for a new workspace container. Containers group registered workspaces; creating a container does not itself register or move workspace files.

## Rename Workspace Container

### Container name

Required replacement display name for the existing container. The container identity remains the routed resource; only its name changes.

## Container membership

Membership editing uses the workspace/container routes to select which registered workspaces belong to a container. Removing membership does not unregister the workspace or delete project files.

All successful workspace-registry editor mutations reload the running runtime registry before the TUI reports success. Agent workspace and container tools therefore see register, relocate, access, container, and membership changes immediately without a runtime restart. Relocate is intentionally a user/control-plane operation and is not available as an MCP tool.
