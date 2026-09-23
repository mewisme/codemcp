# Getting Started with the TUI

The `cm tui` Command Center is the interactive interface for `CodeMCP`. It is intended for human-operated workflows such as browsing registered resources, editing configuration, reviewing approval requests, checking runtime state, and running lifecycle actions. Normal `cm ...` commands remain the stable interface for scripts and automation.

## Navigation model

The top navigation owns the major operational areas: Workspaces, MCP, Tunnel, Requests, Logs, Config, Instruction, and Runtime. `Alt+Left` and `Alt+Right` cycle these areas with wrap-around. Child pages remain owned by their parent area, so opening a workspace, MCP server, managed tunnel, log event, or request does not change the active top-level section.

`Esc` always acts on the nearest active layer. It closes an active editor/child page back to its semantic parent, then returns to Home, and exits from Home. Destructive confirmation dialogs and transient overlays consume `Esc` before normal page navigation. `Backspace` is also a back-navigation key when the current page is not actively editing text.

## Commands

Press `Ctrl+K` to open Commands. The command list combines the shared TUI action registry with discoverable resources. Search can match human titles, keywords, resource IDs, and canonical CLI paths. This is the preferred way to discover actions that are not assigned a local shortcut.

Examples of useful searches include:

```text
workspace register
upstream server add
request approve
config verify
upgrade
guide
```

Use the arrow keys to select a result and `Enter` to run it. `Esc` closes Commands without changing the current page. Recent actions are retained in TUI state and are surfaced again on Home.

## Page-local help

Lists and detail pages use Bubbles help. The compact help row sits immediately above the application footer. Press `?` when a page shows `? more` to expand all available page-local bindings. Expanded help belongs to that page; it is not a replacement for `Ctrl+K`.

Browser-style lists generally support `j/k` or arrow navigation, `Enter` to open the selected item, `/` to filter when filtering is enabled, and mouse selection/scrolling. Exact bindings are always shown in the local help row.

## Mouse support

Header tabs, list rows, editor sections, switches, confirmation buttons, scrollable Markdown, and approval content expose mouse targets. Mouse actions dispatch the same semantic messages as keyboard actions; they do not bypass validation, dirty-draft protection, or security confirmation.

## Responsive layout

The TUI is designed around wrapping rather than horizontal overflow. Titles, key/value rows, Markdown, JSON previews, editor labels, request arguments, and long paths reflow at narrow terminal widths. Browser and editor content is clamped to the available body size so the application footer and critical controls remain visible.

## When to use this Guide

Open `Ctrl+K` and choose **Guide: Browse topics**. The Guide list is an index only. Opening a topic loads and renders that topic's embedded Markdown with Glamour; unrelated topics are not concatenated into one large document. `Esc` from a topic returns to the topic list.
