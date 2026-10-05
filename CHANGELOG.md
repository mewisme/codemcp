# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Added managed ChatGPT Web agents with workspace claims, browser-backed temporary-chat execution, shared browser tabs, browser authentication, and fanout strategy control.
- Added dynamic tool capability discovery and effective capability projection.
- Added GitHub-backed skill discovery, installation, security review, management CLI, and direct-root skill discovery.

### Changed

- Made `CHANGELOG.md` the canonical source for GitHub Release descriptions; the release workflow now extracts the matching SemVer section and passes it to GoReleaser as explicit release notes instead of publishing generated commit-log summaries.
- Replaced the standalone approval tool with inline guarded retries carrying exact approval metadata.
- Redesigned Admin and Telegram operator surfaces.
- Unified CLI presentation ownership and lifecycle progress.

### Fixed

- Hardened managed restart readiness, runtime persistence, cleanup, startup error handling, and CodeGraph catch-up.
- Hardened managed Chrome lifecycle and WSL browser-host cleanup.

## [0.3.2] - 2026-10-03

### Added

- Ensure install integration assets.
- Add install integration opt out.
- Bind plan implementation phases.
- Guard agent completion with plan progress.
- Add telegram expandable content blocks.
- Add skill and rule slash activation.

### Changed

- Refine install progress output.
- Define install supplement bootstrap options.
- Group install supplement progress.
- Enforce canonical plan progress transitions.
- Update session key handling for plan execution and project context.
- Group long telegram content.
- Replace resolved telegram approvals.
- Define inno setup release contract.
- Replace nsis setup with inno.
- Stage inno setup in release workflow.
- Define slash workflow directives.
- Unify skill activation resolution.

### Fixed

- Enhance systemd ownership verification with alias support.
- Release completed plan bindings.
- Wait for completion hooks to exit on stop.
- Share canonical windows release inputs.

## [0.3.1] - 2026-10-03

### Fixed

- Migrate released production state safely.
- Harden migration file hashing.
- Fall back to fresh install on migration failure.
- Remove release binary packing.
- Preserve release binary during extraction.

## [0.3.0] - 2026-10-02

### Added

- Establish workspace local cm state.
- Harden workspace runtime ownership.
- Finalize workspace state lifecycle.
- Protect workspace local cm from git.
- Migrate workspace state into local cm.
- Migrate feature settings to integrations.
- Add rtk integration lifecycle.
- Add codegraph integration runtime.
- Add codegraph workspace lifecycle.
- Expose native codegraph exploration.
- Migrate upstream persisted state.
- Add cli terminal presentation capabilities.
- Add unified cli presenter.
- Add cli rail progress presentation.
- Add init and uninit commands to Makefile.
- Unify cli help presentation.
- Add universal config operations.
- Migrate released credentials to codemcp.
- Publish canonical approval lifecycle.
- Coordinate approval notifications.
- Broadcast approvals across interfaces.
- Add agent completion domain.
- Expose agent complete signal.
- Add agent completion hooks.
- Sync codegraph on agent completion.
- Notify accepted agent completion.
- Expose completion history across interfaces.
- Expose safe mcp config reads.
- Bind mcp config batches to local approval.
- Apply approved mcp config batches.
- Add openai mcp profile.
- Align openai mcp authentication.
- Publish background terminal lifecycle.
- Add background delivery broker.
- Dedupe background completion delivery.
- Add background continuation adapters.
- Expose truthful background capabilities.
- Expose background lifecycle diagnostics.
- Add mcp resource registry.
- Resolve duplicate workspace identities.
- Merge duplicate workspace local state safely.
- Add native instruction authoring.
- Expose native instruction authoring tools.
- Expose codemcp resources.
- Add mcp resource subscriptions.
- Add native prompt store.
- Expose mcp prompts.
- Expose compatible mcp skills.
- Add typesafe integration settings.
- Add semantic judgment primitives.
- Add bounded semantic runtime.
- Semantically rank project context.
- Semantically rerank memory search.
- Advise canonical approvals with semantic risk.
- Add product telemetry state model.
- Add bounded product telemetry transport.
- Add product telemetry administration.
- Instrument canonical product usage.
- Instrument safe telemetry lifecycle events.
- Add secure telegram runtime.
- Add telegram guided setup.
- Restore typesafe systemone capabilities.
- Implement structured configuration output and enhance presentation rendering.
- Archive retained checkpoint history.
- Improve checkpoint history management and enhance configuration output.
- Separate checkpoint clear and purge.
- Restore single tunnel connectivity behavior.
- Add aggregate diagnostic model.
- Add codemcp doctor command.
- Restore cli capability reachability.
- Restore admin api capability parity.
- Restore browser admin parity.
- Restore tui read capability parity.
- Restore tui action capability parity.
- Add vite-plugin-singlefile for improved build output.
- Add lazy tui log timelines.
- Restore telegram button ui parity.
- Add rich telegram interaction primitives.
- Add telegram private topic routing.
- Add telegram workspace approvals.
- Add telegram network administration.
- Add telegram settings integrations.
- Add telegram system administration.
- Add telegram logs mini app ingress.
- Embed telegram logs frontend.
- Add telegram logs mini app.
- Make telegram mini app more native.
- Add telegram completion experience.
- Add universal cm command aliases.
- Unify cli alias help completion.
- Align cross interface information depth.
- Detect released chatgpt mcp state.
- Stage codemcp state migration.
- Activate migrated codemcp state.
- Finalize codemcp install cutover.
- Define canonical llm provider domain.
- Persist llm provider registry.
- Add llm protocol adapters.
- Add openrouter llm provider.
- Add ollama local and cloud provider.
- Manage custom llm providers.
- Expose canonical llm operations.
- Explain approval requests with llm.
- Add llm cli administration.
- Add browser llm administration.
- Add rich llm model catalog queries.
- Add tui llm administration.
- Add telegram llm administration.
- Close browser admin semantic parity.
- Close telegram semantic parity.
- Implement model browser initialization and resize safety tests.
- Close tui semantic parity.
- Close cli semantic parity.
- Add ollama auto model access checks.
- Refine interface presentation and logs.
- Enhance command progress handling and improve nested field presentation.
- Add structured mutation input flows.
- Unify operator observability surfaces.
- Define canonical plan document.
- Persist workspace plans.
- Add canonical plan authoring.
- Expose create plan tool.
- Add agent plan mode directive.
- Surface workspace plan context.
- Delegate linux package managed upgrades.

