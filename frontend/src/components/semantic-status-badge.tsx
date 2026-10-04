import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

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

const active = new Set([
  "active",
  "approved",
  "completed",
  "connected",
  "consumed",
  "ok",
  "pending",
  "ready",
  "running",
  "success",
])

export function SemanticStatusBadge({
  status,
  className,
}: {
  status: string
  className?: string
}) {
  const normalized = status.trim().toLowerCase()
  return (
    <Badge
      className={cn("shrink-0", className)}
      variant={
        danger.has(normalized)
          ? "destructive"
          : active.has(normalized)
            ? "secondary"
            : "outline"
      }
    >
      {status}
    </Badge>
  )
}
