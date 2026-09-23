import { ScrollArea } from "@/components/ui/scroll-area"

export function JsonViewer({ value, maxHeight = "24rem" }: { value: unknown; maxHeight?: string | null }) {
  const text = JSON.stringify(value ?? null, null, 2)
  return <div className="min-w-0 max-w-full overflow-hidden rounded-lg border bg-background"><ScrollArea className="min-w-0 max-w-full overflow-hidden" scrollbars="both" style={maxHeight ? { maxHeight } : undefined}><pre className="m-0 w-max min-w-full whitespace-pre p-4 font-mono text-[13px] leading-relaxed"><code>{text}</code></pre></ScrollArea></div>
}
