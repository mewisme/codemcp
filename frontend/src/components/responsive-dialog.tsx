import type { ReactNode } from "react"
import { useIsMobile } from "@/hooks/use-mobile"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Drawer, DrawerContent, DrawerDescription, DrawerFooter, DrawerHeader, DrawerTitle } from "@/components/ui/drawer"
import { ScrollArea } from "@/components/ui/scroll-area"
import { cn } from "@/lib/utils"

type Props = { open: boolean; onOpenChange: (open: boolean) => void; title: string; description?: string; children: ReactNode; footer?: ReactNode; className?: string; wide?: boolean; scrollbars?: "vertical" | "horizontal" | "both" }

export function ResponsiveDialog({ open, onOpenChange, title, description, children, footer, className, wide = false, scrollbars = "vertical" }: Props) {
  const mobile = useIsMobile()
  if (mobile) {
    const overflow = scrollbars === "both"
      ? "overflow-auto"
      : scrollbars === "horizontal"
        ? "overflow-x-auto overflow-y-hidden"
        : "overflow-x-hidden overflow-y-auto"
    return (
      <Drawer open={open} onOpenChange={onOpenChange}>
        <DrawerContent
          className="min-h-0 min-w-0 overflow-hidden"
          style={{
            height: "min(92dvh, var(--tg-viewport-height, var(--tg-viewport-stable-height, 92dvh)))",
            maxHeight: "min(92dvh, var(--tg-viewport-height, var(--tg-viewport-stable-height, 92dvh)))",
          }}
        >
          <DrawerHeader className="shrink-0"><DrawerTitle>{title}</DrawerTitle>{description ? <DrawerDescription>{description}</DrawerDescription> : null}</DrawerHeader>
          <div
            data-vaul-no-drag
            className={cn("min-h-0 min-w-0 max-w-full flex-1 overscroll-contain", overflow)}
            style={{ WebkitOverflowScrolling: "touch" }}
          >
            <div className={cn("min-w-0 max-w-full px-4 pb-[max(1rem,var(--tg-content-safe-area-inset-bottom,0px))]", className)}>{children}</div>
          </div>
          {footer ? <DrawerFooter className="shrink-0 border-t">{footer}</DrawerFooter> : null}
        </DrawerContent>
      </Drawer>
    )
  }
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className={cn("min-w-0 overflow-hidden sm:max-w-2xl", wide && "sm:max-w-4xl")} style={{ maxHeight: "min(85dvh, var(--tg-viewport-stable-height, 85dvh))" }}><DialogHeader className="min-w-0"><DialogTitle>{title}</DialogTitle>{description ? <DialogDescription>{description}</DialogDescription> : null}</DialogHeader><ScrollArea className="min-h-0 min-w-0 max-w-full overflow-hidden" style={{ maxHeight: "min(65dvh, calc(var(--tg-viewport-stable-height, 65dvh) - 8rem))" }} scrollbars={scrollbars}><div className={cn("min-w-0 max-w-full pr-3", className)}>{children}</div></ScrollArea>{footer ? <DialogFooter>{footer}</DialogFooter> : null}</DialogContent></Dialog>
}
