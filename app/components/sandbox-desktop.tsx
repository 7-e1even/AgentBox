"use client"

import { useEffect, useRef, useState, type DragEvent } from "react"
import {
  AlertCircleIcon,
  ExpandIcon,
  FileUpIcon,
  KeyboardIcon,
  MonitorIcon,
  RefreshCwIcon,
} from "lucide-react"
import type RFB from "@novnc/novnc"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { errorMessage, requestJson } from "@/lib/api-client"

type DesktopState = "disconnected" | "connecting" | "ready" | "error"

type DesktopTicket = {
  ticket: string
  expiresAt: string
}

type DesktopUploadProgress = {
  fileName: string
  fileIndex: number
  fileCount: number
  percent: number
}

export function SandboxDesktop({
  sandboxId,
  active,
  running,
  uploadDestination,
  uploadEnabled,
  uploadProgress,
  onUploadFiles,
}: {
  sandboxId: string
  active: boolean
  running: boolean
  uploadDestination: string
  uploadEnabled: boolean
  uploadProgress: DesktopUploadProgress | null
  onUploadFiles: (files: FileList) => void
}) {
  const screenRef = useRef<HTMLDivElement>(null)
  const frameRef = useRef<HTMLDivElement>(null)
  const rfbRef = useRef<RFB | null>(null)
  const uploadInputRef = useRef<HTMLInputElement>(null)
  const dragDepthRef = useRef(0)
  const [state, setState] = useState<DesktopState>("disconnected")
  const [failure, setFailure] = useState("")
  const [desktopName, setDesktopName] = useState("XFCE")
  const [retry, setRetry] = useState(0)
  const [fullscreen, setFullscreen] = useState(false)
  const [draggingFiles, setDraggingFiles] = useState(false)
  const canUpload = uploadEnabled && !uploadProgress

  useEffect(() => {
    const updateFullscreen = () =>
      setFullscreen(document.fullscreenElement === frameRef.current)
    document.addEventListener("fullscreenchange", updateFullscreen)
    return () =>
      document.removeEventListener("fullscreenchange", updateFullscreen)
  }, [])

  useEffect(() => {
    let cancelled = false
    let connection: RFB | null = null

    function disconnect() {
      const current = connection ?? rfbRef.current
      current?.disconnect()
      connection = null
      rfbRef.current = null
      screenRef.current?.replaceChildren()
    }

    async function connect() {
      if (!active || !running || !screenRef.current) {
        setState("disconnected")
        return
      }
      setState("connecting")
      setFailure("")
      try {
        const ticket = await requestJson<DesktopTicket>(
          `/api/sandboxes/${encodeURIComponent(sandboxId)}/desktop-ticket`,
          { method: "POST" }
        )
        const noVNC = await import("@novnc/novnc")
        if (cancelled || !screenRef.current) return
        const scheme = window.location.protocol === "https:" ? "wss:" : "ws:"
        const url = `${scheme}//${window.location.host}/api/sandboxes/${encodeURIComponent(sandboxId)}/desktop?ticket=${encodeURIComponent(ticket.ticket)}`
        connection = new noVNC.default(screenRef.current, url, {
          credentials: {},
          shared: true,
        })
        rfbRef.current = connection
        connection.background = "rgb(18, 18, 20)"
        connection.clipViewport = true
        connection.focusOnClick = true
        connection.resizeSession = false
        connection.scaleViewport = true
        connection.showDotCursor = true
        connection.viewOnly = false
        connection.addEventListener("connect", () => {
          if (!cancelled) setState("ready")
        })
        connection.addEventListener("desktopname", (event) => {
          const name = (event as CustomEvent<{ name?: string }>).detail?.name
          if (!cancelled && name) setDesktopName(name)
        })
        connection.addEventListener("securityfailure", (event) => {
          const reason = (event as CustomEvent<{ reason?: string }>).detail
            ?.reason
          if (!cancelled) {
            setFailure(reason || "桌面安全协商失败")
            setState("error")
          }
        })
        connection.addEventListener("disconnect", (event) => {
          if (cancelled) return
          const clean = (event as CustomEvent<{ clean?: boolean }>).detail
            ?.clean
          if (clean) {
            setState("disconnected")
          } else {
            setFailure("桌面连接已中断，请重新连接")
            setState("error")
          }
        })
      } catch (error) {
        if (!cancelled) {
          setFailure(errorMessage(error))
          setState("error")
        }
      }
    }

    void connect()
    return () => {
      cancelled = true
      disconnect()
    }
  }, [active, retry, running, sandboxId])

  async function toggleFullscreen() {
    if (!frameRef.current) return
    if (document.fullscreenElement === frameRef.current) {
      await document.exitFullscreen()
    } else {
      await frameRef.current.requestFullscreen()
    }
  }

  function isFileDrag(event: DragEvent<HTMLElement>) {
    return Array.from(event.dataTransfer.types).includes("Files")
  }

  function markFileDrag(event: DragEvent<HTMLDivElement>) {
    if (!isFileDrag(event)) return
    event.preventDefault()
    event.stopPropagation()
    dragDepthRef.current += 1
    event.dataTransfer.dropEffect = canUpload ? "copy" : "none"
    setDraggingFiles(true)
  }

  function keepFileDrag(event: DragEvent<HTMLDivElement>) {
    if (!isFileDrag(event)) return
    event.preventDefault()
    event.stopPropagation()
    event.dataTransfer.dropEffect = canUpload ? "copy" : "none"
  }

  function clearFileDrag(event: DragEvent<HTMLDivElement>) {
    if (!isFileDrag(event)) return
    event.preventDefault()
    event.stopPropagation()
    dragDepthRef.current = Math.max(0, dragDepthRef.current - 1)
    if (dragDepthRef.current === 0) setDraggingFiles(false)
  }

  function uploadDroppedFiles(event: DragEvent<HTMLDivElement>) {
    if (!isFileDrag(event)) return
    event.preventDefault()
    event.stopPropagation()
    dragDepthRef.current = 0
    setDraggingFiles(false)
    if (canUpload && event.dataTransfer.files.length > 0) {
      onUploadFiles(event.dataTransfer.files)
    }
  }

  function chooseUploadFiles() {
    if (!canUpload || !uploadInputRef.current) return
    uploadInputRef.current.value = ""
    uploadInputRef.current.click()
  }

  return (
    <section
      ref={frameRef}
      className="flex h-full min-h-0 flex-col bg-background"
      aria-label="沙箱图形桌面"
    >
      <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/20 px-2">
        <MonitorIcon aria-hidden="true" className="text-muted-foreground" />
        <span className="min-w-0 truncate text-xs font-medium">
          {desktopName}
        </span>
        <Badge variant="outline" className="gap-1.5">
          <span
            aria-hidden="true"
            className={`size-1.5 rounded-full ${
              state === "ready"
                ? "bg-emerald-500"
                : state === "error"
                  ? "bg-destructive"
                  : "bg-muted-foreground/60"
            }`}
          />
          {desktopStateLabel(state)}
        </Badge>
        <span className="hidden text-[11px] text-muted-foreground sm:inline">
          1440 × 900
        </span>
        <div className="ml-auto flex items-center gap-1">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={!canUpload}
                aria-label="上传文件到工作目录"
                onClick={chooseUploadFiles}
              >
                <FileUpIcon aria-hidden="true" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>上传到 {uploadDestination}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={state !== "ready"}
                aria-label="发送 Ctrl Alt Delete"
                onClick={() => rfbRef.current?.sendCtrlAltDel()}
              >
                <KeyboardIcon aria-hidden="true" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>发送 Ctrl + Alt + Delete</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="重新连接桌面"
                onClick={() => setRetry((value) => value + 1)}
              >
                <RefreshCwIcon aria-hidden="true" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>重新连接</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={fullscreen ? "退出全屏" : "全屏显示"}
                onClick={() => void toggleFullscreen()}
              >
                <ExpandIcon aria-hidden="true" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{fullscreen ? "退出全屏" : "全屏"}</TooltipContent>
          </Tooltip>
        </div>
      </div>
      <div
        data-slot="sandbox-desktop-viewport"
        className="relative min-h-0 flex-1 overflow-hidden bg-neutral-950"
        onDragEnterCapture={markFileDrag}
        onDragOverCapture={keepFileDrag}
        onDragLeaveCapture={clearFileDrag}
        onDropCapture={uploadDroppedFiles}
      >
        <div
          ref={screenRef}
          className="flex size-full items-start justify-center overflow-hidden [&_canvas]:!mx-auto [&_canvas]:!my-0"
        />
        {state === "connecting" ? (
          <div className="absolute inset-0 grid grid-rows-[1fr_auto] gap-3 bg-neutral-950 p-3">
            <Skeleton className="min-h-0 bg-neutral-800" />
            <div className="flex items-center gap-2 text-xs text-neutral-400">
              <Skeleton className="h-2 w-24 bg-neutral-800" />
              正在建立沙箱桌面会话…
            </div>
          </div>
        ) : null}
        {state === "error" ? (
          <div className="absolute inset-0 flex items-center justify-center bg-neutral-950/95 p-6">
            <Alert variant="destructive" className="max-w-lg bg-background">
              <AlertCircleIcon />
              <AlertTitle>无法连接沙箱桌面</AlertTitle>
              <AlertDescription>{failure}</AlertDescription>
              <Button
                variant="outline"
                size="sm"
                className="mt-3 w-fit"
                onClick={() => setRetry((value) => value + 1)}
              >
                <RefreshCwIcon data-icon="inline-start" />
                重新连接
              </Button>
            </Alert>
          </div>
        ) : null}
        {uploadProgress ? (
          <div className="absolute inset-x-3 bottom-3 z-10 border bg-background/95 px-3 py-2 shadow-sm backdrop-blur-sm">
            <div className="mb-1.5 flex min-w-0 items-center gap-2 text-xs">
              <FileUpIcon
                aria-hidden="true"
                className="shrink-0 text-muted-foreground"
              />
              <span className="min-w-0 flex-1 truncate">
                {uploadProgress.fileName}
              </span>
              <span className="shrink-0 text-muted-foreground tabular-nums">
                {uploadProgress.fileIndex}/{uploadProgress.fileCount} ·{" "}
                {uploadProgress.percent}%
              </span>
            </div>
            <Progress
              aria-label={`正在上传 ${uploadProgress.fileName}`}
              value={uploadProgress.percent}
            />
          </div>
        ) : null}
        {draggingFiles ? (
          <div
            role="status"
            aria-live="polite"
            className="absolute inset-0 z-20 flex flex-col items-center justify-center gap-2 border-2 border-dashed border-primary/70 bg-background/90 p-6 text-center backdrop-blur-sm"
          >
            <FileUpIcon aria-hidden="true" className="text-primary" />
            <p className="text-sm font-medium">
              {uploadProgress
                ? "当前文件上传完成后再试"
                : uploadEnabled
                  ? "松开上传到沙箱工作目录"
                  : "文件会话连接后可上传"}
            </p>
            <p className="max-w-full truncate font-mono text-xs text-muted-foreground">
              {uploadDestination}
            </p>
            <p className="text-xs text-muted-foreground">
              支持多文件，单个文件不超过 50 MiB
            </p>
          </div>
        ) : null}
      </div>
      <input
        ref={uploadInputRef}
        type="file"
        multiple
        className="sr-only"
        aria-label="选择要上传到沙箱工作目录的文件"
        onChange={(event) => {
          if (event.currentTarget.files?.length) {
            onUploadFiles(event.currentTarget.files)
          }
        }}
      />
    </section>
  )
}

function desktopStateLabel(state: DesktopState) {
  switch (state) {
    case "connecting":
      return "连接中"
    case "ready":
      return "可操作"
    case "error":
      return "连接失败"
    default:
      return "未连接"
  }
}
