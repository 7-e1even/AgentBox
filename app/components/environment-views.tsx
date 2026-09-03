"use client"

import { useEffect, useMemo, useState } from "react"
import type { CellContext, ColumnDef } from "@tanstack/react-table"
import Link from "next/link"
import {
  BotIcon,
  BoxIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  ChevronRightIcon,
  CircleIcon,
  ContainerIcon,
  KeyRoundIcon,
  LayoutTemplateIcon,
  LoaderCircleIcon,
  MonitorIcon,
  MoreHorizontalIcon,
  NetworkIcon,
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
import { SandboxAgentToolsPanel } from "@/components/sandbox-agent-tools-panel"
import {
  SandboxInstallCancelButton,
  SandboxInstallCancelDialog,
} from "@/components/sandbox-install-cancel-button"
import { ExtensionProgressPanel } from "@/components/extension-progress-panel"
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
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  provisioningProgressSchema,
  type ProvisioningProgress,
  type Resource,
  type ResourceOfKind,
} from "@/lib/platform-schema"
import {
  provisioningAgentToolStatusLabel,
  provisioningCacheLabel,
  provisioningDuration,
  provisioningStageLabel,
  sandboxInstallCancellation,
} from "@/lib/provisioning"
import { agentToolOptions, supportedAgentToolList } from "@/lib/agent-tools"
import { runtimeInventoryImages } from "@/lib/runtime-images"
import type { ManagedServer } from "@/lib/server-schema"
import {
  sandboxProxyOperationSchema,
  type ManagedNetworkProxy,
} from "@/lib/network-proxy-schema"
import { cn } from "@/lib/utils"

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
  proxies,
  canMutate,
  canOpenWorkspace,
  onCreate,
  onEdit,
  busyId,
  onAction,
  onApplyNetworkProxy,
  onDelete,
  onResourceChange,
  onRefresh,
}: EnvironmentProps & {
  proxies: ManagedNetworkProxy[]
  canMutate: boolean
  canOpenWorkspace: boolean
  onCreate: () => void
  onEdit: (resource: Resource) => void
  busyId: string | null
  onAction: (
    resource: Resource,
    action: "start" | "stop" | "restart" | "delete" | "cancel-install"
  ) => Promise<void>
  onApplyNetworkProxy: (
    sandbox: ResourceOfKind<"sandbox">,
    proxyId: string
  ) => Promise<Resource>
  onDelete: (resource: Resource) => void
  onResourceChange: (resource: Resource) => void
  onRefresh: () => Promise<void>
}) {
  const sandboxes = resources.filter((item) => item.kind === "sandbox")
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [agentToolsId, setAgentToolsId] = useState<string | null>(null)
  const [cancelInstallId, setCancelInstallId] = useState<string | null>(null)
  const columns = useMemo(
    () =>
      sandboxColumns({
        resources,
        servers,
        busyId,
        canMutate,
        canOpenWorkspace,
        onOpen: (sandbox) => setSelectedId(sandbox.id),
        onOpenAgentTools: (sandbox) => setAgentToolsId(sandbox.id),
        onCancelInstall: (sandbox) => setCancelInstallId(sandbox.id),
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
      setAgentToolsId,
      setCancelInstallId,
      setSelectedId,
    ]
  )
  const selected = sandboxes.find((item) => item.id === selectedId) ?? null
  const agentToolsSandbox =
    sandboxes.find((item) => item.id === agentToolsId) ?? null
  const cancelInstallSandbox =
    sandboxes.find((item) => item.id === cancelInstallId) ?? null
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
                  { label: "已取消", value: "cancelled" },
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
          environment={resources
            .filter((item) => item.kind === "runtime")
            .find((item) => item.id === selected.spec.runtimeId)}
          server={servers.find((item) => item.id === selected.spec.serverId)}
          resources={resources}
          proxies={proxies}
          canMutate={canMutate}
          canOpenWorkspace={canOpenWorkspace}
          busy={busyId === selected.id}
          onCancelInstall={() => onAction(selected, "cancel-install")}
          onApplyNetworkProxy={onApplyNetworkProxy}
          onResourceChange={onResourceChange}
          onOpenChange={(open) => !open && setSelectedId(null)}
        />
      )}
      {agentToolsSandbox && (
        <SandboxAgentToolsDialog
          sandbox={agentToolsSandbox}
          resources={resources}
          onResourceChange={onResourceChange}
          onRefresh={onRefresh}
          onOpenChange={(open) => !open && setAgentToolsId(null)}
        />
      )}
      {canMutate && cancelInstallSandbox && (
        <SandboxInstallCancelDialog
          sandbox={cancelInstallSandbox}
          busy={busyId === cancelInstallSandbox.id}
          onCancel={() => onAction(cancelInstallSandbox, "cancel-install")}
          open
          onOpenChange={(open) => !open && setCancelInstallId(null)}
          onCloseAutoFocus={(event) => {
            const trigger = document.getElementById(
              `sandbox-actions-${cancelInstallSandbox.id}`
            )
            if (trigger) {
              event.preventDefault()
              trigger.focus()
            }
          }}
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
}): ColumnDef<ResourceOfKind<"runtime">>[] {
  const columns: ColumnDef<ResourceOfKind<"runtime">>[] = [
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

type SandboxActionsMeta = {
  className?: string
  busyId: string | null
  canMutate: boolean
  canOpenWorkspace: boolean
  onOpen: (sandbox: Resource) => void
  onOpenAgentTools: (sandbox: Resource) => void
  onCancelInstall: (sandbox: Resource) => void
  onAction: (
    resource: Resource,
    action: "start" | "stop" | "restart" | "delete" | "cancel-install"
  ) => Promise<void>
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
}

function sandboxColumns({
  resources,
  servers,
  busyId,
  canMutate,
  canOpenWorkspace,
  onOpen,
  onOpenAgentTools,
  onCancelInstall,
  onAction,
  onEdit,
  onDelete,
}: EnvironmentProps & SandboxActionsMeta): ColumnDef<
  ResourceOfKind<"sandbox">
>[] {
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
        const inherited = resources
          .filter((item) => item.kind === "runtime")
          .find((resource) => resource.id === sandbox.spec.runtimeId)
          ?.spec.agentTools
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
      cell: SandboxActionsCell,
      enableSorting: false,
      enableHiding: false,
      meta: {
        className: "w-28 sm:w-56 lg:w-72",
        busyId,
        canMutate,
        canOpenWorkspace,
        onOpen,
        onOpenAgentTools,
        onCancelInstall,
        onAction,
        onEdit,
        onDelete,
      } satisfies SandboxActionsMeta,
    },
  ]
}