### Changed

- Establish codemcp repository identity.
- Make cm the only executable.
- Canonicalize cm machine namespaces.
- Rebrand codemcp release artifacts.
- Establish codemcp domain layout.
- Rename web source to frontend.
- Add thin developer makefile.
- Define canonical operation identities.
- Centralize codemcp application operations.
- Make codemcp interfaces canonical adapters.
- Make codemcp config json only.
- Standardize codemcp state formats.
- Replace config bundles with json envelopes.
- Localize workspace owned state.
- Unify workspace context composition.
- Establish first party integrations.
- Centralize rtk command integration.
- Integrate codegraph with project context.
- Remove features and builtins ownership.
- Canonicalize upstream domain terminology.
- Centralize upstream administration.
- Harden upstream tool proxying.
- Centralize cli output modes.
- Render status through cli presenter.
- Migrate cli config auth request presentation.
- Adopt rail first cli read presentation.
- Normalize cli rail palette.
- Migrate cli resource presentation.
- Migrate cli lifecycle presentation.
- Unify codemcp setting operations.
- Map scoped settings to canonical operations.
- Separate configured and derived settings.
- Centralize transactional setting mutations.
- Unify approval review operations.
- Centralize cli command presentation lifecycle.
- Unify cli phase loading animation.
- Render actionable cli failures in workflow.
- Migrate cli commands to shared presentation lifecycle.
- Make mcp request handling stateless.
- Establish mcp core profiles.
- Apply dynamic setting batches atomically.
- Move secure mcp tunnel under openai profile.
- Define background no poll contract.
- Derive tasks from process lifecycle.
- Centralize mcp non tool features.
- Simplify managed service result layout.
- Normalize cli output hierarchy.
- Project native skills into mcp.
- Inject product telemetry endpoint.
- Map telegram to canonical operations.
- Update Makefile to streamline command definitions and improve help output.
- Enhance Makefile for improved command handling and help output.
- Remove admin mutation ownership.
- Adopt telegram bot api transport.
- Rename telegram frontend to mini app.
- Enhance logs page execution clear handling and help view integration.
- Navigate mini app details as child pages.
- Secure cli alias canonicalization.
- Normalize cross interface operation semantics.
- Unify interface realtime feeds.
- Retire released executable identities.
- Verify codemcp release cutover.
- Isolate legacy migration compatibility.
- Enforce strict four interface parity.
- Remove openrouter provider and telegram user picker.
- Unify local http configuration.
- Normalize interface ux semantics.
- Replace bounded text functions with notification text functions for improved content handling.
- Converge interface realtime lifecycle.
- Standardize presentation blocks and improve semantic clarity.
- Decouple explanations from approval.
- Decouple release version from artifact names.
- Publish stable codemcp archive names.
- Add native linux release packages.
- Add windows setup bootstrap.
- Sign and publish native release artifacts.
- Enable core capabilities by default.
- Make default-on capabilities prerequisite aware.
- Remove global instruction settings.
- Consolidate repository automation.
- Harden runtime and refresh project docs.

