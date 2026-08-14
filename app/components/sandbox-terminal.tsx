"use client"

import { useEffect, useRef } from "react"
import { FitAddon } from "@xterm/addon-fit"
import { Terminal } from "@xterm/xterm"

import {
  SandboxSessionClient,
  type SandboxSessionState,
} from "@/lib/sandbox-session"

export function SandboxTerminal({
  session,
}: {
  session: SandboxSessionClient
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const terminalRef = useRef<Terminal | null>(null)

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
    const unsubscribeOutput = session.onOutput((data) => terminal.write(data))
    const unsubscribeState = session.onState((state, detail) => {
      if (state === lastState && !detail) return
      lastState = state
      if (state === "ready") {
        terminal.focus()
        session.resize(terminal.cols, terminal.rows)
      } else if (detail) {
        terminal.writeln(`\r\n\x1b[90m${detail}\x1b[0m`)
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
      ref={containerRef}
      aria-label="沙箱 root 终端"
      className="h-full min-h-0 w-full bg-[#0c0c0c] p-2"
    />
  )
}

function terminalTheme() {
  return {
    background: "#0c0c0c",
    foreground: "#cccccc",
    cursor: "#ffffff",
    selectionBackground: "#264f78",
  }
}
