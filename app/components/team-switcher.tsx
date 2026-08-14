"use client"

import { CheckIcon, ChevronsUpDownIcon } from "lucide-react"

import { AgentBoxMark } from "@/components/agentbox-mark"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import type { Resource } from "@/lib/platform-schema"
import { getProjectEmoji } from "@/lib/project-emoji"

export function TeamSwitcher({
  projects,
  projectId,
  onProjectChange,
}: {
  projects: Resource[]
  projectId: string
  onProjectChange: (projectId: string) => void
}) {
  const { isMobile } = useSidebar()
  const activeProject =
    projects.find((project) => project.id === projectId) ?? projects[0]

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <span className="flex aspect-square size-8 items-center justify-center rounded-lg border border-sidebar-border bg-sidebar-accent text-sidebar-foreground">
                <AgentBoxMark />
              </span>
              <span className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-semibold">AgentBox</span>
                <span className="truncate text-xs">
                  {activeProject?.name ?? "Control Plane"}
                </span>
              </span>
              <ChevronsUpDownIcon className="ml-auto" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
            align="start"
            side={isMobile ? "bottom" : "right"}
            sideOffset={4}
          >
            <DropdownMenuLabel className="text-xs text-muted-foreground">
              当前项目
            </DropdownMenuLabel>
            <DropdownMenuGroup>
              {projects.map((project) => (
                <DropdownMenuItem
                  key={project.id}
                  onClick={() => onProjectChange(project.id)}
                >
                  <span
                    aria-hidden="true"
                    className="w-4 shrink-0 text-center text-sm leading-none"
                  >
                    {getProjectEmoji(project)}
                  </span>
                  <span className="min-w-0 flex-1 truncate">
                    {project.name}
                  </span>
                  {project.id === activeProject?.id && <CheckIcon />}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
