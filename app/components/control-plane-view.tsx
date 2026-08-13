"use client"

import { useMemo, type ReactNode } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  Clock3Icon,
  ContainerIcon,
  Disc3Icon,
  FolderKanbanIcon,
  KeyRoundIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlugZapIcon,
  PlusIcon,
  SparklesIcon,
  Trash2Icon,
  WebhookIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react"

import {
  CollectionContent,
  CollectionTablePrimaryContent,
} from "@/components/collection-list"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import { SiteHeader } from "@/components/site-header"
import type { Agent } from "@/lib/agent-schema"
import type { Resource } from "@/lib/platform-schema"
import type { ManagedServer } from "@/lib/server-schema"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"

type ResourceTableKind =
  "project" | "image" | "runtime" | "skill" | "mcp" | "variable"

const responsiveColumnClasses = [
  "hidden md:table-cell",
  "hidden lg:table-cell",
  "hidden xl:table-cell",
] as const

const meta = {
  project: {
    title: "项目",
    singular: "项目",
    empty: "还没有项目",
    description: "用于组织智能体和相关配置。",
    icon: FolderKanbanIcon,
  },
  image: {
    title: "镜像",
    singular: "镜像",
    empty: "还没有镜像",
    description: "统一维护 Docker 与 VM 环境模板可选择的 OCI 镜像。",
    icon: Disc3Icon,
  },
  runtime: {
    title: "环境模板",
    singular: "环境模板",
    empty: "还没有环境模板",
    description: "预先装好 Agent 工具与能力的可复用隔离环境。",
    icon: ContainerIcon,
  },
  skill: {
    title: "Skills",
    singular: "Skill",
    empty: "还没有 Skill",
    description: "安装到 Agent 沙箱中的指令、脚本和资源。",
    icon: SparklesIcon,
  },
  mcp: {
    title: "MCP Servers",
    singular: "MCP Server",
    empty: "还没有 MCP Server",
    description: "供 Agent 绑定和调用的工具服务。",
    icon: PlugZapIcon,
  },
  variable: {
    title: "环境变量",
    singular: "变量引用",
    empty: "还没有变量引用",
    description: "保存环境变量和密钥引用，不保存宿主机明文。",
    icon: KeyRoundIcon,
  },
} satisfies Record<
  "project" | "image" | "runtime" | "skill" | "mcp" | "variable",
  {
    title: string
    singular: string
    empty: string
    description: string
    icon: LucideIcon
  }
>

export function CollectionHeader({
  title,
  count,
  action,
}: {
  title: string
  count?: number
  action?: ReactNode
}) {
  return <SiteHeader title={title} count={count} action={action} />
}

export function ResourceView({
  kind,
  resources,
  agents,
  servers,
  onCreate,
  onEdit,
  onDelete,
  onProjectOpen,
}: {
  kind: ResourceTableKind
  resources: Resource[]
  agents: Agent[]
  servers: ManagedServer[]
  onCreate: () => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
  onProjectOpen?: (projectId: string) => void
}) {
  const config = meta[kind]
  const all = resources.filter((item) => item.kind === kind)
  const total = all.length
  const columns = useMemo(
    () =>
      resourceColumns({
        kind,
        icon: config.icon,
        resources,
        agents,
        servers,
        onOpen: (resource) =>
          resource.kind === "project" && onProjectOpen
            ? onProjectOpen(resource.id)
            : onEdit(resource),
        onEdit,
        onDelete,
      }),
    [
      agents,
      config.icon,
      kind,
      onDelete,
      onEdit,
      onProjectOpen,
      resources,
      servers,
    ]
  )
  const Icon = config.icon

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title={config.title}
        count={total}
        action={
          <Button size="sm" onClick={onCreate}>
            <PlusIcon data-icon="inline-start" />
            新建{config.singular}
          </Button>
        }
      />

      <CollectionContent>
        {total === 0 ? (
          <CollectionEmpty
            icon={Icon}
            title={config.empty}
            description={config.description}
            actionLabel={`创建第一个${config.singular}`}
            onAction={onCreate}
          />
        ) : (
          <DataTable
            data={all}
            columns={columns}
            getRowId={(resource) => resource.id}
            searchPlaceholder={`搜索${config.title}…`}
            searchValue={(resource) =>
              `${resource.name} ${resource.description} ${summary(resource)}`
            }
            filters={[
              {
                columnId: "status",
                title: "状态",
                options: [
                  { label: "已启用", value: "enabled" },
                  { label: "已停用", value: "disabled" },
                ],
              },
            ]}
          />
        )}
      </CollectionContent>
    </section>
  )
}

