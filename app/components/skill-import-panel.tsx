"use client"

import { useEffect, useRef, useState } from "react"
import {
  ArrowRightIcon,
  FileIcon,
  FileSearchIcon,
  LinkIcon,
} from "lucide-react"

import { errorMessage, requestJson } from "@/lib/api-client"
import {
  skillImportResponseSchema,
  skillUploadError,
  type ImportedSkill,
} from "@/lib/skill-import"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import { Spinner } from "@/components/ui/spinner"

export function SkillImportPanel({
  mode,
  disabled,
  onBusyChange,
  onImported,
  onInvalidate,
}: {
  mode: "url" | "upload"
  disabled: boolean
  onBusyChange: (busy: boolean) => void
  onImported: (skill: ImportedSkill) => void
  onInvalidate: () => void
}) {
  const [url, setUrl] = useState("")
  const [file, setFile] = useState<File | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loaded, setLoaded] = useState(false)
  const activeRequest = useRef<AbortController | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  useEffect(() => () => activeRequest.current?.abort(), [])

  function invalidate() {
    setError("")
    setLoaded(false)
    onInvalidate()
  }

  async function preview() {
    if (busy || disabled) return
    if (mode === "upload") {
      if (!file) return
      const message = skillUploadError(file)
      if (message) {
        setError(message)
        return
      }
    }
    const controller = new AbortController()
    activeRequest.current = controller
    invalidate()
    setBusy(true)
    onBusyChange(true)
    try {
      const input =
        mode === "url"
          ? { url: url.trim() }
          : { filename: file!.name, content: await fileContent(file!) }
      if (controller.signal.aborted) return
      const result = skillImportResponseSchema.parse(
        await requestJson<unknown>("/api/skills/import-preview", {
          method: "POST",
          body: JSON.stringify(input),
          signal: controller.signal,
        })
      )
      onImported(result.skill)
      setLoaded(true)
    } catch (error) {
      if (!controller.signal.aborted) setError(errorMessage(error))
    } finally {
      if (!controller.signal.aborted) {
        setBusy(false)
        onBusyChange(false)
      }
    }
  }

  return (
    <FieldGroup>
      <Field data-invalid={Boolean(error)}>
        <FieldLabel htmlFor="skill-import-source">
          {mode === "url" ? "来源链接" : "Skill 文件"}
        </FieldLabel>
        {mode === "url" ? (
          <InputGroup>
            <InputGroupAddon>
              <LinkIcon />
            </InputGroupAddon>
            <InputGroupInput
              id="skill-import-source"
              type="url"
              autoFocus
              value={url}
              disabled={busy || disabled}
              aria-invalid={Boolean(error)}
              aria-describedby="skill-import-help"
              placeholder="https://skills.sh/vercel-labs/skills/find-skills"
              onChange={(event) => {
                setUrl(event.target.value)
                invalidate()
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter" && url.trim()) {
                  event.preventDefault()
                  void preview()
                }
              }}
            />
          </InputGroup>
        ) : (
          <>
            <input
              id="skill-import-source"
              ref={fileInput}
              className="sr-only"
              tabIndex={-1}
              type="file"
              accept=".md,.zip"
              disabled={busy || disabled}
              aria-invalid={Boolean(error)}
              aria-describedby="skill-import-help"
              onChange={(event) => {
                const selected = event.target.files?.[0] ?? null
                setFile(selected)
                invalidate()
                if (selected) setError(skillUploadError(selected))
              }}
            />
            <Item variant="muted">
              <ItemMedia variant="icon">
                <FileIcon />
              </ItemMedia>
              <ItemContent className="min-w-0">
                <ItemTitle className="max-w-full">
                  <span className="truncate" title={file?.name}>
                    {file?.name ?? "选择 SKILL.md 或 ZIP"}
                  </span>
                </ItemTitle>
                <ItemDescription>
                  {file
                    ? `${(file.size / 1024).toFixed(1)} KB · ${file.name.toLowerCase().endsWith(".zip") ? "Skill 压缩包" : "Markdown 文档"}`
                    : "ZIP 中可包含脚本、参考文档和资源文件"}
                </ItemDescription>
              </ItemContent>
              <ItemActions>
                <Button
                  id="skill-file-picker"
                  variant="outline"
                  disabled={busy || disabled}
                  onClick={() => fileInput.current?.click()}
                >
                  {file ? "更换文件" : "选择文件"}
                </Button>
              </ItemActions>
            </Item>
          </>
        )}
        <FieldDescription id="skill-import-help">
          {mode === "url"
            ? "支持 skills.sh 的 GitHub 来源详情页、GitHub 文件页面，以及公开 SKILL.md 或 ZIP 直链。"
            : "最大 4 MiB，ZIP 中只放一个 Skill。最多 128 个文件，单个文件最大 1 MiB。"}
        </FieldDescription>
        {error && <FieldError role="alert">{error}</FieldError>}
      </Field>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          disabled={
            busy ||
            disabled ||
            (mode === "url"
              ? !url.trim()
              : !file || Boolean(skillUploadError(file)))
          }
          onClick={() => void preview()}
        >
          {busy ? (
            <Spinner data-icon="inline-start" />
          ) : (
            <FileSearchIcon data-icon="inline-start" />
          )}
          {busy ? "正在读取…" : loaded ? "重新读取" : "读取并预览"}
          {!busy && <ArrowRightIcon data-icon="inline-end" />}
        </Button>
        <p className="text-sm text-muted-foreground" role="status">
          {loaded
            ? "已读取，可重新选择来源或再次预览。"
            : "读取只生成预览，不会保存或执行内容。"}
        </p>
      </div>
    </FieldGroup>
  )
}

function fileContent(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result).split(",", 2)[1])
    reader.onerror = () => reject(new Error("文件读取失败，请重新选择"))
    reader.readAsDataURL(file)
  })
}
