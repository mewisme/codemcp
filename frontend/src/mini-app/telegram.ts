export type TelegramInset = { top?: number; bottom?: number; left?: number; right?: number }
export type TelegramThemeParams = Record<string, string | undefined>

type TelegramButton = {
  isVisible?: boolean
  isActive?: boolean
  isProgressVisible?: boolean
  setText?: (text: string) => TelegramButton
  show?: () => TelegramButton
  hide?: () => TelegramButton
  enable?: () => TelegramButton
  disable?: () => TelegramButton
  showProgress?: (leaveActive?: boolean) => TelegramButton
  hideProgress?: () => TelegramButton
  setParams?: (params: {
    text?: string
    color?: string
    text_color?: string
    has_shine_effect?: boolean
    position?: "left" | "right" | "top" | "bottom"
    is_active?: boolean
    is_visible?: boolean
  }) => TelegramButton
  onClick?: (callback: () => void) => TelegramButton
  offClick?: (callback: () => void) => TelegramButton
}

type TelegramSettingsButton = {
  show?: () => TelegramSettingsButton
  hide?: () => TelegramSettingsButton
  onClick?: (callback: () => void) => TelegramSettingsButton
  offClick?: (callback: () => void) => TelegramSettingsButton
}

type TelegramStorage = {
  getItem?: (key: string, callback: (error: string | null, value?: string | null) => void) => void
  setItem?: (key: string, value: string, callback?: (error: string | null, stored?: boolean) => void) => void
}

export type TelegramWebApp = {
  initData?: string
  colorScheme?: "light" | "dark"
  themeParams?: TelegramThemeParams
  isActive?: boolean
  viewportHeight?: number
  viewportStableHeight?: number
  safeAreaInset?: TelegramInset
  contentSafeAreaInset?: TelegramInset
  isFullscreen?: boolean
  isVerticalSwipesEnabled?: boolean
  ready?: () => void
  expand?: () => void
  hideKeyboard?: () => void
  setHeaderColor?: (color: string) => void
  setBackgroundColor?: (color: string) => void
  setBottomBarColor?: (color: string) => void
  enableVerticalSwipes?: () => void
  disableVerticalSwipes?: () => void
  requestFullscreen?: () => void
  exitFullscreen?: () => void
  showConfirm?: (message: string, callback?: (confirmed: boolean) => void) => void
  onEvent?: (event: string, callback: (...args: unknown[]) => void) => void
  offEvent?: (event: string, callback: (...args: unknown[]) => void) => void
  BackButton?: TelegramButton
  MainButton?: TelegramButton
  SecondaryButton?: TelegramButton
  SettingsButton?: TelegramSettingsButton
  HapticFeedback?: { impactOccurred?: (style: "light" | "medium" | "heavy" | "rigid" | "soft") => void }
  DeviceStorage?: TelegramStorage
}

declare global {
  interface Window {
    Telegram?: { WebApp?: TelegramWebApp }
  }
}

export type MiniAppPreferences = {
  density: "comfortable" | "compact"
  autoFollow: boolean
  defaultFeed: "runtime" | "executions" | "tools"
}

const preferenceKey = "codemcp_logs_prefs_v1"
const defaultPreferences: MiniAppPreferences = { density: "comfortable", autoFollow: true, defaultFeed: "runtime" }

export function telegramWebApp() {
  return window.Telegram?.WebApp
}

function safeTelegramCall(callback: () => void) {
  try {
    callback()
  } catch {
    // Telegram chrome capabilities are optional and must not break the Mini App.
  }
}

export function initializeTelegramMiniApp() {
  const webApp = telegramWebApp()
  if (!webApp) return () => {}
  const apply = () => {
    applyTelegramTheme(webApp)
    applyTelegramViewport(webApp)
  }
  apply()
  const events = ["themeChanged", "viewportChanged", "safeAreaChanged", "contentSafeAreaChanged", "fullscreenChanged"]
  for (const event of events) safeTelegramCall(() => webApp.onEvent?.(event, apply))
  safeTelegramCall(() => webApp.expand?.())
  safeTelegramCall(() => webApp.ready?.())
  return () => {
    for (const event of events) safeTelegramCall(() => webApp.offEvent?.(event, apply))
  }
}

