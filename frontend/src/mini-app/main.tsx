import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import "../index.css"
import { TooltipProvider } from "@/components/ui/tooltip"
import { MiniApp } from "@/mini-app/app"

createRoot(document.getElementById("mini-app-root")!).render(
  <StrictMode>
    <TooltipProvider>
      <MiniApp />
    </TooltipProvider>
  </StrictMode>
)