### Fixed

- Make installer cosign verification optional.
- Keep cli mutation output in one rail.
- Normalize cli progress completion spacing.
- Restore cobra default help rendering.
- Restore make action target contract.
- Align mcp discovery and version authority.
- Reconcile workspace registry with local identity.
- Expose telegram token setting.
- Expose telegram guided setup command.
- Animate telegram pairing progress.
- Render telegram pairing ready as section.
- Restore codemcp source run classification.
- Enforce source run control plane parity.
- Restore integration executable resolution.
- Update CodeGraph integration state in tests and default configuration.
- Restore integration lifecycle reachability.
- Wire typesafe systemone runtime capabilities.
- Restore windows shell resolution order.
- Reconcile single tunnel lifecycle.
- Close cli capability lifecycle gaps.
- Reconcile codegraph project scope.
- Stabilize tui browser mouse identity.
- Separate tui log retention semantics.
- Clean frontend build and runtime regressions.
- Make telegram approval notifications interactive.
- Update telegram approval notifications in place.
- Add inline copy affordances to telegram rich messages.
- Compact telegram approval actions.
- Reconcile telegram tunnel runtime audits.
- Align integration executable ownership.
- Render all telegram integration actions.
- Render and redesign telegram mini app.
- Serve telegram mini app assets directly.
- Serve telegram mini app from tunnel root.
- Make telegram mini app details scrollable.
- Refine telegram mini app tool details.
- Rebuild go-run runtime before restart.
- Paginate telegram tool inventory.
- Restore observability request response details.
- Improve tui log timeline rendering.
- Make ollama cloud the default llm mode.
- Harden telegram topic lifecycle.
- Make root command help-only.
- Resolve 7-Zip for release payload verification.
- Pack release binaries before Windows setup.
- Update EditMessageTextParams to include text and parse_mode in bot API.
- Skip unsupported Windows arm64 UPX packing.
- Align release platform support.

### Security

- Store encrypted secrets in json envelopes.
- Canonicalize codemcp secret storage.
- Centralize codemcp secret inventory.
- Add llm settings and secret lifecycle.
- Unify protected managed secret input.
- Read telemetry endpoint from release secret.
- Validate release telemetry secret contract.

## [0.2.24] - 2026-09-15

### Added

- Standardize editor submit keys.

### Fixed

- Hand off package manager updates.
- Ignore mouse release replays.

## [0.2.23] - 2026-09-15

### Added

- Support grep file paths.

### Fixed

- Accept tunnel integer arguments.
- Bound filesystem and command outputs.
- Align contracts and annotations.
- Allow normal pushes without approval.

### Security

- Publish master key atomically.
- Require approval for workspace and git mutations.

## [0.2.22] - 2026-09-14

### Fixed

- Compact tool call body rendering.
- Resume log follow after navigation.

## [0.2.21] - 2026-09-14

### Added

- Unify log views and tool call streaming.
- Add tab and navbar dividers.

### Fixed

- Align browser mouse hitboxes.

## [0.2.20] - 2026-09-13

### Fixed

- Bound feed replay by event count.

## [0.2.19] - 2026-09-13

### Added

- Compact ids and refresh idle tunnels.

### Fixed

- Remove redundant child page titles.

## [0.2.18] - 2026-09-13

### Added

- Support package manager upgrades.
- Improve command execution logs.
- Refine routed navigation and execution settings.

### Fixed

- Use full log viewport height.
- Restrict package manager commands.

## [0.2.17] - 2026-09-13

### Added

- Improve operation progress and update flow.

## [0.2.16] - 2026-09-13

### Fixed

- Normalize execution output newlines.

## [0.2.15] - 2026-09-12

### Fixed

- Gracefully restart managed runtime.
- Harden local state integrity.
- Harden background process lifecycle.
- Recover runtime stream gaps.
- Make delete fallback portable.
- Bound workspace file memory use.
- Bound stdio message size.
- Bound transient session state.
- Add rooted path operations.
- Anchor filesystem mutations to workspace roots.
- Anchor recursive filesystem reads.
- Snapshot workspace state through rooted handles.
- Verify rooted access anchors.
- Anchor store access to config root.
- Anchor credential store to config root.
- Anchor control plane files to config roots.
- Anchor tunnel metadata to config root.
- Restore risk-based execution policy.
- Preserve existing user configuration.
- Handle canonical path aliases safely.

### Security

- Anchor file backend to config root.

## [0.2.14] - 2026-09-12

### Added

- Add stdio transport runtime.
- Bind stdio sessions to workspaces.
- Add streamable http and sse transports.
- Add mcp oauth authority.
- Project bound workspace tool schemas.
- Surface generic mcp transports.

### Changed

- Move upstream mcp commands.

### Fixed

