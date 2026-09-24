# Storage & Maintenance

Storage contains maintenance operations for persistent configuration/state rather than individual schema fields.

## Verify

**Verify** checks structured config/state consistency and configuration validity. Use it after access, exposure, format, migration, or manual state changes when you want an explicit health check.

## Migrate and convert

**Migrate** moves supported legacy credential state into the managed secret store and converts legacy secret files to encrypted JSON envelopes. **Convert** changes the structured configuration/state representation among supported formats while preserving validated semantics.

## Import and export

**Export** creates a portable JSON envelope with managed secrets explicitly excluded. **Import** validates and restores an envelope and requires explicit confirmation before replacing existing persistent state.

Import uses an existing-file picker with manual input fallback. Export accepts a destination that may not exist yet.

See the child envelope topic for the contextual import/export behavior. Use the public [Configuration](../../../../configuration.md) guide for the broader storage model.
