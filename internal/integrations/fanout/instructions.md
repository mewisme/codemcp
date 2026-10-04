# Fanout

Fanout guides managed-agent delegation strategy. It is advisory only.

## Authority boundaries

- Existing managed-agent tools own execution and lifecycle.
- Current runtime/backend readiness and capacity remain authoritative. Never invent or raise concurrency limits.
- Once Fanout is active and the selected backend is ready, automatic delegation is the default: spawn useful child jobs as soon as safe independent work exists, up to reported effective capacity. The user does not need to request fanout or specify a job count.
- Workspace access, child claim, plan execution, approvals, and completion rules remain authoritative.
- Exact workspace binding remains authoritative. Fanout never selects or widens child workspace access.
- Fanout guidance never grants permissions, workspace access, or nested delegation.

## Delegation rules

- Automatically delegate meaningful work with positive payoff from independence, parallelism, specialization, or independent review whenever it can run safely.
- Choose child jobs dynamically from the work itself; do not create filler jobs merely to consume available capacity.
- Do not delegate trivial work, immediate sequential dependencies, or work whose coordination cost exceeds doing it directly.
- Split work by independent subsystem, question, or workstream.
- Prefer read-only fanout for broad audits and research.
- Parallel mutation requires explicit disjoint ownership. Never let workers race on the same files or shared mutable state.
- Do not create duplicate workers for the same question unless independent review is intentional.
- Child prompts contain only parent-only goal, decisions, constraints, references, scope boundaries, and expected output. Do not copy the full transcript or workspace context.
- Child bootstrap is fixed: claim the assigned child session/workspace with `agent_claim`, then call `project_context` with memory enabled before substantial project work. Obey that fresh canonical context and stay inside assigned scope.
- Continue useful independent parent work after spawning children.
- Use bounded `agent_wait` only when a child result is actually needed.
- Use `agent_send` only for useful follow-up to a live idle child.
- Use `agent_cancel` when child output is no longer needed.
- Depth is one. Claimed children cannot use `agent_spawn` to create grandchildren.
- Aggregate child results at the parent. Deduplicate overlapping findings, resolve contradictions, and verify material conclusions before treating them as final.

When no useful independent work exists, continue directly instead of manufacturing delegation.
