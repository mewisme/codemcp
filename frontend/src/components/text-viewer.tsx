import { ScrollArea } from "@/components/ui/scroll-area"

export function TextViewer({ value, maxHeight = "36rem" }: { value: string; maxHeight?: string | null }) { return <div className="min-w-0 max-w-full overflow-hidden rounded-lg border bg-background"><ScrollArea className="min-w-0 max-w-full" scrollbars="both" style={maxHeight ? { maxHeight } : undefined}><pre className="m-0 w-max min-w-full whitespace-pre p-4 font-mono text-[13px] leading-relaxed"><code>{value}</code></pre></ScrollArea></div> }
