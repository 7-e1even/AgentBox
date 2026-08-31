"use client"

import { useEffect, useRef, useState } from "react"
import {
  ArrowRightIcon,
  CheckIcon,
  ExternalLinkIcon,
  SearchIcon,
} from "lucide-react"

import { errorMessage, requestJson } from "@/lib/api-client"
import type { Resource } from "@/lib/platform-schema"
import {
  skillCatalogId,
  skillImportResponseSchema,
  skillSearchResponseSchema,
  type ImportedSkill,
  type SkillSearchResult,
} from "@/lib/skill-import"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle,
} from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"

const installCount = new Intl.NumberFormat("zh-CN", {
  notation: "compact",
  maximumFractionDigits: 1,
})

export function SkillSearchPanel({
  disabled,
  resources,
  onBusyChange,
  onImported,
}: {
  disabled: boolean
  resources: Resource[]
  onBusyChange: (busy: boolean) => void
  onImported: (skill: ImportedSkill) => void
}) {
  const [query, setQuery] = useState("")
  const [result, setResult] = useState<SkillSearchResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [selectedId, setSelectedId] = useState("")
  const searchRequest = useRef<AbortController | null>(null)
  const previewRequest = useRef<AbortController | null>(null)
  const errorRef = useRef<HTMLDivElement>(null)
  const selecting = Boolean(selectedId)
  const installed = new Set(
    resources
      .filter((resource) => resource.kind === "skill")
      .map((resource) => skillCatalogId(String(resource.spec.path ?? "")))
      .filter(Boolean)
  )

  useEffect(
    () => () => {
      searchRequest.current?.abort()
      previewRequest.current?.abort()
    },
    []
  )

  function changeQuery(value: string) {
    searchRequest.current?.abort()
    setQuery(value)
    setResult(null)
    setLoading(false)
    setError("")
  }

  async function search(value = query) {
    if (disabled || selecting || value.trim().length < 2) return
    searchRequest.current?.abort()
    const controller = new AbortController()
    searchRequest.current = controller
    setError("")
    setResult(null)
    setLoading(true)
    try {
      const response = await requestJson<unknown>(
        `/api/skills/search?q=${encodeURIComponent(value.trim())}`,
        { signal: controller.signal }
      )
      if (controller.signal.aborted) return
      const parsed = skillSearchResponseSchema.safeParse(response)
      if (!parsed.success) throw new Error("无法读取搜索结果，请稍后重试")
      setResult(parsed.data)
    } catch (error) {
      if (!controller.signal.aborted) {
        setError(errorMessage(error))
        requestAnimationFrame(() =>
          errorRef.current?.scrollIntoView({ block: "nearest" })
        )
      }
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }

  async function selectSkill(skill: SkillSearchResult["skills"][number]) {
    if (disabled || selecting) return
    const controller = new AbortController()
    previewRequest.current = controller
    setSelectedId(skill.id)
    setError("")
    onBusyChange(true)
    try {
      const response = await requestJson<unknown>(
        "/api/skills/import-preview",
        {
          method: "POST",
          body: JSON.stringify({ url: skill.url }),
          signal: controller.signal,
        }
      )
      if (controller.signal.aborted) return
      onImported(skillImportResponseSchema.parse(response).skill)
    } catch (error) {
      if (!controller.signal.aborted) {
        setError(errorMessage(error))
        requestAnimationFrame(() =>
          errorRef.current?.scrollIntoView({ block: "nearest" })
        )
      }
    } finally {
      if (!controller.signal.aborted) {
        setSelectedId("")
        onBusyChange(false)
      }
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="skill-search">搜索 skills.sh</FieldLabel>
          <InputGroup>
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput
              id="skill-search"
              type="search"
              autoFocus
              maxLength={100}
              value={query}
              disabled={disabled || selecting}
              placeholder="名称、技术栈或任务，例如 react、code review"
              aria-describedby="skill-search-help"
              onChange={(event) => changeQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault()
                  void search()
                }
              }}
            />
            <InputGroupAddon align="inline-end">
              <InputGroupButton
                variant="secondary"
                disabled={
                  disabled || loading || selecting || query.trim().length < 2
                }
                onClick={() => void search()}
              >
                {loading && <Spinner data-icon="inline-start" />}
                搜索
              </InputGroupButton>
            </InputGroupAddon>
          </InputGroup>
          <FieldDescription id="skill-search-help">
            搜索公开目录中的 GitHub 来源，选择后预览正文与附件。
          </FieldDescription>
        </Field>
      </FieldGroup>

      {error && (
        <Alert ref={errorRef} variant="destructive">
          <AlertDescription role="alert">{error}</AlertDescription>
        </Alert>
      )}

      {loading ? (
        <div
          role="status"
          aria-label="正在搜索 skills.sh"
          className="flex flex-col gap-4 py-2"
        >
          {[0, 1, 2, 3].map((index) => (
            <div key={index} className="flex items-center gap-4">
              <div className="flex flex-1 flex-col gap-2">
                <Skeleton className="h-4 w-3/5" />
                <Skeleton className="h-3 w-2/5" />
              </div>
              <Skeleton className="h-8 w-16" />
            </div>
          ))}
        </div>
      ) : result?.skills.length ? (
        <div className="flex flex-col gap-2">
          <div
            className="flex items-center justify-between gap-3 text-xs text-muted-foreground"
            role="status"
          >
            <span>
              展示 {result.skills.length} 个结果
              {result.skills.length === 20 ? " · 最多 20 个" : ""}
            </span>
            <span>安装量来自 skills.sh</span>
          </div>
          <ItemGroup className="gap-1">
            {result.skills.map((skill) => {
              const alreadyInstalled = installed.has(skillCatalogId(skill.url))
              const pending = selectedId === skill.id
              return (
                <Item
                  key={skill.id}
                  role="listitem"
                  variant="outline"
                  className="min-w-0"
                >
                  <ItemContent className="min-w-0">
                    <ItemTitle className="max-w-full">
                      <span className="truncate" title={skill.name}>
                        {skill.name}
                      </span>
                    </ItemTitle>
                    <ItemDescription>
                      <span className="break-all">{skill.source}</span>
                      <span
                        className="ml-2 tabular-nums"
                        title={`${skill.installs.toLocaleString("zh-CN")} 次安装`}
                      >
                        {installCount.format(skill.installs)} 次安装
                      </span>
                    </ItemDescription>
                  </ItemContent>
                  <ItemActions>
                    <Button asChild variant="ghost" size="icon-sm">
                      <a
                        href={skill.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        aria-label={`在 skills.sh 查看 ${skill.id}`}
                      >
                        <ExternalLinkIcon />
                      </a>
                    </Button>
                    <Button
                      size="sm"
                      variant={alreadyInstalled ? "secondary" : "outline"}
                      disabled={disabled || selecting || alreadyInstalled}
                      aria-label={
                        alreadyInstalled
                          ? `已导入 ${skill.id}`
                          : `选择 ${skill.id}`
                      }
                      onClick={() => void selectSkill(skill)}
                    >
                      {pending ? (
                        <Spinner data-icon="inline-start" />
                      ) : alreadyInstalled ? (
                        <CheckIcon data-icon="inline-start" />
                      ) : null}
                      {pending
                        ? "读取中"
                        : alreadyInstalled
                          ? "已导入"
                          : "选择"}
                      {!pending && !alreadyInstalled && (
                        <ArrowRightIcon data-icon="inline-end" />
                      )}
                    </Button>
                  </ItemActions>
                </Item>
              )
            })}
          </ItemGroup>
          {result.excluded > 0 && (
            <p className="text-xs text-muted-foreground">
              另有 {result.excluded} 个非 GitHub 或不可直接导入的结果未展示。
            </p>
          )}
        </div>
      ) : (
        <Empty className="min-h-56">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <SearchIcon />
            </EmptyMedia>
            <EmptyTitle>
              {error
                ? "暂时无法加载结果"
                : result
                  ? "没有找到可导入的 Skill"
                  : "搜索你要导入的 Skill"}
            </EmptyTitle>
            <EmptyDescription>
              {error
                ? "可以重试，或切换到链接导入与本地上传。"
                : result?.excluded
                  ? "搜索结果的来源暂不支持直接导入，可以下载后使用本地上传。"
                  : result
                    ? "换一个名称或更具体的英文关键词试试。"
                    : "输入关键词搜索 skills.sh，无需复制链接或执行安装命令。"}
            </EmptyDescription>
          </EmptyHeader>
          {!result && !error && (
            <EmptyContent>
              <div className="flex flex-wrap justify-center gap-2">
                {["react", "frontend design", "code review", "python"].map(
                  (value) => (
                    <Button
                      key={value}
                      size="sm"
                      variant="outline"
                      disabled={disabled || selecting}
                      onClick={() => {
                        changeQuery(value)
                        void search(value)
                      }}
                    >
                      {value}
                    </Button>
                  )
                )}
              </div>
            </EmptyContent>
          )}
          {error && (
            <EmptyContent>
              <Button
                size="sm"
                variant="outline"
                disabled={disabled || selecting || query.trim().length < 2}
                onClick={() => void search()}
              >
                重试搜索
              </Button>
            </EmptyContent>
          )}
        </Empty>
      )}
    </div>
  )
}
