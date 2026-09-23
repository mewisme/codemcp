# Global Context

Instruction → Global Context is Markdown instruction content managed by `cm`.

## Preview state

The default state is a Glamour-rendered Markdown preview, not an active textarea. This matters for keyboard ownership: page/tab navigation keys remain available while previewing instead of being swallowed by an editor that is not in edit mode.

## Edit state

Trigger **Edit** to enter the routed Global Context editor. The textarea owns normal text-entry keys only in this state. `Enter` inserts newlines; `Ctrl+Enter` saves through the page workflow; Esc/back navigation participates in the global dirty-draft guard.

## Content

The content is free-form Markdown instruction text. There are no additional structured fields. Saving persists the complete managed Global Context text. On success the editor is cleared/committed before navigation back to preview, so closing the success toast does not reveal a false Discard changes dialog.

## Rules and Sources

Global Context is one of three Instruction tabs. **Rules** manages structured named/enabled rule documents. **Sources** previews detected instruction sources/policy. Preview-mode key handling deliberately leaves tab navigation available across these tabs.
