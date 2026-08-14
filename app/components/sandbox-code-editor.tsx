"use client"

import { useMemo, useRef, type KeyboardEvent, type UIEvent } from "react"

export function SandboxCodeEditor({
  path,
  value,
  onChange,
  onSave,
}: {
  path: string
  value: string
  onChange: (value: string) => void
  onSave: () => void
}) {
  const gutterRef = useRef<HTMLPreElement>(null)
  const lineNumbers = useMemo(
    () =>
      Array.from({ length: value.split("\n").length }, (_, index) => index + 1),
    [value]
  )

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
      event.preventDefault()
      onSave()
      return
    }
    if (event.key !== "Tab") return
    event.preventDefault()
    const editor = event.currentTarget
    const nextValue = `${value.slice(0, editor.selectionStart)}  ${value.slice(editor.selectionEnd)}`
    const nextCaret = editor.selectionStart + 2
    onChange(nextValue)
    window.requestAnimationFrame(() =>
      editor.setSelectionRange(nextCaret, nextCaret)
    )
  }

  function syncGutter(event: UIEvent<HTMLTextAreaElement>) {
    if (gutterRef.current) {
      gutterRef.current.scrollTop = event.currentTarget.scrollTop
    }
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-[auto_minmax(0,1fr)] overflow-hidden bg-background font-mono text-[13px] leading-5">
      <pre
        ref={gutterRef}
        aria-hidden="true"
        className="min-w-12 overflow-hidden border-r bg-muted/30 px-3 py-3 text-right text-muted-foreground select-none"
      >
        {lineNumbers.join("\n")}
      </pre>
      <textarea
        aria-label={`编辑 ${path}`}
        name="sandbox-file-editor"
        autoComplete="off"
        value={value}
        wrap="off"
        spellCheck={false}
        className="h-full min-h-0 w-full resize-none overflow-auto border-0 bg-transparent p-3 font-mono text-[13px] leading-5 whitespace-pre text-foreground outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-inset"
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={handleKeyDown}
        onScroll={syncGutter}
      />
    </div>
  )
}
