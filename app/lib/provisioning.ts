const stageLabels: Record<string, string> = {
  "runtime-check": "检查运行时",
  "agent-image": "准备 Agent 镜像",
  "image-prepare": "准备运行时镜像",
  "runtime-create": "创建沙箱实例",
  "runtime-start": "启动沙箱实例",
  "agent-tools": "安装 Agent 工具",
  configuration: "写入配置",
  setup: "执行初始化",
  verify: "验证可用性",
  completed: "已完成",
  failed: "失败",
}

const cacheStatusLabels: Record<string, string> = {
  hit: "命中",
  miss: "未命中",
  partial: "复用部分缓存",
  refreshed: "已更新",
  fallback: "使用旧缓存",
  "not-needed": "无需缓存",
}

const cacheReasonLabels: Record<string, string> = {
  "exact-cache": "精确匹配",
  "no-agent-tools": "未选择工具",
  "not-found": "首次构建",
  expired: "缓存已过期",
  "invalid-metadata": "缓存元数据无效",
  "compatible-subset": "兼容工具子集",
  "cache-created": "构建完成",
  "refresh-failed": "刷新失败",
}

const agentToolStatusLabels: Record<string, string> = {
  pending: "等待安装",
  running: "安装中",
  installed: "等待验证",
  verifying: "验证中",
  succeeded: "已完成",
  failed: "失败",
  cached: "缓存复用",
}

export function provisioningStageLabel(stage: string) {
  return (stageLabels[stage] ?? stage) || "—"
}

export function provisioningCacheLabel(status: string, reason: string) {
  if (!status) return "—"
  const label = cacheStatusLabels[status] ?? status
  return reason ? `${label} · ${cacheReasonLabels[reason] ?? reason}` : label
}

export function provisioningDuration(durationMs: number) {
  if (durationMs < 1000) return `${durationMs} ms`
  const seconds = Math.round(durationMs / 1000)
  if (seconds < 60) return `${seconds} 秒`
  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  return remainingSeconds > 0
    ? `${minutes} 分 ${remainingSeconds} 秒`
    : `${minutes} 分`
}

export function provisioningAgentToolStatusLabel(status: string) {
  return (agentToolStatusLabels[status] ?? status) || "等待安装"
}
