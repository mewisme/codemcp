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

const telegramWebAppScriptURL = "https://telegram.org/js/telegram-web-app.js"
const telegramWebAppScriptSelector = "script[data-codemcp-telegram-web-app]"
const telegramWebAppLoadTimeout = 5_000

export async function initializeTelegramMiniApp() {
  const webApp = await resolveTelegramWebApp()
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

async function resolveTelegramWebApp() {
  if (window.Telegram?.WebApp) return window.Telegram.WebApp

  let script = document.querySelector<HTMLScriptElement>(telegramWebAppScriptSelector)
  if (!script) {
    script = document.createElement("script")
    script.src = telegramWebAppScriptURL
    script.async = true
    script.dataset.codemcpTelegramWebApp = "true"
    document.head.append(script)
  }

  return await new Promise<TelegramWebApp>((resolve, reject) => {
    const timeout = window.setTimeout(() => {
      cleanup()
      reject(new Error("Telegram Mini App context is unavailable"))
    }, telegramWebAppLoadTimeout)

    const loaded = () => {
      const webApp = window.Telegram?.WebApp
      cleanup()
      if (webApp) {
        resolve(webApp)
      } else {
        reject(new Error("Telegram Mini App context is unavailable"))
      }
    }

    const failed = () => {
      cleanup()
      script?.remove()
      reject(new Error("Telegram Mini App context is unavailable"))
    }

    const cleanup = () => {
      window.clearTimeout(timeout)
      script?.removeEventListener("load", loaded)
      script?.removeEventListener("error", failed)
    }

    script.addEventListener("load", loaded, { once: true })
    script.addEventListener("error", failed, { once: true })
  })
}