function applyTelegramTheme(webApp: TelegramWebApp) {
  const root = document.documentElement
  root.classList.toggle("dark", webApp.colorScheme === "dark")
  root.classList.toggle("light", webApp.colorScheme !== "dark")
  const params = webApp.themeParams || {}
  for (const [name, value] of Object.entries(params)) {
    if (!value) continue
    root.style.setProperty("--tg-theme-" + name.replaceAll("_", "-"), value)
  }
  const sharedTheme: Record<string, string | undefined> = {
    "--background": params.bg_color,
    "--foreground": params.text_color,
    "--card": params.section_bg_color || params.secondary_bg_color || params.bg_color,
    "--card-foreground": params.text_color,
    "--popover": params.section_bg_color || params.secondary_bg_color || params.bg_color,
    "--popover-foreground": params.text_color,
    "--primary": params.button_color || params.accent_text_color,
    "--primary-foreground": params.button_text_color || params.bg_color,
    "--secondary": params.secondary_bg_color || params.section_bg_color,
    "--secondary-foreground": params.text_color,
    "--muted": params.secondary_bg_color || params.section_bg_color,
    "--muted-foreground": params.hint_color,
    "--accent": params.secondary_bg_color || params.section_bg_color,
    "--accent-foreground": params.accent_text_color || params.text_color,
    "--destructive": params.destructive_text_color,
    "--border": params.section_separator_color,
    "--input": params.section_separator_color,
    "--ring": params.accent_text_color || params.button_color,
  }
  for (const [name, value] of Object.entries(sharedTheme)) {
    if (value) root.style.setProperty(name, value)
  }
  safeTelegramCall(() => webApp.setHeaderColor?.(params.header_bg_color || params.bg_color || "bg_color"))
  safeTelegramCall(() => webApp.setBackgroundColor?.(params.bg_color || "bg_color"))
  safeTelegramCall(() => webApp.setBottomBarColor?.(params.bottom_bar_bg_color || params.secondary_bg_color || params.bg_color || "bg_color"))
}

function applyTelegramViewport(webApp: TelegramWebApp) {
  const root = document.documentElement
  if (webApp.viewportHeight) root.style.setProperty("--tg-viewport-height", webApp.viewportHeight + "px")
  if (webApp.viewportStableHeight) root.style.setProperty("--tg-viewport-stable-height", webApp.viewportStableHeight + "px")
  applyInset(root, "--tg-safe-area-inset-", webApp.safeAreaInset)
  applyInset(root, "--tg-content-safe-area-inset-", webApp.contentSafeAreaInset)
}

function applyInset(root: HTMLElement, prefix: string, inset?: TelegramInset) {
  if (!inset) return
  for (const side of ["top", "right", "bottom", "left"] as const) {
    const value = inset[side]
    if (typeof value === "number") root.style.setProperty(prefix + side, value + "px")
  }
}

export function bindBackButton(visible: boolean, callback: () => void) {
  const button = telegramWebApp()?.BackButton
  if (!button) return () => {}
  safeTelegramCall(() => button.offClick?.(callback))
  safeTelegramCall(() => {
    if (visible) {
      button.onClick?.(callback)
      button.show?.()
    } else {
      button.hide?.()
    }
  })
  return () => {
    safeTelegramCall(() => button.offClick?.(callback))
    safeTelegramCall(() => button.hide?.())
  }
}

export function bindMainButton(text: string, visible: boolean, callback: () => void) {
  return bindBottomButton(telegramWebApp()?.MainButton, text, visible, callback)
}

export function bindSecondaryButton(text: string, visible: boolean, callback: () => void) {
  return bindBottomButton(telegramWebApp()?.SecondaryButton, text, visible, callback, { position: "left" })
}

function bindBottomButton(
  button: TelegramButton | undefined,
  text: string,
  visible: boolean,
  callback: () => void,
  options: { active?: boolean; progress?: boolean; position?: "left" | "right" | "top" | "bottom"; shine?: boolean } = {}
) {
  if (!button) return () => {}
  const active = options.active !== false
  safeTelegramCall(() => button.offClick?.(callback))
  safeTelegramCall(() => {
    if (visible) {
      button.setParams?.({
        text,
        is_active: active,
        is_visible: true,
        has_shine_effect: options.shine === true,
        position: options.position,
      })
      button.setText?.(text)
      if (active) button.enable?.()
      else button.disable?.()
      if (options.progress) button.showProgress?.(true)
      else button.hideProgress?.()
      button.onClick?.(callback)
      button.show?.()
    } else {
      button.hideProgress?.()
      button.hide?.()
    }
  })
  return () => {
    safeTelegramCall(() => button.offClick?.(callback))
    safeTelegramCall(() => button.hideProgress?.())
    safeTelegramCall(() => button.hide?.())
  }
}

