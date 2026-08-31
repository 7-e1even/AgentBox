"use client"

import { useEffect, useMemo, useSyncExternalStore } from "react"
import { PollingController } from "@/lib/polling-controller"

export function observePollingVisibility<T>(controller: PollingController<T>) {
  const update = () => controller.pause(document.hidden || !navigator.onLine)
  update()
  document.addEventListener("visibilitychange", update)
  window.addEventListener("online", update)
  window.addEventListener("offline", update)
  return () => {
    document.removeEventListener("visibilitychange", update)
    window.removeEventListener("online", update)
    window.removeEventListener("offline", update)
  }
}

export function usePolling<T>({
  queryKey,
  load,
  enabled = true,
  interval = false,
  initialData,
  initialDelay = 0,
}: {
  queryKey: string
  load: (signal: AbortSignal) => Promise<T>
  enabled?: boolean
  interval?: number | false | ((data: T | undefined) => number | false)
  initialData?: T
  initialDelay?: number
}) {
  const controller = useMemo(() => {
    // Each query owns its controller, so late results cannot cross query keys.
    void queryKey
    return new PollingController<T>(initialData)
  }, [queryKey, initialData])
  const state = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot
  )

  useEffect(
    () => controller.configure(load, interval),
    [controller, load, interval]
  )
  useEffect(() => {
    const stopObserving = observePollingVisibility(controller)
    const timer = enabled
      ? setTimeout(() => controller.start(), initialDelay)
      : null
    return () => {
      if (timer !== null) clearTimeout(timer)
      controller.stop()
      stopObserving()
    }
  }, [controller, enabled, initialDelay])

  return { ...state, refresh: controller.refresh, setData: controller.setData }
}