export function AutomationsView({
  resources,
  agents,
  onCreate,
  onEdit,
  onDelete,
}: {
  resources: Resource[]
  agents: Agent[]
  onCreate: (kind: "schedule" | "webhook") => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
}) {
  const all = resources.filter(
    (item) => item.kind === "schedule" || item.kind === "webhook"
  )
  const columns = useMemo(
    () => automationColumns(agents, onEdit, onDelete),
    [agents, onDelete, onEdit]
  )
  const createAction = (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm">
          <PlusIcon data-icon="inline-start" />
          新建自动化
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuGroup>
          <DropdownMenuItem onClick={() => onCreate("schedule")}>
            <Clock3Icon />
            定时任务
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => onCreate("webhook")}>
            <WebhookIcon />
            Webhook
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="自动化"
        count={all.length}
        action={createAction}
      />
      <CollectionContent>
        {all.length === 0 ? (
          <AutomationEmpty onCreate={onCreate} />
        ) : (
          <DataTable
            data={all}
            columns={columns}
            getRowId={(resource) => resource.id}
            searchPlaceholder="搜索自动化…"
            searchValue={(resource) =>
              `${resource.name} ${resource.description} ${summary(resource)}`
            }
            filters={[
              {
                columnId: "type",
                title: "触发方式",
                options: [
                  { label: "定时", value: "schedule" },
                  { label: "Webhook", value: "webhook" },
                ],
              },
              {
                columnId: "status",
                title: "状态",
                options: [
                  { label: "运行中", value: "enabled" },
                  { label: "已暂停", value: "disabled" },
                ],
              },
            ]}
          />
        )}
      </CollectionContent>
    </section>
  )
}

function AutomationEmpty({
  onCreate,
}: {
  onCreate: (kind: "schedule" | "webhook") => void
}) {
  const templates = [
    {
      title: "定时巡检",
      description: "按 Cron 周期唤醒一个 Agent。",
      icon: Clock3Icon,
      kind: "schedule" as const,
    },
    {
      title: "定时报告",
      description: "每天或每周生成固定报告。",
      icon: ZapIcon,
      kind: "schedule" as const,
    },
    {
      title: "Webhook 触发",
      description: "收到外部事件后调用一个 Agent。",
      icon: WebhookIcon,
      kind: "webhook" as const,
    },
  ]
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-5 py-16">
      <ZapIcon className="mb-3 size-9 text-muted-foreground" />
      <p className="text-sm font-medium">还没有自动化</p>
      <p className="mt-1 text-sm text-muted-foreground">
        自动化只负责触发一个 Agent，不编排任务节点。
      </p>
      <div className="mt-6 grid w-full max-w-3xl grid-cols-1 gap-3 sm:grid-cols-3">
        {templates.map((template) => (
          <button
            key={template.title}
            type="button"
            className="flex items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-muted/40"
            onClick={() => onCreate(template.kind)}
          >
            <template.icon className="mt-0.5 size-5 shrink-0 text-muted-foreground" />
            <span className="min-w-0">
              <span className="block text-sm font-medium">
                {template.title}
              </span>
              <span className="mt-0.5 block text-xs text-muted-foreground">
                {template.description}
              </span>
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}

function automationColumns(
  agents: Agent[],
  onEdit: (resource: Resource) => void,
  onDelete: (resource: Resource) => void
): ColumnDef<Resource>[] {
  return [
    {
      id: "name",
      accessorFn: (resource) => resource.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="自动化" />
      ),
      cell: ({ row }) => {
        const Icon = row.original.kind === "schedule" ? Clock3Icon : WebhookIcon
        return (
          <CollectionTablePrimaryContent
            icon={Icon}
            title={row.original.name}
            description={row.original.description || "触发一个 Agent"}
            onClick={() => onEdit(row.original)}
          />
        )
      },
      meta: { label: "自动化" },
      enableHiding: false,
    },
    {
      id: "agent",
      accessorFn: (resource) =>
        agents.find(
          (agent) => agent.id === stringValue(resource.spec, "agentId")
        )?.name ?? "未绑定",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="目标 Agent" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{String(getValue())}</span>
      ),
      meta: { label: "目标 Agent", className: "hidden md:table-cell" },
    },
    {
      id: "type",
      accessorFn: (resource) => resource.kind,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="触发方式" />
      ),
      cell: ({ row }) => {
        const isSchedule = row.original.kind === "schedule"
        const Icon = isSchedule ? Clock3Icon : WebhookIcon
        return (
          <span className="flex items-center gap-1.5 text-muted-foreground">
            <Icon className="size-3" />
            {isSchedule ? "定时" : "Webhook"}
          </span>
        )
      },
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "触发方式", className: "hidden lg:table-cell" },
    },
    {
      id: "config",
      accessorFn: (resource) =>
        resource.kind === "schedule"
          ? text(resource.spec, "cron")
          : text(resource.spec, "path"),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="触发配置" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate font-mono text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: { label: "触发配置", className: "hidden xl:table-cell" },
    },
    {
      id: "status",
      accessorFn: (resource) => (resource.enabled ? "enabled" : "disabled"),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="状态" />
      ),
      cell: ({ row }) => (
        <Badge variant={row.original.enabled ? "secondary" : "outline"}>
          {row.original.enabled ? "运行中" : "已暂停"}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "状态" },
    },
    {
      id: "updatedAt",
      accessorFn: (resource) => resource.updatedAt,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="最近更新" />
      ),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {relativeTime(row.original.updatedAt)}
        </span>
      ),
      meta: { label: "最近更新", className: "hidden xl:table-cell" },
    },
    {
      id: "actions",
      cell: ({ row }) => (
        <ResourceActions
          resource={row.original}
          onEdit={() => onEdit(row.original)}
          onDelete={() => onDelete(row.original)}
        />
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-10" },
    },
  ]
}

