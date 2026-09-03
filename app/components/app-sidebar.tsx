"use client"

import {
  BoxIcon,
  FolderKanbanIcon,
  ImagesIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  LayoutTemplateIcon,
  NetworkIcon,
  PackageIcon,
  PlugZapIcon,
  ScrollTextIcon,
  ServerIcon,
  SparklesIcon,
  UsersIcon,
  WorkflowIcon,
} from "lucide-react"

import { NavMain, type NavigationGroup } from "@/components/nav-main"
import { TeamSwitcher } from "@/components/team-switcher"
import {
  Sidebar,
  SidebarContent,
  SidebarHeader,
} from "@/components/ui/sidebar"
import type { Resource } from "@/lib/platform-schema"
import type { ManagedUser } from "@/lib/user-schema"

export function AppSidebar({
  currentUser,
  projects,
  projectId,
  onProjectChange,
  ...props
}: React.ComponentProps<typeof Sidebar> & {
  currentUser: ManagedUser
  projects: Resource[]
  projectId: string
  onProjectChange: (projectId: string) => void
}) {
  const groups: NavigationGroup[] = [
    {
      title: "工作区",
      items: [
        { title: "概览", section: "overview", icon: LayoutDashboardIcon },
        { title: "项目", section: "projects", icon: FolderKanbanIcon },
        { title: "沙箱", section: "sandboxes", icon: BoxIcon },
        { title: "沙箱模板", section: "runtimes", icon: LayoutTemplateIcon },
        { title: "自动化", section: "automations", icon: WorkflowIcon },
      ],
    },
    {
      title: "配置",
      items: [
        { title: "沙箱扩展", section: "extensions", icon: PackageIcon },
        ...(currentUser.preferences.showCapabilities
          ? ([
              { title: "模型服务", section: "access", icon: KeyRoundIcon },
              { title: "网络代理", section: "proxies", icon: NetworkIcon },
              { title: "Skills", section: "skills", icon: SparklesIcon },
              { title: "MCP Servers", section: "mcp", icon: PlugZapIcon },
            ] satisfies NavigationGroup["items"])
          : []),
      ],
    },
    ...(currentUser.preferences.showInfrastructure
      ? [
          {
            title: "基础设施",
            items: [
              { title: "服务器", section: "servers", icon: ServerIcon },
              { title: "镜像", section: "images", icon: ImagesIcon },
            ],
          } satisfies NavigationGroup,
        ]
      : []),
    ...(currentUser.preferences.showGovernance && currentUser.role === "admin"
      ? [
          {
            title: "管理",
            items: [
              { title: "用户管理", section: "users", icon: UsersIcon },
              { title: "日志", section: "logs", icon: ScrollTextIcon },
            ],
          } satisfies NavigationGroup,
        ]
      : []),
  ]

  return (
    <Sidebar collapsible="offcanvas" variant="inset" {...props}>
      <SidebarHeader>
        <TeamSwitcher
          projects={projects}
          projectId={projectId}
          onProjectChange={onProjectChange}
        />
      </SidebarHeader>
      <SidebarContent>
        <NavMain groups={groups} />
      </SidebarContent>
    </Sidebar>
  )
}
