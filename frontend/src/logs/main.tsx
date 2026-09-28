import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import "../index.css"
import { TooltipProvider } from "@/components/ui/tooltip"
import { LogsMiniApp } from "@/logs/app"

createRoot(document.getElementById("logs-root")!).render(
  <StrictMode>
    <TooltipProvider>
      <LogsMiniApp />
    </TooltipProvider>
  </StrictMode>
)