function CollectionEmpty({
  icon: Icon,
  title,
  description,
  actionLabel,
  onAction,
  action,
}: {
  icon: LucideIcon
  title: string
  description: string
  actionLabel?: string
  onAction?: () => void
  action?: ReactNode
}) {
  return (
    <Empty className="min-h-72 flex-1 border-0">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <Icon />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        {action ??
          (actionLabel && onAction ? (
            <Button variant="outline" size="sm" onClick={onAction}>
              {actionLabel}
            </Button>
          ) : null)}
      </EmptyContent>
    </Empty>
  )
}

function resourceColumns({
  kind,
  icon,
  resources,
  agents,
  servers,
  onOpen,
  onEdit,
  onDelete,
}: {
  kind: ResourceTableKind
  icon: LucideIcon
  resources: Resource[]
  agents: Agent[]
  servers: ManagedServer[]
  onOpen: (resource: Resource) => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
}): ColumnDef<Resource>[] {
  const labels =
    kind === "project"
      ? ["项目", "智能体", "环境模板", "标识"]
      : kind === "image"
        ? ["镜像", "OCI 引用", "兼容类型", "使用者"]
        : kind === "runtime"
          ? ["环境模板", "默认服务器", "隔离类型", "镜像"]
          : kind === "skill"
            ? ["Skill", "使用者", "来源", "版本"]
            : ["MCP Server", "使用者", "传输方式", "服务地址"]

  const columns: ColumnDef<Resource>[] = [
    {
      id: "name",
      accessorFn: (resource) => resource.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={labels[0]} />
      ),
      cell: ({ row }) => (
        <CollectionTablePrimaryContent
          icon={icon}
          title={row.original.name}
          description={row.original.description || summary(row.original)}
          onClick={() => onOpen(row.original)}
        />
      ),
      meta: { label: labels[0] },
      enableHiding: false,
    },
  ]

  for (let index = 0; index < 3; index += 1) {
    columns.push({
      id: `detail-${index + 1}`,
      accessorFn: (resource) =>
        resourceColumnValues(resource, resources, agents, servers)[index],
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={labels[index + 1]} />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-44 truncate text-muted-foreground">
          {String(getValue() ?? "—")}
        </span>
      ),
      meta: {
        label: labels[index + 1],
        className: responsiveColumnClasses[index],
      },
    })
  }

  columns.push(
    {
      id: "status",
      accessorFn: (resource) => (resource.enabled ? "enabled" : "disabled"),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="状态" />
      ),
      cell: ({ row }) => (
        <Badge variant={row.original.enabled ? "secondary" : "outline"}>
          {row.original.enabled ? "已启用" : "已停用"}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "状态" },
    },
    {
      id: "updatedAt",
      accessorFn: (resource) => resource.updatedAt,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="最近更新" />
      ),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {relativeTime(row.original.updatedAt)}
        </span>
      ),
      meta: { label: "最近更新", className: "hidden xl:table-cell" },
    },
    {
      id: "actions",
      cell: ({ row }) => (
        <ResourceActions
          resource={row.original}
          onEdit={() => onEdit(row.original)}
          onDelete={() => onDelete(row.original)}
        />
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-10" },
    }
  )

  return columns
}

