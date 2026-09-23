# Workspaces

Workspaces define project roots that `CodeMCP` can operate on. The Workspaces area also owns workspace containers and the Project Context builder.

## Workspace list and details

The main Workspaces tab lists registered roots. Open a row with `Enter` to view its ID, path, access configuration, and child actions. Registering a workspace opens a full-page editor with a directory picker and manual path fallback.

If the project directory has already been renamed or moved, press `m` from the workspace detail (or choose **Relocate** from Commands). Relocate opens a routed full-page directory editor. Pressing `Enter` on its final field rebinds the existing workspace to that directory; it does not move project files. The path-derived canonical workspace ID changes and the previous ID remains a legacy alias. Container membership and workspace-scoped persistent state follow the new ID.

Unregister removes the workspace record without deleting project files and therefore requires confirmation.

## Additional access directories

Each workspace has an Access child page for directories outside the workspace root that have been explicitly granted to that workspace. Add/Remove are routed editors. Paths are validated against the same workspace access model used by tools; the TUI does not create a separate permission system.

## Containers

The Containers tab groups registered workspaces without duplicating them. A container has its own ID, name, and membership list. Create and rename are full-page editors. Membership editing uses a compact filterable picker that shows workspace names/IDs without leaking unnecessarily long absolute paths into every option.

Deleting a container does not unregister or delete the member workspaces.

## Project Context builder

From a workspace detail, open **Project Context** to configure and build the context that would be supplied for that workspace. The editor is divided into:

- **Scope** — optional relative path/scope selection.
- **Budgets** — limits controlling instruction and memory/context loading.
- **Include** — persistent switches for sources such as Git, memory, and skills.

Complete the final **Include** field with `Enter` to start the build. The build is asynchronous and cancellable. An unsuccessful build keeps the draft and displays feedback instead of replacing the previous preview.

## Project Context preview

A successful build opens the volatile preview session. The preview has `Rendered / Sources / JSON` tabs:

- **Rendered** shows the composed instruction/context result.
- **Sources** shows the source tree, including project/user instruction files, Global Context, rules, auto memory, skills, and detected providers.
- **JSON** shows the structured build result. JSON is wrapped to terminal width and does not use horizontal scrolling.

Opening a source node displays the loaded source content. Long paths wrap, including platform-specific temporary paths on macOS and Windows.

The preview is session-local to the TUI. Reopening a fresh Project Context session does not silently reuse an unrelated previous build.