- Harden oauth consent nonce.
- Drain process output before wait.

## [0.2.13] - 2026-09-11

### Added

- Support workspace relocation.
- Expose relocate control plane.
- Add workspace relocate flow.

### Fixed

- Harden relocation migration.
- Ignore corrupt checkpoint manifests during relocate.
- Separate explicit and remembered navigation.
- Make relocation portable.

## [0.2.12] - 2026-09-11

### Added

- Group execution stream segments.
- Stream background process executions.
- Expose managed process views.
- Add process execution scope.
- Render managed process streams.
- Cleanup detached process views.
- Guard repeated tool loops.
- Block duplicate mutations.

### Changed

- Share background process manager.

### Fixed

- Distinguish running execution tails.
- Stabilize execution frame scrolling.

## [0.2.11] - 2026-09-10

### Added

- Propagate execution attribution metadata.
- Add command execution scope model.
- Wire command execution scope selector.
- Render execution stream segments.
- Support enter completion for non-mutating forms.
- Refine execution stream framing.
- Add session view state lifecycle.
- Remember last stable routes.
- Restore logs session view state.

### Changed

- Move execution id into frame body.
- Space execution stream frames.
- Move end marker to bottom border.

### Fixed

- Handle stale execution scopes.

## [0.2.10] - 2026-09-10

### Added

- Bound runtime grants with TTL.
- Wire grant list/revoke surfaces.
- Fail-closed unauth loopback and shell warnings.

### Fixed

- Make rewind snapshots fail safe.
- Sigstore verify and safe extract.
- Commit config before runtime apply.
- Acknowledge loopback auth-off in release smoke.

### Security

- SSRF policy and header allowlist.
- Encrypt secrets at rest.

## [0.2.9] - 2026-09-10

### Fixed

- Stage go-run binary before system elevation.
- Reload workspace runtime after mutations.

## [0.2.8] - 2026-09-10

### Added

- Expose workspace containers to agents.

## [0.2.7] - 2026-09-09

### Added

- Auto-generate runtime keys from admin access.
- Complete verbose tracing instrumentation.

### Fixed

- Satisfy verbose tracing static analysis.
- Stabilize native release checks.

### Security

- Clear verbose tracing security checks.

## [0.2.6] - 2026-09-09

### Added

- Embed contextual feature guides.
- Support hierarchical embedded guides.
- Navigate guide tabs with arrows.
- Add approval command viewer.
- Allow similar commands for runtime session.
- Relax allow approval policy.
- Switch managed tunnels by admin key.
- Adapt admin UX to verified access.

### Changed

- Use section layout for guide.
- Auto-reload after mutations.
- Render structured code blocks with glamour.
- Separate command from request title.
- Let agent author request title.
- Avoid blocking configure on metadata sync.
- Do not block service startup on tunnel readiness.
- Verify readiness without long poll delay.
- Unify managed runtime lifecycle.
- Expose lifecycle state without blocking status.
- Parallelize shutdown paths.
- Defer upstream proxy discovery.
- Wait on lifecycle changes.
- Minimize listener reload downtime.
- Make bootstrap idempotent.

### Fixed

- Commit editor state before navigation.
- Summarize command in request title.
- Require real poll readiness.
- Reject removed config subcommands.

## [0.2.5] - 2026-09-08

### Added

- Extend shell approval policy.
- Add approval command glob policies.
- Expose shell approval controls across admin UIs.
- Show container members in details.
- Add config field search.
- Expose runtime config state.
- Add approval expiry countdown.
- Add test approval request action.
- Add markdown viewer.
- Add instruction center.
- Manage global instruction rules.
- Manage instruction sources.
- Expose tool profile status.
- Configure workspace project context.
- Preview workspace project context.
- Add schema-driven explain command.
- Move workspace forms to pages.
- Redesign instruction editors.
- Redesign project context editor.
- Support mcp servers json schema.
- Route mcp server editors.
- Add mcp form json create modes.
- Move mcp oauth setup to page.
- Redesign tunnel editors.
- Move config forms to pages.
- Move runtime forms to pages.
- Move log filters to page.
- Move request forms to pages.

### Changed

- Use tabs for approval requests.
- Resolve approval choices directly.
- Add field presentation metadata.
- Add configuration dashboard.
- Group config fields by domain.
- Improve config field details.
- Centralize config maintenance.
- Merge commands and quick open.
- Deepen bubbles integration.
- Build commands on bubbles list.
- Share settings service.
- Add editor-mode form behavior.
- Add shared editor navigation guard.
- Route editor actions through child pages.
- Prefer descriptive form labels.
- Use placeholders for input guidance.
- Remove legacy form dialogs.

### Fixed

