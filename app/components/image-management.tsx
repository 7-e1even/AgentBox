"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  BoxIcon,
  ChevronDownIcon,
  ContainerIcon,
  CpuIcon,
  HardDriveIcon,
  PlusIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
  type LucideIcon,
} from "lucide-react"
import { appToast as toast } from "@/lib/app-toast"

import {
  CollectionContent,
  CollectionTablePrimaryContent,
  CollectionToolbar,
} from "@/components/collection-list"
import { CollectionHeader } from "@/components/control-plane-view"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  serversResponseSchema,
  type ManagedServer,
  type ServerImage,
} from "@/lib/server-schema"
import { runtimeInventoryImages } from "@/lib/runtime-images"

type ImageRow = { image: ServerImage }
type ImageScope = "container" | "vm"
type CreatableDriver = "docker" | "boxlite" | "microsandbox"

type ImageScopeDefinition = {
  label: string
  icon: LucideIcon
  description: string
  emptyTitle: string
  emptyDescription: string
}

const imageScopes: Record<ImageScope, ImageScopeDefinition> = {
  container: {
    label: "容器镜像",
    icon: ContainerIcon,
    description:
      "统一展示 Worker 本地 Docker、公共 OCI 与运行时缓存中的镜像；创建环境时再选择 Docker、BoxLite 或 Microsandbox。",
    emptyTitle: "没有容器镜像",
    emptyDescription: "当前服务器没有 Worker 可盘点到的容器镜像。",
  },
  vm: {
    label: "VM 磁盘",
    icon: HardDriveIcon,
    description: "Worker 本地的 qcow2、raw 与 img 文件，目前仅供盘点。",
    emptyTitle: "没有 VM 磁盘",
    emptyDescription:
      "当前镜像目录中没有 Worker 可盘点到的 qcow2、raw 或 img 文件。",
  },
}

const runtimeDefinitions: Record<
  CreatableDriver,
  Pick<ImageScopeDefinition, "label" | "icon"> & { capability: string }
> = {
  docker: { label: "Docker", icon: ContainerIcon, capability: "docker" },
  boxlite: { label: "BoxLite", icon: BoxIcon, capability: "boxlite" },
  microsandbox: {
    label: "Microsandbox",
    icon: CpuIcon,
    capability: "microsandbox",
  },
}

