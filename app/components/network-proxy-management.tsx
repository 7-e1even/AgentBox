"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import {
  CircleCheckIcon,
  CircleXIcon,
  EyeIcon,
  EyeOffIcon,
  GaugeIcon,
  NetworkIcon,
  PlusIcon,
  RefreshCwIcon,
  SaveIcon,
  ShieldCheckIcon,
  Trash2Icon,
} from "lucide-react"

import { CollectionHeader } from "@/components/control-plane-view"
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
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  networkProxyInputSchema,
  type ManagedNetworkProxy,
  type NetworkProxyCheckResult,
  type NetworkProxyInput,
} from "@/lib/network-proxy-schema"
import type { ManagedServer } from "@/lib/server-schema"
import { cn } from "@/lib/utils"

type NetworkProxyManagementProps = {
  proxies: ManagedNetworkProxy[]
  servers: ManagedServer[]
  canMutate: boolean
  onSave: (
    input: NetworkProxyInput,
    editing: ManagedNetworkProxy | null
  ) => Promise<ManagedNetworkProxy>
  onCheck: (
    proxy: ManagedNetworkProxy,
    serverId: string,
    signal: AbortSignal
  ) => Promise<NetworkProxyCheckResult>
  onDelete: (proxy: ManagedNetworkProxy) => Promise<void>
}

type ProxyCheckState =
  | { status: "checking"; serverName: string }
  | { status: "complete"; result: NetworkProxyCheckResult }
  | { status: "error"; error: string; serverName: string }