- Keep detail state until parent navigation.
- Preserve pending requests after waiter cancellation.
- Wrap long detail content.
- Wrap long dialog and stream content.
- Align config browser layout.
- Simplify escape navigation.
- Align tabbed page spacing.
- Normalize tabbed page spacing.
- Harden instruction context navigation.
- Wrap structured content by default.
- Harden managed tunnel edit loading.
- Stabilize editors tab layouts and approvals.
- Preserve tab navigation and normalize boolean forms.
- Pin browser help to section footer.
- Harden editor responsive layouts.
- Use switches for persistent booleans.
- Harden cross-platform tui checks.

### Security

- Update goldmark security patch.

## [0.2.4] - 2026-09-08

### Added

- Guard destructive shell mutations.
- Classify host and external mutations.
- Add configurable strict shell approvals.
- Confine strict shell reads to workspace.
- Enforce trusted shell executable path.
- Sandbox strict shell filesystem access.
- Isolate strict network egress.
- Add command lifecycle diagnostics.
- Trace core command workflows.
- Complete verbose and debug tracing audit.
- Support detail-only browser actions.
- Dismiss dialogs on outside click.
- Add shared child detail page.
- Support routed child pages.
- Expose command execution in quick open.

### Changed

- Support generic guarded retries.
- Rename update command to upgrade.
- Merge workspace and container views.
- Improve container member picker.
- Align page tabs with navbar styling.
- Move item actions into detail dialogs.
- Inline tunnel key field hints.
- Emit tool call start events.
- Make browser list-only.
- Move workspace details to child pages.
- Route resource detail pages.
- Add config and runtime child details.
- Split logs into routed child pages.
- Remove legacy browser detail state.
- Share page help footer.
- Restore tabbed workspace and logs parents.
- Render toasts as modal dialogs.

### Fixed

- Harden mutation path policy.
- Require approval for dynamic strict reads.
- Minimize sandbox system config mounts.
- Drop sandbox privileges.
- Allow HTTP-only managed startup.
- Hot-reload registry in running runtime.
- Return before tunnel response deadline.
- Keep managed runtime supervised.
- Cap synchronous tunnel calls.
- Terminate command process trees.
- Stage go-run binaries for managed runtime.
- Stage dev binary for runtime actions.
- Preserve connection on request cancellation.
- Retire cancelled in-memory responses.
- Keep log stream status out of toast dialogs.
- Harden Windows architecture detection.

### Security

- Isolate shell environment secrets.

## [0.2.3] - 2026-09-07

### Fixed

- Honor allowed dirs and hide Windows launcher.
- Initialize workspace forms on open.

## [0.2.2] - 2026-09-07

### Fixed

- Decouple managed readiness from tunnel.
- Hide managed runtime console.

## [0.2.1] - 2026-09-07

### Added

- Require an available MCP transport.
- Refresh command center dashboard.
- Toggle MCP HTTP transport.
- Surface state in page and terminal titles.

### Fixed

- Unify command center interactions.
- Complete binary aliases and go run.
- Make page title toasts transient.

## [0.2.0] - 2026-09-07

### Added

- Report server uptime with version.
- Port mode as built-in.
- Configure interactive TUI mode.
- Add command center shell.
- Add unified action registry.
- Add command palette.
- Add quick open and recent state.
- Add shared components and forms.
- Add workspace management.
- Fill terminal layout.
- Add MCP server management.
- Add tunnel management.
- Add approval request inbox.
- Add config center.
- Finalize command center interactions.
- Finalize responsive interaction model.
- Add live logs viewer.
- Add system and runtime actions.
- Copy workspace resource ids.
- Surface pending approvals globally.
- Render update toast bottom right.
- Add logs visibility levels.
- Stream command executions in logs.

### Changed

- Bump google.golang.org/grpc from 1.82.1 to 1.83.1.
- Extract reusable command operations.
- Simplify global navigation hints.
- Remove legacy command interactive mode.
- Unify browser key hints.

### Fixed

- Use config as active mode state.
- Harden command center UX.
- Make logs history and stream reliable.
- Pin browser detail during live updates.
- Preserve expanded browser help.
- Preserve browser help across reloads.
- Stabilize command execution logs.
- Align page key hints with footer.
- Wait for tunnel before runtime ready.
- Pin config logs runtime hints to bottom.
- Read macOS boot time natively.
- Read macOS machine uptime natively.
- Pin supported upx version.

## [0.1.4] - 2026-09-05

### Added

- Add workspace containers.
- Manage workspace containers.
- Add workspace container tui.
- Add workspace container api.
- Manage workspace containers in ui.
- Support multi-workspace sessions.
- Key scoped remember entries.
- Add scoped memory retrieval.
- Add scoped memory removal.
- Add rebuildable memory index.
- Add relevance search.
- Retrieve relevant workspace memory.
- Analyze memory optimization candidates.
- Surface compaction recommendations.
- Add local hybrid retrieval.
- Synchronize derived memory index.