export function ImageManagement({
  servers,
  canMutate,
  onServersChange,
  onCreateRuntime,
}: {
  servers: ManagedServer[]
  canMutate: boolean
  onServersChange: (servers: ManagedServer[]) => void
  onCreateRuntime: (
    serverId: string,
    imageReference: string,
    driver: CreatableDriver
  ) => void
}) {
  const [requestedServerId, setRequestedServerId] = useState(
    () =>
      servers.find((server) => server.status === "online")?.id ??
      servers[0]?.id ??
      ""
  )
  const [scope, setScope] = useState<ImageScope>("container")
  const [refreshing, setRefreshing] = useState(false)

  const refresh = useCallback(async () => {
    const response = await fetch("/api/servers")
    if (!response.ok) throw new Error("无法刷新服务器镜像")
    const body = serversResponseSchema.parse(await response.json())
    onServersChange(body.servers)
  }, [onServersChange])

  useEffect(() => {
    const timer = window.setInterval(
      () => void refresh().catch(() => undefined),
      15_000
    )
    return () => window.clearInterval(timer)
  }, [refresh])

  const selectedServerId = servers.some(
    (server) => server.id === requestedServerId
  )
    ? requestedServerId
    : (servers.find((server) => server.status === "online")?.id ??
      servers[0]?.id ??
      "")
  const server = servers.find((item) => item.id === selectedServerId)
  const definition = imageScopes[scope]
  const images = inventoryImages(server, scope).map((image) => ({ image }))
  const columns = useMemo(
    () =>
      server
        ? imageColumns(server, scope, canMutate, onCreateRuntime)
        : ([] as ColumnDef<ImageRow>[]),
    [canMutate, onCreateRuntime, scope, server]
  )

  async function refreshNow() {
    setRefreshing(true)
    try {
      await refresh()
      toast.success("镜像清单已刷新")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "刷新失败")
    } finally {
      setRefreshing(false)
    }
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="镜像"
        count={images.length}
        action={
          <Button
            variant="outline"
            size="sm"
            disabled={refreshing}
            onClick={refreshNow}
          >
            <RefreshCwIcon className={refreshing ? "animate-spin" : ""} />
            刷新
          </Button>
        }
      />

      <CollectionContent>
        {servers.length === 0 ? (
          <Empty className="min-h-80 border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <HardDriveIcon />
              </EmptyMedia>
              <EmptyTitle>还没有服务器</EmptyTitle>
              <EmptyDescription>
                先在“服务器”中接入物理机，Worker 才能盘点实际镜像。
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            <CollectionToolbar>
              <div className="max-w-full overflow-x-auto pb-1">
                <ToggleGroup
                  type="single"
                  variant="outline"
                  size="sm"
                  spacing={0}
                  value={scope}
                  onValueChange={(value) => {
                    if (value) setScope(value as ImageScope)
                  }}
                >
                  {(Object.keys(imageScopes) as ImageScope[]).map((value) => {
                    const item = imageScopes[value]
                    const Icon = item.icon
                    return (
                      <ToggleGroupItem key={value} value={value}>
                        <Icon data-icon="inline-start" />
                        {item.label}
                        <span className="text-muted-foreground tabular-nums">
                          {inventoryImages(server, value).length}
                        </span>
                      </ToggleGroupItem>
                    )
                  })}
                </ToggleGroup>
              </div>
              <Select
                value={selectedServerId}
                onValueChange={(value) => setRequestedServerId(value)}
              >
                <SelectTrigger className="w-full sm:w-72">
                  <SelectValue placeholder="选择服务器" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectLabel>物理服务器</SelectLabel>
                    {servers.map((item) => (
                      <SelectItem key={item.id} value={item.id}>
                        {item.name} ·{" "}
                        {item.status === "online" ? "在线" : "离线"}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </CollectionToolbar>

            <p className="text-sm text-muted-foreground">
              {definition.description}
            </p>

            {scope === "vm" ? (
              <Alert>
                <TriangleAlertIcon />
                <AlertTitle>VM 磁盘暂不支持直接创建环境</AlertTitle>
                <AlertDescription>
                  这一栏只反映 Worker 的原生磁盘文件，不会混入三个 OCI
                  运行时的缓存。
                </AlertDescription>
              </Alert>
            ) : server && !hasCreatableRuntime(server) ? (
              <Alert>
                <TriangleAlertIcon />
                <AlertTitle>容器运行时未就绪</AlertTitle>
                <AlertDescription>
                  当前 Worker 尚未通过 Docker、BoxLite 或 Microsandbox
                  的运行时自检；镜像清单仍会保留展示。
                </AlertDescription>
              </Alert>
            ) : null}

            {server && images.length > 0 ? (
              <DataTable
                data={images}
                columns={columns}
                getRowId={({ image }) =>
                  `${image.id}-${image.reference}-${image.created}-${image.path}`
                }
                initialPageSize={8}
                searchPlaceholder={`搜索 ${definition.label} 镜像…`}
                searchValue={({ image }) =>
                  `${image.reference} ${image.id} ${image.architecture} ${image.format}`
                }
              />
            ) : server ? (
              <Empty className="min-h-64 border">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <definition.icon />
                  </EmptyMedia>
                  <EmptyTitle>{definition.emptyTitle}</EmptyTitle>
                  <EmptyDescription>
                    {definition.emptyDescription}
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : null}
          </>
        )}
      </CollectionContent>
    </section>
  )
}

function inventoryImages(
  server: ManagedServer | undefined,
  scope: ImageScope
): ServerImage[] {
  if (!server) return []
  if (scope === "container") return runtimeInventoryImages(server, "docker")
  return server.inventory.vmImages
}

function hasCreatableRuntime(server: ManagedServer) {
  return (Object.keys(runtimeDefinitions) as CreatableDriver[]).some((driver) =>
    server.capabilities.includes(runtimeDefinitions[driver].capability)
  )
}

function imageColumns(
  server: ManagedServer,
  scope: ImageScope,
  canMutate: boolean,
  onCreateRuntime: (
    serverId: string,
    imageReference: string,
    driver: CreatableDriver
  ) => void
): ColumnDef<ImageRow>[] {
  const Icon = imageScopes[scope].icon
  const columns: ColumnDef<ImageRow>[] = [
    {
      id: "reference",
      accessorFn: ({ image }) => image.reference,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="镜像" />
      ),
      cell: ({ row }) => (
        <CollectionTablePrimaryContent
          icon={Icon}
          title={row.original.image.reference}
          description={imageDescription(row.original.image, scope)}
        />
      ),
      meta: { label: "镜像" },
      enableHiding: false,
    },
    {
      id: "architecture",
      accessorFn: ({ image }) => image.architecture || "—",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="架构" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{String(getValue())}</span>
      ),
      meta: { label: "架构", className: "hidden md:table-cell" },
    },
    {
      id: "size",
      accessorFn: ({ image }) => image.size || "—",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="大小" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{String(getValue())}</span>
      ),
      meta: { label: "大小", className: "hidden lg:table-cell" },
    },
    {
      id: "format",
      accessorFn: ({ image }) => image.format || "—",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="格式" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{String(getValue())}</span>
      ),
      meta: { label: "格式", className: "hidden xl:table-cell" },
    },
    {
      id: "source",
      accessorFn: ({ image }) => imageSourceLabel(image),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="来源" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{String(getValue())}</span>
      ),
      meta: { label: "来源", className: "hidden xl:table-cell" },
    },
    {
      id: "created",
      accessorFn: ({ image }) => image.created || "—",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="创建时间" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{String(getValue())}</span>
      ),
      meta: { label: "创建时间", className: "hidden xl:table-cell" },
    },
  ]

  if (scope !== "vm" && canMutate) {
    columns.push({
      id: "actions",
      cell: ({ row }) => (
        <RuntimeCreateMenu
          server={server}
          image={row.original.image}
          onCreateRuntime={onCreateRuntime}
        />
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-44" },
    })
  }

  return columns
}

