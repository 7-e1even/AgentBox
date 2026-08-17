"use client"

import { useState, type FormEvent } from "react"
import { LockKeyholeIcon } from "lucide-react"

import { AgentBoxMark } from "@/components/agentbox-mark"
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
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"

export function LoginForm({ needsSetup }: { needsSetup: boolean }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError("")
    const form = new FormData(event.currentTarget)
    const password = String(form.get("password") ?? "")
    if (needsSetup && password !== String(form.get("confirmPassword") ?? "")) {
      setBusy(false)
      setError("两次输入的密码不一致")
      return
    }
    const payload = needsSetup
      ? {
          name: String(form.get("name") ?? ""),
          username: String(form.get("username") ?? ""),
          email: String(form.get("email") ?? ""),
          password,
        }
      : {
          username: String(form.get("username") ?? ""),
          password,
        }
    try {
      const response = await fetch(
        needsSetup ? "/api/auth/setup" : "/api/auth/login",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        }
      )
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as {
          error?: string
        } | null
        throw new Error(body?.error || "登录失败，请稍后重试")
      }
      window.location.assign("/")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "登录失败，请稍后重试")
      setBusy(false)
    }
  }

  return (
    <main className="grid min-h-svh bg-muted/30 lg:grid-cols-2">
      <section className="hidden flex-col justify-between border-r bg-sidebar p-10 lg:flex">
        <div className="flex items-center gap-3">
          <span className="flex size-10 items-center justify-center rounded-lg border bg-muted text-foreground">
            <AgentBoxMark className="size-6" />
          </span>
          <div>
            <p className="font-semibold">AgentBox</p>
            <p className="text-sm text-muted-foreground">Agent 环境控制台</p>
          </div>
        </div>
        <div className="max-w-lg">
          <p className="text-3xl font-semibold tracking-tight text-balance">
            从统一控制面配置、预配并管理 Agent 环境。
          </p>
          <p className="mt-4 text-sm leading-6 text-muted-foreground">
            统一管理服务器、沙箱模板、模型凭据与隔离沙箱。
          </p>
        </div>
        <p className="text-xs text-muted-foreground">AgentBox Control Plane</p>
      </section>

      <section className="flex items-center justify-center p-4 sm:p-8">
        <Card className="w-full max-w-md">
          <CardHeader>
            <div className="mb-2 flex size-10 items-center justify-center rounded-lg border bg-muted text-foreground lg:hidden">
              <AgentBoxMark className="size-6" />
            </div>
            <CardTitle>{needsSetup ? "创建管理员" : "登录 AgentBox"}</CardTitle>
            <CardDescription>
              {needsSetup
                ? "首次启动需要创建管理员账号。完成后将直接进入控制台。"
                : "使用管理员为你创建的账号进入控制台。"}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form id="login-form" onSubmit={submit}>
              <FieldGroup>
                {needsSetup && (
                  <Field>
                    <FieldLabel htmlFor="name">名称</FieldLabel>
                    <Input
                      id="name"
                      name="name"
                      autoComplete="name"
                      minLength={2}
                      maxLength={80}
                      placeholder="AgentBox 管理员"
                      required
                    />
                  </Field>
                )}
                <Field>
                  <FieldLabel htmlFor="username">用户名</FieldLabel>
                  <Input
                    id="username"
                    name="username"
                    autoComplete="username"
                    autoCapitalize="none"
                    spellCheck={false}
                    minLength={3}
                    maxLength={64}
                    pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,63}"
                    placeholder="admin"
                    required
                  />
                  <FieldDescription>
                    使用字母、数字、点、下划线或短横线。
                  </FieldDescription>
                </Field>
                {needsSetup && (
                  <Field>
                    <FieldLabel htmlFor="email">联系邮箱</FieldLabel>
                    <Input
                      id="email"
                      name="email"
                      type="email"
                      autoComplete="email"
                      placeholder="name@example.com"
                      required
                    />
                    <FieldDescription>
                      仅用于账号资料，不作为登录凭据。
                    </FieldDescription>
                  </Field>
                )}
                <Field>
                  <FieldLabel htmlFor="password">密码</FieldLabel>
                  <Input
                    id="password"
                    name="password"
                    type="password"
                    autoComplete={
                      needsSetup ? "new-password" : "current-password"
                    }
                    minLength={8}
                    maxLength={128}
                    required
                  />
                  <FieldDescription>至少 8 个字符。</FieldDescription>
                </Field>
                {needsSetup && (
                  <Field>
                    <FieldLabel htmlFor="confirmPassword">确认密码</FieldLabel>
                    <Input
                      id="confirmPassword"
                      name="confirmPassword"
                      type="password"
                      autoComplete="new-password"
                      minLength={8}
                      maxLength={128}
                      required
                    />
                  </Field>
                )}
                {error && (
                  <Alert variant="destructive">
                    <LockKeyholeIcon />
                    <AlertTitle>无法继续</AlertTitle>
                    <AlertDescription>{error}</AlertDescription>
                  </Alert>
                )}
              </FieldGroup>
            </form>
          </CardContent>
          <CardFooter className="flex-col gap-3">
            <Button
              form="login-form"
              type="submit"
              className="w-full"
              disabled={busy}
            >
              {busy && <Spinner data-icon="inline-start" />}
              {busy ? "正在处理…" : needsSetup ? "创建并进入控制台" : "登录"}
            </Button>
            <p className="text-center text-xs text-muted-foreground">
              会话保存在仅 HTTP 可读的安全 Cookie 中。
            </p>
          </CardFooter>
        </Card>
      </section>
    </main>
  )
}
