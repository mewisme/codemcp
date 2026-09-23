import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

type Props = { title: string; description?: string; actions?: ReactNode; className?: string }

export function PageHeader({ title, description, actions, className }: Props) {
  return <div className={cn("flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between", className)}><div className="min-w-0"><h1 className="text-xl font-semibold tracking-tight sm:text-2xl">{title}</h1>{description ? <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{description}</p> : null}</div>{actions ? <div className="flex shrink-0 flex-wrap items-center gap-2 sm:justify-end">{actions}</div> : null}</div>
}
