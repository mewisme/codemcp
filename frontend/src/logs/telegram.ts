type TelegramWebApp = {
  initData: string
  colorScheme?: "light" | "dark"
  ready: () => void
  expand?: () => void
  onEvent?: (event: "themeChanged", callback: () => void) => void
  offEvent?: (event: "themeChanged", callback: () => void) => void
}

declare global {
  interface Window {
    Telegram?: { WebApp?: TelegramWebApp }
  }
}

export function initializeTelegramLogsApp() {
  const webApp = window.Telegram?.WebApp
  if (!webApp || !webApp.initData) {
    throw new Error("Telegram Mini App context is unavailable")
  }
  const applyTheme = () => {
    document.documentElement.classList.toggle("dark", webApp.colorScheme === "dark")
    document.documentElement.classList.toggle("light", webApp.colorScheme !== "dark")
  }
  applyTheme()
  webApp.onEvent?.("themeChanged", applyTheme)
  webApp.expand?.()
  webApp.ready()
  return {
    webApp,
    dispose: () => webApp.offEvent?.("themeChanged", applyTheme),
  }
}
