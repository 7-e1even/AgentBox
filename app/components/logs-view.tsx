"use client"

import { Fragment, useCallback, useEffect, useState } from "react"
import { usePolling } from "@/hooks/use-polling"
import { LoadState } from "@/components/load-state"
import {
  ChevronDownIcon,
  ChevronRightIcon,
  RefreshCwIcon,
  ScrollTextIcon,
} from "lucide-react"

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
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { errorMessage, requestJson } from "@/lib/api-client"
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

type LogEntry = {
  id: string
  createdAt: string
  level: string
  category: string
  action: string
  message: string
  actorName: string
  resourceKind: string
  resourceName: string
  status: string
  durationMs: number | null
  remoteAddr: string
  detail: unknown
}

// 后端字段名以实际响应为准，这里同时容忍 camelCase 与 snake_case。
function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function pickField(record: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const value = record[key]
    if (value !== undefined && value !== null) return value
  }
  return undefined
}

function pickString(record: Record<string, unknown>, ...keys: string[]) {
  const value = pickField(record, ...keys)
  if (value === undefined || value === null) return ""
  return typeof value === "string" ? value : String(value)
}

function normalizeLogEntry(value: unknown, index: number): LogEntry {
  const record = asRecord(value) ?? {}
  const id = pickField(record, "id")
  const duration = pickField(record, "durationMs", "duration_ms")
  return {
    id: id !== undefined ? String(id) : `row-${index}`,
    createdAt: pickString(record, "createdAt", "created_at"),
    level: pickString(record, "level") || "info",
    category: pickString(record, "category") || "system",
    action: pickString(record, "action"),
    message: pickString(record, "message"),
    actorName:
      pickString(record, "actorName", "actor_name") ||
      pickString(record, "actorId", "actor_id"),
    resourceKind: pickString(record, "resourceKind", "resource_kind"),
    resourceName: pickString(record, "resourceName", "resource_name"),
    status: pickString(record, "status"),
    durationMs: typeof duration === "number" ? duration : null,
    remoteAddr: pickString(record, "remoteAddr", "remote_addr"),
    detail: pickField(record, "detail", "details"),
  }
}

function normalizeLogsResponse(body: unknown) {
  const record = asRecord(body) ?? {}
  const rawEntries = Array.isArray(record.entries) ? record.entries : []
  const total =
    typeof record.total === "number" ? record.total : rawEntries.length
  return {
    entries: rawEntries.map(normalizeLogEntry),
    total,
  }
}

