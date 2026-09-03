"use client"

import { createContext, useContext } from "react"

export const unsavedNavigationMessage = "存在未保存的文件，确定要离开工作区吗？"

export type NavigationBlocker = {
  confirmNavigation: () => boolean
  isBlocked: () => boolean
  setBlocked: (blocked: boolean) => void
}

export const NavigationBlockerContext = createContext<NavigationBlocker | null>(
  null
)

export function useNavigationBlocker() {
  const blocker = useContext(NavigationBlockerContext)
  if (!blocker) {
    throw new Error(
      "useNavigationBlocker must be used inside NavigationBlockerContext"
    )
  }
  return blocker
}

const historyIndexKey = "__agentboxHistoryIndex"

export function historyEntryIndex(state: unknown) {
  if (!state || typeof state !== "object") return null
  const value = (state as Record<string, unknown>)[historyIndexKey]
  return Number.isSafeInteger(value) && Number(value) >= 0
    ? Number(value)
    : null
}

export function withHistoryEntryIndex(state: unknown, index: number) {
  const current =
    state && typeof state === "object" ? (state as Record<string, unknown>) : {}
  return { ...current, [historyIndexKey]: index }
}

export function historyRestoreDelta(currentIndex: number, targetIndex: number) {
  return currentIndex - targetIndex
}
