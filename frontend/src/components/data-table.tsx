import * as React from "react"
import { flexRender, useTable, type ColumnDef, type PaginationState, type RowData, type SortingState } from "@tanstack/react-table"
import { ChevronLeft, ChevronRight } from "lucide-react"
import { dataTableFeatures, type DataTableFeatures } from "@/components/data-table-features"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { cn } from "@/lib/utils"

type Props<TData extends RowData> = { columns: ColumnDef<DataTableFeatures, TData>[]; data: TData[]; empty?: React.ReactNode; toolbar?: React.ReactNode; onRowClick?: (row: TData) => void; pageSize?: number; className?: string }

export function DataTable<TData extends RowData>({ columns, data, empty, toolbar, onRowClick, pageSize = 10, className }: Props<TData>) {
  const [sorting, setSorting] = React.useState<SortingState>([])
  const [pagination, setPagination] = React.useState<PaginationState>({ pageIndex: 0, pageSize })
  const table = useTable({ features: dataTableFeatures, data, columns, onPaginationChange: setPagination, onSortingChange: setSorting, state: { pagination, sorting } })
  const rows = table.getRowModel().rows
  return <div className={cn("space-y-3", className)}>{toolbar}<div className="overflow-hidden rounded-lg border bg-card"><Table><TableHeader>{table.getHeaderGroups().map((group) => <TableRow key={group.id}>{group.headers.map((header) => <TableHead key={header.id}>{header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}</TableHead>)}</TableRow>)}</TableHeader><TableBody>{rows.length ? rows.map((row) => <TableRow className={onRowClick ? "cursor-pointer" : undefined} key={row.id} tabIndex={onRowClick ? 0 : undefined} onClick={() => onRowClick?.(row.original)} onKeyDown={(event) => { if (onRowClick && (event.key === "Enter" || event.key === " ")) { event.preventDefault(); onRowClick(row.original) } }}>{row.getVisibleCells().map((cell) => <TableCell className="max-w-0 whitespace-normal break-words align-top" key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>)}</TableRow>) : <TableRow><TableCell className="h-32 whitespace-normal text-center text-muted-foreground" colSpan={columns.length}>{empty ?? "No results."}</TableCell></TableRow>}</TableBody></Table></div>{table.getPageCount() > 1 ? <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><div className="text-xs text-muted-foreground">Page {pagination.pageIndex + 1} of {table.getPageCount()} · {data.length} rows</div><div className="flex items-center gap-2 sm:justify-end"><Select value={String(pagination.pageSize)} onValueChange={(value) => table.setPageSize(Number(value))}><SelectTrigger className="w-20"><SelectValue /></SelectTrigger><SelectContent>{[10, 20, 50].map((size) => <SelectItem key={size} value={String(size)}>{size}</SelectItem>)}</SelectContent></Select><Button aria-label="Previous page" disabled={!table.getCanPreviousPage()} size="icon-sm" variant="outline" onClick={() => table.previousPage()}><ChevronLeft /></Button><Button aria-label="Next page" disabled={!table.getCanNextPage()} size="icon-sm" variant="outline" onClick={() => table.nextPage()}><ChevronRight /></Button></div></div> : null}</div>
}
