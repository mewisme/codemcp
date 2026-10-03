# Integrations scope

Reserved for first-party optional CodeMCP integrations. It is not a generic plugin platform.

New optional first-party capability implementations belong below this root.

Ponytail and Caveman are owned here together with the canonical integration identity, status, and configuration contracts. Additional first-party integrations extend this domain without creating a generic plugin authority.

Integrations must not depend on presentation adapters.

The browser integration detects user-installed Chrome, Chromium, or Edge executables and owns only CodeMCP-isolated browser profile state. It does not download, install, update, or mutate browser binaries, and it never targets a user's ordinary browser profile.
