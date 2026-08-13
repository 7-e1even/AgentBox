"use client"

import {
  BotIcon,
  BoxesIcon,
  BoxIcon,
  FolderKanbanIcon,
  ImagesIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  PlugZapIcon,
  ServerIcon,
  SparklesIcon,
  UsersIcon,
  WorkflowIcon,
} from "lucide-react"

import type { AppSection } from "@/components/agent-management"
import { NavMain, type NavigationGroup } from "@/components/nav-main"
import { TeamSwitcher } from "@/components/team-switcher"
import {
  Sidebar,
  SidebarContent,
  SidebarHeader,
  SidebarRail,
} from "@/components/ui/sidebar"
import type { Resource } from "@/lib/platform-schema"
import type { ManagedUser } from "@/lib/user-schema"

export function AppSidebar({
  section,
  onSectionChange,
  currentUser,
  projects,
  projectId,
  onProjectChange,
  ...props
}: React.ComponentProps<typeof Sidebar> & {
  section: AppSection
  onSectionChange: (section: AppSection) => void
  currentUser: ManagedUser
  projects: Resource[]
  projectId: string
  onProjectChange: (projectId: string) => void
}) {
  const groups: NavigationGroup[] = [
    {
      title: "工作台",
      items: [
        { title: "概览", section: "overview", icon: LayoutDashboardIcon },
        { title: "环境模板", section: "runtimes", icon: BoxesIcon },
        { title: "沙箱", section: "sandboxes", icon: BoxIcon },
        { title: "智能体", section: "agents", icon: BotIcon },
      ],
    },
    ...(currentUser.preferences.showCapabilities
      ? [
          {
            title: "能力配置",
            items: [
              { title: "Skills", section: "skills", icon: SparklesIcon },
              { title: "MCP Servers", section: "mcp", icon: PlugZapIcon },
              { title: "模型服务", section: "access", icon: KeyRoundIcon },
            ],
          } satisfies NavigationGroup,
        ]
      : []),
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
    ...(currentUser.preferences.showGovernance
      ? [
          {
            title: "组织与治理",
            items: [
              { title: "项目", section: "projects", icon: FolderKanbanIcon },
              { title: "自动化", section: "automations", icon: WorkflowIcon },
              ...(currentUser.role === "admin"
                ? ([
                    { title: "用户管理", section: "users", icon: UsersIcon },
                  ] satisfies NavigationGroup["items"])
                : []),
            ],
          } satisfies NavigationGroup,
        ]
      : []),
  ]

  return (
    <Sidebar collapsible="icon" variant="inset" {...props}>
      <SidebarHeader>
        <TeamSwitcher
          projects={projects}
          projectId={projectId}
          onProjectChange={onProjectChange}
        />
      </SidebarHeader>
      <SidebarContent>
        <NavMain
          groups={groups}
          section={section}
          onSectionChange={onSectionChange}
        />
      </SidebarContent>
      <SidebarRail />
    </Sidebar>
  )
}