export function LogsView() {
  const [page, setPage] = useState(1)
  const [category, setCategory] = useState("all")
  const [level, setLevel] = useState("all")
  const [status, setStatus] = useState("all")
  const [search, setSearch] = useState("")
  const [query, setQuery] = useState("")
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  // 关键字输入防抖 300ms，回车立即生效。
  useEffect(() => {
    const timer = window.setTimeout(() => {
      setPage(1)
      setQuery(search.trim())
    }, 300)
    return () => window.clearTimeout(timer)
  }, [search])

  const fetchLogs = useCallback(
    async (signal: AbortSignal) => {
      const params = new URLSearchParams()
      if (category !== "all") params.set("category", category)
      if (level !== "all") params.set("level", level)
      if (status !== "all") params.set("status", status)
      if (query) params.set("q", query)
      params.set("page", String(page))
      params.set("pageSize", String(PAGE_SIZE))
      const body = await requestJson<unknown>(`/api/logs?${params}`, { signal })
      const result = normalizeLogsResponse(body)
      return result
    },
    [category, level, status, query, page]
  )

  const logsPolling = usePolling({
    queryKey: `logs:${category}:${level}:${status}:${query}:${page}`,
    load: fetchLogs,
    interval: autoRefresh ? 5000 : false,
  })
  const entries = logsPolling.data?.entries ?? []
  const total = logsPolling.data?.total ?? 0
  const loadError = logsPolling.error ? errorMessage(logsPolling.error) : ""
  const loading = logsPolling.data === undefined && !logsPolling.error
  const refreshing = logsPolling.loading
  const loadLogs = async () => {
    await logsPolling.refresh()
  }

  const hasFilters =
    category !== "all" || level !== "all" || status !== "all" || query !== ""

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="日志"
        count={total}
        action={
          <div className="flex items-center gap-2">
            <Label
              htmlFor="logs-auto-refresh"
              className="hidden text-xs text-muted-foreground sm:inline"
            >
              自动刷新
            </Label>
            <Switch
              id="logs-auto-refresh"
              checked={autoRefresh}
              onCheckedChange={setAutoRefresh}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={loading || refreshing}
              onClick={() => void loadLogs()}
            >
              <RefreshCwIcon
                data-icon="inline-start"
                className={cn(refreshing && "animate-spin")}
              />
              刷新
            </Button>
          </div>
        }
      />
      <CollectionContent>
        <CollectionToolbar>
          <CollectionSearch
            value={search}
            placeholder="搜索日志内容、动作或资源名"
            onChange={(event) => setSearch(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                setPage(1)
                setQuery(search.trim())
              }
            }}
          />
          <div className="flex flex-wrap items-center gap-2">
            <Select
              value={category}
              onValueChange={(value) => {
                setPage(1)
                setCategory(value)
              }}
            >
              <SelectTrigger className="w-32" aria-label="按分类筛选">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部分类</SelectItem>
                {CATEGORY_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={level}
              onValueChange={(value) => {
                setPage(1)
                setLevel(value)
              }}
            >
              <SelectTrigger className="w-28" aria-label="按级别筛选">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部级别</SelectItem>
                <SelectItem value="info">info</SelectItem>
                <SelectItem value="warn">warn</SelectItem>
                <SelectItem value="error">error</SelectItem>
              </SelectContent>
            </Select>
            <Select
              value={status}
              onValueChange={(value) => {
                setPage(1)
                setStatus(value)
              }}
            >
              <SelectTrigger className="w-28" aria-label="按结果筛选">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部结果</SelectItem>
                <SelectItem value="success">成功</SelectItem>
                <SelectItem value="failed">失败</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CollectionToolbar>

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
          <div className="overflow-hidden rounded-lg border bg-card p-4">
            <div className="flex flex-col gap-3">
              {Array.from({ length: 10 }).map((_, index) => (
                <Skeleton key={index} className="h-9 w-full" />
              ))}
            </div>
          </div>
        ) : entries.length === 0 && !loadError ? (
          <Empty className="min-h-72 border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ScrollTextIcon />
              </EmptyMedia>
              <EmptyTitle>
                {hasFilters ? "没有匹配的日志" : "还没有日志记录"}
              </EmptyTitle>
              <EmptyDescription>
                {hasFilters
                  ? "调整筛选条件或搜索词后再试。"
                  : "登录、沙箱操作、自动化等系统事件会记录在这里。"}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : entries.length === 0 ? null : (
          <CollectionTable
            pagination={
              <CollectionPagination
                currentPage={page}
                pageSize={PAGE_SIZE}
                totalItems={total}
                onPageChange={setPage}
              />
            }
          >
            <TableHeader>
              <TableRow>
                <TableHead className="w-10 pl-4" />
                <TableHead>时间</TableHead>
                <TableHead>级别</TableHead>
                <TableHead>分类</TableHead>
                <TableHead>操作者</TableHead>
                <TableHead>动作</TableHead>
                <TableHead className="pr-4">摘要</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((entry) => {
                const expanded = expandedId === entry.id
                return (
                  <Fragment key={entry.id}>
                    <TableRow
                      className="cursor-pointer"
                      aria-expanded={expanded}
                      onClick={() => setExpandedId(expanded ? null : entry.id)}
                    >
                      <TableCell className="pl-4 text-muted-foreground">
                        {expanded ? (
                          <ChevronDownIcon className="size-4" />
                        ) : (
                          <ChevronRightIcon className="size-4" />
                        )}
                      </TableCell>
                      <TableCell className="whitespace-nowrap">
                        {formatLogTime(entry.createdAt)}
                      </TableCell>
                      <TableCell>
                        <LevelBadge level={entry.level} />
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">
                          {CATEGORY_LABELS.get(entry.category) ??
                            entry.category}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {entry.actorName ? (
                          entry.actorName
                        ) : (
                          <span className="text-muted-foreground">系统</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <code className="font-mono text-xs">
                          {entry.action || "—"}
                        </code>
                      </TableCell>
                      <TableCell className="max-w-96 pr-4">
                        <span
                          className={cn(
                            "block truncate",
                            entry.status === "failed" && "text-destructive"
                          )}
                        >
                          {entry.message || "—"}
                        </span>
                      </TableCell>
                    </TableRow>
                    {expanded && (
                      <TableRow className="bg-muted/30 hover:bg-muted/30">
                        <TableCell colSpan={7} className="px-4 py-3">
                          <div className="flex flex-col gap-2">
                            <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground">
                              <span>
                                结果：
                                {entry.status === "failed"
                                  ? "失败"
                                  : entry.status === "success"
                                    ? "成功"
                                    : "—"}
                              </span>
                              <span>
                                耗时：
                                {entry.durationMs !== null
                                  ? `${entry.durationMs} ms`
                                  : "—"}
                              </span>
                              <span>来源地址：{entry.remoteAddr || "—"}</span>
                              {(entry.resourceName || entry.resourceKind) && (
                                <span>
                                  资源：
                                  {entry.resourceKind
                                    ? `${entry.resourceKind}/`
                                    : ""}
                                  {entry.resourceName || "—"}
                                </span>
                              )}
                            </div>
                            {entry.detail !== undefined &&
                              entry.detail !== null && (
                                <pre className="max-h-64 overflow-auto rounded-md bg-muted p-3 font-mono text-xs">
                                  {formatDetail(entry.detail)}
                                </pre>
                              )}
                          </div>
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                )
              })}
            </TableBody>
          </CollectionTable>
        )}
      </CollectionContent>
    </section>
  )
}

function LevelBadge({ level }: { level: string }) {
  if (level === "error") {
    return <Badge variant="destructive">error</Badge>
  }
  if (level === "warn") {
    return (
      <Badge
        variant="outline"
        className="border-amber-500/50 text-amber-600 dark:text-amber-400"
      >
        warn
      </Badge>
    )
  }
  return <Badge variant="secondary">{level || "info"}</Badge>
}

function formatLogTime(value: string) {
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date)
}

function formatDetail(detail: unknown) {
  if (typeof detail === "string") return detail
  try {
    return JSON.stringify(detail, null, 2)
  } catch {
    return String(detail)
  }
}
