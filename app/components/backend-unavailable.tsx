import Link from "next/link"
import { DatabaseZapIcon, RefreshCwIcon, TerminalIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

export function BackendUnavailable() {
  return (
    <main className="flex min-h-svh items-center justify-center p-4">
      <Card className="w-full max-w-lg">
        <CardHeader>
          <div className="mb-2 flex size-10 items-center justify-center rounded-xl bg-muted">
            <DatabaseZapIcon />
          </div>
          <CardTitle>Go API 尚未连接</CardTitle>
          <CardDescription>
            前端已经就绪，但 Agent 数据只从独立的 Go 服务读取。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Alert>
            <TerminalIcon />
            <AlertTitle>启动控制面后端</AlertTitle>
            <AlertDescription>
              在 server 目录运行{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                go run ./cmd/agentbox
              </code>
              ，然后重新连接。
            </AlertDescription>
          </Alert>
        </CardContent>
        <CardFooter>
          <Button asChild className="w-full">
            <Link href="/">
              <RefreshCwIcon data-icon="inline-start" />
              重新连接
            </Link>
          </Button>
        </CardFooter>
      </Card>
    </main>
  )
}
