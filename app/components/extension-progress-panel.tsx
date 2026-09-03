"use client"

import { ChevronDownIcon, PackageIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
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
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import { Spinner } from "@/components/ui/spinner"

export type ExtensionProgressItem = {
  id: string
  status: string
  message?: string
  output?: string
  startedAt?: string | null
  finishedAt?: string | null
  durationMs?: number
}

type ExtensionSnapshot = {
  id: string
  name?: string
  version?: string
  spec?: { version?: string }
}

const statusLabels: Record<string, string> = {
  installing: "正在安装",
  verifying: "正在验证",
  succeeded: "安装成功",
  failed: "安装失败",
  cancelling: "正在取消",
  cancelled: "已取消",
}

export function ExtensionProgressPanel({
  progress,
  states,
  snapshots = [],
}: {
  progress?: { extensions?: ExtensionProgressItem[]; status?: string } | null
  states?: ExtensionProgressItem[] | null
  snapshots?: ExtensionSnapshot[]
}) {
  const reported = new Map(
    (progress?.extensions ?? []).map((item) => [item.id, item])
  )
  for (const item of states ?? []) reported.set(item.id, item)
  const names = new Map(snapshots.map((snapshot) => [snapshot.id, snapshot]))
  const ids = [
    ...new Set([
      ...snapshots.map((snapshot) => snapshot.id),
      ...reported.keys(),
    ]),
  ]
  if (!ids.length) return null
  const succeeded = [...reported.values()].filter(
    (item) => item.status === "succeeded"
  ).length
  const failed = [...reported.values()].filter(
    (item) => item.status === "failed"
  ).length

  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle>扩展安装结果</CardTitle>
        <CardDescription aria-live="polite">
          {succeeded} 个成功{failed ? ` · ${failed} 个失败` : ""} · 共{" "}
          {ids.length} 个，仅显示 Worker 上报结果。
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ItemGroup className="gap-2">
          {ids.map((id) => {
            const item = reported.get(id)
            const snapshot = names.get(id)
            const version = snapshot?.version ?? snapshot?.spec?.version
            const status =
              progress?.status === "cancelled" &&
              item?.status !== "succeeded" &&
              item?.status !== "failed"
                ? "cancelled"
                : item?.status
            const running =
              status === "installing" ||
              status === "verifying" ||
              status === "cancelling"
            const label = status
              ? (statusLabels[status] ?? status)
              : "等待上报"
            return (
              <Collapsible key={id}>
                <Item variant="outline" size="sm" className="min-w-0">
                  <ItemMedia variant="icon">
                    {running ? <Spinner aria-label={label} /> : <PackageIcon />}
                  </ItemMedia>
                  <ItemContent className="min-w-0">
                    <ItemTitle className="max-w-full">
                      <span className="truncate">{snapshot?.name || id}</span>
                      {version && (
                        <span className="truncate text-muted-foreground">
                          {version}
                        </span>
                      )}
                    </ItemTitle>
                    <ItemDescription className="break-words">
                      {status === "cancelled"
                        ? "本次创建已取消"
                        : item?.message ||
                          (item
                            ? "Worker 已上报当前状态。"
                            : "尚未收到此扩展的安装结果。")}
                    </ItemDescription>
                  </ItemContent>
                  <ItemActions className="ml-auto flex-wrap justify-end">
                    <Badge
                      variant={
                        item?.status === "failed"
                          ? "destructive"
                          : item?.status === "succeeded"
                            ? "secondary"
                            : "outline"
                      }
                    >
                      {label}
                    </Badge>
                    {item?.durationMs !== undefined && item.durationMs > 0 && (
                      <span className="text-xs text-muted-foreground tabular-nums">
                        {(item.durationMs / 1000).toFixed(1)} 秒
                      </span>
                    )}
                    {item?.output && (
                      <CollapsibleTrigger asChild>
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`查看 ${snapshot?.name || id} 安装输出`}
                        >
                          输出
                          <ChevronDownIcon data-icon="inline-end" />
                        </Button>
                      </CollapsibleTrigger>
                    )}
                  </ItemActions>
                </Item>
                {item?.output && (
                  <CollapsibleContent>
                    <pre
                      className="mt-2 max-h-64 overflow-auto rounded-lg border bg-muted p-3 font-mono text-xs break-words whitespace-pre-wrap"
                      aria-label={`${snapshot?.name || id} 安装输出`}
                    >
                      {item.output}
                    </pre>
                  </CollapsibleContent>
                )}
              </Collapsible>
            )
          })}
        </ItemGroup>
      </CardContent>
    </Card>
  )
}
