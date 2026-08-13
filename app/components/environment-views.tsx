"use client"

import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  BotIcon,
  BoxIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  ContainerIcon,
  CopyIcon,
  KeyRoundIcon,
  LoaderCircleIcon,
  LogInIcon,
  MonitorIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  PlugZapIcon,
  RocketIcon,
  ServerIcon,
  Settings2Icon,
  SquareIcon,
  SparklesIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react"

import type { AppSection } from "@/components/agent-management"
import {
  CollectionContent,
  CollectionTablePrimaryContent,
} from "@/components/collection-list"
import { CollectionHeader } from "@/components/control-plane-view"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
import { Progress } from "@/components/ui/progress"
import type { Agent } from "@/lib/agent-schema"
import type { Resource } from "@/lib/platform-schema"
import type { ManagedServer } from "@/lib/server-schema"

type EnvironmentProps = {
  resources: Resource[]
  agents: Agent[]
  servers: ManagedServer[]
}

export function DashboardView({
  resources,
  agents,
  servers,
  configuredCredentials,
  onNavigate,
  onCreateEnvironment,
  onCreateSandbox,
}: EnvironmentProps & {
  configuredCredentials: number
  onNavigate: (section: AppSection) => void
  onCreateEnvironment: () => void
  onCreateSandbox: () => void
}) {
  const environments = resources.filter((item) => item.kind === "runtime")
  const sandboxes = resources.filter((item) => item.kind === "sandbox")
  const onlineServers = servers.filter((item) => item.status === "online")
  const imageCount = onlineServers.reduce(
    (total, server) =>
      total +
      server.inventory.dockerImages.length +
      server.inventory.vmImages.length,
    0
  )
  const activeAgents = agents.filter((item) => item.status === "active")
  const readiness = [
    {
      label: "可用服务器",
      ready: onlineServers.length > 0,
      value: `${onlineServers.length} 台在线`,
      section: "servers" as const,
    },
    {
      label: "基础镜像",
      ready: imageCount > 0,
      value: `${imageCount} 个可用`,
      section: "images" as const,
    },
    {
      label: "智能体配置",
      ready: activeAgents.length > 0,
      value: `${activeAgents.length} 个启用`,
      section: "agents" as const,
    },
    {
      label: "访问凭据",
      ready: configuredCredentials > 0,
      value: `${configuredCredentials} 个已配置`,
      section: "access" as const,
    },
  ]
  const readyCount = readiness.filter((item) => item.ready).length

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader title="概览" />
      <div className="min-h-0 flex-1 overflow-auto p-4 sm:p-6">
        <div className="mx-auto grid w-full max-w-[1600px] gap-6">
          <div className="flex flex-col gap-4 border-b pb-6 lg:flex-row lg:items-end lg:justify-between">
            <div className="flex max-w-3xl flex-col gap-1.5">
              <h2 className="text-xl font-semibold tracking-tight text-pretty">
                运行环境状态
              </h2>
              <p className="text-sm leading-6 text-pretty text-muted-foreground">
                集中查看环境模板、沙箱、服务器与访问凭据，并从这里开始日常环境操作。
              </p>
            </div>
            <div className="flex shrink-0 flex-wrap gap-2">
              <Button variant="outline" onClick={onCreateEnvironment}>
                <PlusIcon data-icon="inline-start" />
                新建环境模板
              </Button>
              <Button onClick={onCreateSandbox}>
                <BoxIcon data-icon="inline-start" />
                创建沙箱
              </Button>
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              icon={MonitorIcon}
              label="环境模板"
              value={environments.length}
              detail="可复用的预配环境"
              onClick={() => onNavigate("runtimes")}
            />
            <StatCard
              icon={BoxIcon}
              label="沙箱"
              value={sandboxes.length}
              detail={`${sandboxes.filter(isRunningSandbox).length} 个运行中`}
              onClick={() => onNavigate("sandboxes")}
            />
            <StatCard
              icon={BotIcon}
              label="智能体"
              value={agents.length}
              detail={`${activeAgents.length} 个已启用`}
              onClick={() => onNavigate("agents")}
            />
            <StatCard
              icon={ServerIcon}
              label="服务器"
              value={servers.length}
              detail={`${onlineServers.length} 台在线`}
              onClick={() => onNavigate("servers")}
            />
          </div>

          <div className="grid gap-6 xl:grid-cols-[minmax(0,1.45fr)_minmax(320px,0.75fr)]">
            <Card>
              <CardHeader>
                <CardTitle>环境模板</CardTitle>
                <CardDescription>
                  最近配置的环境。模板只是声明，创建沙箱后才会在服务器上实例化。
                </CardDescription>
                <CardAction>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => onNavigate("runtimes")}
                  >
                    查看全部
                    <ChevronRightIcon data-icon="inline-end" />
                  </Button>
                </CardAction>
              </CardHeader>
              <CardContent className="grid gap-3">
                {environments.length > 0 ? (
                  environments.slice(0, 4).map((environment) => (
                    <button
                      key={environment.id}
                      type="button"
                      className="flex items-center gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50"
                      onClick={() => onNavigate("runtimes")}
                    >
                      <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">
                        {specString(environment.spec, "driver") === "vm" ? (
                          <MonitorIcon className="size-4" />
                        ) : (
                          <ContainerIcon className="size-4" />
                        )}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium">
                          {environment.name}
                        </span>
                        <span className="block truncate text-xs text-muted-foreground">
                          {environmentSummary(environment, resources, servers)}
                        </span>
                      </span>
                      <EnvironmentBadges resource={environment} />
                    </button>
                  ))
                ) : (
                  <div className="rounded-lg border border-dashed p-8 text-center">
                    <p className="font-medium">还没有环境模板</p>
                    <p className="mt-1 text-sm text-muted-foreground">
                      从一个镜像开始，加入 Agent 工具与能力配置。
                    </p>
                    <Button
                      size="sm"
                      className="mt-4"
                      onClick={onCreateEnvironment}
                    >
                      创建第一个模板
                    </Button>
                  </div>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>系统准备度</CardTitle>
                <CardDescription>
                  完成四项基础配置后即可稳定创建沙箱。
                </CardDescription>
              </CardHeader>
              <CardContent className="grid gap-4">
                <div className="flex items-center justify-between text-sm">
                  <span>
                    {readyCount} / {readiness.length} 项已就绪
                  </span>
                  <span className="text-muted-foreground">
                    {Math.round((readyCount / readiness.length) * 100)}%
                  </span>
                </div>
                <Progress value={(readyCount / readiness.length) * 100} />
                <div className="grid gap-1">
                  {readiness.map((item) => (
                    <button
                      key={item.label}
                      type="button"
                      className="flex items-center gap-3 rounded-lg px-2 py-2 text-left hover:bg-muted/50"
                      onClick={() => onNavigate(item.section)}
                    >
                      {item.ready ? (
                        <CheckCircle2Icon className="size-4 shrink-0 text-primary" />
                      ) : (
                        <TriangleAlertIcon className="size-4 shrink-0 text-muted-foreground" />
                      )}
                      <span className="min-w-0 flex-1 text-sm">
                        {item.label}
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {item.value}
                      </span>
                    </button>
                  ))}
                </div>
              </CardContent>
            </Card>
          </div>
        </div>
      </div>
    </section>
  )
}