function RuntimeCreateMenu({
  server,
  image,
  onCreateRuntime,
}: {
  server: ManagedServer
  image: ServerImage
  onCreateRuntime: (
    serverId: string,
    imageReference: string,
    driver: CreatableDriver
  ) => void
}) {
  const drivers: CreatableDriver[] = ["docker", "boxlite", "microsandbox"]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" variant="outline">
          <PlusIcon data-icon="inline-start" />
          创建环境
          <ChevronDownIcon data-icon="inline-end" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>使用同一镜像运行</DropdownMenuLabel>
        <DropdownMenuGroup>
          {drivers.map((driver) => {
            const definition = runtimeDefinitions[driver]
            const Icon = definition.icon
            const ready = server.capabilities.includes(definition.capability)
            return (
              <DropdownMenuItem
                key={driver}
                disabled={!ready}
                onSelect={() =>
                  onCreateRuntime(server.id, image.reference, driver)
                }
              >
                <Icon />
                {definition.label}
                {!ready && (
                  <DropdownMenuShortcut>未就绪</DropdownMenuShortcut>
                )}
              </DropdownMenuItem>
            )
          })}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function imageDescription(image: ServerImage, scope: ImageScope) {
  if (scope === "vm" && image.path) return image.path
  return image.id ? shortID(image.id) : "未提供镜像标识"
}

function imageSourceLabel(image: ServerImage) {
  if (image.source === "docker-local") return "Worker Docker"
  if (image.source === "worker-oci") return "Worker 公共 OCI"
  if (image.source === "registry-cache") return "Registry 缓存"
  if (image.source === "runtime-cache") return "运行时缓存"
  if (image.source === "disk-local") return "Worker 磁盘"
  return "未标记"
}

function shortID(value: string) {
  const normalized = value.replace(/^sha256:/, "")
  return normalized.length > 12 ? normalized.slice(0, 12) : normalized
}
