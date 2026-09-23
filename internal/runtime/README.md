# Runtime scope

Execution/runtime mechanics live here:

- `activity`: bounded live activity stream;
- `control`: runtime control socket/client and execution projections;
- `event`: structured runtime event journal/stream;
- `shell`: command execution, sessions, and managed processes.

Runtime code may depend on lower-level product primitives and persistence. It must not depend on presentation adapters or process composition.
