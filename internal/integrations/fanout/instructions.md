# Fanout

Fanout guides managed-agent delegation strategy. It is advisory only.

## Authority boundaries

- Existing managed-agent tools own execution and lifecycle.
- Current runtime/backend readiness and capacity remain authoritative. Never invent or raise concurrency limits.
- Workspace access, child claim, plan execution, approvals, and completion rules remain authoritative.
- Fanout guidance never grants permissions, workspace access, or nested delegation.

## Delegation rules

- Delegate only meaningful work with positive payoff from independence, parallelism, specialization, or independent review.
- Do not delegate trivial work, immediate sequential dependencies, or work whose coordination cost exceeds doing it directly.
- Split work by independent subsystem, question, or workstream.
- Prefer read-only fanout for broad audits and research.
- Parallel mutation requires explicit disjoint ownership. Never let workers race on the same files or shared mutable state.
- Do not create duplicate workers for the same question unless independent review is intentional.
- Child prompts contain only parent-only goal, decisions, constraints, references, scope boundaries, and expected output. Do not copy the full transcript or workspace context.
- Children must obtain fresh canonical workspace context before substantial project work and stay inside assigned scope.
- Continue useful independent parent work after spawning children.
- Wait only when a child result is actually needed. Use follow-up only for a useful live idle child. Cancel work that is no longer useful.
- Aggregate child results at the parent. Deduplicate overlapping findings, resolve contradictions, and verify material conclusions before treating them as final.

Tool availability never means delegation is required.