function resourceColumnValues(
  resource: Resource,
  resources: Resource[],
  agents: Agent[],
  servers: ManagedServer[]
) {
  const projectAgents = agents.filter(
    (agent) => agent.projectId === resource.id
  )
  const projectRuntimes = resources.filter(
    (item) => item.kind === "runtime" && item.projectId === resource.id
  )
  const imageRuntimes = resources.filter(
    (item) => item.kind === "runtime" && item.spec.imageId === resource.id
  )
  const skillAgents = agents.filter((agent) =>
    agent.skillIds.includes(resource.id)
  )
  const mcpAgents = agents.filter((agent) =>
    agent.mcpServerIds.includes(resource.id)
  )
  const server = servers.find(
    (item) => item.id === stringValue(resource.spec, "serverId")
  )
  return resource.kind === "project"
    ? [
        `${projectAgents.length} 个`,
        `${projectRuntimes.length} 个`,
        resource.id,
      ]
    : resource.kind === "image"
      ? [
          text(resource.spec, "reference"),
          stringArray(resource.spec.modes)
            .map((mode) => (mode === "vm" ? "VM" : "Docker"))
            .join(" / ") || "—",
          imageRuntimes.length > 0
            ? `${imageRuntimes.length} 个环境模板`
            : "未使用",
        ]
      : resource.kind === "runtime"
        ? [
            server?.name ?? "未绑定",
            text(resource.spec, "driver") === "vm" ? "VM" : "Docker",
            resources.find(
              (item) =>
                item.kind === "image" && item.id === resource.spec.imageId
            )?.name ?? "未选择",
          ]
        : resource.kind === "skill"
          ? [
              skillAgents.length > 0
                ? `${skillAgents.length} 个智能体`
                : "未使用",
              text(resource.spec, "source"),
              `v${text(resource.spec, "version")}`,
            ]
          : [
              mcpAgents.length > 0 ? `${mcpAgents.length} 个智能体` : "未使用",
              text(resource.spec, "transport").toUpperCase(),
              text(resource.spec, "transport") === "http"
                ? text(resource.spec, "url")
                : [text(resource.spec, "command"), text(resource.spec, "args")]
                    .filter((value) => value !== "—")
                    .join(" ") || "—",
            ]
}

function ResourceActions({
  resource,
  onEdit,
  onDelete,
}: {
  resource: Resource
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`${resource.name} 操作`}
        >
          <MoreHorizontalIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuGroup>
          <DropdownMenuItem onClick={onEdit}>
            <PencilIcon />
            编辑
          </DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem variant="destructive" onClick={onDelete}>
            <Trash2Icon />
            删除
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function stringValue(spec: Record<string, unknown>, key: string) {
  const value = spec[key]
  return typeof value === "string" ? value : ""
}

function relativeTime(value: string) {
  const minutes = Math.max(
    0,
    Math.floor((Date.now() - new Date(value).getTime()) / 60_000)
  )
  if (minutes < 1) return "刚刚"
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}

function text(spec: Record<string, unknown>, key: string) {
  const value = spec[key]
  return typeof value === "string" && value ? value : "—"
}

function stringArray(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : []
}

function summary(resource: Resource) {
  switch (resource.kind) {
    case "project":
      return resource.id
    case "image":
      return `${text(resource.spec, "reference")} · ${stringArray(resource.spec.modes).join(" / ")}`
    case "runtime":
      return `${text(resource.spec, "driver")} · ${text(resource.spec, "imageId")} · ${stringArray(resource.spec.agentTools).length} Agent 工具`
    case "skill":
      return `v${text(resource.spec, "version")} · ${text(resource.spec, "source")}`
    case "mcp":
      return `${text(resource.spec, "transport")} · ${text(resource.spec, "command")}`
    case "schedule":
      return `${text(resource.spec, "cron")} · ${text(resource.spec, "timezone")}`
    case "webhook":
      return `${text(resource.spec, "event")} · ${text(resource.spec, "path")}`
    case "variable":
      return `${text(resource.spec, "key")} · ${text(resource.spec, "reference")}`
    case "sandbox":
      return `${text(resource.spec, "status")} · ${text(resource.spec, "runtimeId")}`
  }
}
