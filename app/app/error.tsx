"use client"

import { useEffect } from "react"
import { RefreshCwIcon, TriangleAlertIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

export default function GlobalError({
  error,
  unstable_retry,
}: {
  error: Error & { digest?: string }
  unstable_retry: () => void
}) {
  useEffect(() => {
    console.error(error)
  }, [error])

  return (
    <main className="flex min-h-svh items-center justify-center p-4">
      <Card className="w-full max-w-lg">
        <CardHeader>
          <div className="mb-2 flex size-10 items-center justify-center rounded-xl bg-muted">
            <TriangleAlertIcon />
          </div>
          <CardTitle>页面出错了</CardTitle>
          <CardDescription>
            渲染过程中发生未预期的错误，重试通常可以恢复。
            {error.digest ? `（错误标识：${error.digest}）` : null}
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <Button className="w-full" onClick={() => unstable_retry()}>
            <RefreshCwIcon data-icon="inline-start" />
            重试
          </Button>
        </CardFooter>
      </Card>
    </main>
  )
}
