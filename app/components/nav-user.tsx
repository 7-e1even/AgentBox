"use client"

import { useSyncExternalStore } from "react"
import {
  BadgeCheckIcon,
  LogOutIcon,
  MoonIcon,
  SettingsIcon,
  SunIcon,
  UsersIcon,
} from "lucide-react"
import { useTheme } from "next-themes"

import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { ManagedUser } from "@/lib/user-schema"

export function TopbarAccountActions({
  user,
  onSettings,
  onManageUsers,
  onLogout,
}: {
  user: ManagedUser
  onSettings: () => void
  onManageUsers: () => void
  onLogout: () => void
}) {
  const { resolvedTheme, setTheme } = useTheme()
  const mounted = useSyncExternalStore(
    noopSubscribe,
    () => true,
    () => false
  )
  const fallback = initials(user.name)
  const isDark = mounted && resolvedTheme === "dark"
  const nextTheme = isDark ? "light" : "dark"

  return (
    <div className="flex items-center gap-1">
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={nextTheme === "dark" ? "切换到深色模式" : "切换到浅色模式"}
        onClick={() => setTheme(nextTheme)}
      >
        {isDark ? <SunIcon /> : <MoonIcon />}
      </Button>

      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="打开设置"
        onClick={onSettings}
      >
        <SettingsIcon />
      </Button>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-lg"
            className="rounded-full"
            aria-label={`${user.name} 账户菜单`}
          >
            <Avatar className="size-8">
              <AvatarFallback>{fallback}</AvatarFallback>
            </Avatar>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-64">
          <DropdownMenuLabel className="font-normal">
            <div className="flex flex-col gap-0.5 px-1 py-1">
              <span className="truncate text-sm font-semibold">
                {user.name}
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {user.email}
              </span>
            </div>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem onClick={onSettings}>
              <SettingsIcon />
              设置
            </DropdownMenuItem>
            <DropdownMenuItem disabled>
              <BadgeCheckIcon />
              {roleLabel(user.role)}
            </DropdownMenuItem>
            {user.role === "admin" ? (
              <DropdownMenuItem onClick={onManageUsers}>
                <UsersIcon />
                用户管理
              </DropdownMenuItem>
            ) : null}
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem variant="destructive" onClick={onLogout}>
              <LogOutIcon />
              退出登录
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

function noopSubscribe() {
  return () => undefined
}

function initials(name: string) {
  return name
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase()
}

function roleLabel(role: ManagedUser["role"]) {
  if (role === "admin") return "管理员"
  if (role === "operator") return "运维人员"
  return "只读成员"
}
