"use client"

import {
  BotIcon,
  BoxesIcon,
  ChevronsUpDownIcon,
  KeyRoundIcon,
  PlugZapIcon,
  SparklesIcon,
} from "lucide-react"

import type { AppSection } from "@/components/agent-management"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
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
  { id: "agents" as const, label: "Agents", icon: BotIcon },
  { id: "skills" as const, label: "Skills", icon: SparklesIcon },
  { id: "mcp" as const, label: "MCP Servers", icon: PlugZapIcon },
]

export function AppSidebar({
  section,
  onSectionChange,
  counts,
}: {
  section: AppSection
  onSectionChange: (section: AppSection) => void
  counts: Record<AppSection, number>
}) {
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
                      默认项目
                    </span>
                  </div>
                  <ChevronsUpDownIcon className="ml-auto" />
                </SidebarMenuButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent className="w-64" align="start" side="bottom">
                <DropdownMenuLabel>当前项目</DropdownMenuLabel>
                <DropdownMenuGroup>
                  <DropdownMenuItem>
                    <div className="flex size-7 items-center justify-center rounded-md border">
                      <BoxesIcon />
                    </div>
                    <span>AgentBox Studio</span>
                    <Badge variant="secondary" className="ml-auto">
                      当前
                    </Badge>
                  </DropdownMenuItem>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  <DropdownMenuItem disabled>
                    多项目将在后续版本提供
                  </DropdownMenuItem>
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>配置</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {navigation.map((item) => (
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
                  <SidebarMenuBadge>{counts[item.id]}</SidebarMenuBadge>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>运行准备</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton
                  type="button"
                  disabled
                  tooltip="后续版本提供"
                >
                  <KeyRoundIcon />
                  <span>Credentials</span>
                </SidebarMenuButton>
                <SidebarMenuBadge>后续</SidebarMenuBadge>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
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
