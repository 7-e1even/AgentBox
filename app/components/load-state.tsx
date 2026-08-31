"use client"

import { RefreshCwIcon } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { errorMessage } from "@/lib/api-client"

export function LoadState({
  label,
  error,
  stale = false,
  loading = false,
  onRetry,
}: {
  label: string
  error?: unknown
  stale?: boolean
  loading?: boolean
  onRetry: () => unknown
}) {
  if (error)
    return (
      <Alert className="my-2" role="status">
        <AlertTitle>
          {label}
          {stale ? "暂未刷新，正在显示上次数据" : "加载失败"}
        </AlertTitle>
        <AlertDescription>
          <p className="break-words">{errorMessage(error)}</p>
          <Button
            variant="outline"
            size="sm"
            disabled={loading}
            onClick={() => void onRetry()}
          >
            <RefreshCwIcon data-icon="inline-start" />
            重试
          </Button>
        </AlertDescription>
      </Alert>
    )
  return (
    <div
      className="flex flex-col gap-3 p-4"
      role="status"
      aria-label={`正在加载${label}`}
    >
      <p className="text-sm text-muted-foreground">正在加载{label}…</p>
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-24 w-full" />
    </div>
  )
}
