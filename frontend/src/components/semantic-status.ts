const danger = new Set([
  "blocked",
  "cancelled",
  "degraded",
  "denied",
  "error",
  "expired",
  "failed",
  "timed_out",
  "unreachable",
])

const success = new Set([
  "approved",
  "completed",
  "connected",
  "consumed",
  "ok",
  "ready",
  "success",
])

const active = new Set(["active", "pending", "running", "start", "progress"])
const warning = new Set(["warn", "warning"])

export type SemanticStatusTone =
  "danger" | "success" | "active" | "warning" | "neutral"

export function semanticStatusTone(status: string): SemanticStatusTone {
  const normalized = status.trim().toLowerCase()
  if (danger.has(normalized)) return "danger"
  if (success.has(normalized)) return "success"
  if (active.has(normalized)) return "active"
  if (warning.has(normalized)) return "warning"
  return "neutral"
}
