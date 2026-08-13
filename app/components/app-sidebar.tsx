"use client"

import {
  BotIcon,
  BoxIcon,
  BoxesIcon,
  ChevronsUpDownIcon,
  Clock3Icon,
  ContainerIcon,
  FolderGit2Icon,
  GaugeIcon,
  KeyRoundIcon,
  PlugZapIcon,
  SparklesIcon,
  WebhookIcon,
} from "lucide-react"

import type { AppSection } from "@/components/agent-management"
import type { Resource } from "@/lib/platform-schema"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar"

const navigation = [
  {
    label: "工作区",
    items: [
      { id: "overview" as const, label: "概览", icon: GaugeIcon },
      { id: "projects" as const, label: "Projects", icon: FolderGit2Icon },
      { id: "agents" as const, label: "Agents", icon: BotIcon },
    ],
  },
  {
    label: "运行环境",
    items: [
      { id: "runtimes" as const, label: "Runtimes", icon: ContainerIcon },
      { id: "sandboxes" as const, label: "Sandboxes", icon: BoxIcon },
    ],
  },
  {
    label: "能力",
    items: [
      { id: "skills" as const, label: "Skills", icon: SparklesIcon },
      { id: "mcp" as const, label: "MCP Servers", icon: PlugZapIcon },
    ],
  },
  {
    label: "触发器",
    items: [
      { id: "schedules" as const, label: "Schedules", icon: Clock3Icon },
      { id: "webhooks" as const, label: "Webhooks", icon: WebhookIcon },
    ],
  },
  {
    label: "安全",
    items: [
      { id: "variables" as const, label: "Variables", icon: KeyRoundIcon },
    ],
  },
]

export function AppSidebar({
  section,
  onSectionChange,
  counts,
  projects,
  projectId,
  onProjectChange,
}: {
  section: AppSection
  onSectionChange: (section: AppSection) => void
  counts: Record<AppSection, number>
  projects: Resource[]
  projectId: string
  onProjectChange: (projectId: string) => void
}) {
  const currentProject =
    projects.find((item) => item.id === projectId) ?? projects[0]
  return (
    <Sidebar collapsible="icon" variant="inset">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <SidebarMenuButton size="lg" tooltip="切换项目">
                  <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                    <BoxesIcon />
                  </div>
                  <div className="grid flex-1 text-left text-sm leading-tight">
                    <span className="truncate font-semibold">
                      AgentBox Studio
                    </span>
                    <span className="truncate text-xs text-muted-foreground">
                      {currentProject?.name ?? "选择项目"}
                    </span>
                  </div>
                  <ChevronsUpDownIcon className="ml-auto" />
                </SidebarMenuButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent className="w-64" align="start" side="bottom">
                <DropdownMenuLabel>当前项目</DropdownMenuLabel>
                <DropdownMenuGroup>
                  {projects.map((project) => (
                    <DropdownMenuItem
                      key={project.id}
                      onClick={() => onProjectChange(project.id)}
                    >
                      <div className="flex size-7 items-center justify-center rounded-md border">
                        <BoxesIcon />
                      </div>
                      <span>{project.name}</span>
                      {project.id === projectId && (
                        <Badge variant="secondary" className="ml-auto">
                          当前
                        </Badge>
                      )}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        {navigation.map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => (
                  <SidebarMenuItem key={item.id}>
                    <SidebarMenuButton
                      type="button"
                      isActive={section === item.id}
                      tooltip={item.label}
                      onClick={() => onSectionChange(item.id)}
                    >
                      <item.icon />
                      <span>{item.label}</span>
                    </SidebarMenuButton>
                    {item.id !== "overview" && (
                      <SidebarMenuBadge>{counts[item.id]}</SidebarMenuBadge>
                    )}
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>

      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" tooltip="本地管理员">
              <Avatar className="size-8 rounded-lg">
                <AvatarFallback className="rounded-lg">AB</AvatarFallback>
              </Avatar>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-medium">Local workspace</span>
                <span className="truncate text-xs text-muted-foreground">
                  PostgreSQL 已连接
                </span>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
