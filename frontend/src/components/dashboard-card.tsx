import type { LucideIcon } from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"

type Props = { title: string; value: string | number; description?: string; icon?: LucideIcon }

export function DashboardCard({ title, value, description, icon: Icon }: Props) {
  return <Card><CardHeader className="flex-row items-center justify-between space-y-0 pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">{title}</CardTitle>{Icon ? <Icon className="size-4 text-muted-foreground" /> : null}</CardHeader><CardContent><div className="text-2xl font-semibold tracking-tight">{value}</div>{description ? <div className="mt-1 text-xs text-muted-foreground">{description}</div> : null}</CardContent></Card>
}
