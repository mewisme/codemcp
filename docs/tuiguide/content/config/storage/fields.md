# Config Storage Editor Fields

## Convert

### Target format

Selects the structured format used for configuration/state files after conversion: **JSON**, **YAML**, or **TOML**. Conversion applies to the application's structured configuration/state set rather than one individual field.

## Export Envelope

### Envelope file

Destination path for the portable JSON configuration envelope. Export uses manual input because the destination may not exist yet. The path is required and must name a file rather than `.`.

### Overwrite destination if it exists

Boolean controlling whether export may replace an existing destination envelope. Leave disabled to avoid overwriting an existing file.

## Import Envelope

### Envelope file

Existing JSON envelope file to restore. Import uses the file picker with manual-input fallback and validates that a file path is supplied.

### Replace existing configuration/state

Boolean opt-in allowing imported data to replace existing configuration/state. Import is destructive enough that submission opens a separate confirmation dialog before activating the staged envelope.

Import/export envelopes include portable configuration/state only. Managed secret-store values are excluded, and forced import preserves the target secret store.
