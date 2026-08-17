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

export function BackendUnavailable({
  variant = "unreachable",
}: {
  variant?: "unreachable" | "error"
}) {
  const unreachable = variant === "unreachable"
  return (
    <main className="flex min-h-svh items-center justify-center p-4">
      <Card className="w-full max-w-lg">
        <CardHeader>
          <div className="mb-2 flex size-10 items-center justify-center rounded-xl bg-muted">
            <DatabaseZapIcon />
          </div>
          <CardTitle>
            {unreachable ? "无法连接控制面服务" : "控制面服务暂时不可用"}
          </CardTitle>
          <CardDescription>
            {unreachable
              ? "前端已经就绪，但连不上 AgentBox 控制面服务（server 容器）。"
              : "控制面服务已启动，但返回了错误响应，请检查其运行状态。"}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Alert>
            <TerminalIcon />
            <AlertTitle>排查部署状态</AlertTitle>
            <AlertDescription>
              在部署目录运行{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                docker compose ps
              </code>{" "}
              确认 server 容器在运行；异常时用{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                docker compose logs server
              </code>{" "}
              查看原因，修复后{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                docker compose up -d
              </code>{" "}
              重启服务。
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
