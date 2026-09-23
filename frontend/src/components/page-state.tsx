import type { LucideIcon } from "lucide-react"
import { AlertCircle } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"

export function PageError({ message, title = "Something went wrong" }: { message: string; title?: string }) {
  if (!message) return null
  return <Alert variant="destructive"><AlertCircle /><AlertTitle>{title}</AlertTitle><AlertDescription className="break-words">{message}</AlertDescription></Alert>
}

export function PageEmpty({ icon: Icon, title, description, action }: { icon?: LucideIcon; title: string; description?: string; action?: React.ReactNode }) {
  return <Empty className="min-h-48 border"><EmptyHeader>{Icon ? <EmptyMedia variant="icon"><Icon /></EmptyMedia> : null}<EmptyTitle>{title}</EmptyTitle>{description ? <EmptyDescription>{description}</EmptyDescription> : null}</EmptyHeader>{action ? <EmptyContent>{action}</EmptyContent> : null}</Empty>
}

export function PageLoading({ rows = 4 }: { rows?: number }) {
  return <div className="space-y-3" aria-label="Loading"><Skeleton className="h-9 w-full" />{Array.from({ length: rows }, (_, index) => <Skeleton className="h-14 w-full" key={index} />)}</div>
}
