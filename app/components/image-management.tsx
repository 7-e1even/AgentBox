"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  BoxIcon,
  ContainerIcon,
  HardDriveIcon,
  PlusIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
import {
  serversResponseSchema,
  type ManagedServer,
  type ServerImage,
} from "@/lib/server-schema"

type RuntimeDriver = "docker" | "boxlite" | "microsandbox"
type ImageRow = { image: ServerImage; driver: RuntimeDriver }

export function ImageManagement({
  servers,
  onServersChange,
  onCreateRuntime,
}: {
  servers: ManagedServer[]
  onServersChange: (servers: ManagedServer[]) => void
  onCreateRuntime: (
    serverId: string,
    imageReference: string,
    driver: RuntimeDriver
  ) => void
}) {
  const [requestedServerId, setRequestedServerId] = useState(
    () =>
      servers.find((server) => server.status === "online")?.id ??
      servers[0]?.id ??
      ""
  )
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
  const preferredDriver = server
    ? preferredRuntimeDriver(server.capabilities)
    : undefined
  const imageCount = server?.inventory.dockerImages.length ?? 0
  const images =
    server && preferredDriver
      ? server.inventory.dockerImages.map((image) => ({
          image,
          driver: preferredDriver,
        }))
      : []
  const columns = useMemo(
    () =>
      server
        ? imageColumns(server, onCreateRuntime)
        : ([] as ColumnDef<ImageRow>[]),
    [onCreateRuntime, server]
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
        title="服务器镜像"
        count={imageCount}
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
              <p className="text-sm text-muted-foreground">
                选择服务器查看 Worker 实际盘点到的镜像。
              </p>
              <Select
                value={selectedServerId}
                onValueChange={(value) => {
                  setRequestedServerId(value)
                }}
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

            {server && server.inventory.vmImages.length > 0 && (
              <Alert>
                <TriangleAlertIcon />
                <AlertTitle>原生 VM 磁盘仅供盘点</AlertTitle>
                <AlertDescription>
                  BoxLite 与 Microsandbox 通过 SDK 使用 OCI 镜像；qcow2/raw
                  磁盘暂不进入可操作列表。
                </AlertDescription>
              </Alert>
            )}

            {server && images.length > 0 ? (
              <DataTable
                data={images}
                columns={columns}
                getRowId={({ image, driver }) =>
                  `${driver}-${image.id}-${image.reference}`
                }
                initialPageSize={8}
                searchPlaceholder="搜索镜像…"
                searchValue={({ image }) =>
                  `${image.reference} ${image.id} ${image.architecture} ${image.format}`
                }
                filters={[
                  {
                    columnId: "driver",
                    title: "类型",
                    options: [
                      { label: "Docker", value: "docker" },
                      { label: "BoxLite", value: "boxlite" },
                      { label: "Microsandbox", value: "microsandbox" },
                    ],
                  },
                ]}
              />
            ) : server ? (
              <Empty className="min-h-64 border">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <HardDriveIcon />
                  </EmptyMedia>
                  <EmptyTitle>没有镜像</EmptyTitle>
                  <EmptyDescription>
                    当前服务器没有符合此类型的镜像。
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

function imageColumns(
  server: ManagedServer,
  onCreateRuntime: (
    serverId: string,
    imageReference: string,
    driver: RuntimeDriver
  ) => void
): ColumnDef<ImageRow>[] {
  return [
    {
      id: "reference",
      accessorFn: ({ image }) => image.reference,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="镜像" />
      ),
      cell: ({ row }) => (
        <CollectionTablePrimaryContent
          icon={row.original.driver === "docker" ? ContainerIcon : BoxIcon}
          title={row.original.image.reference}
          description={
            row.original.image.id
              ? shortID(row.original.image.id)
              : "未提供镜像标识"
          }
        />
      ),
      meta: { label: "镜像" },
      enableHiding: false,
    },
    {
      id: "driver",
      accessorFn: ({ driver }) => driver,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="类型" />
      ),
      cell: ({ row }) => (
        <Badge variant="outline">
          {runtimeDriverLabel(row.original.driver)}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "类型" },
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
    {
      id: "actions",
      cell: ({ row }) => {
        const { image, driver } = row.original
        return (
          <Button
            size="sm"
            variant="outline"
            onClick={() => onCreateRuntime(server.id, image.reference, driver)}
          >
            <PlusIcon data-icon="inline-start" />
            创建环境
          </Button>
        )
      },
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-28" },
    },
  ]
}

function preferredRuntimeDriver(capabilities: string[]) {
  if (capabilities.includes("boxlite")) return "boxlite" as const
  if (capabilities.includes("docker")) return "docker" as const
  if (capabilities.includes("microsandbox")) return "microsandbox" as const
  return undefined
}

function runtimeDriverLabel(driver: RuntimeDriver) {
  if (driver === "boxlite") return "BoxLite"
  if (driver === "microsandbox") return "Microsandbox"
  return "Docker"
}

function shortID(value: string) {
  const normalized = value.replace(/^sha256:/, "")
  return normalized.length > 12 ? normalized.slice(0, 12) : normalized
}
