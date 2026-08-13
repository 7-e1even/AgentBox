"use client"

import { toast as sonnerToast } from "sonner"

export const appToast = {
  success(...args: Parameters<typeof sonnerToast.success>) {
    if (
      typeof document !== "undefined" &&
      document.documentElement.dataset.successNotifications === "false"
    ) {
      return
    }
    return sonnerToast.success(...args)
  },
  error(...args: Parameters<typeof sonnerToast.error>) {
    return sonnerToast.error(...args)
  },
}