function SandboxActionsCell({
  row,
  column,
}: CellContext<ResourceOfKind<"sandbox">, unknown>) {
  const {
    busyId,
    canMutate,
    canOpenWorkspace,
    onOpen,
    onOpenAgentTools,
    onCancelInstall,
    onAction,
    onEdit,
    onDelete,
  } = column.columnDef.meta as SandboxActionsMeta
  const sandbox = row.original
  const busy = busyId === sandbox.id
  const cancellation = sandboxInstallCancellation(sandbox.spec)
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
              aria-label={`更新 ${sandbox.name} Agent`}
              onClick={() => onOpenAgentTools(sandbox)}
            >
              <BotIcon data-icon="inline-start" />
              <span className="hidden lg:inline">Agent 更新</span>
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
      ) : (sandbox.spec.status === "stopped" ||
          sandbox.spec.status === "error") &&
        !sandbox.spec.provisioning?.cancelRequested ? (
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
            id={`sandbox-actions-${sandbox.id}`}
            size="icon-sm"
            variant="ghost"
            aria-label={`${sandbox.name} 操作`}
          >
            <MoreHorizontalIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className={cn(canMutate && cancellation === "unsupported" && "w-56")}
        >
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
              {cancellation !== "hidden" && (
                <DropdownMenuModalItem
                  disabled={busy || cancellation !== "available"}
                  onOpen={() => onCancelInstall(sandbox)}
                >
                  {busy || cancellation === "cancelling" ? (
                    <LoaderCircleIcon className="animate-spin" />
                  ) : (
                    <SquareIcon />
                  )}
                  <span>
                    {cancellation === "cancelling" ? "正在取消" : "取消安装"}
                    {cancellation === "unsupported" && (
                      <span className="block text-xs text-muted-foreground">
                        Worker 尚未确认取消能力
                      </span>
                    )}
                  </span>
                </DropdownMenuModalItem>
              )}
              <DropdownMenuModalItem
                variant="destructive"
                disabled={
                  busy ||
                  [
                    "requested",
                    "starting",
                    "cancelling",
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
}

function sandboxFilterStatus(sandbox: ResourceOfKind<"sandbox">) {
  const status = String(sandbox.spec.status ?? "")
  return [
    "requested",
    "starting",
    "cancelling",
    "stopping",
    "restarting",
    "deleting",
  ].includes(status)
    ? "pending"
    : status || "error"
}

function SandboxAgentToolsDialog({
  sandbox,
  resources,
  onResourceChange,
  onRefresh,
  onOpenChange,
}: {
  sandbox: ResourceOfKind<"sandbox">
  resources: Resource[]
  onResourceChange: (resource: Resource) => void
  onRefresh: () => Promise<void>
  onOpenChange: (open: boolean) => void
}) {
  const runtime = resources
    .filter((item) => item.kind === "runtime")
    .find((resource) => resource.id === sandbox.spec.runtimeId)
  const toolIds = supportedAgentToolList(
    Array.isArray(sandbox.spec.agentTools)
      ? sandbox.spec.agentTools
      : runtime?.spec.agentTools
  )

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[min(46rem,calc(100dvh-2rem))] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl">
        <DialogHeader className="shrink-0 gap-1 border-b px-4 py-4 pr-12">
          <DialogTitle className="flex items-center gap-2">
            <BotIcon className="size-4" aria-hidden="true" />
            Agent 更新
          </DialogTitle>
          <DialogDescription>
            {sandbox.name} · 在当前沙箱内原地检测和更新
            {sandbox.spec.driver === "boxlite"
              ? "；更新期间终端会短暂重连。"
              : "，不重建沙箱。"}
          </DialogDescription>
        </DialogHeader>
        <SandboxAgentToolsPanel
          sandboxId={sandbox.id}
          spec={sandbox.spec}
          toolIds={toolIds}
          running={sandbox.spec.status === "running"}
          onResourceChange={onResourceChange}
          onRefresh={onRefresh}
        />
      </DialogContent>
    </Dialog>
  )
}

function SandboxDetailsDialog({
  sandbox,
  environment,
  server,
  resources,
  proxies,
  canMutate,
  canOpenWorkspace,
  busy,
  onCancelInstall,
  onApplyNetworkProxy,
  onResourceChange,
  onOpenChange,
}: {
  sandbox: ResourceOfKind<"sandbox">
  environment?: ResourceOfKind<"runtime">
  server?: ManagedServer
  resources: Resource[]
  proxies: ManagedNetworkProxy[]
  canMutate: boolean
  canOpenWorkspace: boolean
  busy: boolean
  onCancelInstall: () => Promise<void>
  onApplyNetworkProxy: (
    sandbox: ResourceOfKind<"sandbox">,
    proxyId: string
  ) => Promise<Resource>
  onResourceChange: (resource: Resource) => void
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
  const provisioningResult = provisioningProgressSchema.safeParse(
    sandbox.spec.provisioning
  )
  const provisioning = provisioningResult.success
    ? provisioningResult.data
    : null
  const isProvisioning =
    provisioning?.status === "running" || provisioning?.status === "cancelling"
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!isProvisioning) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [isProvisioning])

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] grid-cols-[minmax(0,1fr)] gap-3 overflow-y-auto sm:max-w-3xl">
        <DialogHeader className="gap-1 pr-8">
          <DialogTitle>{sandbox.name}</DialogTitle>
          <DialogDescription>沙箱模板、运行位置与配置概览。</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="overflow-hidden rounded-xl border">
            <div className="grid gap-x-4 gap-y-2.5 p-3 sm:grid-cols-3">
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

            {provisioning?.stage && (
              <div className="grid gap-x-4 gap-y-2.5 border-t bg-muted/20 px-3 py-2.5 sm:grid-cols-3">
                <Detail
                  label="创建阶段"
                  value={provisioningStageLabel(provisioning.stage)}
                />
                <Detail
                  label="累计耗时"
                  value={provisioningElapsedDuration(provisioning, now)}
                />
                <Detail
                  label="工具缓存"
                  value={provisioningCacheLabel(
                    provisioning.cacheStatus,
                    provisioning.cacheReason
                  )}
                />
                {!failureMessage && provisioning.message && (
                  <p className="line-clamp-2 text-xs break-words text-muted-foreground sm:col-span-3">
                    {provisioning.message}
                  </p>
                )}
              </div>
            )}
          </div>

          <SandboxNetworkProxyPanel
            key={`${sandbox.id}:${String(sandbox.spec.proxyId ?? environment?.spec.proxyId ?? "")}`}
            sandbox={sandbox}
            environment={environment}
            proxies={proxies}
            canMutate={canMutate}
            onApply={onApplyNetworkProxy}
            onResourceChange={onResourceChange}
          />

          {tools.length > 0 && provisioning && (
            <AgentToolProgressPanel
              key={`${sandbox.id}:${provisioning.status}`}
              tools={tools}
              provisioning={provisioning}
              now={now}
            />
          )}

          <ExtensionProgressPanel
            progress={provisioning}
            states={sandbox.spec.extensionStates}
            snapshots={sandbox.spec.extensionSnapshots?.map((extension) => ({
              id: extension.id,
              name: extension.name,
              version: extension.spec.version,
            }))}
          />

          {failureMessage && (
            <div className="rounded-xl border border-destructive/20 bg-destructive/5 p-3">
              <div className="flex items-center gap-2 text-sm font-medium text-destructive">
                <TriangleAlertIcon className="size-4" />
                失败原因
              </div>
              <pre className="mt-2 font-mono text-xs leading-5 break-words whitespace-pre-wrap text-foreground">
                {failureMessage}
              </pre>
            </div>
          )}

          {skills.length > 0 || mcpServers.length > 0 ? (
            <div className="grid gap-x-6 gap-y-3 border-t pt-3 sm:grid-cols-2">
              <CapabilityList title="Skills" values={skills} />
              <CapabilityList title="MCP Servers" values={mcpServers} />
            </div>
          ) : (
            <p className="border-t pt-3 text-xs text-muted-foreground">
              Skills 与 MCP Servers 均未配置
            </p>
          )}
        </div>

        <DialogFooter className="mx-0 mb-0 min-w-0 p-3">
          {canMutate && (
            <SandboxInstallCancelButton
              sandbox={sandbox}
              busy={busy}
              onCancel={onCancelInstall}
            />
          )}
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

