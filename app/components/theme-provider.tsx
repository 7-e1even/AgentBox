"use client"

import * as React from "react"
import { ThemeProvider as NextThemesProvider, useTheme } from "next-themes"

const ACCENT_STORAGE_KEY = "agentbox-accent-theme"
const ACCENT_CHANGE_EVENT = "agentbox-accent-theme-change"
const ACCENT_THEMES = ["neutral", "blue", "green", "orange"] as const

type AccentTheme = (typeof ACCENT_THEMES)[number]

const AccentThemeContext = React.createContext<{
  accentTheme: AccentTheme
  setAccentTheme: (theme: AccentTheme) => void
} | null>(null)

function ThemeProvider({
  children,
  ...props
}: React.ComponentProps<typeof NextThemesProvider>) {
  return (
    <NextThemesProvider
      attribute="class"
      defaultTheme="light"
      enableSystem
      disableTransitionOnChange
      {...props}
    >
      <AccentThemeProvider>
        <ThemeHotkey />
        {children}
      </AccentThemeProvider>
    </NextThemesProvider>
  )
}

function isAccentTheme(value: string | null): value is AccentTheme {
  return ACCENT_THEMES.includes(value as AccentTheme)
}

function getAccentThemeSnapshot(): AccentTheme {
  const storedTheme = window.localStorage.getItem(ACCENT_STORAGE_KEY)
  return isAccentTheme(storedTheme) ? storedTheme : "neutral"
}

function getAccentThemeServerSnapshot(): AccentTheme {
  return "neutral"
}

function subscribeToAccentTheme(onStoreChange: () => void) {
  window.addEventListener("storage", onStoreChange)
  window.addEventListener(ACCENT_CHANGE_EVENT, onStoreChange)
  return () => {
    window.removeEventListener("storage", onStoreChange)
    window.removeEventListener(ACCENT_CHANGE_EVENT, onStoreChange)
  }
}

function AccentThemeProvider({ children }: { children: React.ReactNode }) {
  const accentTheme = React.useSyncExternalStore(
    subscribeToAccentTheme,
    getAccentThemeSnapshot,
    getAccentThemeServerSnapshot
  )

  const applyAccentTheme = React.useCallback((theme: AccentTheme) => {
    document.documentElement.dataset.accent = theme
  }, [])

  React.useEffect(() => {
    applyAccentTheme(accentTheme)
  }, [accentTheme, applyAccentTheme])

  const setAccentTheme = React.useCallback(
    (theme: AccentTheme) => {
      window.localStorage.setItem(ACCENT_STORAGE_KEY, theme)
      applyAccentTheme(theme)
      window.dispatchEvent(new Event(ACCENT_CHANGE_EVENT))
    },
    [applyAccentTheme]
  )

  return (
    <AccentThemeContext value={{ accentTheme, setAccentTheme }}>
      {children}
    </AccentThemeContext>
  )
}

function useAccentTheme() {
  const context = React.use(AccentThemeContext)
  if (!context) {
    throw new Error("useAccentTheme must be used within ThemeProvider")
  }
  return context
}

function isTypingTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) {
    return false
  }

  return (
    target.isContentEditable ||
    target.tagName === "INPUT" ||
    target.tagName === "TEXTAREA" ||
    target.tagName === "SELECT"
  )
}

function ThemeHotkey() {
  const { resolvedTheme, setTheme } = useTheme()

  React.useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented || event.repeat) {
        return
      }

      if (event.metaKey || event.ctrlKey || event.altKey) {
        return
      }

      if (event.key.toLowerCase() !== "d") {
        return
      }

      if (isTypingTarget(event.target)) {
        return
      }

      setTheme(resolvedTheme === "dark" ? "light" : "dark")
    }

    window.addEventListener("keydown", onKeyDown)

    return () => {
      window.removeEventListener("keydown", onKeyDown)
    }
  }, [resolvedTheme, setTheme])

  return null
}

export { ThemeProvider, useAccentTheme }
export type { AccentTheme }
