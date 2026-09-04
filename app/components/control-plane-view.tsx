"use client"

import { useMemo, type ReactNode } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  ContainerIcon,
  Disc3Icon,
  ExternalLinkIcon,
  FolderKanbanIcon,
  KeyRoundIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlugZapIcon,
  PlusIcon,
  SparklesIcon,
  Trash2Icon,
  type LucideIcon,
} from "lucide-react"

import {
  CollectionContent,
  CollectionTablePrimaryContent,
} from "@/components/collection-list"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import { SiteHeader } from "@/components/site-header"
import type { Resource } from "@/lib/platform-schema"
import { getProjectEmoji } from "@/lib/project-emoji"
import {
  capabilityUsage,
  capabilityUsageLabel,
} from "@/lib/resource-capabilities"
import type { ManagedServer } from "@/lib/server-schema"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuModalItem,
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
    description: "统一维护沙箱模板可选择的 OCI 镜像。",
    icon: Disc3Icon,
  },
  runtime: {
    title: "沙箱模板",
    singular: "沙箱模板",
    empty: "还没有沙箱模板",
    description: "预先装好 Agent 工具与能力的可复用隔离环境。",
    icon: ContainerIcon,
  },
  skill: {
    title: "Skills",
    singular: "Skill",
    empty: "还没有 Skill",
    description:
      "从 skills.sh、公开链接、本地 SKILL.md 或 ZIP 导入指令、脚本和资源，也可以手动编写。",
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
  servers,
  canMutate,
  onCreate,
  onEdit,
  onDelete,
  onProjectOpen,
}: {
  kind: ResourceTableKind
  resources: Resource[]
  servers: ManagedServer[]
  canMutate: boolean
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
        servers,
        canMutate,
        onOpen: (resource) =>
          resource.kind === "project" && onProjectOpen
            ? onProjectOpen(resource.id)
            : onEdit(resource),
        onEdit,
        onDelete,
      }),
    [
      canMutate,
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
          canMutate ? (
            <div className="flex items-center gap-2">
              {kind === "skill" && (
                <Button variant="outline" size="sm" asChild>
                  <a
                    href="https://skills.sh/"
                    target="_blank"
                    rel="noopener noreferrer"
                    aria-label="浏览 skills.sh"
                    title="浏览 skills.sh"
                  >
                    <ExternalLinkIcon data-icon="inline-start" />
                    <span className="hidden sm:inline">浏览 skills.sh</span>
                  </a>
                </Button>
              )}
              <Button size="sm" onClick={onCreate}>
                <PlusIcon data-icon="inline-start" />
                {kind === "skill" ? "添加 Skill" : `新建${config.singular}`}
              </Button>
            </div>
          ) : undefined
        }
      />

      <CollectionContent>
        {total === 0 ? (
          <CollectionEmpty
            icon={Icon}
            title={config.empty}
            description={config.description}
            actionLabel={
              canMutate
                ? kind === "skill"
                  ? "导入第一个 Skill"
                  : `创建第一个${config.singular}`
                : undefined
            }
            onAction={canMutate ? onCreate : undefined}
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
  servers,
  canMutate,
  onOpen,
  onEdit,
  onDelete,
}: {
  kind: ResourceTableKind
  icon: LucideIcon
  resources: Resource[]
  servers: ManagedServer[]
  canMutate: boolean
  onOpen: (resource: Resource) => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
}): ColumnDef<Resource>[] {
  const labels =
    kind === "project"
      ? ["项目", "沙箱", "沙箱模板", "标识"]
      : kind === "image"
        ? ["镜像", "OCI 引用", "兼容类型", "使用者"]
        : kind === "runtime"
          ? ["沙箱模板", "默认服务器", "隔离类型", "镜像"]
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
          media={
            kind === "project" ? (
              <span
                aria-hidden="true"
                className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-base"
              >
                {getProjectEmoji(row.original)}
              </span>
            ) : undefined
          }
          title={row.original.name}
          description={row.original.description || summary(row.original)}
          onClick={canMutate ? () => onOpen(row.original) : undefined}
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
        resourceColumnValues(resource, resources, servers)[index],
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
    }
  )

  if (canMutate) {
    columns.push({
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
    })
  }

  return columns
}