function StatCard({
  icon: Icon,
  label,
  value,
  detail,
  onClick,
}: {
  icon: typeof MonitorIcon
  label: string
  value: number
  detail: string
  onClick: () => void
}) {
  return (
    <button type="button" className="text-left" onClick={onClick}>
      <Card className="h-full transition-colors hover:bg-muted/30">
        <CardHeader>
          <span className="flex size-9 items-center justify-center rounded-lg bg-muted">
            <Icon className="size-4" />
          </span>
          <CardAction>
            <ChevronRightIcon className="size-4 text-muted-foreground" />
          </CardAction>
        </CardHeader>
        <CardContent>
          <p className="text-2xl font-semibold tabular-nums">{value}</p>
          <p className="mt-1 font-medium">{label}</p>
          <p className="text-xs text-muted-foreground">{detail}</p>
        </CardContent>
      </Card>
    </button>
  )
}

export function EnvironmentTemplatesView({
  resources,
  agents,
  servers,
  onCreate,
  onEdit,
  onDelete,
  onLaunch,
}: EnvironmentProps & {
  onCreate: () => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
  onLaunch: (resource: Resource) => void
}) {
  const environments = resources.filter((item) => item.kind === "runtime")
  const columns = useMemo(
    () =>
      environmentColumns({
        agents,
        servers,
        onEdit,
        onDelete,
        onLaunch,
      }),
    [agents, onDelete, onEdit, onLaunch, servers]
  )

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="环境模板"
        count={environments.length}
        action={
          <Button size="sm" onClick={onCreate}>
            <PlusIcon data-icon="inline-start" />
            新建环境模板
          </Button>
        }
      />
      <CollectionContent>
        {environments.length === 0 ? (
          <Empty className="mx-auto min-h-[28rem] max-w-3xl border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <MonitorIcon />
              </EmptyMedia>
              <EmptyTitle>还没有环境模板</EmptyTitle>
              <EmptyDescription>
                预先选择隔离方式与镜像，并装好 Agent 工具、Skills、MCP
                和环境变量。
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button onClick={onCreate}>创建第一个环境模板</Button>
            </EmptyContent>
          </Empty>
        ) : (
          <DataTable
            data={environments}
            columns={columns}
            getRowId={(environment) => environment.id}
            initialPageSize={8}
            searchPlaceholder="搜索环境模板…"
            searchValue={(environment) =>
              `${environment.name} ${environment.description}`
            }
            filters={[
              {
                columnId: "driver",
                title: "隔离方式",
                options: [
                  { label: "Docker", value: "docker" },
                  { label: "VM", value: "vm" },
                ],
              },
              {
                columnId: "status",
                title: "状态",
                options: [
                  { label: "可用", value: "enabled" },
                  { label: "停用", value: "disabled" },
                ],
              },
            ]}
          />
        )}
      </CollectionContent>
    </section>
  )
}

