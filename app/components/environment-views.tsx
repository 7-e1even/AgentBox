"use client"

import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import Link from "next/link"
import {
  BotIcon,
  BoxIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  ContainerIcon,
  KeyRoundIcon,
  LayoutTemplateIcon,
  LoaderCircleIcon,
  MonitorIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  PlugZapIcon,
  RocketIcon,
  RotateCwIcon,
  ServerIcon,
  Settings2Icon,
  SquareIcon,
  SparklesIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react"

import type { AppSection } from "@/lib/app-section"
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
import { Progress } from "@/components/ui/progress"
import type { Resource } from "@/lib/platform-schema"
import { runtimeInventoryImages } from "@/lib/runtime-images"
import type { ManagedServer } from "@/lib/server-schema"

type EnvironmentProps = {
  resources: Resource[]
  servers: ManagedServer[]
}

export function DashboardView({
  resources,
  servers,
  configuredCredentials,
  canMutate,
  onNavigate,
  onCreateEnvironment,
  onCreateSandbox,
}: EnvironmentProps & {
  configuredCredentials: number
  canMutate: boolean
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
      runtimeInventoryImages(server, "docker").length +
      server.inventory.vmImages.length,
    0
  )
  const canPullDockerImage = onlineServers.some((server) =>
    server.capabilities.includes("docker")
  )
  const enabledEnvironments = environments.filter((item) => item.enabled)
  const readiness = [
    {
      label: "可用服务器",
      ready: onlineServers.length > 0,
      value: `${onlineServers.length} 台在线`,
      section: "servers" as const,
    },
    {
      label: "基础镜像",
      ready: imageCount > 0 || canPullDockerImage,
      value: imageCount > 0 ? `${imageCount} 个可用` : "Docker 可自动拉取",
      section: "images" as const,
    },
    {
      label: "沙箱模板",
      ready: enabledEnvironments.length > 0,
      value: `${enabledEnvironments.length} 个可用`,
      section: "runtimes" as const,
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
                Agent 沙箱运行底座
              </h2>
              <p className="text-sm leading-6 text-pretty text-muted-foreground">
                先配置可复用的沙箱模板，再按需创建隔离沙箱。
              </p>
            </div>
            <div className="flex shrink-0 flex-wrap gap-2">
              {canMutate ? (
                <>
                  <Button variant="outline" onClick={onCreateEnvironment}>
                    <PlusIcon data-icon="inline-start" />
                    新建沙箱模板
                  </Button>
                  <Button onClick={onCreateSandbox}>
                    <BoxIcon data-icon="inline-start" />
                    创建沙箱
                  </Button>
                </>
              ) : null}
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              icon={LayoutTemplateIcon}
              label="沙箱模板"
              value={environments.length}
              detail="镜像、工具与凭据组合"
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
              icon={KeyRoundIcon}
              label="模型凭据"
              value={configuredCredentials}
              detail="注入沙箱模板后自动生效"
              onClick={() => onNavigate("access")}
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
                <CardTitle>沙箱模板</CardTitle>
                <CardDescription>
                  可复用的沙箱蓝图，包含镜像、Agent 工具、Skills、MCP 与凭据。
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
                        ) : specString(environment.spec, "driver") ===
                          "docker" ? (
                          <ContainerIcon className="size-4" />
                        ) : (
                          <BoxIcon className="size-4" />
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
                    <p className="font-medium">还没有沙箱模板</p>
                    <p className="mt-1 text-sm text-muted-foreground">
                      选择服务器和镜像，再加入需要的 Agent 工具与凭据。
                    </p>
                    {canMutate ? (
                      <Button
                        size="sm"
                        className="mt-4"
                        onClick={onCreateEnvironment}
                      >
                        创建第一个沙箱模板
                      </Button>
                    ) : null}
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
  servers,
  canMutate,
  onCreate,
  onEdit,
  onDelete,
  onLaunch,
}: EnvironmentProps & {
  canMutate: boolean
  onCreate: () => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
  onLaunch: (resource: Resource) => void
}) {
  const environments = resources.filter((item) => item.kind === "runtime")
  const columns = useMemo(
    () =>
      environmentColumns({
        servers,
        canMutate,
        onEdit,
        onDelete,
        onLaunch,
      }),
    [canMutate, onDelete, onEdit, onLaunch, servers]
  )

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="沙箱模板"
        count={environments.length}
        action={
          canMutate ? (
            <Button size="sm" onClick={onCreate}>
              <PlusIcon data-icon="inline-start" />
              新建沙箱模板
            </Button>
          ) : undefined
        }
      />
      <CollectionContent>
        {environments.length === 0 ? (
          <Empty className="mx-auto min-h-[28rem] max-w-3xl border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <LayoutTemplateIcon />
              </EmptyMedia>
              <EmptyTitle>还没有沙箱模板</EmptyTitle>
              <EmptyDescription>
                预先选择隔离方式与镜像，并装好 Agent 工具、Skills、MCP
                与模型凭据。
              </EmptyDescription>
            </EmptyHeader>
            {canMutate ? (
              <EmptyContent>
                <Button onClick={onCreate}>创建第一个沙箱模板</Button>
              </EmptyContent>
            ) : null}
          </Empty>
        ) : (
          <DataTable
            data={environments}
            columns={columns}
            getRowId={(environment) => environment.id}
            initialPageSize={8}
            searchPlaceholder="搜索沙箱模板…"
            searchValue={(environment) =>
              `${environment.name} ${environment.description}`
            }
            filters={[
              {
                columnId: "driver",
                title: "隔离方式",
                options: [
                  { label: "Docker", value: "docker" },
                  { label: "BoxLite", value: "boxlite" },
                  { label: "Microsandbox", value: "microsandbox" },
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
  servers,
  canMutate,
  canOpenWorkspace,
  onCreate,
  onEdit,
  busyId,
  onAction,
  onDelete,
}: EnvironmentProps & {
  canMutate: boolean
  canOpenWorkspace: boolean
  onCreate: () => void
  onEdit: (resource: Resource) => void
  busyId: string | null
  onAction: (
    resource: Resource,
    action: "start" | "stop" | "restart" | "delete"
  ) => Promise<void>
  onDelete: (resource: Resource) => void
}) {
  const sandboxes = resources.filter((item) => item.kind === "sandbox")
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const columns = useMemo(
    () =>
      sandboxColumns({
        resources,
        servers,
        busyId,
        canMutate,
        canOpenWorkspace,
        onOpen: (sandbox) => setSelectedId(sandbox.id),
        onAction,
        onEdit,
        onDelete,
      }),
    [
      busyId,
      canMutate,
      canOpenWorkspace,
      onAction,
      onDelete,
      onEdit,
      resources,
      servers,
      setSelectedId,
    ]
  )
  const selected = sandboxes.find((item) => item.id === selectedId) ?? null
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="沙箱"
        count={sandboxes.length}
        action={
          canMutate ? (
            <Button size="sm" onClick={onCreate}>
              <PlusIcon data-icon="inline-start" />
              创建沙箱
            </Button>
          ) : undefined
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
                选择一个沙箱模板即可创建；服务器、工具和凭据会自动继承。
              </EmptyDescription>
            </EmptyHeader>
            {canMutate ? (
              <EmptyContent>
                <Button onClick={onCreate}>创建第一个沙箱</Button>
              </EmptyContent>
            ) : null}
          </Empty>
        ) : (
          <DataTable
            data={sandboxes}
            columns={columns}
            tableClassName="table-fixed"
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
          environment={resources.find(
            (item) => item.id === selected.spec.runtimeId
          )}
          server={servers.find((item) => item.id === selected.spec.serverId)}
          resources={resources}
          canOpenWorkspace={canOpenWorkspace}
          onOpenChange={(open) => !open && setSelectedId(null)}
        />
      )}
    </section>
  )
}

function environmentColumns({
  servers,
  canMutate,
  onEdit,
  onDelete,
  onLaunch,
}: {
  servers: ManagedServer[]
  canMutate: boolean
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
  onLaunch: (resource: Resource) => void
}): ColumnDef<Resource>[] {
  const columns: ColumnDef<Resource>[] = [
    {
      id: "name",
      accessorFn: (environment) => environment.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="沙箱模板" />
      ),
      cell: ({ row }) => {
        const driver = specString(row.original.spec, "driver")
        return (
          <CollectionTablePrimaryContent
            icon={
              driver === "vm"
                ? MonitorIcon
                : driver === "docker"
                  ? ContainerIcon
                  : BoxIcon
            }
            title={row.original.name}
            description={row.original.description || "可复用的 Agent 工作环境"}
            onClick={canMutate ? () => onEdit(row.original) : undefined}
          />
        )
      },
      meta: { label: "沙箱模板" },
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
          {runtimeDriverLabel(String(getValue()))}
        </span>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "隔离方式", className: "hidden lg:table-cell" },
    },
    {
      id: "tools",
      accessorFn: (environment) =>
        stringList(environment.spec.agentTools).join(" · "),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="已装 Agent" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-56 truncate text-muted-foreground">
          {String(getValue()) || "未安装"}
        </span>
      ),
      meta: { label: "已装 Agent", className: "hidden xl:table-cell" },
    },
  ]
  if (canMutate) {
    columns.push({
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
                <DropdownMenuModalItem onOpen={() => onEdit(row.original)}>
                  <PencilIcon />
                  编辑
                </DropdownMenuModalItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuModalItem
                variant="destructive"
                onOpen={() => onDelete(row.original)}
              >
                <Trash2Icon />
                删除
              </DropdownMenuModalItem>
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
    })
  }

  return columns
}

function sandboxColumns({
  resources,
  servers,
  busyId,
  canMutate,
  canOpenWorkspace,
  onOpen,
  onAction,
  onEdit,
  onDelete,
}: {
  resources: Resource[]
  servers: ManagedServer[]
  busyId: string | null
  canMutate: boolean
  canOpenWorkspace: boolean
  onOpen: (sandbox: Resource) => void
  onAction: (
    resource: Resource,
    action: "start" | "stop" | "restart" | "delete"
  ) => Promise<void>
  onEdit: (resource: Resource) => void
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
      meta: { label: "状态", className: "w-24" },
    },
    {
      id: "environment",
      accessorFn: (sandbox) =>
        resources.find((resource) => resource.id === sandbox.spec.runtimeId)
          ?.name ?? "未绑定",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="沙箱模板" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: {
        label: "沙箱模板",
        className: "hidden w-36 md:table-cell",
      },
    },
    {
      id: "tools",
      accessorFn: (sandbox) => {
        const inherited = resources.find(
          (resource) => resource.id === sandbox.spec.runtimeId
        )?.spec.agentTools
        const tools = Array.isArray(sandbox.spec.agentTools)
          ? sandbox.spec.agentTools
          : inherited
        return stringList(tools).join(" · ")
      },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="沙箱内 Agent" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-56 truncate text-muted-foreground">
          {String(getValue()) || "未安装"}
        </span>
      ),
      meta: {
        label: "沙箱内 Agent",
        className: "hidden w-44 lg:table-cell",
      },
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
      meta: {
        label: "服务器",
        className: "hidden w-36 xl:table-cell",
      },
    },
    {
      id: "actions",
      cell: ({ row }) => {
        const sandbox = row.original
        const busy = busyId === sandbox.id
        return (
          <div className="flex items-center justify-end gap-1">
            {sandbox.spec.status === "running" ? (
              <>
                {canOpenWorkspace ? (
                  <Button
                    size="sm"
                    variant="outline"
                    className="px-2 sm:px-3"
                    asChild
                  >
                    <Link
                      href={`/sandboxes/${sandbox.id}`}
                      aria-label={`打开 ${sandbox.name} 工作台`}
                    >
                      <MonitorIcon data-icon="inline-start" />
                      <span className="hidden sm:inline">工作台</span>
                    </Link>
                  </Button>
                ) : null}
                {canMutate ? (
                  <Button
                    size="sm"
                    variant="outline"
                    className="px-2 sm:px-3"
                    aria-label={`停止 ${sandbox.name}`}
                    disabled={busy}
                    onClick={() => void onAction(sandbox, "stop")}
                  >
                    {busy ? (
                      <LoaderCircleIcon className="animate-spin" />
                    ) : (
                      <SquareIcon />
                    )}
                    <span className="hidden sm:inline">停止</span>
                  </Button>
                ) : null}
              </>
            ) : sandbox.spec.status === "stopped" ||
              sandbox.spec.status === "error" ? (
              canMutate ? (
                <Button
                  size="sm"
                  variant="outline"
                  className="px-2 sm:px-3"
                  aria-label={`启动 ${sandbox.name}`}
                  disabled={busy}
                  onClick={() => void onAction(sandbox, "start")}
                >
                  {busy ? (
                    <LoaderCircleIcon className="animate-spin" />
                  ) : (
                    <PlayIcon />
                  )}
                  <span className="hidden sm:inline">启动</span>
                </Button>
              ) : null
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
                  <DropdownMenuModalItem onOpen={() => onOpen(sandbox)}>
                    <Settings2Icon />
                    管理
                  </DropdownMenuModalItem>
                  {canMutate ? (
                    <DropdownMenuModalItem onOpen={() => onEdit(sandbox)}>
                      <PencilIcon />
                      编辑配置
                    </DropdownMenuModalItem>
                  ) : null}
                  {canMutate && sandbox.spec.status === "running" && (
                    <DropdownMenuItem
                      disabled={busy}
                      onClick={() => void onAction(sandbox, "restart")}
                    >
                      <RotateCwIcon />
                      重启并应用配置
                    </DropdownMenuItem>
                  )}
                </DropdownMenuGroup>
                {canMutate ? (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuModalItem
                      variant="destructive"
                      disabled={
                        busy ||
                        [
                          "requested",
                          "starting",
                          "stopping",
                          "restarting",
                          "deleting",
                        ].includes(String(sandbox.spec.status ?? ""))
                      }
                      onOpen={() => onDelete(sandbox)}
                    >
                      <Trash2Icon />
                      删除
                    </DropdownMenuModalItem>
                  </>
                ) : null}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        )
      },
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-28 sm:w-48" },
    },
  ]
}

function sandboxFilterStatus(sandbox: Resource) {
  const status = String(sandbox.spec.status ?? "")
  return [
    "requested",
    "starting",
    "stopping",
    "restarting",
    "deleting",
  ].includes(status)
    ? "pending"
    : status || "error"
}

function SandboxDetailsDialog({
  sandbox,
  environment,
  server,
  resources,
  canOpenWorkspace,
  onOpenChange,
}: {
  sandbox: Resource
  environment?: Resource
  server?: ManagedServer
  resources: Resource[]
  canOpenWorkspace: boolean
  onOpenChange: (open: boolean) => void
}) {
  const tools = stringList(
    Array.isArray(sandbox.spec.agentTools)
      ? sandbox.spec.agentTools
      : environment?.spec.agentTools
  )
  const credentials = stringList(
    Array.isArray(sandbox.spec.credentialIds)
      ? sandbox.spec.credentialIds
      : environment?.spec.credentialIds
  )
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
  const failureMessage =
    sandbox.spec.status === "error" && typeof sandbox.spec.message === "string"
      ? sandbox.spec.message.trim()
      : ""

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{sandbox.name}</DialogTitle>
          <DialogDescription>
            查看这个沙箱的沙箱模板、运行位置和已配置能力。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-3 rounded-xl border p-4 sm:grid-cols-3">
            <Detail label="状态" value={sandboxStatus(sandbox)} />
            <Detail label="服务器" value={server?.name ?? "未知服务器"} />
            <Detail label="沙箱模板" value={environment?.name ?? "未绑定"} />
            <Detail label="实例" value={externalId} mono />
            <Detail
              label="沙箱内 Agent"
              value={tools.length > 0 ? tools.join(" · ") : "未预装"}
            />
            <Detail
              label="模型凭据"
              value={
                credentials.length > 0 ? `${credentials.length} 个` : "未配置"
              }
            />
          </div>

          {failureMessage && (
            <div className="rounded-xl border border-destructive/20 bg-destructive/5 p-4">
              <div className="flex items-center gap-2 text-sm font-medium text-destructive">
                <TriangleAlertIcon className="size-4" />
                失败原因
              </div>
              <pre className="mt-2 max-h-48 overflow-auto font-mono text-xs leading-relaxed break-words whitespace-pre-wrap text-foreground">
                {failureMessage}
              </pre>
            </div>
          )}

          <div className="grid gap-3 sm:grid-cols-2">
            <CapabilityList title="Skills" values={skills} />
            <CapabilityList title="MCP Servers" values={mcpServers} />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
          {sandbox.spec.status === "running" && canOpenWorkspace && (
            <Button asChild>
              <Link href={`/sandboxes/${sandbox.id}`}>
                <MonitorIcon data-icon="inline-start" />
                打开工作台
              </Link>
            </Button>
          )}
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
          {runtimeDriverLabel(specString(resource.spec, "driver"))}
        </Badge>
        {tools.length > 0 && (
          <Badge variant="outline">{tools.length} 个 Agent</Badge>
        )}
      </div>
    )
  }

  const items = [
    { icon: BotIcon, label: `${tools.length} 个 Agent` },
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
  const driver = runtimeDriverLabel(specString(environment.spec, "driver"))
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

function runtimeDriverLabel(driver: string) {
  if (driver === "docker") return "Docker"
  if (driver === "boxlite") return "BoxLite"
  if (driver === "microsandbox") return "Microsandbox"
  if (driver === "vm") return "VM（旧）"
  return "未配置"
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
      return "运行异常"
    case "starting":
      return "启动中"
    case "stopping":
      return "停止中"
    case "restarting":
      return "重启中"
    case "deleting":
      return "删除中"
    default:
      return "等待创建"
  }
}
