"use client"

import type { LucideIcon } from "lucide-react"
import Link from "next/link"
import { usePathname } from "next/navigation"

import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import { appSectionPath, type AppSection } from "@/lib/app-section"
import { useNavigationBlocker } from "@/lib/navigation-blocker"

export type NavigationGroup = {
  title: string
  items: Array<{
    title: string
    section: AppSection
    icon: LucideIcon
  }>
}

export function NavMain({ groups }: { groups: NavigationGroup[] }) {
  const { setOpenMobile } = useSidebar()
  const { confirmNavigation } = useNavigationBlocker()
  const pathname = usePathname()
  return groups.map((group) => (
    <SidebarGroup key={group.title}>
      <SidebarGroupLabel>{group.title}</SidebarGroupLabel>
      <SidebarGroupContent className="flex flex-col gap-2">
        <SidebarMenu className="gap-1">
          {group.items.map((item) => (
            <SidebarMenuItem key={item.section}>
              <SidebarMenuButton
                asChild
                isActive={isActivePath(pathname, appSectionPath(item.section))}
                tooltip={item.title}
              >
                <Link
                  href={appSectionPath(item.section)}
                  aria-current={
                    isActivePath(pathname, appSectionPath(item.section))
                      ? "page"
                      : undefined
                  }
                  onNavigate={(event) => {
                    if (!confirmNavigation()) event.preventDefault()
                  }}
                  onClick={() => setOpenMobile(false)}
                >
                  <item.icon />
                  <span>{item.title}</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  ))
}

function isActivePath(pathname: string, sectionPath: string) {
  return pathname === sectionPath || pathname.startsWith(`${sectionPath}/`)
}
