import { Badge } from "@/components/ui/badge"
import { semanticStatusTone } from "@/components/semantic-status"
import { cn } from "@/lib/utils"

export function SemanticStatusBadge({
  status,
  className,
}: {
  status: string
  className?: string
}) {
  const tone = semanticStatusTone(status)
  return (
    <Badge
      className={cn(
        "shrink-0",
        tone === "success" &&
          "border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
        tone === "warning" &&
          "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
        className
      )}
      variant={
        tone === "danger"
          ? "destructive"
          : tone === "active"
            ? "secondary"
            : "outline"
      }
    >
      {status}
    </Badge>
  )
}
