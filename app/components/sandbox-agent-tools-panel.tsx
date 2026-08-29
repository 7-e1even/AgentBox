"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  BotIcon,
  CircleAlertIcon,
  DownloadIcon,
  RefreshCwIcon,
  WrenchIcon,
} from "lucide-react"

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
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { appToast as toast } from "@/lib/app-toast"
import { agentToolOptions, type AgentToolId } from "@/lib/agent-tools"
import { errorMessage, requestJson } from "@/lib/api-client"
import { resourceResponseSchema, type Resource } from "@/lib/platform-schema"
import {
  agentToolLabel,
  agentToolNeedsUpdate,
  sandboxAgentToolOperation,
  sandboxAgentToolStates,
  type SandboxAgentToolState,
} from "@/lib/sandbox-agent-tools"

type SandboxAgentToolsPanelProps = {
  sandboxId: string
  spec: Record<string, unknown>
  toolIds: AgentToolId[]
  running: boolean
  onResourceChange: (resource: Resource) => void
  onRefresh: () => Promise<void>
}

export function SandboxAgentToolsPanel({
  sandboxId,
  spec,
  toolIds,
  running,
  onResourceChange,
  onRefresh,
}: SandboxAgentToolsPanelProps) {
  const autoCheckAttempted = useRef(false)
  const [submitting, setSubmitting] = useState<"check" | "update" | null>(null)
  const states = useMemo(() => sandboxAgentToolStates(spec), [spec])
  const operation = useMemo(() => sandboxAgentToolOperation(spec), [spec])
  const isBoxLite = spec.driver === "boxlite"
  const stateByTool = useMemo(
    () => new Map(states.map((state) => [state.tool, state])),
    [states]
  )
  const active =
    operation?.status === "queued" || operation?.status === "running"
  const updateableTools = toolIds.filter((tool) =>
    agentToolNeedsUpdate(stateByTool.get(tool))
  )
  const upToDateCount = toolIds.filter((tool) => {
    const state = stateByTool.get(tool)
    return (
      !!state?.currentVersion &&
      !!state.latestVersion &&
      !agentToolNeedsUpdate(state)
    )
  }).length

  const runAction = useCallback(
    async (action: "check" | "update", tools: AgentToolId[]) => {
      if (tools.length === 0) return
      setSubmitting(action)
      try {
        const result = resourceResponseSchema.parse(
          await requestJson<unknown>(
            `/api/sandboxes/${encodeURIComponent(sandboxId)}/agent-tools/actions/${action}`,
            {
              method: "POST",
              body: JSON.stringify({ tools }),
            }
          )
        )
        onResourceChange(result.resource)
        toast.success(
          action === "check" ? "检测任务已开始" : "更新任务已开始",
          {
            description: `${tools.length} 个 Agent 工具将在当前沙箱中处理。`,
          }
        )
      } catch (error) {
        toast.error(action === "check" ? "无法开始检测" : "无法开始更新", {
          description: errorMessage(error),
        })
      } finally {
        setSubmitting(null)
      }
    },
    [onResourceChange, sandboxId]
  )

  useEffect(() => {
    if (
      autoCheckAttempted.current ||
      !running ||
      toolIds.length === 0 ||
      states.length > 0 ||
      active
    ) {
      return
    }
    autoCheckAttempted.current = true
    void runAction("check", toolIds)
  }, [active, runAction, running, states.length, toolIds])

  useEffect(() => {
    if (!active) return
    let cancelled = false
    let timer = 0
    const poll = async () => {
      try {
        await onRefresh()
      } finally {
        if (!cancelled) timer = window.setTimeout(poll, 750)
      }
    }
    timer = window.setTimeout(poll, 500)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [active, onRefresh])

  if (toolIds.length === 0) {
    return (
      <Empty className="h-full rounded-none border-0 px-4">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <BotIcon aria-hidden="true" />
          </EmptyMedia>
          <EmptyTitle>未配置 Agent</EmptyTitle>
          <EmptyDescription>
            在沙箱或运行时模板中选择 Agent 工具后，可在这里检测和更新。
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const completedTools = operation?.progress?.agentTools.filter((tool) =>
    ["succeeded", "failed", "cached"].includes(tool.status)
  ).length
  const operationPercent = operation
    ? Math.round(((completedTools ?? 0) / operation.toolIds.length) * 100)
    : 0

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-start gap-3 border-b px-3 py-3">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold">Agent 工具</p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {states.length === 0
              ? "正在读取当前沙箱中的实际版本"
              : `${updateableTools.length} 个可更新 · ${upToDateCount} 个已是最新`}
          </p>
        </div>
        <Button
          variant="outline"
          size="xs"
          disabled={!running || active || submitting !== null}
          onClick={() => void runAction("check", toolIds)}
        >
          {submitting === "check" ? (
            <Spinner data-icon="inline-start" />
          ) : (
            <RefreshCwIcon data-icon="inline-start" aria-hidden="true" />
          )}
          检测
        </Button>
      </div>

      {active && operation ? (
        <div
          className="shrink-0 border-b bg-muted/20 px-3 py-2.5"
          aria-live="polite"
        >
          <div className="mb-2 flex items-center gap-2 text-xs">
            <Spinner className="size-3.5" />
            <span className="min-w-0 flex-1 truncate">
              {operation.message ||
                (operation.action === "check" ? "正在检测" : "正在更新")}
            </span>
            <span className="text-muted-foreground tabular-nums">
              {completedTools ?? 0}/{operation.toolIds.length}
            </span>
          </div>
          <Progress value={operationPercent} aria-label="Agent 工具任务进度" />
        </div>
      ) : null}

      {operation?.status === "failed" ? (
        <div className="shrink-0 border-b p-3">
          <Alert variant="destructive">
            <CircleAlertIcon aria-hidden="true" />
            <AlertTitle>Agent 工具任务未全部完成</AlertTitle>
            <AlertDescription className="line-clamp-3 break-all">
              {operation.message || "请查看失败项后重试。"}
            </AlertDescription>
          </Alert>
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-auto">
        {states.length === 0 && active ? (
          <div className="space-y-0">
            {toolIds.map((tool) => (
              <div key={tool} className="border-b px-3 py-3">
                <div className="mb-2 flex items-center justify-between gap-3">
                  <Skeleton className="h-4 w-28" />
                  <Skeleton className="h-5 w-14" />
                </div>
                <Skeleton className="h-3 w-40" />
              </div>
            ))}
          </div>
        ) : (
          toolIds.map((tool) => (
            <AgentToolRow
              key={tool}
              tool={tool}
              state={stateByTool.get(tool)}
              active={active || submitting !== null}
              running={running}
              progressStatus={
                operation?.progress?.agentTools.find(
                  (progress) => progress.tool === tool
                )?.status
              }
              onUpdate={() => void runAction("update", [tool])}
            />
          ))
        )}
      </div>

      <div className="shrink-0 border-t bg-background px-3 py-3">
        <Button
          className="w-full"
          size="sm"
          disabled={
            !running ||
            active ||
            submitting !== null ||
            updateableTools.length === 0
          }
          onClick={() => void runAction("update", updateableTools)}
        >
          {submitting === "update" ? (
            <Spinner data-icon="inline-start" />
          ) : (
            <DownloadIcon data-icon="inline-start" aria-hidden="true" />
          )}
          更新全部
          {updateableTools.length > 0 ? `（${updateableTools.length}）` : ""}
        </Button>
        <p className="mt-2 text-[11px] leading-4 text-muted-foreground">
          {isBoxLite
            ? "BoxLite 更新会短暂停止 Agent 会话并自动重连；工作目录保留，新进程使用更新后的版本。"
            : "原地更新，不重建沙箱；工作目录和已运行进程保持不变，新进程使用更新后的版本。"}
        </p>
      </div>
    </div>
  )
}

function AgentToolRow({
  tool,
  state,
  active,
  running,
  progressStatus,
  onUpdate,
}: {
  tool: AgentToolId
  state: SandboxAgentToolState | undefined
  active: boolean
  running: boolean
  progressStatus?: string
  onUpdate: () => void
}) {
  const needsUpdate = agentToolNeedsUpdate(state)
  const pending =
    progressStatus &&
    !["succeeded", "failed", "cached"].includes(progressStatus)
  const actionLabel =
    state?.status === "not-installed"
      ? "安装"
      : state?.status === "broken" || state?.status === "failed"
        ? "修复"
        : "更新"
  const badge = toolBadge(state, needsUpdate)

  return (
    <div className="border-b px-3 py-3 last:border-b-0">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p className="truncate text-sm font-medium">
              {agentToolLabel(tool, agentToolOptions)}
            </p>
            <Badge variant={badge.variant}>{badge.label}</Badge>
          </div>
          <div className="mt-1 grid grid-cols-[3.5rem_minmax(0,1fr)] gap-x-2 gap-y-0.5 text-[11px]">
            <span className="text-muted-foreground">当前</span>
            <span className="truncate font-mono" title={state?.currentVersion}>
              {state?.currentVersion || "—"}
            </span>
            <span className="text-muted-foreground">最新</span>
            <span className="truncate font-mono" title={state?.latestVersion}>
              {state?.latestVersion || "未获取"}
            </span>
          </div>
          {state?.message ? (
            <p className="mt-1.5 line-clamp-2 text-[11px] leading-4 text-muted-foreground">
              {state.message}
            </p>
          ) : null}
        </div>
        <Button
          variant="outline"
          size="xs"
          disabled={!running || active || !needsUpdate}
          onClick={onUpdate}
        >
          {pending ? (
            <Spinner data-icon="inline-start" />
          ) : needsUpdate &&
            ["broken", "failed"].includes(state?.status ?? "") ? (
            <WrenchIcon data-icon="inline-start" aria-hidden="true" />
          ) : (
            <DownloadIcon data-icon="inline-start" aria-hidden="true" />
          )}
          {actionLabel}
        </Button>
      </div>
    </div>
  )
}

function toolBadge(
  state: SandboxAgentToolState | undefined,
  needsUpdate: boolean
): {
  label: string
  variant: "default" | "secondary" | "destructive" | "outline"
} {
  if (!state) return { label: "待检测", variant: "outline" }
  if (["broken", "failed"].includes(state.status)) {
    return { label: "需修复", variant: "destructive" }
  }
  if (state.status === "not-installed") {
    return { label: "未安装", variant: "outline" }
  }
  if (needsUpdate) return { label: "可更新", variant: "default" }
  if (!state.latestVersion) return { label: "已安装", variant: "outline" }
  if (state.status === "updated") {
    return { label: "已更新", variant: "secondary" }
  }
  return { label: "最新", variant: "secondary" }
}