### Changed

- Remove unused server stubs.
- Remove unused file-backed logger.
- Use charm default colors.
- Apply charm defaults consistently.
- Center interactive layouts.
- Require immediate remember on request.
- Scope cross-session notes.
- Introduce canonical memory entries.
- Define canonical memory workflow.
- Allow scope-level notes without duplicate keys.

### Fixed

- Tolerate nullable instruction settings.
- Harden rewind restore.
- Replace files atomically on Windows.
- Bound background process history.
- Harden Windows service and bundle permissions.
- Verify signed release checksums.
- Nest auto memory headings.
- Satisfy staticcheck.
- Avoid hash bucket narrowing.

## [0.1.3] - 2026-09-05

### Fixed

- Use valid Windows restart interval.
- Simplify serve shutdown and hide Windows service.

## [0.1.2] - 2026-09-05

### Added

- Add portable config bundles.

### Changed

- Default portable bundle filename.

### Fixed

- Harden bundle export traversal.

## [0.1.1] - 2026-09-05

### Added

- Add global instruction source policy.
- Expose project context management.
- Add workspace child routing.
- Manage and preview project context.
- Scope requests to workspace routes.
- Stream workspace command executions.
- Route tool calls by uuidv7.
- Add combined command stream.

### Changed

- Share project context builder.
- Use react router for all routes.
- Route command executions.
- Simplify workspace navigation.
- Simplify tool call ids.

### Fixed

- Contain tool schema overflow.
- Auto scroll combined command stream.

## [0.1.0] - 2026-09-05

### Added

- Add progress spinners to slow commands.
- Migrate legacy installs and modernize admin UI.

### Fixed

- Default structured commands to text output.
- Make managed restart deterministic.
- Canonicalize symlinked legacy paths.

## [0.0.15] - 2026-09-05

### Added

- Add installation layout and detection.
- Add transactional binary installation.
- Add alias management commands.
- Add self-install command.
- Add release checking.
- Add verified release downloads.
- Add self-update command.
- Enforce installation method policy.
- Restart managed runtime after update.
- Add automatic rollback on restart failure.
- Cache update availability.
- Add control guard approval domain.
- Add control approval request flow.
- Execute approved one-shot commands.
- Add control approval request commands.
- Expose control approval requests.
- Add control approval dialog.
- Add interactive request management.
- Reuse interactive list experience.
- Refine interactive TUI styling.
- Add approval request management.
- Copy workspace ID from TUI.
- Add dummy approval request command.
- Make request dialog buttons interactive.

### Changed

- Delegate bootstrap installers to binary.
- Remove unnecessary environment variables from capture function.
- Use Charm default TUI palette.
- Tone down TUI help keys.
- Use Bubbles list UI across TUIs.
- Improve workspace detail view.
- Align workspace detail with list browser.
- Show browser details in modal.
- Show request details in modal.
- Refine request approval dialogs.
- Add tabbed interactive detail views.
- Redesign tunnel admin page.
- Prepare embed from fresh frontend build.

### Fixed

- Isolate checkpoint state from startup verify.
- Preserve MCP session identity for approvals.
- Hide resolved approval requests in TUI.
- Harden managed install detection.
- Preserve rollback target identity.
- Subscribe before approval stream ready.
- Avoid duplicate browser empty state.

### Security

- Replace OS keyring with file storage.
- Recover missing tunnel secret files.

## [0.0.14] - 2026-09-03

### Added

- Load project instruction memory.
- Expand project instruction imports.
- Budget project instruction memory.
- Load unconditional project rules.
- Add project git snapshot.
- Add project environment snapshot.
- Load workspace auto memory.
- Summarize project skills.
- Add agent workflow instructions.
- Format project instruction context.
- Build project instruction context.
- Simplify project context options.

### Changed

- Add instruction context domain.
- Share agent instruction guidance.

### Fixed

- Prioritize agents instruction memory.
- Canonicalize instruction context paths.

## [0.0.13] - 2026-09-03

### Added

- Add persistent instance identity.
- Scope workspace ids by instance.
- Add cluster federation protocol.
- Route workspace calls to cluster owners.
- Add cluster runtime discovery.
- Enforce cluster catalog compatibility.
- Add cluster tunnel leader leases.
- Coordinate tunnel cluster leadership.
- Add secure cluster relay config.
- Add websocket cluster relay.
- Bootstrap cluster runtime lifecycle.
- Add cluster relay command.
- Add cluster observability.
- Recover cluster relay connections.
- Configure cluster from admin settings.
- Harden cluster relay.
- Add cluster relay deployment support.
- Propagate MCP session identity.
- Add MCP session workspace binder.
- Enforce MCP session workspace isolation.
- Expire idle MCP session bindings.
- Expose MCP session workspace activity.

