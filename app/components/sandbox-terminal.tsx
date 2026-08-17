"use client"

import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react"
import { FitAddon } from "@xterm/addon-fit"
import { Terminal } from "@xterm/xterm"
import { RefreshCwIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  SandboxSessionClient,
  type SandboxSessionState,
} from "@/lib/sandbox-session"

export type SandboxTerminalHandle = {
  pasteFromClipboard: () => Promise<boolean>
}

export const SandboxTerminal = forwardRef<
  SandboxTerminalHandle,
  {
    session: SandboxSessionClient
  }
>(function SandboxTerminal({ session }, ref) {
  const containerRef = useRef<HTMLDivElement>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const [closedDetail, setClosedDetail] = useState<string | null>(null)

  useImperativeHandle(ref, () => ({
    async pasteFromClipboard() {
      const terminal = terminalRef.current
      if (!terminal) return false
      const text = await navigator.clipboard.readText()
      if (!text) {
        terminal.focus()
        return false
      }
      terminal.paste(text)
      terminal.focus()
      return true
    },
  }))

  useEffect(() => {
    if (!containerRef.current) return

    const terminal = new Terminal({
      convertEol: false,
      cursorBlink: true,
      cursorStyle: "bar",
      fontFamily: "var(--font-mono)",
      fontSize: 13,
      lineHeight: 1.25,
      scrollback: 5000,
      theme: terminalTheme(),
    })
    const fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
    terminal.open(containerRef.current)
    terminalRef.current = terminal
    terminal.writeln("\x1b[90m正在建立 root PTY 会话…\x1b[0m")

    let lastState: SandboxSessionState | null = null
    let hasBeenReady = false
    const unsubscribeOutput = session.onOutput((data) => terminal.write(data))
    const unsubscribeState = session.onState((state, detail) => {
      if (state === lastState && !detail) return
      lastState = state
      if (state === "ready") {
        if (hasBeenReady) terminal.clear()
        hasBeenReady = true
        setClosedDetail(null)
        terminal.focus()
        session.resize(terminal.cols, terminal.rows)
      } else {
        if (state === "disconnected" && detail) setClosedDetail(detail)
        if (detail) {
          terminal.writeln(`\r\n\x1b[90m${detail}\x1b[0m`)
        }
      }
    })
    const dataDisposable = terminal.onData((data) => session.sendInput(data))
    const resizeDisposable = terminal.onResize(({ cols, rows }) =>
      session.resize(cols, rows)
    )
    const fit = () => {
      fitAddon.fit()
      session.resize(terminal.cols, terminal.rows)
    }
    const resizeObserver = new ResizeObserver(fit)
    resizeObserver.observe(containerRef.current)
    window.requestAnimationFrame(fit)

    return () => {
      resizeObserver.disconnect()
      unsubscribeOutput()
      unsubscribeState()
      dataDisposable.dispose()
      resizeDisposable.dispose()
      terminal.dispose()
      terminalRef.current = null
    }
  }, [session])

  return (
    <div
      aria-label="沙箱 root 终端"
      className="relative h-full min-h-0 w-full bg-[#0c0c0c] p-2"
    >
      <div ref={containerRef} className="h-full min-h-0 w-full" />
      {closedDetail ? (
        <div className="absolute inset-2 flex flex-col items-center justify-center gap-3 rounded-sm bg-[#0c0c0c]/85 text-center">
          <p className="text-xs text-[#9ca3af]">{closedDetail}</p>
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setClosedDetail(null)
              session.restart()
            }}
          >
            <RefreshCwIcon data-icon="inline-start" />
            重新连接
          </Button>
        </div>
      ) : null}
    </div>
  )
})

function terminalTheme() {
  return {
    background: "#0c0c0c",
    foreground: "#cccccc",
    cursor: "#ffffff",
    selectionBackground: "#264f78",
  }
}
