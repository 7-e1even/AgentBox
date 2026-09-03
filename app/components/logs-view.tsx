"use client"

import { Fragment, useCallback, useEffect, useRef, useState } from "react"
import {
  CheckIcon,
  CircleAlertIcon,
  RefreshCwIcon,
  ScrollTextIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { usePolling } from "@/hooks/use-polling"
import { LoadState } from "@/components/load-state"
import { CollectionHeader } from "@/components/control-plane-view"
import {
  CollectionContent,
  CollectionPagination,
  CollectionSearch,
  CollectionTable,
  CollectionToolbar,
} from "@/components/collection-list"
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
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { errorMessage, requestJson } from "@/lib/api-client"
import {
  createLogsQuery,
  formatLogDetail,
  formatLogDuration,
  normalizeLogsResponse,
  type LogEntry,
  type LogTimeRange,
} from "@/lib/logs"
import { cn } from "@/lib/utils"

const PAGE_SIZE = 50

const CATEGORY_OPTIONS = [
  { value: "auth", label: "认证" },
  { value: "sandbox", label: "沙箱" },
  { value: "session", label: "会话" },
  { value: "automation", label: "自动化" },
  { value: "server", label: "服务器" },
  { value: "job", label: "任务" },
  { value: "llm", label: "模型调用" },
  { value: "resource", label: "资源" },
  { value: "credential", label: "凭据" },
  { value: "proxy", label: "网络代理" },
  { value: "user", label: "用户" },
  { value: "api", label: "API 访问" },
  { value: "system", label: "系统" },
] as const

const CATEGORY_LABELS = new Map<string, string>(
  CATEGORY_OPTIONS.map((option) => [option.value, option.label])
)

export function LogsView() {
  const [pagination, setPagination] = useState<{
    page: number
    before: string | null
  }>({ page: 1, before: null })
  const [timeRange, setTimeRange] = useState<LogTimeRange>("24h")
  const [category, setCategory] = useState("all")
  const [level, setLevel] = useState("all")
  const [status, setStatus] = useState("all")
  const [search, setSearch] = useState("")
  const [query, setQuery] = useState("")
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [selectedLog, setSelectedLog] = useState<LogEntry | null>(null)
  const detailTrigger = useRef<HTMLButtonElement | null>(null)

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setPagination({ page: 1, before: null })
      setQuery(search.trim())
    }, 300)
    return () => window.clearTimeout(timer)
  }, [search])

  const fetchLogs = useCallback(
    async (signal: AbortSignal) => {
      const { params, from, to } = createLogsQuery({
        ...pagination,
        pageSize: PAGE_SIZE,
        category,
        level,
        status,
        query,
        timeRange,
      })
      const body = await requestJson<unknown>(`/api/logs?${params}`, { signal })
      return { ...normalizeLogsResponse(body), from, to }
    },
    [pagination, category, level, status, query, timeRange]
  )

  const refreshPaused = pagination.page !== 1 || selectedLog !== null
  const logsPolling = usePolling({
    queryKey: `logs:${category}:${level}:${status}:${query}:${timeRange}:${pagination.page}:${pagination.before}`,
    load: fetchLogs,
    interval: autoRefresh && !refreshPaused ? 5000 : false,
  })
  const entries = logsPolling.data?.entries ?? []
  const total = logsPolling.data?.total ?? 0
  const loadError = logsPolling.error ? errorMessage(logsPolling.error) : ""
  const loading = logsPolling.data === undefined && !logsPolling.error
  const refreshing = logsPolling.loading
  const hasFilters =
    category !== "all" || level !== "all" || status !== "all" || query !== ""
  const resetPage = () => setPagination({ page: 1, before: null })
  const loadLogs = async () => {
    await logsPolling.refresh()
  }
  const clearFilters = () => {
    setCategory("all")
    setLevel("all")
    setStatus("all")
    setSearch("")
    setQuery("")
    setTimeRange("all")
    resetPage()
  }

  return (
    <Sheet
      open={selectedLog !== null}
      onOpenChange={(open) => {
        if (!open) setSelectedLog(null)
      }}
    >
      <section className="flex min-h-0 flex-1 flex-col">
        <CollectionHeader
          title="日志"
          count={total}
          action={
            <div className="flex items-center gap-2">
              <Label htmlFor="logs-auto-refresh" className="hidden sm:inline">
                每 5 秒刷新
              </Label>
              <Switch
                id="logs-auto-refresh"
                aria-label="每 5 秒自动刷新日志"
                checked={autoRefresh}
                onCheckedChange={setAutoRefresh}
              />
              <Button
                variant="outline"
                size="sm"
                aria-label="刷新日志"
                disabled={loading || refreshing}
                onClick={() => void loadLogs()}
              >
                <RefreshCwIcon
                  data-icon="inline-start"
                  className={cn(refreshing && "animate-spin")}
                />
                <span className="hidden sm:inline">刷新</span>
              </Button>
            </div>
          }
        />
        <CollectionContent className="gap-2">
          <CollectionToolbar>
            <CollectionSearch
              value={search}
              aria-label="搜索日志内容、动作或资源名"
              placeholder="搜索事件、动作、资源名"
              onChange={(event) => setSearch(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  resetPage()
                  setQuery(search.trim())
                }
              }}
            />
            <div className="grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center">
              <Select
                value={timeRange}
                onValueChange={(value) => {
                  resetPage()
                  setTimeRange(value as LogTimeRange)
                }}
              >
                <SelectTrigger className="w-full sm:w-32" aria-label="时间范围">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="1h">最近 1 小时</SelectItem>
                    <SelectItem value="24h">最近 24 小时</SelectItem>
                    <SelectItem value="7d">最近 7 天</SelectItem>
                    <SelectItem value="all">全部时间</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select
                value={category}
                onValueChange={(value) => {
                  resetPage()
                  setCategory(value)
                }}
              >
                <SelectTrigger
                  className="w-full sm:w-32"
                  aria-label="按分类筛选"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">全部分类</SelectItem>
                    {CATEGORY_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select
                value={level}
                onValueChange={(value) => {
                  resetPage()
                  setLevel(value)
                }}
              >
                <SelectTrigger
                  className="w-full sm:w-28"
                  aria-label="按级别筛选"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">全部级别</SelectItem>
                    <SelectItem value="info">INFO</SelectItem>
                    <SelectItem value="warn">WARN</SelectItem>
                    <SelectItem value="error">ERROR</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select
                value={status}
                onValueChange={(value) => {
                  resetPage()
                  setStatus(value)
                }}
              >
                <SelectTrigger
                  className="w-full sm:w-28"
                  aria-label="按结果筛选"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">全部结果</SelectItem>
                    <SelectItem value="success">成功</SelectItem>
                    <SelectItem value="failed">失败</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          </CollectionToolbar>

          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
            <div className="flex flex-wrap items-center gap-2">
              {logsPolling.data && (
                <span>
                  {logsPolling.data.from
                    ? `${formatLogTime(logsPolling.data.from)} — ${formatLogTime(logsPolling.data.to)}`
                    : `全部时间 · 截至 ${formatLogTime(logsPolling.data.to)}`}
                  {" · 本地时间"}
                </span>
              )}
              {hasFilters && (
                <Button variant="ghost" size="xs" onClick={clearFilters}>
                  清除筛选
                </Button>
              )}
            </div>
            <span>
              {autoRefresh && refreshPaused
                ? "自动刷新已暂停 · 正在查看历史或详情"
                : autoRefresh
                  ? "每 5 秒刷新 · 最新优先"
                  : "最新优先"}
            </span>
          </div>

          {loadError && !loading && (
            <LoadState
              label="日志"
              error={new Error(loadError)}
              stale={logsPolling.data !== undefined}
              loading={refreshing}
              onRetry={loadLogs}
            />
          )}

          {loading ? (
            <div
              className="flex flex-col gap-3 py-4"
              role="status"
              aria-label="正在加载日志"
            >
              {Array.from({ length: 8 }).map((_, index) => (
                <Skeleton key={index} className="h-14 w-full" />
              ))}
            </div>
          ) : entries.length === 0 && !loadError ? (
            <Empty className="min-h-72 border">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <ScrollTextIcon />
                </EmptyMedia>
                <EmptyTitle>
                  {hasFilters
                    ? "没有匹配的日志"
                    : timeRange === "all"
                      ? "还没有日志记录"
                      : "所选时间范围内暂无日志"}
                </EmptyTitle>
                <EmptyDescription>
                  调整时间范围或筛选条件，查看其他日志记录。
                </EmptyDescription>
              </EmptyHeader>
              {(hasFilters || timeRange !== "all") && (
                <EmptyContent>
                  <Button variant="outline" size="sm" onClick={clearFilters}>
                    查看全部日志
                  </Button>
                </EmptyContent>
              )}
            </Empty>
          ) : entries.length === 0 ? null : (
            <CollectionTable
              className="table-fixed"
              aria-label="系统日志"
              pagination={
                <CollectionPagination
                  currentPage={pagination.page}
                  pageSize={PAGE_SIZE}
                  totalItems={total}
                  onPageChange={(page) =>
                    setPagination({
                      page,
                      before:
                        page === 1 ? null : (logsPolling.data?.to ?? null),
                    })
                  }
                />
              }
            >
              <TableHeader>
                <TableRow>
                  <TableHead className="hidden w-44 pl-4 lg:table-cell">
                    时间
                  </TableHead>
                  <TableHead className="pl-4 lg:pl-2">
                    事件 / 关联资源
                  </TableHead>
                  <TableHead className="w-24 pr-4 sm:pr-2">结果</TableHead>
                  <TableHead className="hidden w-24 pr-4 text-right sm:table-cell">
                    耗时
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map((entry) => (
                  <TableRow
                    key={entry.id}
                    data-state={
                      selectedLog?.id === entry.id ? "selected" : undefined
                    }
                  >
                    <TableCell className="hidden pl-4 lg:table-cell">
                      <time
                        dateTime={entry.createdAt}
                        className="font-mono text-xs tabular-nums"
                      >
                        {formatLogTime(entry.createdAt)}
                      </time>
                    </TableCell>
                    <TableCell className="py-1 pl-4 whitespace-normal lg:pl-2">
                      <div className="flex min-w-0 flex-col">
                        <Button
                          variant="ghost"
                          className="h-auto w-full justify-start p-0 text-left whitespace-normal"
                          aria-haspopup="dialog"
                          onClick={(event) => {
                            detailTrigger.current = event.currentTarget
                            setSelectedLog(entry)
                          }}
                        >
                          <span className="line-clamp-2 break-all sm:line-clamp-1">
                            {entry.message || entry.action || "日志事件"}
                          </span>
                        </Button>
                        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                          <time
                            dateTime={entry.createdAt}
                            className="font-mono tabular-nums lg:hidden"
                          >
                            {formatLogTime(entry.createdAt)}
                          </time>
                          <LevelBadge level={entry.level} />
                          <span>
                            {CATEGORY_LABELS.get(entry.category) ??
                              entry.category}
                          </span>
                          {(entry.resourceName || entry.resourceId) && (
                            <span className="truncate">
                              · {entry.resourceName || entry.resourceId}
                            </span>
                          )}
                          <span className="truncate">
                            · {entry.actorName || entry.actorId || "系统"}
                          </span>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="pr-4 sm:pr-2">
                      <StatusBadge status={entry.status} />
                    </TableCell>
                    <TableCell className="hidden pr-4 text-right sm:table-cell">
                      <span className="font-mono text-xs tabular-nums">
                        {formatLogDuration(entry.durationMs)}
                      </span>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </CollectionTable>
          )}
        </CollectionContent>
      </section>

      <SheetContent
        className="gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-lg"
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          detailTrigger.current?.focus()
        }}
      >
        <SheetHeader className="shrink-0 gap-2 pr-12">
          <SheetTitle>日志详情</SheetTitle>
          <SheetDescription>
            {selectedLog
              ? formatLogTime(selectedLog.createdAt, true)
              : "查看事件详情"}
          </SheetDescription>
          {selectedLog && (
            <div className="flex flex-wrap items-center gap-2">
              <StatusBadge status={selectedLog.status} />
              <LevelBadge level={selectedLog.level} />
              <Badge variant="outline">
                {CATEGORY_LABELS.get(selectedLog.category) ??
                  selectedLog.category}
              </Badge>
            </div>
          )}
        </SheetHeader>
        <Separator />
        {selectedLog && (
          <Tabs
            key={selectedLog.id}
            defaultValue="overview"
            className="min-h-0 flex-1 gap-0"
          >
            <div className="shrink-0 px-4 py-2">
              <TabsList variant="line" aria-label="日志详情视图">
                <TabsTrigger value="overview">概览</TabsTrigger>
                <TabsTrigger value="raw">原始数据</TabsTrigger>
              </TabsList>
            </div>
            <Separator />
            <TabsContent value="overview" className="min-h-0 overflow-auto p-4">
              <div className="flex flex-col gap-5">
                <p className="break-all whitespace-pre-wrap">
                  {selectedLog.message || selectedLog.action || "—"}
                </p>
                <dl className="grid grid-cols-[5rem_minmax(0,1fr)] gap-x-4 gap-y-3 text-sm">
                  {[
                    [
                      "操作者",
                      selectedLog.actorName || selectedLog.actorId || "系统",
                    ],
                    ["操作者 ID", selectedLog.actorId],
                    ["资源", selectedLog.resourceName],
                    ["资源类型", selectedLog.resourceKind],
                    ["资源 ID", selectedLog.resourceId],
                    ["动作", selectedLog.action],
                    [
                      "耗时",
                      selectedLog.durationMs === 0
                        ? "未记录或低于 1 ms"
                        : formatLogDuration(selectedLog.durationMs),
                    ],
                    ["来源地址", selectedLog.remoteAddr],
                    ["日志 ID", selectedLog.id],
                  ].map(([label, value]) => (
                    <Fragment key={label}>
                      <dt className="text-muted-foreground">{label}</dt>
                      <dd className="min-w-0 break-all">{value || "—"}</dd>
                    </Fragment>
                  ))}
                </dl>
                {selectedLog.detail !== undefined &&
                  selectedLog.detail !== null && (
                    <div className="flex min-w-0 flex-col gap-2">
                      <p className="text-sm font-medium">事件元数据</p>
                      <pre className="rounded-lg border bg-muted/30 p-3 font-mono text-xs break-all whitespace-pre-wrap">
                        {formatLogDetail(selectedLog.detail)}
                      </pre>
                    </div>
                  )}
              </div>
            </TabsContent>
            <TabsContent value="raw" className="min-h-0 overflow-auto p-4">
              <pre className="rounded-lg border bg-muted/30 p-3 font-mono text-xs break-all whitespace-pre-wrap">
                {formatLogDetail(selectedLog.raw)}
              </pre>
            </TabsContent>
          </Tabs>
        )}
      </SheetContent>
    </Sheet>
  )
}

function StatusBadge({ status }: { status: string }) {
  if (status === "failed") {
    return (
      <Badge variant="destructive">
        <CircleAlertIcon data-icon="inline-start" />
        失败
      </Badge>
    )
  }
  if (status === "success") {
    return (
      <Badge variant="outline">
        <CheckIcon data-icon="inline-start" />
        成功
      </Badge>
    )
  }
  return <span className="text-muted-foreground">{status || "—"}</span>
}

function LevelBadge({ level }: { level: string }) {
  return (
    <Badge
      variant={
        level === "error"
          ? "destructive"
          : level === "warn"
            ? "outline"
            : "secondary"
      }
    >
      {level === "warn" && <TriangleAlertIcon data-icon="inline-start" />}
      {level.toUpperCase()}
    </Badge>
  )
}

function formatLogTime(value: string, full = false) {
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("zh-CN", {
    ...(full
      ? ({ year: "numeric", timeZoneName: "shortOffset" } as const)
      : {}),
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date)
}