### Changed

- Add workspace context resolver.
- Bind filesystem tools to workspace roots.
- Bind git tools to workspace ids.
- Use persisted shell cwd.
- Expose workspace runtime context.
- Remove working directory tool context.
- Remove cluster federation.
- Require explicit workspace selection.
- Restore stable workspace ids.

### Fixed

- Preserve MCP session across tunnel transport.
- Isolate only local workspace tools.
- Patch qs audit vulnerability.
- Make workspace migration tests cross-platform.

## [0.0.12] - 2026-09-02

### Added

- Enrich tunnel metadata and activity details.
- Add tunnel create command and help descriptions.
- Redesign tunnel admin management.
- Use kibo code blocks for raw data.
- Improve tunnel admin ui and task feedback.
- Animate cli task states and slim code highlighting.
- Show tunnel metadata after managed startup.
- Persist tunnel metadata and add restart flow.
- Add admin page routes.
- Reconnect admin activity stream.
- Store credentials in OS keyring.

### Changed

- Replace kibo code block with animate ui.
- Limit code highlighting languages.
- Simplify raw json viewer.

### Fixed

- Refine tunnel cli hierarchy and status.
- Keep code blocks within dialog width.
- Wrap long code block lines.
- Support headless linux keyring.
- Simplify json viewer scrolling.
- Isolate json viewer scrolling.
- Update ci regressions.
- Subscribe before acknowledging tool changes.

## [0.0.11] - 2026-08-31

### Added

- Default logs to latest session.
- Establish admin UI foundation.
- Add reusable admin data views.
- Redesign admin activity view.
- Improve admin resource views.
- Redesign admin MCP servers.
- Polish admin settings and tunnel.
- Improve CLI visual hierarchy.
- Add JSON MCP server import.
- Advertise MCP bootstrap instructions.

### Changed

- Lazy load admin pages.

## [0.0.10] - 2026-08-31

### Added

- Redesign status output.
- Preserve managed execution environment.

### Fixed

- Preserve log alignment in raw terminal mode.
- Improve portability across timezones and IPv6.

### Security

- Harden MCP tool context enforcement.
- Require auth for network exposure.
- Harden direct HTTP and admin UI.
- Restrict upstream OAuth network targets.
- Harden release supply chain.
- Harden managed Linux service.

## [0.0.9] - 2026-08-31

### Added

- Add explicit system service scope.
- Improve runtime log sessions.
- Add shared interactive interrupts.
- Replace cmcp alias with cgm.
- Add focused CLI aliases.
- Add dynamic shell completions.

### Fixed

- Avoid duplicate tunnel degraded events.

## [0.0.8] - 2026-08-30

### Added

- Expose runtime version tool.
- Reload runtime config without restart.
- Add persistent runtime event journal.
- Add runtime log querying.
- Expand local runtime control plane.
- Define managed service runtime.
- Add linux managed service backend.
- Add macos managed service backend.
- Add windows managed service backend.
- Add managed runtime up and down commands.
- Report managed runtime state.
- Add runtime logs command.
- Add runtime log management.

### Fixed

- Make managed service updates version-safe.
- Canonicalize protected shell paths.

## [0.0.7] - 2026-08-30

### Added

- Redesign logger for cli-first output.
- Isolate runtime state and supervise tunnel.
- Add explicit allowed directories.
- Refine exposure and command hierarchy.

### Changed

- Bump golang.org/x/net to 0.55.0.

### Fixed

- Prevent mcp tool self-grants.
- Improve config redaction output.

## [0.0.6] - 2026-08-30

### Added

- Add multi-format configuration.
- Add auth-aware network exposure.
- Add config transform and verify.
- Add configurable caveman and ponytail features.
- Add cmcp installer alias.

### Fixed

- Harden shell workspace boundaries.
- Make tunnel reconfiguration transactional.
- Refresh upstream proxies atomically.
- Harden runtime config concurrency.
- Harden activity stream and tunnel lifecycle.

## [0.0.5] - 2026-08-29

### Fixed

- Unify tool observability across transports.

## [0.0.4] - 2026-08-29

### Fixed

- Restore admin dashboard rendering.

## [0.0.3] - 2026-08-29

### Added

- Polish admin dashboard.
- Polish runtime lifecycle logging.
- Add adaptive network exposure.

## [0.0.2] - 2026-08-29

### Added

