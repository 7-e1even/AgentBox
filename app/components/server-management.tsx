"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  ArrowLeftIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  CircleDotIcon,
  CopyIcon,
  LayoutTemplateIcon,
  PlusIcon,
  ServerIcon,
  Trash2Icon,
} from "lucide-react"
import { appToast as toast } from "@/lib/app-toast"

import {
  CollectionContent,
  CollectionList,
  CollectionListItem,
  CollectionTablePrimaryContent,
} from "@/components/collection-list"
import { CollectionHeader } from "@/components/control-plane-view"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import { Spinner } from "@/components/ui/spinner"
import type { Resource } from "@/lib/platform-schema"
import {
  serverPairingResponseSchema,
  serversResponseSchema,
  type ManagedServer,
  type ServerPairing,
} from "@/lib/server-schema"
import { normalizeHttpOrigin } from "@/lib/worker-address"

type AddressStatus = "idle" | "detecting" | "verified" | "manual" | "failed"

export function ServerManagement({
  servers,
  runtimes,
  onServersChange,
  onCreateRuntime,
  onEditRuntime,
}: {
  servers: ManagedServer[]
  runtimes: Resource[]
  onServersChange: (servers: ManagedServer[]) => void
  onCreateRuntime: (serverId: string) => void
  onEditRuntime: (runtime: Resource) => void
}) {
  const [selectedServerId, setSelectedServerId] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const [pairing, setPairing] = useState<ServerPairing | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState("")
  const [serverUrl, setServerUrl] = useState("")
  const [addressStatus, setAddressStatus] =
    useState<AddressStatus>("idle")

  const detectServerUrl = useCallback(async () => {
    setAddressStatus("detecting")
    try {
      const body = await requestJson<{ url?: unknown }>("/api/worker-address")
      if (typeof body.url !== "string") throw new Error("检测结果无效")
      setServerUrl(body.url)
      setAddressStatus("verified")
    } catch {
      setAddressStatus("failed")
    }
  }, [])

  const refreshServers = useCallback(async () => {
    const body = serversResponseSchema.parse(
      await requestJson<unknown>("/api/servers")
    )
    onServersChange(body.servers)
  }, [onServersChange])

  useEffect(() => {
    const timer = window.setInterval(() => void refreshServers(), 15_000)
    return () => window.clearInterval(timer)
  }, [refreshServers])

  useEffect(() => {
    if (!open || !pairing || pairing.serverId) return
    let stopped = false
    let completed = false
    const check = async () => {
      try {
        const body = serverPairingResponseSchema.parse(
          await requestJson<unknown>(`/api/server-pairings/${pairing.id}`)
        )
        if (stopped) return
        setPairing((current) =>
          current ? { ...body.pairing, token: current.token } : current
        )
        if (body.pairing.serverId && !completed) {
          completed = true
          await refreshServers()
          toast.success("服务器已接入")
        }
      } catch (cause) {
        if (!stopped) setError(errorMessage(cause))
      }
    }
    const timer = window.setInterval(() => void check(), 2_000)
    return () => {
      stopped = true
      window.clearInterval(timer)
    }
  }, [open, pairing, refreshServers])

  async function addServer() {
    void detectServerUrl()
    setOpen(true)
    setCreating(true)
    setPairing(null)
    setError("")
    try {
      const body = serverPairingResponseSchema.parse(
        await requestJson<unknown>("/api/server-pairings", { method: "POST" })
      )
      setPairing(body.pairing)
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setCreating(false)
    }
  }

  const normalizedServerUrl = normalizeHttpOrigin(serverUrl)
  const installCommand = normalizedServerUrl
    ? `curl -fsSL ${normalizedServerUrl}/api/worker/install.sh | sudo sh -s -- ${normalizedServerUrl}`
    : ""
  const setupCommand = pairing?.token && normalizedServerUrl
    ? `sudo agentbox-worker setup --server ${normalizedServerUrl} --token ${pairing.token}`
    : ""
  const invalidServerUrl = serverUrl.length > 0 && !normalizedServerUrl

  const selectedServer =
    servers.find((server) => server.id === selectedServerId) ?? null
  const columns = useMemo(
    () => serverColumns((server) => setSelectedServerId(server.id)),
    []
  )

  if (selectedServer) {
    return (
      <ServerDetail
        server={selectedServer}
        runtimes={runtimes.filter(
          (runtime) =>
            specString(runtime.spec, "serverId") === selectedServer.id
        )}
        onBack={() => setSelectedServerId(null)}
        onCreateRuntime={() => onCreateRuntime(selectedServer.id)}
        onEditRuntime={onEditRuntime}
        onDeleted={() => {
          onServersChange(
            servers.filter((server) => server.id !== selectedServer.id)
          )
          setSelectedServerId(null)
        }}
      />
    )
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="服务器"
        count={servers.length}
        action={
          <Button size="sm" onClick={addServer}>
            <PlusIcon data-icon="inline-start" />
            添加服务器
          </Button>
        }
      />

      <CollectionContent>
        {servers.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ServerIcon />
              </EmptyMedia>
              <EmptyTitle>还没有服务器</EmptyTitle>
              <EmptyDescription>
                先接入一台 Linux 物理机，创建沙箱时再选择它作为目标服务器。
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button onClick={addServer}>添加第一台服务器</Button>
            </EmptyContent>
          </Empty>
        ) : (
          <DataTable
            data={servers}
            columns={columns}
            getRowId={(server) => server.id}
            initialPageSize={8}
            searchPlaceholder="搜索服务器…"
            searchValue={(server) =>
              `${server.name} ${server.hostname} ${server.os} ${server.arch}`
            }
            filters={[
              {
                columnId: "status",
                title: "状态",
                options: [
                  { label: "在线", value: "online" },
                  { label: "离线", value: "offline" },
                ],
              },
            ]}
          />
        )}
      </CollectionContent>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>添加服务器</DialogTitle>
            <DialogDescription>
              在 Linux 物理机上依次运行两条命令，Worker
              上线后会自动出现在列表中。
            </DialogDescription>
          </DialogHeader>

          <Field data-invalid={invalidServerUrl || undefined}>
            <FieldLabel htmlFor="worker-api-url">平台入口地址</FieldLabel>
            <Input
              id="worker-api-url"
              value={serverUrl}
              aria-invalid={invalidServerUrl || undefined}
              onChange={(event) => {
                setServerUrl(event.target.value)
                setAddressStatus("manual")
              }}
              onBlur={() => {
                const normalized = normalizeHttpOrigin(serverUrl)
                if (normalized) setServerUrl(normalized)
              }}
              placeholder="http://192.168.1.10:3000"
            />
            <AddressDescription status={addressStatus} />
            {invalidServerUrl ? (
              <FieldError>请输入以 http:// 或 https:// 开头的地址。</FieldError>
            ) : null}
          </Field>

          {creating ? (
            <div className="flex items-center justify-center gap-2 py-8 text-muted-foreground">
              <Spinner /> 正在生成配对命令…
            </div>
          ) : error ? (
            <Alert variant="destructive">
              <AlertTitle>连接准备失败</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : pairing?.serverId ? (
            <Alert className="border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-200">
              <CheckCircle2Icon />
              <AlertTitle>服务器已上线</AlertTitle>
              <AlertDescription>已登记物理机并启动心跳。</AlertDescription>
            </Alert>
          ) : pairing && normalizedServerUrl ? (
            <div className="grid gap-4">
              <CommandStep
                number={1}
                title="安装 AgentBox Worker"
                command={installCommand}
              />
              <CommandStep
                number={2}
                title="连接到当前平台"
                command={setupCommand}
              />
              <Alert>
                <CircleDotIcon className="text-emerald-500" />
                <AlertTitle>等待服务器上线</AlertTitle>
                <AlertDescription>
                  配对命令将在 {formatTime(pairing.expiresAt)} 过期。
                </AlertDescription>
              </Alert>
            </div>
          ) : pairing ? (
            <Alert variant="destructive">
              <AlertTitle>需要平台入口地址</AlertTitle>
              <AlertDescription>
                自动检测失败，请填写物理服务器能够访问的 AgentBox 前端入口。
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {pairing?.serverId ? "完成" : "取消"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}

function AddressDescription({ status }: { status: AddressStatus }) {
  if (status === "detecting") {
    return (
      <FieldDescription className="flex items-center gap-1.5">
        <Spinner /> 正在检测 AgentBox 平台入口…
      </FieldDescription>
    )
  }
  if (status === "verified") {
    return (
      <FieldDescription className="flex items-center gap-1.5">
        <CheckCircle2Icon className="size-3.5" />
        已验证前端网关及后端代理。物理机只需访问此地址。
      </FieldDescription>
    )
  }
  if (status === "failed") {
    return (
      <FieldDescription>
        自动检测失败，请手动填写平台入口；物理服务器不能使用 localhost。
      </FieldDescription>
    )
  }
  if (status === "manual") {
    return (
      <FieldDescription>
        已手动修改。这里填写网页访问地址，API 与终端会话会由它统一代理。
      </FieldDescription>
    )
  }
  return (
    <FieldDescription>
      将自动检测网页入口；Go API 的 8091 端口无需对外开放。
    </FieldDescription>
  )
}

function serverColumns(
  onOpen: (server: ManagedServer) => void
): ColumnDef<ManagedServer>[] {
  return [
    {
      id: "name",
      accessorFn: (server) => server.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="服务器" />
      ),
      cell: ({ row }) => (
        <CollectionTablePrimaryContent
          icon={ServerIcon}
          title={row.original.name}
          description={row.original.hostname}
          onClick={() => onOpen(row.original)}
        />
      ),
      meta: { label: "服务器" },
      enableHiding: false,
    },
    {
      id: "status",
      accessorFn: (server) => server.status,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="状态" />
      ),
      cell: ({ row }) => (
        <Badge
          variant={row.original.status === "online" ? "default" : "secondary"}
        >
          {row.original.status === "online" ? "在线" : "离线"}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "状态" },
    },
    {
      id: "system",
      accessorFn: (server) => `${server.os} / ${server.arch}`,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="系统" />
      ),
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{String(getValue())}</span>
      ),
      meta: { label: "系统", className: "hidden md:table-cell" },
    },
    {
      id: "capabilities",
      accessorFn: (server) => server.capabilities.join(" "),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="能力" />
      ),
      cell: ({ row }) => (
        <div className="flex flex-wrap gap-1">
          {row.original.capabilities.map((capability) => (
            <Badge key={capability} variant="outline">
              {capability}
            </Badge>
          ))}
        </div>
      ),
      meta: { label: "能力", className: "hidden lg:table-cell" },
    },
    {
      id: "lastSeenAt",
      accessorFn: (server) => server.lastSeenAt,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="最近心跳" />
      ),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {formatTime(row.original.lastSeenAt)}
        </span>
      ),
      meta: { label: "最近心跳", className: "hidden xl:table-cell" },
    },
    {
      id: "actions",
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`查看 ${row.original.name}`}
          onClick={() => onOpen(row.original)}
        >
          <ChevronRightIcon />
        </Button>
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-10" },
    },
  ]
}

