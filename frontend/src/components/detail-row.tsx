import { cn } from "@/lib/utils"

export function DetailRow({ label, value, mono = false, className }: { label: string; value: React.ReactNode; mono?: boolean; className?: string }) {
  return <div className={cn("grid gap-1 py-2 sm:grid-cols-[minmax(8rem,0.35fr)_minmax(0,1fr)] sm:gap-4", className)}><div className="text-sm text-muted-foreground">{label}</div><div className={cn("min-w-0 break-words text-sm font-medium", mono && "break-all font-mono font-normal")}>{value}</div></div>
}
