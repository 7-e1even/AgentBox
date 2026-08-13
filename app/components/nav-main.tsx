"use client"

import type { LucideIcon } from "lucide-react"

import type { AppSection } from "@/components/agent-management"
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"

export type NavigationGroup = {
  title: string
  items: Array<{
    title: string
    section: AppSection
    icon: LucideIcon
  }>
}

export function NavMain({
  groups,
  section,
  onSectionChange,
}: {
  groups: NavigationGroup[]
  section: AppSection
  onSectionChange: (section: AppSection) => void
}) {
  const { setOpenMobile } = useSidebar()
  return groups.map((group) => (
    <SidebarGroup key={group.title}>
      <SidebarGroupLabel>{group.title}</SidebarGroupLabel>
      <SidebarMenu>
        {group.items.map((item) => (
          <SidebarMenuItem key={item.section}>
            <SidebarMenuButton
              isActive={section === item.section}
              tooltip={item.title}
              onClick={() => {
                onSectionChange(item.section)
                setOpenMobile(false)
              }}
            >
              <item.icon />
              <span>{item.title}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        ))}
      </SidebarMenu>
    </SidebarGroup>
  ))
}
