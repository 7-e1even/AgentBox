export type LogTimeRange = "1h" | "24h" | "7d" | "all"

export type LogEntry = {
  id: string
  createdAt: string
  level: string
  category: string
  action: string
  message: string
  actorId: string
  actorName: string
  resourceKind: string
  resourceId: string
  resourceName: string
  status: string
  durationMs: number | null
  remoteAddr: string
  detail: unknown
  raw: Record<string, unknown>
}

export function createLogsQuery(
  filter: {
    page: number
    pageSize: number
    category: string
    level: string
    status: string
    query: string
    timeRange: LogTimeRange
    before: string | null
  },
  now = new Date()
) {
  const end = filter.before ? new Date(filter.before) : now
  const to = end.toISOString()
  const hours = { "1h": 1, "24h": 24, "7d": 168 }
  const from =
    filter.timeRange === "all"
      ? null
      : new Date(
          end.getTime() - hours[filter.timeRange] * 60 * 60 * 1000
        ).toISOString()
  const params = new URLSearchParams({
    page: String(filter.page),
    pageSize: String(filter.pageSize),
    to,
  })
  if (from) params.set("from", from)
  if (filter.category !== "all") params.set("category", filter.category)
  if (filter.level !== "all") params.set("level", filter.level)
  if (filter.status !== "all") params.set("status", filter.status)
  if (filter.query) params.set("q", filter.query)
  return { params, from, to }
}

function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
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
  return value === undefined ? "" : String(value)
}

// 保留原始响应供详情查看，同时兼容已有的 camelCase / snake_case 字段。
function normalizeLogEntry(value: unknown, index: number): LogEntry {
  const record = asRecord(value)
  const id = pickField(record, "id")
  const duration = pickField(record, "durationMs", "duration_ms")
  return {
    id: id !== undefined ? String(id) : `row-${index}`,
    createdAt: pickString(record, "createdAt", "created_at"),
    level: pickString(record, "level") || "info",
    category: pickString(record, "category") || "system",
    action: pickString(record, "action"),
    message: pickString(record, "message"),
    actorId: pickString(record, "actorId", "actor_id"),
    actorName: pickString(record, "actorName", "actor_name"),
    resourceKind: pickString(record, "resourceKind", "resource_kind"),
    resourceId: pickString(record, "resourceId", "resource_id"),
    resourceName: pickString(record, "resourceName", "resource_name"),
    status: pickString(record, "status"),
    durationMs: typeof duration === "number" ? duration : null,
    remoteAddr: pickString(record, "remoteAddr", "remote_addr"),
    detail: pickField(record, "detail", "details"),
    raw: record,
  }
}

export function normalizeLogsResponse(body: unknown) {
  const record = asRecord(body)
  const rawEntries = Array.isArray(record.entries) ? record.entries : []
  return {
    entries: rawEntries.map(normalizeLogEntry),
    total: typeof record.total === "number" ? record.total : rawEntries.length,
  }
}

export function formatLogDuration(value: number | null) {
  // 后端 0 同时可能表示未采集或不足 1ms，不把它标成精确的 0ms。
  if (value === null || value <= 0) return "—"
  return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value} ms`
}

export function formatLogDetail(detail: unknown) {
  if (typeof detail === "string") return detail
  return JSON.stringify(detail, null, 2) ?? "—"
}
