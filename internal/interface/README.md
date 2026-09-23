# Interface scope

High-level presentation adapters only. Current children are Admin HTTP, TUI, and the embedded web asset handler.

CLI remains top-level because it owns top-level process workflow through `app`. Lower-level packages must never import this scope. Sibling adapters must communicate through application/domain contracts rather than importing each other for business behavior.
