"use client"

import { createContext, useContext, type ReactNode } from "react"

import { TopbarAccountActions } from "@/components/nav-user"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import type { ManagedUser } from "@/lib/user-schema"

type SiteHeaderContextValue = {
  user: ManagedUser
  onSettings: () => void
  onManageUsers: () => void
  onLogout: () => void
}

const SiteHeaderContext = createContext<SiteHeaderContextValue | null>(null)

export function useSiteHeaderUser() {
  return useContext(SiteHeaderContext)?.user ?? null
}

export function SiteHeaderProvider({
  children,
  ...value
}: SiteHeaderContextValue & { children: ReactNode }) {
  return (
    <SiteHeaderContext.Provider value={value}>
      {children}
    </SiteHeaderContext.Provider>
  )
}

export function SiteHeader({
  title,
  count,
  center,
  action,
}: {
  title: ReactNode
  count?: number
  center?: ReactNode
  action?: ReactNode
}) {
  const account = useContext(SiteHeaderContext)
  return (
    <header className="relative flex h-(--header-height) shrink-0 items-center gap-2 border-b transition-[width,height] ease-linear">
      <div className="flex min-w-0 flex-1 items-center gap-1 px-4 lg:gap-2 lg:px-6">
        <SidebarTrigger className="-ml-1" />
        <Separator
          orientation="vertical"
          className="mx-2 data-[orientation=vertical]:h-4"
        />
        <h1 className="truncate text-sm font-semibold sm:text-base">{title}</h1>
        {typeof count === "number" && (
          <Badge
            variant="secondary"
            className="hidden rounded-full sm:inline-flex"
          >
            {count}
          </Badge>
        )}
      </div>
      {center ? (
        <div className="pointer-events-none absolute left-1/2 hidden w-[min(28rem,34vw)] -translate-x-1/2 lg:block">
          {center}
        </div>
      ) : null}
      <div className="flex shrink-0 items-center gap-2 px-4 lg:px-6">
        {action}
        {action && account ? (
          <Separator orientation="vertical" className="h-5" />
        ) : null}
        {account ? (
          <TopbarAccountActions
            user={account.user}
            onSettings={account.onSettings}
            onManageUsers={account.onManageUsers}
            onLogout={account.onLogout}
          />
        ) : null}
      </div>
    </header>
  )
}