export function SandboxesView({
  resources,
  agents,
  servers,
  onCreate,
  busyId,
  onAction,
  onDelete,
}: EnvironmentProps & {
  onCreate: () => void
  busyId: string | null
  onAction: (
    resource: Resource,
    action: "start" | "stop" | "delete" | "login-codex"
  ) => Promise<void>
  onDelete: (resource: Resource) => void
}) {
  const sandboxes = resources.filter((item) => item.kind === "sandbox")
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const columns = useMemo(
    () =>
      sandboxColumns({
        resources,
        agents,
        servers,
        busyId,
        onOpen: (sandbox) => setSelectedId(sandbox.id),
        onAction,
        onDelete,
      }),
    [agents, busyId, onAction, onDelete, resources, servers, setSelectedId]
  )
  const selected = sandboxes.find((item) => item.id === selectedId) ?? null
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="沙箱"
        count={sandboxes.length}
        action={
          <Button size="sm" onClick={onCreate}>
            <PlusIcon data-icon="inline-start" />
            创建沙箱
          </Button>
        }
      />
      <CollectionContent>
        {sandboxes.length === 0 ? (
          <Empty className="mx-auto min-h-[28rem] max-w-3xl border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BoxIcon />
              </EmptyMedia>
              <EmptyTitle>还没有沙箱</EmptyTitle>
              <EmptyDescription>
                沙箱是环境模板在某台服务器上的实例。当前控制面会先保存创建请求。
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button onClick={onCreate}>创建第一个沙箱</Button>
            </EmptyContent>
          </Empty>
        ) : (
          <DataTable
            data={sandboxes}
            columns={columns}
            getRowId={(sandbox) => sandbox.id}
            initialPageSize={8}
            searchPlaceholder="搜索沙箱…"
            searchValue={(sandbox) =>
              `${sandbox.name} ${String(sandbox.spec.message ?? "")}`
            }
            filters={[
              {
                columnId: "status",
                title: "状态",
                options: [
                  { label: "运行中", value: "running" },
                  { label: "已停止", value: "stopped" },
                  { label: "处理中", value: "pending" },
                  { label: "异常", value: "error" },
                ],
              },
            ]}
          />
        )}
      </CollectionContent>
      {selected && (
        <SandboxDetailsDialog
          sandbox={selected}
          agent={agents.find((item) => item.id === selected.spec.agentId)}
          environment={resources.find(
            (item) => item.id === selected.spec.runtimeId
          )}
          server={servers.find((item) => item.id === selected.spec.serverId)}
          resources={resources}
          busy={busyId === selected.id}
          onLogin={() => void onAction(selected, "login-codex")}
          onOpenChange={(open) => !open && setSelectedId(null)}
        />
      )}
    </section>
  )
}

