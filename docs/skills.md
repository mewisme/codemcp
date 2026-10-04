# Skills

CodeMCP uses filesystem-backed Agent Skills as the runtime source of truth. Managed GitHub installs and locally authored skills both become ordinary native skills; source metadata is used only for lifecycle operations such as `info` and `update`.

## Scope and storage

Workspace skills are the default:

```text
<workspace>/.cm/skills/<skill-name>/SKILL.md
```

Global skills use the active CodeMCP config root:

```text
<config-root>/skills/<skill-name>/SKILL.md
```

Normally the global root is `~/.cm/skills`. `CM_CONFIG_DIR` and `--config-dir` change the config root consistently.

Workspace-native skills take precedence over same-name global skills. Provider skills and built-in guidance remain part of the effective inventory with their existing precedence and read-only behavior.

## Manage GitHub skills

```bash
cm skills list
cm skills info <name>

cm skills add owner/repo
cm skills add https://github.com/owner/repo
cm skills add https://github.com/owner/repo.git
cm skills add git@github.com:owner/repo.git

cm skills update <name>
cm skills update --all
cm skills remove <name>
```

Mutations target the current registered workspace by default. Use `-w/--workspace` explicitly for workspace scope or `-g/--global` for global scope; the flags are mutually exclusive.

Repositories with multiple discovered skills require `--skill <name>` or `--all`. Repository discovery prefers standard skill catalog/container locations. Use `--full-depth` with `skills add` when a repository intentionally keeps nested skills outside the default discovery set.

Only the four GitHub source forms shown above are accepted. Local paths, archives, arbitrary URLs, generic Git hosts, package registries, and provider-directory sync are not part of this command surface.

## Agent Skills compatibility

A native skill uses an exact uppercase `SKILL.md` manifest. `name` and `description` are required. Names use lowercase letters, digits, and hyphens, are at most 64 characters, cannot start or end with a hyphen, and cannot contain consecutive hyphens.

Descriptions may use YAML string forms, including folded or block values, up to 1024 Unicode characters. Standard optional fields such as `license`, `compatibility`, `metadata`, and `allowed-tools` are accepted. Imported skills may have an empty Markdown body and arbitrary supporting files within CodeMCP's managed-import safety limits.

CodeMCP does not execute downloaded skill scripts during installation or update.

## Runtime visibility

No registration or runtime restart is required after a successful skill filesystem mutation. Subsequent canonical discovery sees the new skill immediately.

The same inventory feeds:

- `cm skills list` and `cm skills info`;
- MCP `skills/list` and `skills/get`;
- the fallback `list_skills` and `load_skill` tools;
- instruction context;
- exact `/<skill-name>` activation.

Slash activation is exact, not fuzzy. A skill named `frontend-design` activates with `/frontend-design`; a partial token such as `/frontend` does not activate it.

## Managed lifecycle safety

Managed installs keep small source metadata beside the native skill store. That metadata records the canonical GitHub source, revision, repository path, and deterministic content hash, but it does not register skills or participate in runtime resolution.

`skills update` reads the recorded source, detects local drift before replacing a managed skill, and keeps the installed tree intact if acquisition, validation, relocation, or metadata commit fails. `skills update --all` groups repository acquisition by source while updating skills deterministically.

`skills remove` can remove managed native skills and explicitly targeted unmanaged native skills in the selected scope. Provider and built-in skills remain read-only.

Generic filesystem mutation tools cannot bypass CodeMCP's native instruction-authoring boundary for `.cm/skills`; use the canonical authoring or skill-management operations for mutations.