function SandboxNetworkProxyPanel({
  sandbox,
  environment,
  proxies,
  canMutate,
  onApply,
  onResourceChange,
}: {
  sandbox: ResourceOfKind<"sandbox">
  environment?: ResourceOfKind<"runtime">
  proxies: ManagedNetworkProxy[]
  canMutate: boolean
  onApply: (
    sandbox: ResourceOfKind<"sandbox">,
    proxyId: string
  ) => Promise<Resource>
  onResourceChange: (resource: Resource) => void
}) {
  const inheritedProxyId =
    typeof environment?.spec.proxyId === "string"
      ? environment.spec.proxyId.trim()
      : ""
  const desiredProxyId = Object.hasOwn(sandbox.spec, "proxyId")
    ? typeof sandbox.spec.proxyId === "string"
      ? sandbox.spec.proxyId.trim()
      : ""
    : inheritedProxyId
  const operationResult = sandboxProxyOperationSchema.safeParse(
    sandbox.spec.proxyOperation
  )
  const operation = operationResult.success ? operationResult.data : null
  const appliedProxyId = operation
    ? operation.appliedProxyId
    : typeof sandbox.spec.appliedProxyId === "string"
      ? sandbox.spec.appliedProxyId.trim()
      : desiredProxyId
  const network =
    typeof sandbox.spec.network === "string"
      ? sandbox.spec.network
      : String(environment?.spec.network ?? "")
  const [selection, setSelection] = useState(
    desiredProxyId === "" ? "__direct__" : desiredProxyId
  )
  const [applying, setApplying] = useState(false)
  const operationActive =
    operation?.status === "queued" || operation?.status === "running"
  const sandboxTransitioning =
    [
      "requested",
      "starting",
      "cancelling",
      "cancelled",
      "stopping",
      "restarting",
      "deleting",
    ].includes(String(sandbox.spec.status ?? "")) ||
    Boolean(sandbox.spec.provisioning?.cancelRequested)
  const selectedProxyId = selection === "__direct__" ? "" : selection
  const selectedProxy = proxies.find((proxy) => proxy.id === selectedProxyId)
  const desiredProxy = proxies.find((proxy) => proxy.id === desiredProxyId)
  const appliedProxy = proxies.find((proxy) => proxy.id === appliedProxyId)
  const options = proxies.filter(
    (proxy) =>
      proxy.enabled ||
      proxy.id === desiredProxyId ||
      proxy.id === appliedProxyId
  )

  let statusLabel = appliedProxyId === "" ? "直连" : "代理已应用"
  let statusVariant: "default" | "secondary" | "destructive" | "outline" =
    appliedProxyId === "" ? "secondary" : "default"
  if (operationActive) {
    statusLabel = "正在应用"
    statusVariant = "outline"
  } else if (operation?.status === "failed") {
    statusLabel = "应用失败"
    statusVariant = "destructive"
  } else if (operation?.status === "pending-start") {
    statusLabel = "等待下次启动"
    statusVariant = "outline"
  } else if (desiredProxyId !== appliedProxyId) {
    statusLabel = "配置待同步"
    statusVariant = "outline"
  }

  async function applyProxy() {
    setApplying(true)
    try {
      const resource = await onApply(sandbox, selectedProxyId)
      onResourceChange(resource)
    } finally {
      setApplying(false)
    }
  }

  return (
    <div className="overflow-hidden rounded-xl border">
      <div className="flex flex-col gap-3 p-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 items-start gap-3">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg border bg-muted/30">
            <NetworkIcon className="size-4" aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <p className="text-sm font-medium">网络出口</p>
              <Badge variant={statusVariant}>{statusLabel}</Badge>
            </div>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">
              当前实际出口：
              {appliedProxyId === ""
                ? "直连"
                : (appliedProxy?.name ?? appliedProxyId)}
              {appliedProxy ? ` · ${appliedProxy.scheme.toUpperCase()}` : ""}
            </p>
          </div>
        </div>

        <div className="grid w-full min-w-0 grid-cols-[minmax(0,1fr)] gap-2 sm:w-[24rem] sm:grid-cols-[minmax(0,1fr)_auto]">
          <Select
            value={selection}
            disabled={
              !canMutate ||
              applying ||
              operationActive ||
              sandboxTransitioning ||
              network === "none"
            }
            onValueChange={setSelection}
          >
            <SelectTrigger
              aria-label="选择沙箱网络出口"
              className="w-full min-w-0"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectLabel>网络出口</SelectLabel>
                <SelectItem value="__direct__">直连 · 不使用代理</SelectItem>
                {options.map((proxy) => (
                  <SelectItem
                    key={proxy.id}
                    value={proxy.id}
                    disabled={!proxy.enabled}
                  >
                    {proxy.name} · {proxy.scheme.toUpperCase()}
                    {proxy.enabled ? "" : " · 已停用"}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          {canMutate && (
            <Button
              variant="outline"
              disabled={
                applying ||
                operationActive ||
                sandboxTransitioning ||
                network === "none" ||
                (selectedProxyId !== "" && !selectedProxy?.enabled)
              }
              onClick={() => void applyProxy()}
            >
              {applying || operationActive ? (
                <LoaderCircleIcon
                  className="animate-spin"
                  data-icon="inline-start"
                />
              ) : null}
              {sandbox.spec.status === "running"
                ? selectedProxyId === desiredProxyId
                  ? "重新应用"
                  : "立即应用"
                : "保存配置"}
            </Button>
          )}
        </div>
      </div>

      <div
        className={cn(
          "border-t bg-muted/20 px-3 py-2.5 text-xs leading-5 text-muted-foreground",
          operation?.status === "failed" &&
            "border-destructive/20 bg-destructive/5 text-destructive"
        )}
      >
        {network === "none"
          ? "当前沙箱完全隔离，不能配置网络代理。"
          : operation?.message
            ? operation.message
            : "运行中切换会影响新启动的 Agent 和终端进程；已有进程与连接不会自动迁移。需要完全生效时请重启相关 Agent 或沙箱。"}
        {selectedProxy?.scheme.startsWith("socks5") && (
          <span className="block">
            SOCKS5 通过 ALL_PROXY 同步；Node/npm
            等程序是否支持取决于自身代理实现。
          </span>
        )}
        {desiredProxy && !desiredProxy.enabled && (
          <span className="block">
            当前配置引用的代理已停用，只能切换到其他出口。
          </span>
        )}
      </div>
    </div>
  )
}

const activeAgentToolStatuses = new Set(["running", "installed", "verifying"])
const finishedAgentToolStatuses = new Set([
  "succeeded",
  "failed",
  "cached",
  "cancelled",
])
const agentToolLabels = new Map<string, string>(
  agentToolOptions.map((option) => [option.value, option.label])
)

function AgentToolProgressPanel({
  tools,
  provisioning,
  now,
}: {
  tools: string[]
  provisioning: ProvisioningProgress
  now: number
}) {
  const isProvisioning =
    provisioning.status === "running" || provisioning.status === "cancelling"
  const [open, setOpen] = useState(isProvisioning)

  const reported = new Map(
    provisioning.agentTools.map((progress) => [progress.tool, progress])
  )
  const items = tools.map((tool) => {
    const progress = reported.get(tool)
    let status = progress?.status ?? "pending"
    if (
      !progress &&
      provisioning.cacheStatus === "hit" &&
      provisioning.cacheReason === "exact-cache"
    ) {
      status = "cached"
    } else if (!progress && provisioning.status === "succeeded") {
      status = provisioning.cacheStatus === "hit" ? "cached" : "succeeded"
    }
    if (
      provisioning.status === "cancelled" &&
      !finishedAgentToolStatuses.has(status)
    ) {
      status = "cancelled"
    }
    return { tool, progress, status }
  })
  const settled = items.filter((item) =>
    finishedAgentToolStatuses.has(item.status)
  ).length
  const installed = items.filter(
    (item) =>
      item.status !== "pending" &&
      item.status !== "running" &&
      item.status !== "cancelled"
  ).length
  const failed = items.filter((item) => item.status === "failed").length
  const progressValue = tools.length > 0 ? (installed / tools.length) * 100 : 0

  return (
    <Collapsible
      className="overflow-hidden rounded-xl border bg-muted/10"
      open={open}
      onOpenChange={setOpen}
    >
      <div className="flex flex-col gap-2.5 p-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <p className="text-sm font-medium">Agent 安装进度</p>
              <Badge variant={failed > 0 ? "destructive" : "outline"}>
                {provisioning.status === "cancelled"
                  ? "已取消"
                  : provisioning.status === "cancelling"
                    ? "正在取消"
                    : failed > 0
                      ? `${failed} 个失败`
                      : provisioning.status === "running"
                        ? `${installed}/${tools.length} 已安装`
                        : `${settled}/${tools.length}`}
              </Badge>
            </div>
            <p className="mt-1 truncate text-xs text-muted-foreground">
              {provisioning.message || "等待 Worker 上报安装状态"}
            </p>
          </div>
          <CollapsibleTrigger asChild>
            <Button variant="ghost" size="sm">
              {open ? "收起" : "查看"}
              <ChevronDownIcon
                data-icon="inline-end"
                className={cn(
                  "transition-transform duration-200",
                  open && "rotate-180"
                )}
              />
            </Button>
          </CollapsibleTrigger>
        </div>
        <Progress
          aria-label={`Agent 安装进度 ${installed}/${tools.length}`}
          value={progressValue}
        />
      </div>

      <CollapsibleContent>
        <ul className="border-t">
          {items.map(({ tool, progress, status }, index) => (
            <li
              className={cn(
                "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-3 py-2.5",
                index > 0 && "border-t",
                activeAgentToolStatuses.has(status) && "bg-muted/40"
              )}
              key={tool}
            >
              <div className="flex min-w-0 items-center gap-2.5">
                <AgentToolProgressIcon status={status} />
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">
                    {agentToolLabels.get(tool) ?? tool}
                  </p>
                  <p
                    className="truncate text-xs text-muted-foreground"
                    title={progress?.message}
                  >
                    {agentToolProgressMessage(progress?.message, status)}
                  </p>
                </div>
              </div>
              <div className="flex items-center justify-end gap-2">
                <Badge variant={agentToolStatusBadge(status)}>
                  {provisioningAgentToolStatusLabel(status)}
                </Badge>
                <span className="min-w-14 text-right font-mono text-xs text-muted-foreground tabular-nums">
                  {agentToolDuration(progress, now)}
                </span>
              </div>
            </li>
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  )
}

function AgentToolProgressIcon({ status }: { status: string }) {
  if (activeAgentToolStatuses.has(status)) {
    return (
      <LoaderCircleIcon className="size-4 shrink-0 animate-spin text-muted-foreground" />
    )
  }
  if (status === "failed") {
    return <TriangleAlertIcon className="size-4 shrink-0 text-destructive" />
  }
  if (status === "succeeded" || status === "cached") {
    return <CheckCircle2Icon className="size-4 shrink-0 text-primary" />
  }
  return <CircleIcon className="size-4 shrink-0 text-muted-foreground" />
}

function agentToolStatusBadge(status: string) {
  if (status === "failed") return "destructive" as const
  if (activeAgentToolStatuses.has(status)) return "secondary" as const
  return "outline" as const
}

function agentToolProgressMessage(message: string | undefined, status: string) {
  if (status === "cancelled") return "本次创建已取消"
  if (message) return message
  if (status === "cached") return "由缓存镜像提供"
  if (status === "succeeded") return "旧任务未记录单项耗时"
  return "等待开始"
}

function agentToolDuration(
  progress: ProvisioningProgress["agentTools"][number] | undefined,
  now: number
) {
  if (!progress?.startedAt) return "—"
  const startedAt = Date.parse(progress.startedAt)
  const finishedAt = progress.finishedAt ? Date.parse(progress.finishedAt) : now
  if (!Number.isFinite(startedAt) || !Number.isFinite(finishedAt)) return "—"
  return provisioningDuration(
    Math.max(progress.durationMs, finishedAt - startedAt)
  )
}

function provisioningElapsedDuration(
  progress: ProvisioningProgress,
  now: number
) {
  const startedAt = progress.startedAt ? Date.parse(progress.startedAt) : NaN
  const liveDuration =
    ["running", "cancelling"].includes(progress.status) &&
    Number.isFinite(startedAt)
      ? now - startedAt
      : 0
  return provisioningDuration(Math.max(0, progress.durationMs, liveDuration))
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
    <div className="min-w-0">
      <p className="text-xs font-medium">{title}</p>
      <div className="mt-1.5 flex flex-wrap gap-1.5">
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
  resource: ResourceOfKind<"runtime" | "sandbox">
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
  environment: ResourceOfKind<"runtime">,
  resources: Resource[],
  servers: ManagedServer[]
) {
  const driver = runtimeDriverLabel(specString(environment.spec, "driver"))
  const server = servers.find((item) => item.id === environment.spec.serverId)
  return `${driver} · ${environmentImage(environment, resources)} · ${server?.name ?? "未绑定服务器"}`
}

function environmentImage(
  environment: ResourceOfKind<"runtime" | "sandbox">,
  resources: Resource[]
) {
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

function isRunningSandbox(resource: ResourceOfKind<"sandbox">) {
  return resource.spec.status === "running"
}

function sandboxStatus(resource: ResourceOfKind<"sandbox">) {
  switch (resource.spec.status) {
    case "running":
      return "运行中"
    case "stopped":
      return "已停止"
    case "error":
      return "运行异常"
    case "starting":
      return "启动中"
    case "cancelling":
      return "正在取消"
    case "cancelled":
      return "已取消"
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