- Add structured logger and version fallback.
- Change default ports and standardize cli logging.
- Log mcp and admin urls on startup.
- Add uninit command for config cleanup.
- Implement filesystem, shell and git core tools.
- Migrate mcp runtime to 2026-07-28.
- Add mcp compliant tool results.
- Add workspace-bound local tool foundation.
- Port local coder filesystem tools.
- Port local coder shell and process tools.
- Port local coder git tools.
- Add context skills rules and rewind tools.
- Add stateful node repl and ponytail tools.
- Add upstream mcp bridge and dynamic proxies.
- Complete cli configuration workflows.
- Add config presets.
- Complete admin runtime api.
- Complete admin web workflows.
- Improve runtime activity dashboard.
- Relay upstream mcp input requests.
- Add upstream oauth lifecycle.
- Add admin oauth workflows.
- Sync upstream tool subscriptions.
- Serve mcp tool subscriptions.
- Embed openai secure mcp tunnel.

### Changed

- Use charmbracelet logger for cli output.
- Use single server port for admin and mcp.
- Unify tool runtime and registry.
- Share config presets across cli and web.
- Add cross-platform local install workflow.
- Add package manager distribution.

### Fixed

- Split mcp and admin servers and stop spa redirects.
- Preserve windows shell state and polish cli logging.
- Resolve shell session state collision.
- Harden shell workspace safety.
- Harden upstream mcp protocol handling.
- Refresh upstream runtime on reconfigure.
- Harden native platform portability.
- Stamp server identity on mcp results.
- Validate inbound mcp tool headers.
- Harden mcp conformance and cancellation.
- Finalize mcp release readiness.

### Security

- Harden tunnel secret and process lifecycle.

## [0.0.1] - 2026-08-27

### Added

- Bootstrap go cli and config system.
- Add workspace and state management.
- Add mcp server runtime.
- Add mcp and admin authentication.
- Add workspace context resolver.
- Add disk backed activity store.
- Bootstrap go mcp foundation.
- Implement workspace management and file handling services.
- Add workspace runtime and context foundation.
- Add upstream mcp foundation.
- Extend upstream mcp integration foundation.
- Implement remaining runtime foundations.
- Add mcp protocol and auth runtime.
- Wire mcp tool runtime.
- Register core mcp tools and initialize handshake.
- Connect mcp tools runtime and workspace validation.
- Wire mcp runtime into application.
- Add mcp http session lifecycle.
- Add mcp route auth and sse foundation.
- Integrate mcp http runtime with app routes.
- Add mcp session tracking and activity streams.
- Add disk activity logging and session delete policy.
- Connect activity streams and session delete scheduler.
- Add mcp concurrency recovery and sse lifecycle.
- Finalize mcp recovery and workspace tool validation.
- Add mcp capabilities and notifications.
- Add mcp tool schema catalog.
- Add tool schema registry and admin api foundation.
- Expose tool schemas through mcp runtime.
- Add shadcn admin ui foundation and api routes.
- Add admin api and shadcn dashboard foundation.
- Connect admin dashboard to api foundation.
- Add shadcn admin navigation and api client.
- Add admin pages foundation and sidebar routing.
- Add admin pages routing foundation.
- Add admin tools and upstream pages.
- Add config api and admin settings foundation.
- Improve admin config and server ui foundation.
- Add config persistence foundation and api mutations.
- Add cli config path command and settings save foundation.
- Extend config cli and settings integration.
- Add config cli foundation and default store.
- Add config set cli and settings save action.
- Add upstream store foundation and mcp cli namespace.
- Extend upstream persistence and mcp cli commands.
- Wire upstream manager into admin api.
- Persist upstream manager and add api crud.
- Add upstream api mutations and mcp server cli commands.
- Complete mcp server cli foundation.
- Add server admin actions and api mutations.
- Wire runtime app stores for phase 11.
- Wire admin runtime dependencies in phase 11.
- Complete phase 11 runtime bootstrap wiring.
- Add activity observability foundation.
- Add activity sse endpoint and runtime stream.
- Connect activity events to mcp runtime.
- Emit mcp runtime activity events.
- Connect activity persistence and mcp events.
- Add mcp activity emission helper.
- Wire mcp request activity lifecycle.
- Add tool activity lifecycle events.
- Add tool result activity lifecycle.
- Add session lifecycle activity events.
- Add activity realtime web page.
- Add activity page navigation.
- Add mcp protocol notification foundation.
- Add mcp jsonrpc validation and error codes.
- Apply jsonrpc request validation to http runtime.
- Add mcp method validation and not found errors.
- Improve mcp initialize handshake negotiation.
- Add streamable http session lifecycle.
- Validate mcp initialize and tool params.
- Add session notification streaming parity.
- Integrate managed tunnel runtime and cli.
- Add tunnel admin ui and integration tests.
- Embed admin web into release binary.
- Add version metadata and portable web embed scripts.

### Fixed

- Enforce token auth across runtime and web.
- Harden persistence and server runtime.
- Finalize production hardening and admin runtime.
- Bootstrap auth tokens and cli error handling.