function environmentColumns({
  agents,
  servers,
  onEdit,
  onDelete,
  onLaunch,
}: {
  agents: Agent[]
  servers: ManagedServer[]
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
  onLaunch: (resource: Resource) => void
}): ColumnDef<Resource>[] {
  return [
    {
      id: "name",
      accessorFn: (environment) => environment.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="环境模板" />
      ),
      cell: ({ row }) => {
        const driver = specString(row.original.spec, "driver")
        return (
          <CollectionTablePrimaryContent
            icon={driver === "vm" ? MonitorIcon : ContainerIcon}
            title={row.original.name}
            description={
              row.original.description || "可复用的 Agent 工作环境"
            }
            onClick={() => onEdit(row.original)}
          />
        )
      },
      meta: { label: "环境模板" },
      enableHiding: false,
    },
    {
      id: "status",
      accessorFn: (environment) =>
        environment.enabled ? "enabled" : "disabled",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="状态" />
      ),
      cell: ({ row }) => (
        <Badge variant={row.original.enabled ? "secondary" : "outline"}>
          {row.original.enabled ? "可用" : "停用"}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "状态" },
    },
    {
      id: "server",
      accessorFn: (environment) =>
        servers.find((server) => server.id === environment.spec.serverId)
          ?.name ?? "未绑定",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="服务器" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: { label: "服务器", className: "hidden md:table-cell" },
    },
    {
      id: "driver",
      accessorFn: (environment) => specString(environment.spec, "driver"),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="隔离方式" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">
          {getValue() === "vm" ? "VM" : "Docker"}
        </span>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "隔离方式", className: "hidden lg:table-cell" },
    },
    {
      id: "agents",
      accessorFn: (environment) =>
        agents.filter((agent) => agent.runtimeId === environment.id).length,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="智能体" />
      ),
      cell: ({ getValue }) => {
        const count = Number(getValue())
        return (
          <span className="text-muted-foreground">
            {count ? `${count} 个智能体` : "未使用"}
          </span>
        )
      },
      meta: { label: "智能体", className: "hidden xl:table-cell" },
    },
    {
      id: "actions",
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-1">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`${row.original.name} 操作`}
              >
                <MoreHorizontalIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuGroup>
                <DropdownMenuItem onClick={() => onEdit(row.original)}>
                  <PencilIcon />
                  编辑
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onClick={() => onDelete(row.original)}
              >
                <Trash2Icon />
                删除
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <Button
            size="sm"
            variant="outline"
            disabled={!row.original.enabled}
            onClick={() => onLaunch(row.original)}
          >
            <RocketIcon data-icon="inline-start" />
            创建沙箱
          </Button>
        </div>
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-36" },
    },
  ]
}

