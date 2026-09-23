import type { Column, RowData } from "@tanstack/react-table"
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { DataTableFeatures } from "@/components/data-table-features"

export function DataTableColumnHeader<TData extends RowData, TValue>({ column, title }: { column: Column<DataTableFeatures, TData, TValue>; title: string }) {
  const sorted = column.getIsSorted()
  return <Button className="-ml-2 h-8" variant="ghost" onClick={() => column.toggleSorting(sorted === "asc")}>{title}{sorted === "asc" ? <ArrowUp /> : sorted === "desc" ? <ArrowDown /> : <ChevronsUpDown className="text-muted-foreground" />}</Button>
}
