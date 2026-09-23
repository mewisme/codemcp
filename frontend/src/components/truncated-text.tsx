import { cn } from "@/lib/utils"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

export function TruncatedText({ children, lines = 2, mono = false, className }: { children: string; lines?: 1 | 2 | 3; mono?: boolean; className?: string }) {
  const clamp = lines === 1 ? "line-clamp-1" : lines === 3 ? "line-clamp-3" : "line-clamp-2"
  return <Tooltip><TooltipTrigger asChild><span className={cn("block min-w-0 break-words", clamp, mono && "font-mono", className)}>{children}</span></TooltipTrigger><TooltipContent className="max-w-sm break-words">{children}</TooltipContent></Tooltip>
}