function ServerDetail({
  server,
  runtimes,
  onBack,
  onCreateRuntime,
  onEditRuntime,
  onDeleted,
}: {
  server: ManagedServer
  runtimes: Resource[]
  onBack: () => void
  onCreateRuntime: () => void
  onEditRuntime: (runtime: Resource) => void
  onDeleted: () => void
}) {
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)

  async function deleteServer() {
    setDeleting(true)
    try {
      await requestJson<void>(`/api/servers/${server.id}`, {
        method: "DELETE",
      })
      onDeleted()
      toast.success("服务器已移除")
    } catch (cause) {
      toast.error("无法移除服务器", { description: errorMessage(cause) })
    } finally {
      setDeleting(false)
    }
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="shrink-0 border-b px-4 pt-3 pb-5 sm:px-6">
        <Button variant="ghost" size="sm" className="-ml-2" onClick={onBack}>
          <ArrowLeftIcon data-icon="inline-start" />
          服务器
        </Button>
        <div className="mt-4 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-muted">
              <ServerIcon className="size-5" />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="truncate text-xl font-semibold">
                  {server.name}
                </h1>
                <Badge
                  variant={server.status === "online" ? "default" : "secondary"}
                >
                  {server.status === "online" ? "在线" : "离线"}
                </Badge>
              </div>
              <p className="mt-1 text-sm text-muted-foreground">
                {server.hostname} · {server.os} / {server.arch} · 最近心跳{" "}
                {formatTime(server.lastSeenAt)}
              </p>
            </div>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2Icon data-icon="inline-start" />
            移除服务器
          </Button>
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-auto p-4 sm:p-6">
        <div className="mx-auto grid max-w-5xl gap-6">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <ServerFact label="主机名" value={server.hostname} />
            <ServerFact label="系统" value={`${server.os} / ${server.arch}`} />
            <ServerFact label="默认环境" value={`${runtimes.length} 个`} />
            <ServerFact
              label="最近心跳"
              value={formatTime(server.lastSeenAt)}
            />
          </div>

          <section className="grid gap-3">
            <div>
              <h2 className="font-medium">可用能力</h2>
              <p className="text-sm text-muted-foreground">
                Worker 上报的执行与虚拟化能力。
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              {server.capabilities.map((capability) => (
                <Badge key={capability} variant="outline">
                  {capability}
                </Badge>
              ))}
            </div>
          </section>

          <section className="grid gap-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h2 className="font-medium">沙箱模板</h2>
                <p className="text-sm text-muted-foreground">
                  使用这台服务器运行的可复用沙箱基座。
                </p>
              </div>
              <Button size="sm" onClick={onCreateRuntime}>
                <PlusIcon data-icon="inline-start" />
                新建沙箱模板
              </Button>
            </div>

            {runtimes.length === 0 ? (
              <Empty className="min-h-56 border">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <LayoutTemplateIcon />
                  </EmptyMedia>
                  <EmptyTitle>还没有沙箱模板</EmptyTitle>
                  <EmptyDescription>
                    新建时会把当前服务器设为默认目标。
                  </EmptyDescription>
                </EmptyHeader>
                <EmptyContent>
                  <Button variant="outline" size="sm" onClick={onCreateRuntime}>
                    创建第一个沙箱模板
                  </Button>
                </EmptyContent>
              </Empty>
            ) : (
              <CollectionList>
                {runtimes.map((runtime) => (
                  <CollectionListItem key={runtime.id} className="p-0">
                    <button
                      type="button"
                      className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset"
                      onClick={() => onEditRuntime(runtime)}
                    >
                      <ItemMedia
                        variant="icon"
                        className="flex size-9 items-center justify-center rounded-lg bg-muted"
                      >
                        <LayoutTemplateIcon />
                      </ItemMedia>
                      <ItemContent className="min-w-0">
                        <ItemTitle>
                          {runtime.name}
                          <Badge
                            variant={runtime.enabled ? "default" : "secondary"}
                          >
                            {runtime.enabled ? "启用" : "停用"}
                          </Badge>
                        </ItemTitle>
                        <ItemDescription>
                          {runtimeDriverLabel(
                            specString(runtime.spec, "driver")
                          )}
                        </ItemDescription>
                      </ItemContent>
                      <ItemActions>
                        <ChevronRightIcon className="size-4 text-muted-foreground" />
                      </ItemActions>
                    </button>
                  </CollectionListItem>
                ))}
              </CollectionList>
            )}
          </section>
        </div>
      </div>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>移除 {server.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              {runtimes.length > 0
                ? `这台服务器仍被 ${runtimes.length} 个沙箱模板使用，请先修改模板。`
                : "移除后 Worker 将不能再向平台上报；服务器本身和其中的数据不会被删除。"}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={runtimes.length > 0 || deleting}
              onClick={() => void deleteServer()}
            >
              {deleting ? "正在移除…" : "确认移除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function ServerFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border p-4">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="mt-1 truncate font-medium" title={value}>
        {value}
      </p>
    </div>
  )
}

function CommandStep({
  number,
  title,
  command,
}: {
  number: number
  title: string
  command: string
}) {
  return (
    <div className="grid gap-2">
      <div className="font-medium">
        {number}. {title}
      </div>
      <div className="flex items-start gap-2 rounded-lg bg-muted p-3 font-mono text-xs">
        <code className="min-w-0 flex-1 break-all">{command}</code>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={`复制${title}命令`}
          onClick={() => {
            void navigator.clipboard.writeText(command)
            toast.success("命令已复制")
          }}
        >
          <CopyIcon />
        </Button>
      </div>
    </div>
  )
}

async function requestJson<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: { "Content-Type": "application/json", ...options?.headers },
  })
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error || "请求失败")
  }
  return response.status === 204 ? (undefined as T) : response.json()
}

function specString(spec: Record<string, unknown>, key: string) {
  const value = spec[key]
  return typeof value === "string" ? value : ""
}

function runtimeDriverLabel(driver: string) {
  if (driver === "boxlite") return "BoxLite"
  if (driver === "microsandbox") return "Microsandbox"
  if (driver === "vm") return "VM（旧）"
  return driver ? "Docker" : "未选择驱动"
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "请稍后重试"
}

function formatTime(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value))
}
