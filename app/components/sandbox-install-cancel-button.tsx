"use client"

import type { ComponentProps } from "react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { ResourceOfKind } from "@/lib/platform-schema"
import { sandboxInstallCancellation } from "@/lib/provisioning"

type SandboxInstallCancelProps = {
  sandbox: ResourceOfKind<"sandbox">
  busy: boolean
  onCancel: () => Promise<void>
}

export function SandboxInstallCancelButton({
  sandbox,
  busy,
  onCancel,
}: SandboxInstallCancelProps) {
  const state = sandboxInstallCancellation(sandbox.spec)
  if (state === "hidden") return null
  const cancelling = state === "cancelling"
  const unsupported = state === "unsupported"
  const disabled = busy || cancelling || unsupported

  return (
    <div className="flex min-w-0 flex-col items-end gap-1">
      <SandboxInstallCancelDialog
        sandbox={sandbox}
        busy={busy}
        onCancel={onCancel}
      >
        <AlertDialogTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            disabled={disabled}
            aria-label={`${cancelling ? "正在取消" : "取消安装"} ${sandbox.name}`}
          >
            {(busy || cancelling) && <Spinner data-icon="inline-start" />}
            {cancelling ? "正在取消" : "取消安装"}
          </Button>
        </AlertDialogTrigger>
      </SandboxInstallCancelDialog>
      {unsupported && (
        <p className="text-right text-xs text-muted-foreground">
          Worker 尚未确认取消能力；旧版 Worker 更新后仅对新任务生效。
        </p>
      )}
    </div>
  )
}

export function SandboxInstallCancelDialog({
  sandbox,
  busy,
  onCancel,
  onCloseAutoFocus,
  children,
  ...props
}: SandboxInstallCancelProps &
  Pick<
    ComponentProps<typeof AlertDialog>,
    "open" | "onOpenChange" | "children"
  > &
  Pick<ComponentProps<typeof AlertDialogContent>, "onCloseAutoFocus">) {
  const disabled =
    busy || sandboxInstallCancellation(sandbox.spec) !== "available"

  return (
    <AlertDialog {...props}>
      {children}
      <AlertDialogContent onCloseAutoFocus={onCloseAutoFocus}>
        <AlertDialogHeader>
          <AlertDialogTitle>取消 {sandbox.name} 的安装？</AlertDialogTitle>
          <AlertDialogDescription>
            这会中止本次沙箱创建，停止安装进程并清理本次创建的临时资源。
            沙箱记录和已上报的安装进度会保留；取消后需要重新创建沙箱。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>继续安装</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={disabled}
            onClick={() => void onCancel()}
          >
            确认取消安装
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