export function bindMainButtonState(
  text: string,
  visible: boolean,
  callback: () => void,
  options: { active?: boolean; progress?: boolean; shine?: boolean } = {}
) {
  return bindBottomButton(telegramWebApp()?.MainButton, text, visible, callback, options)
}

export function bindSettingsButton(callback: () => void) {
  const button = telegramWebApp()?.SettingsButton
  if (!button) return () => {}
  safeTelegramCall(() => button.offClick?.(callback))
  safeTelegramCall(() => {
    button.onClick?.(callback)
    button.show?.()
  })
  return () => {
    safeTelegramCall(() => button.offClick?.(callback))
    safeTelegramCall(() => button.hide?.())
  }
}

export function telegramNativeControls() {
  const webApp = telegramWebApp()
  return {
    backButton: Boolean(webApp?.BackButton),
    mainButton: Boolean(webApp?.MainButton),
    secondaryButton: Boolean(webApp?.SecondaryButton),
    settingsButton: Boolean(webApp?.SettingsButton),
  }
}

export function bindTelegramActivity(callback: (active: boolean) => void) {
  const webApp = telegramWebApp()
  if (!webApp) return () => {}
  const activated = () => callback(true)
  const deactivated = () => callback(false)
  safeTelegramCall(() => webApp.onEvent?.("activated", activated))
  safeTelegramCall(() => webApp.onEvent?.("deactivated", deactivated))
  return () => {
    safeTelegramCall(() => webApp.offEvent?.("activated", activated))
    safeTelegramCall(() => webApp.offEvent?.("deactivated", deactivated))
  }
}

export function setTelegramVerticalSwipesEnabled(enabled: boolean) {
  const webApp = telegramWebApp()
  if (!webApp) return
  safeTelegramCall(() => {
    if (enabled) webApp.enableVerticalSwipes?.()
    else webApp.disableVerticalSwipes?.()
  })
}

export async function confirmTelegramAction(message: string) {
  const webApp = telegramWebApp()
  if (!webApp?.showConfirm) return true
  return await new Promise<boolean>((resolve) => {
    try {
      webApp.showConfirm?.(message, (confirmed) => resolve(Boolean(confirmed)))
    } catch {
      resolve(true)
    }
  })
}

export function hideTelegramKeyboard() {
  try {
    telegramWebApp()?.hideKeyboard?.()
  } catch {
    // Cosmetic capability only.
  }
}

export function telegramHaptic(style: "light" | "medium" = "light") {
  try {
    telegramWebApp()?.HapticFeedback?.impactOccurred?.(style)
  } catch {
    // Cosmetic capability only.
  }
}

export function toggleTelegramFullscreen() {
  const webApp = telegramWebApp()
  if (!webApp) return
  try {
    if (webApp.isFullscreen) webApp.exitFullscreen?.()
    else webApp.requestFullscreen?.()
  } catch {
    // Fullscreen is optional and must not affect navigation.
  }
}

export async function loadMiniAppPreferences(): Promise<MiniAppPreferences> {
  const webApp = telegramWebApp()
  const device = webApp?.DeviceStorage
  if (device?.getItem) {
    try {
      const value = await new Promise<string | null>((resolve) => {
        device.getItem?.(preferenceKey, (error, stored) => resolve(error ? null : stored || null))
      })
      return parsePreferences(value) || defaultPreferences
    } catch {
      return defaultPreferences
    }
  }
  return defaultPreferences
}

export async function saveMiniAppPreferences(preferences: MiniAppPreferences) {
  const value = JSON.stringify(preferences)
  if (value.length > 512) return
  const device = telegramWebApp()?.DeviceStorage
  if (device?.setItem) {
    try {
      await new Promise<void>((resolve) => {
        device.setItem?.(preferenceKey, value, () => resolve())
      })
    } catch {
      // Presentation preferences are optional.
    }
  }
}

function parsePreferences(raw?: string | null): MiniAppPreferences | null {
  if (!raw || raw.length > 512) return null
  try {
    const value = JSON.parse(raw) as Partial<MiniAppPreferences>
    const density = value.density === "compact" ? "compact" : "comfortable"
    const defaultFeed = value.defaultFeed === "executions" || value.defaultFeed === "tools" ? value.defaultFeed : "runtime"
    return { density, defaultFeed, autoFollow: value.autoFollow !== false }
  } catch {
    return null
  }
}
