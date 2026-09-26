# Instruction

Instruction manages user-level instruction context used by Project Context assembly. It has `Context / Rules / Sources` tabs with a shared SectionLayout and consistent title styling.

## Global Context

Global Context opens in **preview mode**, not edit mode. The Markdown is rendered with Glamour and scrolls through a bounded Markdown viewport. Because preview is not an input, normal tab-navigation keys remain owned by the page rather than being consumed by an editor.

Press `e` to enter the routed Global Context editor. The editor uses a multiline textarea; `Enter` inserts a newline and `Ctrl+Enter` saves. On successful save the page returns to the Markdown preview. Unsaved edits participate in the global dirty-navigation guard.

## Global Rules

Rules are shown in a Browser with the Browser's internal title disabled; `Global Rules` is rendered once by SectionLayout using the same no-background title style as Global Context.

Create/Edit opens a full-page rule editor for rule name, enabled Switch, and content. Rule save commits the editor baseline before returning to the rule list. Delete uses explicit confirmation.

Rule browser search includes rule content as well as visible metadata.

## Sources

Sources is a tree of detected instruction providers and resources. The tree is initialized with usable dimensions at first mount, so it does not collapse into a one-character vertical column before the first interaction.

Provider and child-resource policy can be enabled/disabled from the tree when the provider supports policy overrides. A child cannot be enabled while its provider is disabled; the page reports this rather than silently writing contradictory policy.

Source changes are persisted through the instruction settings service and the tree is rebuilt from the resulting settings.

Provider-native hidden directories such as `.agents`, `.claude`, `.cursor`, `.codex`, and compatible future providers are read-only interoperability inputs. CodeMCP does not own or rewrite those trees. Project loading inspects only immediate hidden directories beneath the selected project root, accepts supported direct context files plus bounded `rules/` and `skills/` resources, and rejects symlink-backed provider/resource paths. User-home providers may appear in Sources for policy visibility, but they are not implicitly loaded into a project context.

## Relationship to Project Context

Global Context, Global Rules, source policy, detected instruction files, auto memory, and skills are inputs to the workspace Project Context builder. Use Workspaces → Project Context Preview → Sources when you need to inspect exactly which sources were included in a particular build.