function sandboxColumns({
  resources,
  agents,
  servers,
  busyId,
  onOpen,
  onAction,
  onDelete,
}: {
  resources: Resource[]
  agents: Agent[]
  servers: ManagedServer[]
  busyId: string | null
  onOpen: (sandbox: Resource) => void
  onAction: (
    resource: Resource,
    action: "start" | "stop" | "delete" | "login-codex"
  ) => Promise<void>
  onDelete: (resource: Resource) => void
}): ColumnDef<Resource>[] {
  return [
    {
      id: "name",
      accessorFn: (sandbox) => sandbox.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="沙箱" />
      ),
      cell: ({ row }) => (
        <CollectionTablePrimaryContent
          icon={BoxIcon}
          title={row.original.name}
          description={
            typeof row.original.spec.message === "string" &&
            row.original.spec.message
              ? row.original.spec.message
              : `创建于 ${new Date(row.original.createdAt).toLocaleDateString("zh-CN")}`
          }
          onClick={() => onOpen(row.original)}
        />
      ),
      meta: { label: "沙箱" },
      enableHiding: false,
    },
    {
      id: "status",
      accessorFn: (sandbox) => sandboxFilterStatus(sandbox),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="状态" />
      ),
      cell: ({ row }) => (
        <Badge
          variant={isRunningSandbox(row.original) ? "default" : "secondary"}
        >
          {sandboxStatus(row.original)}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "状态" },
    },
    {
      id: "agent",
      accessorFn: (sandbox) =>
        agents.find((agent) => agent.id === sandbox.spec.agentId)?.name ??
        "未绑定",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="智能体" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: { label: "智能体", className: "hidden md:table-cell" },
    },
    {
      id: "environment",
      accessorFn: (sandbox) =>
        resources.find((resource) => resource.id === sandbox.spec.runtimeId)
          ?.name ?? "未绑定",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="环境模板" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: { label: "环境模板", className: "hidden lg:table-cell" },
    },
    {
      id: "server",
      accessorFn: (sandbox) =>
        servers.find((server) => server.id === sandbox.spec.serverId)?.name ??
        "创建时选择",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="服务器" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: { label: "服务器", className: "hidden xl:table-cell" },
    },
    {
      id: "actions",
      cell: ({ row }) => {
        const sandbox = row.original
        const busy = busyId === sandbox.id
        return (
          <div className="flex items-center justify-end gap-1">
            {sandbox.spec.status === "running" ? (
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => void onAction(sandbox, "stop")}
              >
                {busy ? (
                  <LoaderCircleIcon className="animate-spin" />
                ) : (
                  <SquareIcon />
                )}
                停止
              </Button>
            ) : sandbox.spec.status === "stopped" ||
              sandbox.spec.status === "error" ? (
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => void onAction(sandbox, "start")}
              >
                {busy ? (
                  <LoaderCircleIcon className="animate-spin" />
                ) : (
                  <PlayIcon />
                )}
                启动
              </Button>
            ) : null}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`${sandbox.name} 操作`}
                >
                  <MoreHorizontalIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuGroup>
                  <DropdownMenuItem onClick={() => onOpen(sandbox)}>
                    <Settings2Icon />
                    管理
                  </DropdownMenuItem>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  variant="destructive"
                  disabled={
                    busy ||
                    ["requested", "starting", "stopping", "deleting"].includes(
                      String(sandbox.spec.status ?? "")
                    )
                  }
                  onClick={() => onDelete(sandbox)}
                >
                  <Trash2Icon />
                  删除
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        )
      },
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-28" },
    },
  ]
}

function sandboxFilterStatus(sandbox: Resource) {
  const status = String(sandbox.spec.status ?? "")
  return ["requested", "starting", "stopping", "deleting"].includes(status)
    ? "pending"
    : status || "error"
}

