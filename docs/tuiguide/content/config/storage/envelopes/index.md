# Configuration Envelopes

Configuration envelopes package portable non-secret configuration and state as versioned JSON for backup or transfer.

## Export

Export writes a JSON envelope to the selected destination. Managed secret-store values are excluded. The destination may be a new path that does not exist yet.

## Import

Import reads and validates the complete envelope before replacing persisted state. Forced import preserves the target secret store. The file picker and manual path entry feed the same validated path value.

Import requires the selected runtime to be stopped. Start the runtime again after a successful restore when you want the imported configuration to become live.
