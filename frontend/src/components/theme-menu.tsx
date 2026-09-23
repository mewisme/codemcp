import { Laptop, Moon, Sun } from "lucide-react"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { useTheme } from "@/components/theme-provider"

export function ThemeMenu() {
  const { theme, setTheme } = useTheme()
  const Icon = theme === "dark" ? Moon : theme === "light" ? Sun : Laptop
  return <DropdownMenu><DropdownMenuTrigger asChild><Button aria-label="Change theme" size="icon-sm" variant="ghost"><Icon /></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem onClick={() => setTheme("light")}><Sun />Light</DropdownMenuItem><DropdownMenuItem onClick={() => setTheme("dark")}><Moon />Dark</DropdownMenuItem><DropdownMenuItem onClick={() => setTheme("system")}><Laptop />System</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
}