function SandboxDetailsDialog({
  sandbox,
  agent,
  environment,
  server,
  resources,
  busy,
  onLogin,
  onOpenChange,
}: {
  sandbox: Resource
  agent?: Agent
  environment?: Resource
  server?: ManagedServer
  resources: Resource[]
  busy: boolean
  onLogin: () => void
  onOpenChange: (open: boolean) => void
}) {
  const tools = stringList(environment?.spec.agentTools)
  const skills = stringList(environment?.spec.skillIds).map(
    (id) => resources.find((item) => item.id === id)?.name ?? id
  )
  const mcpServers = stringList(environment?.spec.mcpServerIds).map(
    (id) => resources.find((item) => item.id === id)?.name ?? id
  )
  const externalId =
    typeof sandbox.spec.externalId === "string"
      ? sandbox.spec.externalId
      : `agentbox-${sandbox.id}`
  const loginMessage =
    typeof sandbox.spec.loginMessage === "string"
      ? sandbox.spec.loginMessage
      : ""
  const loginCommand = `docker exec -it ${externalId} codex login --device-auth`

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{sandbox.name}</DialogTitle>
          <DialogDescription>
            管理这个沙箱的运行位置、预装能力和 Agent 登录状态。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-3 rounded-xl border p-4 sm:grid-cols-3">
            <Detail label="状态" value={sandboxStatus(sandbox)} />
            <Detail label="智能体" value={agent?.name ?? "未绑定"} />
            <Detail label="服务器" value={server?.name ?? "未知服务器"} />
            <Detail label="环境模板" value={environment?.name ?? "未绑定"} />
            <Detail label="实例" value={externalId} mono />
            <Detail
              label="Agent 工具"
              value={tools.length > 0 ? tools.join(" · ") : "未预装"}
            />
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <CapabilityList title="Skills" values={skills} />
            <CapabilityList title="MCP Servers" values={mcpServers} />
          </div>

          {tools.includes("codex") && (
            <div className="rounded-xl border p-4">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                <div>
                  <p className="font-medium">Codex 账号登录</p>
                  <p className="mt-1 text-sm leading-5 text-muted-foreground">
                    API Key 已由环境模板自动注入；如果要使用 ChatGPT
                    订阅账号，可在这个沙箱内单独发起设备登录。
                  </p>
                </div>
                <Button
                  size="sm"
                  disabled={busy || sandbox.spec.status !== "running"}
                  onClick={onLogin}
                >
                  {busy ? (
                    <LoaderCircleIcon className="animate-spin" />
                  ) : (
                    <LogInIcon />
                  )}
                  发起登录
                </Button>
              </div>
              {loginMessage && (
                <pre
                  className={`mt-3 max-h-40 overflow-auto rounded-lg p-3 text-xs leading-5 whitespace-pre-wrap ${
                    sandbox.spec.loginStatus === "error"
                      ? "bg-destructive/10 text-destructive"
                      : "bg-muted"
                  }`}
                >
                  {loginMessage}
                </pre>
              )}
              <div className="mt-3 flex items-center gap-2 rounded-lg bg-muted/50 p-2 pl-3">
                <code className="min-w-0 flex-1 truncate text-xs">
                  {loginCommand}
                </code>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="复制终端登录命令"
                  onClick={() =>
                    void navigator.clipboard.writeText(loginCommand)
                  }
                >
                  <CopyIcon />
                </Button>
              </div>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Detail({
  label,
  value,
  mono = false,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="min-w-0">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className={`mt-1 truncate text-sm ${mono ? "font-mono text-xs" : ""}`}>
        {value}
      </p>
    </div>
  )
}

function CapabilityList({
  title,
  values,
}: {
  title: string
  values: string[]
}) {
  return (
    <div className="rounded-xl border p-4">
      <p className="text-sm font-medium">{title}</p>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {values.length > 0 ? (
          values.map((value) => (
            <Badge key={value} variant="secondary">
              {value}
            </Badge>
          ))
        ) : (
          <span className="text-xs text-muted-foreground">未配置</span>
        )}
      </div>
    </div>
  )
}

function EnvironmentBadges({
  resource,
  detailed = false,
}: {
  resource: Resource
  detailed?: boolean
}) {
  const tools = stringList(resource.spec.agentTools)
  const skills = stringList(resource.spec.skillIds)
  const mcp = stringList(resource.spec.mcpServerIds)
  const variables = stringList(resource.spec.variableIds)
  const credentials = stringList(resource.spec.credentialIds)

  if (!detailed) {
    return (
      <div className="hidden shrink-0 gap-1 sm:flex">
        <Badge variant="outline">
          {specString(resource.spec, "driver") === "vm" ? "VM" : "Docker"}
        </Badge>
        {tools.length > 0 && (
          <Badge variant="outline">{tools.length} Agent</Badge>
        )}
      </div>
    )
  }

  const items = [
    { icon: BotIcon, label: `${tools.length} Agent 工具` },
    { icon: SparklesIcon, label: `${skills.length} Skills` },
    { icon: PlugZapIcon, label: `${mcp.length} MCP` },
    {
      icon: KeyRoundIcon,
      label: `${credentials.length} Keys · ${variables.length} 变量`,
    },
  ]
  return (
    <div className="flex flex-wrap gap-2">
      {items.map((item) => (
        <Badge key={item.label} variant="outline">
          <item.icon />
          {item.label}
        </Badge>
      ))}
    </div>
  )
}

function environmentSummary(
  environment: Resource,
  resources: Resource[],
  servers: ManagedServer[]
) {
  const driver =
    specString(environment.spec, "driver") === "vm" ? "VM" : "Docker"
  const server = servers.find((item) => item.id === environment.spec.serverId)
  return `${driver} · ${environmentImage(environment, resources)} · ${server?.name ?? "未绑定服务器"}`
}

function environmentImage(environment: Resource, resources: Resource[]) {
  const reference = specString(environment.spec, "imageReference")
  if (reference) return reference
  return (
    resources.find((item) => item.id === environment.spec.imageId)?.name ??
    "未选镜像"
  )
}

function specString(spec: Record<string, unknown>, key: string) {
  const value = spec[key]
  return typeof value === "string" ? value : ""
}

function stringList(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : []
}

function isRunningSandbox(resource: Resource) {
  return resource.spec.status === "running"
}

function sandboxStatus(resource: Resource) {
  switch (resource.spec.status) {
    case "running":
      return "运行中"
    case "stopped":
      return "已停止"
    case "error":
      return "创建失败"
    case "starting":
      return "启动中"
    case "stopping":
      return "停止中"
    case "deleting":
      return "删除中"
    default:
      return "等待创建"
  }
}
