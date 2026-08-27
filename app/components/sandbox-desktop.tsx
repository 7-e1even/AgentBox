"use client"

import { useEffect, useRef, useState } from "react"
import {
  AlertCircleIcon,
  ExpandIcon,
  KeyboardIcon,
  MonitorIcon,
  RefreshCwIcon,
} from "lucide-react"
import type RFB from "@novnc/novnc"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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

export function SandboxDesktop({
  sandboxId,
  active,
  running,
}: {
  sandboxId: string
  active: boolean
  running: boolean
}) {
  const screenRef = useRef<HTMLDivElement>(null)
  const frameRef = useRef<HTMLDivElement>(null)
  const rfbRef = useRef<RFB | null>(null)
  const [state, setState] = useState<DesktopState>("disconnected")
  const [failure, setFailure] = useState("")
  const [desktopName, setDesktopName] = useState("XFCE")
  const [retry, setRetry] = useState(0)
  const [fullscreen, setFullscreen] = useState(false)

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
      <div className="relative min-h-0 flex-1 overflow-hidden bg-neutral-950">
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
      </div>
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