function resourceColumnValues(
  resource: Resource,
  resources: Resource[],
  servers: ManagedServer[]
) {
  const projectSandboxes = resources.filter(
    (item) => item.kind === "sandbox" && item.projectId === resource.id
  )
  const projectRuntimes = resources.filter(
    (item) => item.kind === "runtime" && item.projectId === resource.id
  )
  const imageRuntimes = resources.filter(
    (item) => item.kind === "runtime" && item.spec.imageId === resource.id
  )
  const skillUsage = capabilityUsage(resources, resource.id, "skillIds")
  const mcpUsage = capabilityUsage(resources, resource.id, "mcpServerIds")
  const server = servers.find(
    (item) => item.id === stringValue(resource.spec, "serverId")
  )
  return resource.kind === "project"
    ? [
        `${projectSandboxes.length} 个`,
        `${projectRuntimes.length} 个`,
        resource.id,
      ]
    : resource.kind === "image"
      ? [
          text(resource.spec, "reference"),
          stringArray(resource.spec.modes)
            .map((mode) => (mode === "vm" ? "VM（旧）" : "Docker"))
            .join(" / ") || "—",
          imageRuntimes.length > 0
            ? `${imageRuntimes.length} 个沙箱模板`
            : "未使用",
        ]
      : resource.kind === "runtime"
        ? [
            server?.name ?? "未绑定",
            runtimeDriverLabel(text(resource.spec, "driver")),
            resources.find(
              (item) =>
                item.kind === "image" && item.id === resource.spec.imageId
            )?.name ?? "未选择",
          ]
        : resource.kind === "skill"
          ? [
              capabilityUsageLabel(skillUsage),
              text(resource.spec, "source"),
              `v${text(resource.spec, "version")}`,
            ]
          : [
              capabilityUsageLabel(mcpUsage),
              text(resource.spec, "transport").toUpperCase(),
              text(resource.spec, "transport") === "http"
                ? text(resource.spec, "url")
                : [
                    text(resource.spec, "command"),
                    arrayOrString(specValue(resource.spec, "args")),
                  ]
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
          <DropdownMenuModalItem onOpen={onEdit}>
            <PencilIcon />
            编辑
          </DropdownMenuModalItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuModalItem variant="destructive" onOpen={onDelete}>
            <Trash2Icon />
            删除
          </DropdownMenuModalItem>
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

function runtimeDriverLabel(driver: string) {
  if (driver === "docker") return "Docker"
  if (driver === "boxlite") return "BoxLite"
  if (driver === "microsandbox") return "Microsandbox"
  if (driver === "vm") return "VM（旧）"
  return driver === "—" ? "未配置" : driver
}

function stringArray(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : []
}

function arrayOrString(value: unknown) {
  if (Array.isArray(value)) {
    return value.filter((item) => typeof item === "string").join(" ") || "—"
  }
  return typeof value === "string" && value ? value : "—"
}

function specValue(spec: object, key: string) {
  return (spec as Record<string, unknown>)[key]
}

function summary(resource: Resource) {
  switch (resource.kind) {
    case "project":
      return resource.id
    case "image":
      return `${text(resource.spec, "reference")} · ${stringArray(resource.spec.modes).join(" / ")}`
    case "runtime":
      return `${runtimeDriverLabel(text(resource.spec, "driver"))} · ${text(resource.spec, "imageReference")} · ${stringArray(resource.spec.agentTools).length} Agent 工具`
    case "skill":
      return `v${text(resource.spec, "version")} · ${text(resource.spec, "source")}`
    case "mcp":
      return `${text(resource.spec, "transport")} · ${text(resource.spec, "command")}`
    case "variable":
      return `${text(resource.spec, "key")} · ${text(resource.spec, "reference")}`
    case "sandbox":
      return `${text(resource.spec, "status")} · ${text(resource.spec, "runtimeId")}`
  }
}
