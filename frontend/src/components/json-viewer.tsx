import { ScrollArea } from "@/components/ui/scroll-area"

export function JsonViewer({ value, maxHeight = "24rem", nativeUnbounded = false }: { value: unknown; maxHeight?: string | null; nativeUnbounded?: boolean }) {
  const text = JSON.stringify(value ?? null, null, 2)
  const content = <pre className="m-0 w-max min-w-full whitespace-pre p-4 font-mono text-[13px] leading-relaxed"><code>{text}</code></pre>
  return (
    <div className="min-w-0 max-w-full overflow-hidden rounded-lg border bg-background">
      {!maxHeight && nativeUnbounded
        ? <div className="min-w-0 max-w-full overflow-x-auto overscroll-x-contain" style={{ WebkitOverflowScrolling: "touch" }}>{content}</div>
        : <ScrollArea className="min-w-0 max-w-full overflow-hidden" scrollbars="both" style={maxHeight ? { maxHeight } : undefined}>{content}</ScrollArea>}
    </div>
  )
}