export function NetworkProxyManagement({
  proxies,
  servers,
  canMutate,
  onSave,
  onCheck,
  onDelete,
}: NetworkProxyManagementProps) {
  const checkController = useRef<AbortController | null>(null)
  useEffect(() => {
    checkController.current = new AbortController()
    return () => checkController.current?.abort()
  }, [])
  const [selection, setSelection] = useState<string | null>(
    proxies[0]?.id ?? null
  )
  const [creating, setCreating] = useState(canMutate && proxies.length === 0)
  const [deleting, setDeleting] = useState<ManagedNetworkProxy | null>(null)
  const [deletingBusy, setDeletingBusy] = useState(false)
  const [checks, setChecks] = useState<Record<string, ProxyCheckState>>({})
  const [checkingAll, setCheckingAll] = useState(false)
  const onlineServers = useMemo(
    () => servers.filter((server) => server.status === "online"),
    [servers]
  )
  const [checkServerId, setCheckServerId] = useState(
    () => onlineServers[0]?.id ?? ""
  )
  const effectiveCheckServerId = onlineServers.some(
    (server) => server.id === checkServerId
  )
    ? checkServerId
    : (onlineServers[0]?.id ?? "")
  const checkServer = onlineServers.find(
    (server) => server.id === effectiveCheckServerId
  )
  const selected = selection
    ? (proxies.find((item) => item.id === selection) ?? null)
    : null
  const checkingAny = Object.values(checks).some(
    (state) => state.status === "checking"
  )

  async function confirmDelete() {
    if (!deleting) return
    setDeletingBusy(true)
    try {
      await onDelete(deleting)
      if (selection === deleting.id) {
        setSelection(
          proxies.find((item) => item.id !== deleting.id)?.id ?? null
        )
      }
      setChecks((current) => {
        const next = { ...current }
        delete next[deleting.id]
        return next
      })
      setDeleting(null)
    } finally {
      setDeletingBusy(false)
    }
  }

  async function runCheck(proxy: ManagedNetworkProxy) {
    if (!checkServer) {
      setChecks((current) => ({
        ...current,
        [proxy.id]: {
          status: "error",
          error: "没有在线 Worker 可执行检测",
          serverName: "",
        },
      }))
      return
    }
    setChecks((current) => ({
      ...current,
      [proxy.id]: { status: "checking", serverName: checkServer.name },
    }))
    try {
      const signal = checkController.current?.signal
      if (!signal || signal.aborted) return
      const result = await onCheck(proxy, checkServer.id, signal)
      signal.throwIfAborted()
      setChecks((current) => ({
        ...current,
        [proxy.id]: { status: "complete", result },
      }))
    } catch (cause) {
      if (checkController.current?.signal.aborted) return
      setChecks((current) => ({
        ...current,
        [proxy.id]: {
          status: "error",
          error: cause instanceof Error ? cause.message : "检测请求失败",
          serverName: checkServer.name,
        },
      }))
    }
  }

  async function runAllChecks() {
    setCheckingAll(true)
    try {
      for (const proxy of proxies) {
        if (checkController.current?.signal.aborted) break
        await runCheck(proxy)
      }
    } finally {
      setCheckingAll(false)
    }
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="网络代理"
        count={proxies.length}
        action={
          canMutate ? (
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                aria-label="检测全部代理"
                disabled={
                  checkingAny || proxies.length === 0 || !effectiveCheckServerId
                }
                onClick={() => void runAllChecks()}
              >
                {checkingAll ? (
                  <Spinner data-icon="inline-start" />
                ) : (
                  <RefreshCwIcon data-icon="inline-start" />
                )}
                <span className="hidden sm:inline">检测全部</span>
              </Button>
              <Button
                size="sm"
                aria-label="添加网络代理"
                onClick={() => setCreating(true)}
              >
                <PlusIcon data-icon="inline-start" />
                <span className="hidden sm:inline">添加代理</span>
              </Button>
            </div>
          ) : undefined
        }
      />

      <div className="grid min-h-0 w-full flex-1 md:grid-cols-[17rem_minmax(0,1fr)]">
        <aside className="flex min-h-0 flex-col border-b bg-muted/20 md:border-r md:border-b-0">
          <ScrollArea className="h-32 px-2 py-3 md:h-auto md:min-h-0 md:flex-1">
            <ItemGroup className="gap-1">
              {proxies.map((proxy) => (
                <Item key={proxy.id} asChild size="sm">
                  <button
                    type="button"
                    className={cn(
                      "cursor-pointer text-left hover:bg-muted",
                      !creating &&
                        selection === proxy.id &&
                        "border-border bg-background shadow-xs"
                    )}
                    onClick={() => {
                      setSelection(proxy.id)
                      setCreating(false)
                    }}
                  >
                    <ItemMedia variant="icon">
                      <NetworkIcon />
                    </ItemMedia>
                    <ItemContent className="min-w-0">
                      <ItemTitle>
                        {proxy.name}
                        <Badge
                          variant="outline"
                          className="px-1.5 py-0 text-[10px]"
                        >
                          {proxy.enabled ? "启用" : "停用"}
                        </Badge>
                        <ProxyCheckBadge state={checks[proxy.id]} />
                      </ItemTitle>
                      <ItemDescription>
                        {proxy.scheme}://{formatHost(proxy.host)}:{proxy.port}
                      </ItemDescription>
                    </ItemContent>
                  </button>
                </Item>
              ))}
              {proxies.length === 0 && (
                <p className="px-3 py-7 text-center text-sm text-muted-foreground">
                  还没有网络代理
                </p>
              )}
            </ItemGroup>
          </ScrollArea>
          {canMutate ? (
            <div className="shrink-0 border-t p-3">
              <Button
                variant="outline"
                className="w-full"
                onClick={() => setCreating(true)}
              >
                <PlusIcon data-icon="inline-start" />
                添加代理
              </Button>
            </div>
          ) : null}
        </aside>

        <div className="min-h-0 overflow-y-auto">
          {creating ? (
            <ProxyEditor
              key="new"
              proxy={null}
              onCancel={() => {
                setCreating(false)
                setSelection(proxies[0]?.id ?? null)
              }}
              onSave={async (input) => {
                const saved = await onSave(input, null)
                setChecks((current) => {
                  const next = { ...current }
                  delete next[saved.id]
                  return next
                })
                setSelection(saved.id)
                setCreating(false)
              }}
            />
          ) : selected ? (
            <ProxyEditor
              key={selected.id}
              proxy={selected}
              canMutate={canMutate}
              checkState={checks[selected.id]}
              onlineServers={onlineServers}
              checkServerId={effectiveCheckServerId}
              onCheckServerChange={setCheckServerId}
              onCheck={canMutate ? () => runCheck(selected) : undefined}
              onDelete={canMutate ? () => setDeleting(selected) : undefined}
              onSave={async (input) => {
                const saved = await onSave(input, selected)
                setChecks((current) => {
                  const next = { ...current }
                  delete next[saved.id]
                  return next
                })
                setSelection(saved.id)
              }}
            />
          ) : (
            <Empty className="min-h-full border-0">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <NetworkIcon />
                </EmptyMedia>
                <EmptyTitle>配置环境网络代理</EmptyTitle>
                <EmptyDescription>
                  {canMutate
                    ? "保存代理后，可在沙箱模板或单个沙箱中选择。"
                    : "当前角色只能查看已配置的网络代理。"}
                </EmptyDescription>
              </EmptyHeader>
              {canMutate ? (
                <EmptyContent>
                  <Button onClick={() => setCreating(true)}>
                    <PlusIcon data-icon="inline-start" />
                    添加代理
                  </Button>
                </EmptyContent>
              ) : null}
            </Empty>
          )}
        </div>
      </div>

      <AlertDialog
        open={Boolean(deleting)}
        onOpenChange={(open) => !open && !deletingBusy && setDeleting(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除 {deleting?.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              删除后无法恢复。已被模板或沙箱引用的代理不能删除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deletingBusy}>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deletingBusy}
              onClick={(event) => {
                event.preventDefault()
                void confirmDelete()
              }}
            >
              {deletingBusy && <Spinner data-icon="inline-start" />}
              删除代理
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function ProxyEditor({
  proxy,
  canMutate = true,
  onSave,
  checkState,
  onlineServers = [],
  checkServerId = "",
  onCheckServerChange,
  onCheck,
  onCancel,
  onDelete,
}: {
  proxy: ManagedNetworkProxy | null
  canMutate?: boolean
  onSave: (input: NetworkProxyInput) => Promise<void>
  checkState?: ProxyCheckState
  onlineServers?: ManagedServer[]
  checkServerId?: string
  onCheckServerChange?: (serverId: string) => void
  onCheck?: () => Promise<void>
  onCancel?: () => void
  onDelete?: () => void
}) {
  const [input, setInput] = useState<NetworkProxyInput>(() => ({
    id: proxy?.id ?? "",
    name: proxy?.name ?? "",
    scheme: proxy?.scheme ?? "http",
    host: proxy?.host ?? "",
    port: proxy?.port ?? 7890,
    username: proxy?.username ?? "",
    password: "",
    noProxy: proxy?.noProxy ?? [],
    enabled: proxy?.enabled ?? true,
  }))
  const [slugEdited, setSlugEdited] = useState(Boolean(proxy))
  const [noProxyText, setNoProxyText] = useState(
    proxy?.noProxy.join("\n") ?? ""
  )
  const [showPassword, setShowPassword] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)

  function update<K extends keyof NetworkProxyInput>(
    key: K,
    value: NetworkProxyInput[K]
  ) {
    setInput((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: "", form: "" }))
  }

  async function submit() {
    const candidate = {
      ...input,
      noProxy: Array.from(
        new Set(
          noProxyText
            .split(/\r?\n/)
            .map((item) => item.trim())
            .filter(Boolean)
        )
      ),
    }
    const parsed = networkProxyInputSchema.safeParse(candidate)
    if (!parsed.success) {
      setErrors(
        Object.fromEntries(
          parsed.error.issues.map((issue) => [
            String(issue.path[0] ?? "form"),
            issue.message,
          ])
        )
      )
      return
    }
    setSaving(true)
    setErrors({})
    try {
      await onSave(parsed.data)
    } catch (cause) {
      setErrors({ form: cause instanceof Error ? cause.message : "保存失败" })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-6 px-5 py-6 lg:px-8 lg:py-8">
      <div>
        <h2 className="text-lg font-semibold tracking-tight">
          {proxy ? proxy.name : "添加网络代理"}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          代理凭据会加密保存，只在 Worker 领取沙箱任务时短暂解密。
        </p>
      </div>

      {proxy && onCheck ? (
        <Item variant="outline" size="sm">
          <ItemMedia variant="icon">
            <GaugeIcon />
          </ItemMedia>
          <ItemContent>
            <ItemTitle>
              连通性检测
              <ProxyCheckBadge state={checkState} showUntested />
            </ItemTitle>
            <ItemDescription>
              {proxyCheckDescription(
                checkState,
                onlineServers.find((server) => server.id === checkServerId)
                  ?.name
              )}
            </ItemDescription>
          </ItemContent>
          <ItemActions>
            <Select
              value={checkServerId || undefined}
              disabled={onlineServers.length === 0}
              onValueChange={(value) => onCheckServerChange?.(value)}
            >
              <SelectTrigger
                className="w-40"
                aria-label="执行代理检测的 Worker"
              >
                <SelectValue placeholder="无在线 Worker" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectLabel>在线 Worker</SelectLabel>
                  {onlineServers.map((server) => (
                    <SelectItem key={server.id} value={server.id}>
                      {server.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="sm"
              disabled={
                saving || !checkServerId || checkState?.status === "checking"
              }
              onClick={() => void onCheck()}
            >
              {checkState?.status === "checking" ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <RefreshCwIcon data-icon="inline-start" />
              )}
              检测
            </Button>
          </ItemActions>
        </Item>
      ) : null}

      <Alert>
        <ShieldCheckIcon />
        <AlertTitle>作用于环境内的安装和运行流量</AlertTitle>
        <AlertDescription>
          AgentBox 会注入标准大小写 HTTP(S) 与 ALL_PROXY 变量。BoxLite
          受限网络可仅放行代理和直连地址；Docker
          仍依赖应用遵守代理变量。宿主机拉取镜像和 AgentBox LLM
          网关发出的模型请求不走这里的代理。
        </AlertDescription>
      </Alert>

      <FieldGroup>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field data-invalid={Boolean(errors.name)}>
            <FieldLabel htmlFor="proxy-name">名称</FieldLabel>
            <Input
              id="proxy-name"
              autoFocus
              value={input.name}
              aria-invalid={Boolean(errors.name)}
              placeholder="例如 办公网关"
              onChange={(event) => {
                update("name", event.target.value)
                if (!slugEdited) update("id", createSlug(event.target.value))
              }}
            />
            <FieldError>{errors.name}</FieldError>
          </Field>
          <Field data-invalid={Boolean(errors.id)}>
            <FieldLabel htmlFor="proxy-id">唯一标识</FieldLabel>
            <Input
              id="proxy-id"
              value={input.id}
              disabled={Boolean(proxy)}
              aria-invalid={Boolean(errors.id)}
              placeholder="office-proxy"
              onChange={(event) => {
                setSlugEdited(true)
                update("id", event.target.value.toLowerCase())
              }}
            />
            <FieldError>{errors.id}</FieldError>
          </Field>
        </div>

        <div className="grid gap-4 sm:grid-cols-[12rem_minmax(0,1fr)_9rem]">
          <Field data-invalid={Boolean(errors.scheme)}>
            <FieldLabel htmlFor="proxy-scheme">协议</FieldLabel>
            <Select
              value={input.scheme}
              onValueChange={(value) =>
                update("scheme", value as NetworkProxyInput["scheme"])
              }
            >
              <SelectTrigger id="proxy-scheme" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectLabel>代理协议</SelectLabel>
                  <SelectItem value="http">HTTP</SelectItem>
                  <SelectItem value="https">HTTPS</SelectItem>
                  <SelectItem value="socks5">SOCKS5</SelectItem>
                  <SelectItem value="socks5h">
                    SOCKS5H · 远端解析 DNS
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            <FieldDescription>
              SOCKS5H 会让兼容程序通过代理解析域名，减少本地 DNS 泄漏。
            </FieldDescription>
            <FieldError>{errors.scheme}</FieldError>
          </Field>
          <Field data-invalid={Boolean(errors.host)}>
            <FieldLabel htmlFor="proxy-host">主机或 IP</FieldLabel>
            <Input
              id="proxy-host"
              value={input.host}
              aria-invalid={Boolean(errors.host)}
              placeholder="proxy.example.com"
              onChange={(event) => update("host", event.target.value)}
            />
            <FieldError>{errors.host}</FieldError>
          </Field>
          <Field data-invalid={Boolean(errors.port)}>
            <FieldLabel htmlFor="proxy-port">端口</FieldLabel>
            <Input
              id="proxy-port"
              type="number"
              min={1}
              max={65535}
              value={input.port || ""}
              aria-invalid={Boolean(errors.port)}
              onChange={(event) => update("port", Number(event.target.value))}
            />
            <FieldError>{errors.port}</FieldError>
          </Field>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field data-invalid={Boolean(errors.username)}>
            <FieldLabel htmlFor="proxy-username">用户名（可选）</FieldLabel>
            <Input
              id="proxy-username"
              autoComplete="username"
              value={input.username}
              aria-invalid={Boolean(errors.username)}
              onChange={(event) => update("username", event.target.value)}
            />
            <FieldError>{errors.username}</FieldError>
          </Field>
          <Field data-invalid={Boolean(errors.password)}>
            <FieldLabel htmlFor="proxy-password">密码（可选）</FieldLabel>
            <InputGroup>
              <InputGroupInput
                id="proxy-password"
                type={showPassword ? "text" : "password"}
                autoComplete="new-password"
                value={input.password}
                aria-invalid={Boolean(errors.password)}
                placeholder={proxy?.hasPassword ? "留空保留现有密码" : ""}
                onChange={(event) => update("password", event.target.value)}
              />
              <InputGroupAddon align="inline-end">
                <InputGroupButton
                  size="icon-xs"
                  aria-label={showPassword ? "隐藏密码" : "显示密码"}
                  onClick={() => setShowPassword((value) => !value)}
                >
                  {showPassword ? <EyeOffIcon /> : <EyeIcon />}
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
            <FieldDescription>
              {proxy?.hasPassword
                ? `当前已保存 ${proxy.maskedPassword}；留空不会覆盖。`
                : "不需要认证时保持为空。"}
            </FieldDescription>
            <FieldError>{errors.password}</FieldError>
          </Field>
        </div>

        <Field data-invalid={Boolean(errors.noProxy)}>
          <FieldLabel htmlFor="proxy-no-proxy">直连地址（可选）</FieldLabel>
          <Textarea
            id="proxy-no-proxy"
            className="min-h-28 font-mono text-sm"
            value={noProxyText}
            aria-invalid={Boolean(errors.noProxy)}
            placeholder={
              "localhost\n127.0.0.1\n*.internal.example.com\n10.0.0.0/8"
            }
            onChange={(event) => {
              setNoProxyText(event.target.value)
              setErrors((current) => ({ ...current, noProxy: "", form: "" }))
            }}
          />
          <FieldDescription>
            每行一个主机、域名、IP 或 CIDR。控制面地址和 localhost 会自动加入。
          </FieldDescription>
          <FieldError>{errors.noProxy}</FieldError>
        </Field>

        <Field orientation="horizontal" className="rounded-lg border p-3">
          <Switch
            id="proxy-enabled"
            checked={input.enabled}
            onCheckedChange={(checked) => update("enabled", checked)}
          />
          <div>
            <FieldLabel htmlFor="proxy-enabled">允许新任务使用</FieldLabel>
            <FieldDescription>
              停用后保留配置，但引用它的模板和沙箱不能创建新任务。
            </FieldDescription>
          </div>
        </Field>

        {errors.form && <FieldError>{errors.form}</FieldError>}
      </FieldGroup>

      <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-5">
        <div>
          {proxy && onDelete && (
            <Button
              variant="outline"
              className="text-destructive hover:text-destructive"
              disabled={saving}
              onClick={onDelete}
            >
              <Trash2Icon data-icon="inline-start" />
              删除
            </Button>
          )}
        </div>
        <div className="flex gap-2">
          {onCancel && (
            <Button variant="outline" disabled={saving} onClick={onCancel}>
              取消
            </Button>
          )}
          {canMutate ? (
            <Button disabled={saving} onClick={() => void submit()}>
              {saving ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <SaveIcon data-icon="inline-start" />
              )}
              {proxy ? "保存修改" : "保存代理"}
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function createSlug(value: string) {
  return (
    value
      .normalize("NFKD")
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 64) || "proxy"
  )
}

function formatHost(host: string) {
  return host.includes(":") ? `[${host}]` : host
}

function ProxyCheckBadge({
  state,
  showUntested = false,
}: {
  state?: ProxyCheckState
  showUntested?: boolean
}) {
  if (!state) {
    return showUntested ? <Badge variant="outline">未检测</Badge> : null
  }
  if (state.status === "checking") {
    return (
      <Badge variant="outline">
        <Spinner />
        检测中
      </Badge>
    )
  }
  if (state.status === "error") {
    return (
      <Badge variant="destructive">
        <CircleXIcon />
        失败
      </Badge>
    )
  }
  if (state.result.ok) {
    return (
      <Badge variant="secondary">
        <CircleCheckIcon />
        可用 · {state.result.latencyMs ?? 0} ms
      </Badge>
    )
  }
  return (
    <Badge variant="destructive">
      <CircleXIcon />
      失败
    </Badge>
  )
}

function proxyCheckDescription(
  state?: ProxyCheckState,
  selectedServerName?: string
) {
  if (!state) {
    return selectedServerName
      ? `由 Worker ${selectedServerName} 通过此代理访问 Google 204；不检测模型出口。`
      : "没有在线 Worker 可执行代理检测。"
  }
  if (state.status === "checking") {
    return `Worker ${state.serverName} 正在建立代理连接，最长等待 10 秒。`
  }
  if (state.status === "error") {
    return state.serverName
      ? `Worker ${state.serverName} · ${state.error}`
      : state.error
  }
  if (state.result.ok) {
    return `Worker ${state.result.serverName} · HTTP ${state.result.statusCode ?? 204} · ${state.result.latencyMs ?? 0} ms · Google 204`
  }
  return `Worker ${state.result.serverName} · ${state.result.error || "代理不可用"}`
}
