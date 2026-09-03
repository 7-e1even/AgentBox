"use client"

import { useCallback, useMemo, useRef, useState, type ReactNode } from "react"
import Link from "next/link"
import { usePolling } from "@/hooks/use-polling"
import { LoadState } from "@/components/load-state"
import {
  ActivityIcon,
  ArrowLeftIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  ClipboardIcon,
  HistoryIcon,
  KeyRoundIcon,
  LoaderCircleIcon,
  PlayIcon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
  WebhookIcon,
  WorkflowIcon,
  XCircleIcon,
} from "lucide-react"

import { CollectionHeader } from "@/components/control-plane-view"
import {
  CollectionContent,
  CollectionCursorPagination,
  CollectionSearch,
  CollectionTable,
  CollectionToolbar,
} from "@/components/collection-list"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
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
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import {
  automationInputSchema,
  automationResponseSchema,
  automationRunsResponseSchema,
  automationSecretResponseSchema,
  automationTriggerResponseSchema,
  automationsResponseSchema,
  type Automation,
  type AutomationAuthMode,
  type AutomationInput,
  type AutomationRun,
  type AutomationRunStatus,
} from "@/lib/automation-schema"
import { appToast as toast } from "@/lib/app-toast"
import { errorMessage, requestJson } from "@/lib/api-client"
import type { ManagedCredential } from "@/lib/credential-schema"
import { reconcileModelBindings } from "@/lib/model-bindings"
import type { Resource, ResourceOfKind } from "@/lib/platform-schema"
import {
  provisioningCacheLabel,
  provisioningDuration,
  provisioningStageLabel,
} from "@/lib/provisioning"
import { shellSingleQuote } from "@/lib/shell-quote"

const noAutomations: Automation[] = []
const noRuns: AutomationRun[] = []

function activeRunsInterval(data: { runs: AutomationRun[] } | undefined) {
  return data?.runs.some((run) =>
    ["evaluating", "queued", "provisioning"].includes(run.status)
  )
    ? 5000
    : false
}

export function AutomationManagement({
  projectId,
  resources,
  credentials,
  canMutate,
  dependenciesReady = true,
}: {
  projectId: string
  resources: Resource[]
  credentials: ManagedCredential[]
  canMutate: boolean
  dependenciesReady?: boolean
}) {
  const templates = useMemo(
    () =>
      resources
        .filter((resource) => resource.kind === "runtime")
        .filter(
          (resource) => resource.projectId === projectId && resource.enabled
        ),
    [projectId, resources]
  )
  const [search, setSearch] = useState("")
  const [editing, setEditing] = useState<Automation | "new" | null>(null)
  const [secret, setSecret] = useState("")
  const [secretLoading, setSecretLoading] = useState(false)
  const [secretError, setSecretError] = useState("")
  const secretRequestId = useRef(0)
  const [deleting, setDeleting] = useState<Automation | null>(null)
  const [testing, setTesting] = useState<Automation | null>(null)

  const fetchData = useCallback(
    async (signal: AbortSignal) => {
      const [automationBody, runBody] = await Promise.all([
        requestJson<unknown>(
          `/api/automations?projectId=${encodeURIComponent(projectId)}`,
          { signal }
        ),
        requestJson<unknown>(
          `/api/automation-runs?projectId=${encodeURIComponent(projectId)}&limit=50`,
          { signal }
        ),
      ])
      return {
        automations:
          automationsResponseSchema.parse(automationBody).automations,
        runs: automationRunsResponseSchema.parse(runBody).runs,
      }
    },
    [projectId]
  )

  const dataPolling = usePolling({
    queryKey: `automations:${projectId}`,
    load: fetchData,
    interval: activeRunsInterval,
  })
  const automations = dataPolling.data?.automations ?? noAutomations
  const runs = dataPolling.data?.runs ?? noRuns
  const loading = dataPolling.data === undefined && !dataPolling.error
  const loadError = dataPolling.error ? errorMessage(dataPolling.error) : ""
  function setAutomations(update: (current: Automation[]) => Automation[]) {
    dataPolling.setData((current) => ({
      automations: update(current?.automations ?? []),
      runs: current?.runs ?? [],
    }))
  }
  function setRuns(update: (current: AutomationRun[]) => AutomationRun[]) {
    dataPolling.setData((current) => ({
      automations: current?.automations ?? [],
      runs: update(current?.runs ?? []),
    }))
  }
  const loadData = async () => {
    await dataPolling.refresh()
  }

  const visibleAutomations = useMemo(() => {
    const normalized = search.trim().toLowerCase()
    if (!normalized) return automations
    return automations.filter((automation) =>
      `${automation.name} ${automation.description}`
        .toLowerCase()
        .includes(normalized)
    )
  }, [automations, search])

  const latestRunByAutomation = useMemo(() => {
    const latest = new Map<string, AutomationRun>()
    for (const run of runs) {
      if (run.automationId && !latest.has(run.automationId)) {
        latest.set(run.automationId, run)
      }
    }
    return latest
  }, [runs])

  function resetSecret() {
    secretRequestId.current += 1
    setSecret("")
    setSecretLoading(false)
    setSecretError("")
  }

  function editAutomation(automation: Automation) {
    resetSecret()
    setEditing(automation)
  }

  async function loadAutomationSecret(automation: Automation) {
    const requestId = secretRequestId.current + 1
    secretRequestId.current = requestId
    setSecret("")
    setSecretError("")
    setSecretLoading(true)
    try {
      const result = automationSecretResponseSchema.parse(
        await requestJson<unknown>(`/api/automations/${automation.id}/secret`, {
          cache: "no-store",
        })
      )
      if (secretRequestId.current !== requestId) return
      setSecret(result.secret)
    } catch (error) {
      if (secretRequestId.current !== requestId) return
      setSecretError(errorMessage(error))
    } finally {
      if (secretRequestId.current === requestId) setSecretLoading(false)
    }
  }

  async function deleteAutomation() {
    if (!deleting) return
    const automation = deleting
    try {
      await requestJson<void>(`/api/automations/${automation.id}`, {
        method: "DELETE",
      })
      setAutomations((current) =>
        current.filter((item) => item.id !== automation.id)
      )
      if (editing !== "new" && editing?.id === automation.id) {
        setEditing(null)
        resetSecret()
      }
      setDeleting(null)
      toast.success("自动化已删除", { description: automation.name })
    } catch (error) {
      toast.error("删除失败", { description: errorMessage(error) })
    }
  }

  async function testAutomation() {
    if (!testing) return
    try {
      const result = automationTriggerResponseSchema.parse(
        await requestJson<unknown>(`/api/automations/${testing.id}/test`, {
          method: "POST",
          body: "{}",
        })
      )
      setRuns((current) => [result.run, ...current])
      setTesting(null)
      toast.success(
        result.run.status === "failed" ? "测试运行已记录" : "测试沙箱已提交",
        {
          description:
            result.run.errorMessage || result.run.sandboxId || result.run.id,
        }
      )
    } catch (error) {
      toast.error("测试触发失败", { description: errorMessage(error) })
    }
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="自动化"
        count={automations.length}
        action={
          editing ? (
            <Button
              variant="outline"
              onClick={() => {
                setEditing(null)
                resetSecret()
              }}
            >
              返回列表
            </Button>
          ) : (
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" asChild>
                <Link href="/automations/runs">
                  <HistoryIcon data-icon="inline-start" />
                  <span className="sr-only sm:not-sr-only">运行记录</span>
                </Link>
              </Button>
              {canMutate && (
                <Button
                  size="sm"
                  disabled={templates.length === 0}
                  onClick={() => {
                    setEditing("new")
                    resetSecret()
                  }}
                >
                  <PlusIcon data-icon="inline-start" />
                  <span className="sr-only sm:not-sr-only">新建自动化</span>
                </Button>
              )}
            </div>
          )
        }
      />
      <CollectionContent className="gap-6">
        {editing ? (
          <AutomationEditor
            key={editing === "new" ? `new:${projectId}` : editing.id}
            projectId={projectId}
            templates={templates}
            credentials={credentials}
            dependenciesReady={dependenciesReady}
            automation={editing === "new" ? null : editing}
            secret={secret}
            secretLoading={secretLoading}
            secretError={secretError}
            onSaved={(automation, nextSecret) => {
              setAutomations((current) => {
                const exists = current.some((item) => item.id === automation.id)
                return exists
                  ? current.map((item) =>
                      item.id === automation.id ? automation : item
                    )
                  : [automation, ...current]
              })
              setEditing(automation)
              setSecret(nextSecret)
              if (nextSecret) {
                setSecretLoading(false)
                setSecretError("")
              }
            }}
            onRotate={(automation, nextSecret) => {
              setAutomations((current) =>
                current.map((item) =>
                  item.id === automation.id ? automation : item
                )
              )
              setEditing(automation)
              setSecret(nextSecret)
              setSecretLoading(false)
              setSecretError("")
            }}
            onLoadSecret={(automation) => void loadAutomationSecret(automation)}
            onTest={setTesting}
            onDelete={setDeleting}
          />
        ) : loading ? (
          <AutomationSkeleton />
        ) : loadError && automations.length === 0 ? (
          <LoadState
            label="自动化"
            error={new Error(loadError)}
            onRetry={loadData}
          />
        ) : (
          <>
            {loadError && (
              <LoadState
                label="自动化"
                error={new Error(loadError)}
                stale
                onRetry={loadData}
              />
            )}
            <CollectionToolbar>
              <CollectionSearch
                value={search}
                placeholder="搜索自动化"
                onChange={(event) => setSearch(event.target.value)}
              />
              <p className="text-sm text-muted-foreground">
                收到 Webhook 后，按模板创建并启动沙箱。
              </p>
            </CollectionToolbar>

            {visibleAutomations.length === 0 ? (
              <Empty className="min-h-72 border">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <WorkflowIcon />
                  </EmptyMedia>
                  <EmptyTitle>
                    {automations.length === 0
                      ? "还没有自动化"
                      : "没有匹配的自动化"}
                  </EmptyTitle>
                  <EmptyDescription>
                    {automations.length === 0
                      ? "选择一个沙箱模板，生成 Webhook 地址即可。"
                      : "调整搜索词后再试。"}
                  </EmptyDescription>
                </EmptyHeader>
                {automations.length === 0 && canMutate && (
                  <EmptyContent>
                    <Button
                      disabled={templates.length === 0}
                      onClick={() => {
                        setEditing("new")
                        resetSecret()
                      }}
                    >
                      <PlusIcon data-icon="inline-start" />
                      创建第一个自动化
                    </Button>
                  </EmptyContent>
                )}
              </Empty>
            ) : (
              <CollectionTable>
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">自动化</TableHead>
                    <TableHead className="hidden lg:table-cell">
                      Webhook 来源
                    </TableHead>
                    <TableHead className="hidden xl:table-cell">
                      沙箱模板
                    </TableHead>
                    <TableHead>最近运行</TableHead>
                    <TableHead className="hidden md:table-cell">状态</TableHead>
                    <TableHead className="pr-4 text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleAutomations.map((automation) => {
                    const latestRun = latestRunByAutomation.get(automation.id)
                    const template = templates.find(
                      (item) => item.id === automation.templateId
                    )
                    return (
                      <TableRow key={automation.id}>
                        <TableCell className="max-w-72 pl-4">
                          <button
                            type="button"
                            className="flex min-w-0 items-center gap-3 text-left"
                            onClick={
                              canMutate
                                ? () => editAutomation(automation)
                                : undefined
                            }
                          >
                            <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                              <WorkflowIcon className="size-4" />
                            </span>
                            <span className="min-w-0">
                              <span className="block truncate font-medium">
                                {automation.name}
                              </span>
                              <span className="block truncate text-xs text-muted-foreground">
                                {automation.description || "Webhook 创建沙箱"}
                              </span>
                            </span>
                          </button>
                        </TableCell>
                        <TableCell className="hidden lg:table-cell">
                          <span className="flex items-center gap-2">
                            <WebhookIcon className="size-4 text-muted-foreground" />
                            {authModeLabel(automation.trigger.authMode)}
                          </span>
                        </TableCell>
                        <TableCell className="hidden xl:table-cell">
                          {template?.name ?? automation.templateId}
                        </TableCell>
                        <TableCell>
                          {latestRun ? (
                            <Link
                              href="/automations/runs"
                              className="flex flex-col items-start gap-0.5"
                            >
                              <RunStatusBadge status={latestRun.status} />
                              <span className="text-xs text-muted-foreground">
                                {formatRelativeTime(latestRun.receivedAt)}
                              </span>
                            </Link>
                          ) : (
                            <span className="text-muted-foreground">
                              尚未运行
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="hidden md:table-cell">
                          <Badge
                            variant={
                              automation.enabled ? "default" : "secondary"
                            }
                          >
                            {automation.enabled ? "已启用" : "已停用"}
                          </Badge>
                        </TableCell>
                        <TableCell className="pr-4 text-right">
                          {canMutate && (
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => editAutomation(automation)}
                            >
                              编辑
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </CollectionTable>
            )}
          </>
        )}
      </CollectionContent>

      <AlertDialog
        open={Boolean(deleting)}
        onOpenChange={(open) => !open && setDeleting(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia>
              <Trash2Icon />
            </AlertDialogMedia>
            <AlertDialogTitle>删除 {deleting?.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              Webhook 地址会立即失效。已经产生的运行历史和沙箱不会被删除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={deleteAutomation}>
              删除自动化
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {testing && (
        <TestAutomationDialog
          key={testing.id}
          automation={testing}
          onOpenChange={(open) => !open && setTesting(null)}
          onConfirm={testAutomation}
        />
      )}
    </section>
  )
}

function AutomationEditor({
  projectId,
  templates,
  credentials,
  automation,
  secret,
  secretLoading,
  secretError,
  onSaved,
  onRotate,
  onLoadSecret,
  onTest,
  onDelete,
  dependenciesReady,
}: {
  projectId: string
  templates: ResourceOfKind<"runtime">[]
  credentials: ManagedCredential[]
  automation: Automation | null
  secret: string
  secretLoading: boolean
  secretError: string
  onSaved: (automation: Automation, secret: string) => void
  onRotate: (automation: Automation, secret: string) => void
  onLoadSecret: (automation: Automation) => void
  onTest: (automation: Automation) => void
  onDelete: (automation: Automation) => void
  dependenciesReady: boolean
}) {
  const [input, setInput] = useState<AutomationInput>(() => {
    if (automation) {
      const parsed = automationInputSchema.parse({
        projectId: automation.projectId,
        name: automation.name,
        description: automation.description,
        enabled: automation.enabled,
        trigger: automation.trigger,
        templateId: automation.templateId,
        modelBindings: automation.modelBindings,
      })
      const template = templates.find((item) => item.id === parsed.templateId)
      return {
        ...parsed,
        modelBindings: reconcileModelBindings(
          resourceStringList(template?.spec.credentialIds),
          credentials,
          {
            ...resourceStringRecord(template?.spec.modelBindings),
            ...parsed.modelBindings,
          }
        ),
      }
    }
    return defaultAutomationInput(
      projectId,
      templates.find((item) => item.spec.driver === "docker") ?? templates[0],
      credentials
    )
  })
  const [saving, setSaving] = useState(false)
  const [rotating, setRotating] = useState(false)
  const [confirmingRotation, setConfirmingRotation] = useState(false)
  const [formError, setFormError] = useState("")

  const selectedTemplate = templates.find(
    (template) => template.id === input.templateId
  )
  const templateCredentialIds = resourceStringList(
    selectedTemplate?.spec.credentialIds
  )

  const webhookURL = automation
    ? `${typeof window === "undefined" ? "" : window.location.origin}/api/webhooks/${automation.endpointId}`
    : ""

  async function save() {
    if (!dependenciesReady) {
      setFormError("模板或凭据尚未加载成功，请重试后保存；当前草稿会保留。")
      return
    }
    const parsed = automationInputSchema.safeParse(input)
    if (!parsed.success) {
      setFormError(parsed.error.issues[0]?.message ?? "请检查自动化配置")
      return
    }
    if (
      templateCredentialIds.some(
        (credentialID) => !parsed.data.modelBindings[credentialID]
      )
    ) {
      setFormError("请为沙箱中的每个模型服务选择具体模型")
      return
    }
    setSaving(true)
    setFormError("")
    try {
      if (automation) {
        const result = automationResponseSchema.parse(
          await requestJson<unknown>(`/api/automations/${automation.id}`, {
            method: "PATCH",
            body: JSON.stringify(parsed.data),
          })
        )
        onSaved(result.automation, secret)
        toast.success("自动化已保存")
      } else {
        const result = automationSecretResponseSchema.parse(
          await requestJson<unknown>("/api/automations", {
            method: "POST",
            body: JSON.stringify(parsed.data),
          })
        )
        onSaved(result.automation, result.secret)
        toast.success("自动化已创建")
      }
    } catch (error) {
      setFormError(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  async function rotateSecret() {
    if (!automation) return
    setRotating(true)
    try {
      const result = automationSecretResponseSchema.parse(
        await requestJson<unknown>(
          `/api/automations/${automation.id}/rotate-secret`,
          { method: "POST" }
        )
      )
      onRotate(result.automation, result.secret)
      toast.success("Webhook 密钥已轮换", {
        description: "旧密钥已经立即失效。",
      })
    } catch (error) {
      toast.error("密钥轮换失败", { description: errorMessage(error) })
    } finally {
      setRotating(false)
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-3 border-b pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-xl font-semibold tracking-tight">
              {automation ? automation.name : "新建自动化"}
            </h2>
            {automation && (
              <Badge variant={automation.enabled ? "default" : "secondary"}>
                {automation.enabled ? "已启用" : "已停用"}
              </Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            收到 Webhook 后，按选定模板创建并启动沙箱。
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {automation && (
            <>
              <Button variant="outline" onClick={() => onTest(automation)}>
                <PlayIcon data-icon="inline-start" />
                测试创建
              </Button>
              <Button variant="outline" onClick={() => onDelete(automation)}>
                <Trash2Icon data-icon="inline-start" />
                删除
              </Button>
            </>
          )}
          <Button
            disabled={saving || !dependenciesReady}
            onClick={() => void save()}
          >
            {saving && (
              <LoaderCircleIcon
                className="animate-spin"
                data-icon="inline-start"
              />
            )}
            {saving ? "正在保存…" : automation ? "保存修改" : "创建自动化"}
          </Button>
        </div>
      </div>

      {formError && (
        <Alert variant="destructive">
          <XCircleIcon />
          <AlertTitle>无法保存自动化</AlertTitle>
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      {templates.length === 0 && (
        <Alert>
          <WorkflowIcon />
          <AlertTitle>需要一个已启用的沙箱模板</AlertTitle>
          <AlertDescription>
            先在沙箱模板中创建并启用模板，再回来配置自动化。
          </AlertDescription>
        </Alert>
      )}

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.7fr)]">
        <Card>
          <CardHeader>
            <CardTitle>自动化配置</CardTitle>
            <CardDescription>
              Webhook 只负责触发；沙箱内容全部沿用模板最新配置。
            </CardDescription>
          </CardHeader>
          <CardContent>
            <FieldGroup>
              <FieldSet>
                <FieldLegend>基本信息</FieldLegend>
                <FieldGroup>
                  <Field
                    data-invalid={Boolean(
                      formError && input.name.trim().length < 2
                    )}
                  >
                    <FieldLabel htmlFor="automation-name">名称</FieldLabel>
                    <Input
                      id="automation-name"
                      value={input.name}
                      aria-invalid={Boolean(
                        formError && input.name.trim().length < 2
                      )}
                      placeholder="例如：PR 预览沙箱"
                      onChange={(event) =>
                        setInput((current) => ({
                          ...current,
                          name: event.target.value,
                        }))
                      }
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="automation-description">
                      简介
                    </FieldLabel>
                    <Textarea
                      id="automation-description"
                      value={input.description}
                      placeholder="说明这个 Webhook 用来启动什么沙箱"
                      onChange={(event) =>
                        setInput((current) => ({
                          ...current,
                          description: event.target.value,
                        }))
                      }
                    />
                  </Field>
                  <Field orientation="horizontal">
                    <Switch
                      id="automation-enabled"
                      checked={input.enabled}
                      onCheckedChange={(enabled) =>
                        setInput((current) => ({ ...current, enabled }))
                      }
                    />
                    <FieldContent>
                      <FieldLabel htmlFor="automation-enabled">
                        启用自动化
                      </FieldLabel>
                      <FieldDescription>
                        停用后 Webhook 不会创建沙箱。
                      </FieldDescription>
                    </FieldContent>
                  </Field>
                </FieldGroup>
              </FieldSet>

              <FieldSet>
                <FieldLegend>Webhook</FieldLegend>
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="automation-auth">
                      Webhook 来源
                    </FieldLabel>
                    <Select
                      value={input.trigger.authMode}
                      onValueChange={(authMode) =>
                        setInput((current) => ({
                          ...current,
                          trigger: {
                            ...current.trigger,
                            authMode: authMode as AutomationAuthMode,
                          },
                        }))
                      }
                    >
                      <SelectTrigger id="automation-auth" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="bearer">通用 Webhook</SelectItem>
                          <SelectItem value="github-sha256">GitHub</SelectItem>
                          <SelectItem value="gitlab-token">GitLab</SelectItem>
                          <SelectSeparator />
                          <SelectItem value="hmac-sha256">
                            AgentBox 签名
                          </SelectItem>
                          <SelectItem value="standard-webhooks">
                            Standard Webhooks
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {authModeDescription(input.trigger.authMode)}
                    </FieldDescription>
                  </Field>
                </FieldGroup>
              </FieldSet>

              <FieldSet>
                <FieldLegend>沙箱模板</FieldLegend>
                <FieldGroup>
                  <Field data-invalid={!input.templateId}>
                    <FieldLabel htmlFor="automation-template">
                      启动模板
                    </FieldLabel>
                    <Select
                      value={input.templateId}
                      onValueChange={(templateId) => {
                        const credentialIDs = resourceStringList(
                          templates.find((item) => item.id === templateId)?.spec
                            .credentialIds
                        )
                        setInput((current) => ({
                          ...current,
                          templateId,
                          modelBindings: reconcileModelBindings(
                            credentialIDs,
                            credentials,
                            {
                              ...current.modelBindings,
                              ...resourceStringRecord(
                                templates.find((item) => item.id === templateId)
                                  ?.spec.modelBindings
                              ),
                            }
                          ),
                        }))
                      }}
                    >
                      <SelectTrigger
                        id="automation-template"
                        className="w-full"
                        aria-invalid={!input.templateId}
                      >
                        <SelectValue placeholder="选择模板" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          {templates.map((template) => (
                            <SelectItem key={template.id} value={template.id}>
                              {template.name}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    {!input.templateId && (
                      <FieldError>请选择沙箱模板。</FieldError>
                    )}
                    <FieldDescription>
                      镜像、资源、网络、Agent 与可用模型服务沿用模板最新配置。
                    </FieldDescription>
                  </Field>
                  {templateCredentialIds.map((credentialID) => {
                    const credential = credentials.find(
                      (item) => item.id === credentialID && item.enabled
                    )
                    const modelID = input.modelBindings[credentialID] ?? ""
                    return (
                      <Field key={credentialID} data-invalid={!modelID}>
                        <FieldLabel
                          htmlFor={`automation-model-${credentialID}`}
                        >
                          {credential?.name ?? credentialID} 模型
                        </FieldLabel>
                        <Select
                          value={modelID}
                          disabled={
                            !credential || credential.models.length === 0
                          }
                          onValueChange={(nextModelID) =>
                            setInput((current) => ({
                              ...current,
                              modelBindings: {
                                ...current.modelBindings,
                                [credentialID]: nextModelID,
                              },
                            }))
                          }
                        >
                          <SelectTrigger
                            id={`automation-model-${credentialID}`}
                            className="w-full"
                            aria-invalid={!modelID}
                          >
                            <SelectValue placeholder="选择具体模型" />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              {credential?.models.map((model) => (
                                <SelectItem key={model.id} value={model.id}>
                                  {model.name}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                        {!modelID && (
                          <FieldError>
                            {credential?.models.length
                              ? "请选择具体模型。"
                              : "该模型服务没有可用模型。"}
                          </FieldError>
                        )}
                      </Field>
                    )
                  })}
                </FieldGroup>
              </FieldSet>
            </FieldGroup>
          </CardContent>
        </Card>

        <div className="flex flex-col gap-6">
          <WebhookConnectionCard
            automation={automation}
            webhookURL={webhookURL}
            secret={secret}
            secretLoading={secretLoading}
            secretError={secretError}
            rotating={rotating}
            onRequestRotate={() => setConfirmingRotation(true)}
            onLoadSecret={() => {
              if (automation) onLoadSecret(automation)
            }}
          />
        </div>
      </div>

      <AlertDialog
        open={confirmingRotation}
        onOpenChange={setConfirmingRotation}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia>
              <KeyRoundIcon />
            </AlertDialogMedia>
            <AlertDialogTitle>轮换 Webhook 密钥？</AlertDialogTitle>
            <AlertDialogDescription>
              旧密钥会立即失效。请确认调用方可以同步更新后再继续。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => void rotateSecret()}>
              确认轮换
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function WebhookConnectionCard({
  automation,
  webhookURL,
  secret,
  secretLoading,
  secretError,
  rotating,
  onRequestRotate,
  onLoadSecret,
}: {
  automation: Automation | null
  webhookURL: string
  secret: string
  secretLoading: boolean
  secretError: string
  rotating: boolean
  onRequestRotate: () => void
  onLoadSecret: () => void
}) {
  if (!automation) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Webhook 接入</CardTitle>
          <CardDescription>创建后生成独立 URL 和密钥。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex min-h-32 items-center justify-center rounded-lg border border-dashed text-sm text-muted-foreground">
            保存配置后显示
          </div>
        </CardContent>
      </Card>
    )
  }

  const sample = webhookSample(automation, webhookURL, secret)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Webhook 接入</CardTitle>
        <CardDescription>
          {authModeLabel(automation.trigger.authMode)}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <CopyField label="Webhook URL" value={webhookURL} />
        {secretLoading ? (
          <Field>
            <FieldLabel>Webhook 密钥</FieldLabel>
            <Skeleton className="h-9 w-full" />
          </Field>
        ) : secretError ? (
          <Alert variant="destructive">
            <XCircleIcon />
            <AlertTitle>无法读取 Webhook 密钥</AlertTitle>
            <AlertDescription className="flex flex-col items-start gap-2">
              <span>{secretError}</span>
              <Button variant="outline" size="sm" onClick={onLoadSecret}>
                重试读取
              </Button>
            </AlertDescription>
          </Alert>
        ) : secret ? (
          <CopyField label="Webhook 密钥" value={secret} copyLabel="复制密钥" />
        ) : (
          <Field>
            <FieldLabel>Webhook 密钥</FieldLabel>
            <FieldDescription>
              当前密钥已配置（{automation.secretLastFour}）。仅在配置调用方时显示完整值。
            </FieldDescription>
            <Button
              variant="outline"
              size="sm"
              className="self-start"
              onClick={onLoadSecret}
            >
              <KeyRoundIcon data-icon="inline-start" />
              显示完整密钥
            </Button>
          </Field>
        )}
        <details className="group overflow-hidden rounded-lg border">
          <summary className="flex cursor-pointer list-none items-center justify-between gap-3 px-3 py-2 text-sm font-medium select-none">
            请求示例
            <ChevronDownIcon className="size-4 text-muted-foreground transition-transform group-open:rotate-180" />
          </summary>
          <div className="relative border-t bg-muted/20">
            <pre className="max-h-52 overflow-auto p-3 pr-12 font-mono text-xs leading-5 whitespace-pre-wrap">
              {sample}
            </pre>
            <Button
              variant="ghost"
              size="icon-sm"
              className="absolute top-1.5 right-1.5"
              aria-label="复制请求示例"
              onClick={() => void copyText(sample, "请求示例已复制")}
            >
              <ClipboardIcon />
            </Button>
          </div>
        </details>
      </CardContent>
      <CardFooter className="justify-end">
        <Button
          variant="outline"
          size="sm"
          disabled={rotating || secretLoading}
          onClick={onRequestRotate}
        >
          {rotating ? (
            <LoaderCircleIcon
              className="animate-spin"
              data-icon="inline-start"
            />
          ) : (
            <KeyRoundIcon data-icon="inline-start" />
          )}
          轮换密钥
        </Button>
      </CardFooter>
    </Card>
  )
}

function CopyField({
  label,
  value,
  copyLabel,
}: {
  label: string
  value: string
  copyLabel?: string
}) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <div className="flex gap-2">
        <Input readOnly value={value} className="font-mono text-xs" />
        <Button
          variant="outline"
          size={copyLabel ? "sm" : "icon"}
          className="shrink-0"
          aria-label={copyLabel ?? `复制${label}`}
          onClick={() => void copyText(value, `${label} 已复制`)}
        >
          <ClipboardIcon data-icon={copyLabel ? "inline-start" : undefined} />
          {copyLabel}
        </Button>
      </div>
    </Field>
  )
}

export function AutomationRunHistory({ projectId }: { projectId: string }) {
  const [search, setSearch] = useState("")
  const [statusFilter, setStatusFilter] = useState<AutomationRunStatus | "all">(
    "all"
  )
  const [cursorStack, setCursorStack] = useState([""])
  const pageSize = 20
  const currentCursor = cursorStack[cursorStack.length - 1]
  const page = cursorStack.length

  const fetchRuns = useCallback(
    async (signal: AbortSignal) => {
      const params = new URLSearchParams({ projectId, limit: String(pageSize) })
      if (statusFilter !== "all") params.set("status", statusFilter)
      if (search.trim()) params.set("search", search.trim())
      if (currentCursor) params.set("cursor", currentCursor)
      return automationRunsResponseSchema.parse(
        await requestJson<unknown>(
          `/api/automation-runs?${params.toString()}`,
          { signal }
        )
      )
    },
    [currentCursor, projectId, search, statusFilter]
  )

  const runsPolling = usePolling({
    queryKey: `runs:${projectId}:${currentCursor}:${statusFilter}:${search}`,
    load: fetchRuns,
    interval: activeRunsInterval,
    initialDelay: search.trim() ? 250 : 0,
  })
  const runs = runsPolling.data?.items ?? []
  const nextCursor = runsPolling.data?.nextCursor ?? ""
  const hasMore = runsPolling.data?.hasMore ?? false
  const loading = runsPolling.data === undefined && !runsPolling.error
  const loadError = runsPolling.error ? errorMessage(runsPolling.error) : ""
  const loadRuns = async () => {
    await runsPolling.refresh()
  }

  const runPagination = (
    <CollectionCursorPagination
      currentPage={page}
      itemCount={runs.length}
      hasPrevious={cursorStack.length > 1}
      hasNext={hasMore && nextCursor !== ""}
      onPrevious={() => {
        setCursorStack((current) => current.slice(0, -1))
      }}
      onNext={() => {
        if (!nextCursor) return
        setCursorStack((current) => [...current, nextCursor])
      }}
    />
  )

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="运行记录"
        count={runs.length}
        action={
          <Button variant="outline" size="sm" asChild>
            <Link href="/automations">
              <ArrowLeftIcon data-icon="inline-start" />
              <span className="sr-only sm:not-sr-only">返回自动化</span>
            </Link>
          </Button>
        }
      />
      <CollectionContent>
        <CollectionToolbar>
          <CollectionSearch
            value={search}
            placeholder="搜索自动化、模板或沙箱"
            onChange={(event) => {
              setSearch(event.target.value)
              setCursorStack([""])
            }}
          />
          <div className="flex items-center gap-2">
            <Select
              value={statusFilter}
              onValueChange={(value) => {
                setStatusFilter(value as AutomationRunStatus | "all")
                setCursorStack([""])
              }}
            >
              <SelectTrigger className="w-full sm:w-40" aria-label="按状态筛选">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="all">全部状态</SelectItem>
                  <SelectItem value="evaluating">正在校验</SelectItem>
                  <SelectItem value="queued">等待 Worker</SelectItem>
                  <SelectItem value="provisioning">正在预配</SelectItem>
                  <SelectItem value="succeeded">成功</SelectItem>
                  <SelectItem value="failed">失败</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="icon"
              aria-label="刷新运行记录"
              onClick={() => void loadRuns()}
            >
              <RefreshCwIcon />
            </Button>
          </div>
        </CollectionToolbar>

        {loadError && runs.length > 0 && (
          <LoadState
            label="运行记录"
            error={new Error(loadError)}
            stale
            onRetry={loadRuns}
          />
        )}
        {loading ? (
          <AutomationRunsSkeleton />
        ) : loadError && runs.length === 0 ? (
          <LoadState
            label="运行记录"
            error={new Error(loadError)}
            onRetry={loadRuns}
          />
        ) : runs.length === 0 ? (
          <>
            <Empty className="min-h-72 flex-1 border-0">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <HistoryIcon />
                </EmptyMedia>
                <EmptyTitle>
                  {search.trim() || statusFilter !== "all"
                    ? "没有匹配的运行记录"
                    : "还没有运行记录"}
                </EmptyTitle>
                <EmptyDescription>
                  {search.trim() || statusFilter !== "all"
                    ? "调整搜索词或状态筛选后再试。"
                    : "Webhook 或控制台测试触发后，结果会出现在这里。"}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
            {runPagination}
          </>
        ) : (
          <AutomationRunsTable runs={runs} pagination={runPagination} />
        )}
      </CollectionContent>
    </section>
  )
}

function AutomationRunsTable({
  runs,
  pagination,
}: {
  runs: AutomationRun[]
  pagination?: ReactNode
}) {
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null)
  const selectedRun = runs.find((run) => run.id === selectedRunId) ?? null
  return (
    <>
      <CollectionTable pagination={pagination}>
        <TableHeader>
          <TableRow>
            <TableHead className="pl-4">状态</TableHead>
            <TableHead>自动化</TableHead>
            <TableHead className="hidden lg:table-cell">模板</TableHead>
            <TableHead className="hidden xl:table-cell">关联沙箱</TableHead>
            <TableHead className="hidden md:table-cell">来源</TableHead>
            <TableHead className="hidden sm:table-cell">接收时间</TableHead>
            <TableHead className="hidden pr-4 text-right sm:table-cell">
              详情
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((run) => (
            <TableRow key={run.id}>
              <TableCell className="pl-4">
                <div className="flex max-w-44 flex-col items-start gap-1">
                  <RunStatusBadge status={run.status} />
                  {run.provisioning.message &&
                    ["queued", "provisioning"].includes(run.status) && (
                      <span className="line-clamp-2 text-xs text-muted-foreground">
                        {run.provisioning.message}
                      </span>
                    )}
                </div>
              </TableCell>
              <TableCell className="max-w-0 sm:max-w-80">
                <button
                  type="button"
                  className="flex w-full min-w-0 flex-col gap-0.5 text-left"
                  aria-label={`查看 ${run.automationName} 运行详情`}
                  onClick={() => setSelectedRunId(run.id)}
                >
                  <span className="truncate font-medium">
                    {run.automationName}
                  </span>
                  {run.errorMessage && (
                    <span
                      className="truncate text-xs text-destructive"
                      title={run.errorMessage}
                    >
                      {run.errorMessage}
                    </span>
                  )}
                  <span className="text-xs text-muted-foreground sm:hidden">
                    {formatDateTime(run.receivedAt)}
                  </span>
                </button>
              </TableCell>
              <TableCell className="hidden lg:table-cell">
                {run.templateName || run.templateId}
              </TableCell>
              <TableCell className="hidden max-w-56 truncate font-mono text-xs xl:table-cell">
                {run.sandboxId ?? "—"}
              </TableCell>
              <TableCell className="hidden text-muted-foreground md:table-cell">
                {run.triggerSource === "manual-test" ? "控制台测试" : "Webhook"}
              </TableCell>
              <TableCell className="hidden sm:table-cell">
                {formatDateTime(run.receivedAt)}
              </TableCell>
              <TableCell className="hidden pr-4 text-right sm:table-cell">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setSelectedRunId(run.id)}
                >
                  查看
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </CollectionTable>
      <Sheet
        open={Boolean(selectedRun)}
        onOpenChange={(open) => !open && setSelectedRunId(null)}
      >
        <SheetContent className="overflow-x-hidden overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
          <SheetHeader>
            <SheetTitle className="flex items-center gap-2">
              {selectedRun && <RunStatusBadge status={selectedRun.status} />}
              运行详情
            </SheetTitle>
            <SheetDescription>
              Webhook 接收、沙箱创建与预配阶段的完整结果。
            </SheetDescription>
          </SheetHeader>
          {selectedRun && (
            <div className="flex min-w-0 flex-col gap-5 overflow-x-hidden px-4 pb-6">
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
                <dt className="text-muted-foreground">Run ID</dt>
                <dd className="font-mono text-xs break-all">
                  {selectedRun.id}
                </dd>
                <dt className="text-muted-foreground">来源</dt>
                <dd>
                  {selectedRun.triggerSource === "manual-test"
                    ? "控制台测试"
                    : "Webhook"}
                </dd>
                <dt className="text-muted-foreground">模板</dt>
                <dd>{selectedRun.templateName || selectedRun.templateId}</dd>
                <dt className="text-muted-foreground">沙箱</dt>
                <dd className="font-mono text-xs break-all">
                  {selectedRun.sandboxId ?? "—"}
                </dd>
                <dt className="text-muted-foreground">接收时间</dt>
                <dd>{formatDateTime(selectedRun.receivedAt)}</dd>
                <dt className="text-muted-foreground">开始预配</dt>
                <dd>
                  {selectedRun.startedAt
                    ? formatDateTime(selectedRun.startedAt)
                    : "—"}
                </dd>
                <dt className="text-muted-foreground">完成时间</dt>
                <dd>
                  {selectedRun.finishedAt
                    ? formatDateTime(selectedRun.finishedAt)
                    : "—"}
                </dd>
              </dl>
              {selectedRun.provisioning.stage && (
                <ProvisioningDetails run={selectedRun} />
              )}
              {selectedRun.errorMessage && (
                <Alert variant="destructive">
                  <XCircleIcon />
                  <AlertTitle>{selectedRun.errorCode || "创建失败"}</AlertTitle>
                  <AlertDescription className="min-w-0">
                    <div className="flex min-w-0 flex-col gap-2">
                      <p className="break-all whitespace-pre-wrap">
                        {selectedRun.errorMessage}
                      </p>
                      {(selectedRun.errorStage ||
                        selectedRun.errorRetryable) && (
                        <div className="flex flex-wrap gap-2">
                          {selectedRun.errorStage && (
                            <Badge variant="outline">
                              阶段 ·{" "}
                              {provisioningStageLabel(selectedRun.errorStage)}
                            </Badge>
                          )}
                          {selectedRun.errorRetryable && (
                            <Badge variant="secondary">可重试</Badge>
                          )}
                        </div>
                      )}
                    </div>
                  </AlertDescription>
                </Alert>
              )}
            </div>
          )}
        </SheetContent>
      </Sheet>
    </>
  )
}

function AutomationRunsSkeleton() {
  return (
    <div className="overflow-hidden rounded-lg border">
      <div className="flex flex-col gap-3 p-4">
        {Array.from({ length: 8 }, (_, index) => (
          <Skeleton key={index} className="h-9 w-full" />
        ))}
      </div>
    </div>
  )
}

function ProvisioningDetails({ run }: { run: AutomationRun }) {
  const progress = run.provisioning
  return (
    <div className="overflow-hidden rounded-lg border">
      <div className="grid gap-3 border-b bg-muted/30 p-4 sm:grid-cols-3">
        <DetailValue
          label="当前阶段"
          value={provisioningStageLabel(progress.stage)}
        />
        <DetailValue
          label="累计耗时"
          value={provisioningDuration(progress.durationMs)}
        />
        <DetailValue
          label="工具缓存"
          value={provisioningCacheLabel(
            progress.cacheStatus,
            progress.cacheReason
          )}
        />
        {progress.message && (
          <div className="min-w-0 sm:col-span-3">
            <p className="text-xs text-muted-foreground">阶段说明</p>
            <p className="mt-1 text-sm break-all whitespace-pre-wrap">
              {progress.message}
            </p>
          </div>
        )}
      </div>
      {progress.timings.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>阶段</TableHead>
              <TableHead className="text-right">耗时</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {progress.timings.map((timing, index) => (
              <TableRow key={`${timing.stage}-${index}`}>
                <TableCell>{provisioningStageLabel(timing.stage)}</TableCell>
                <TableCell className="text-right font-mono text-xs">
                  {provisioningDuration(timing.durationMs)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

function DetailValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="mt-1 truncate text-sm font-medium" title={value}>
        {value}
      </p>
    </div>
  )
}

function TestAutomationDialog({
  automation,
  onOpenChange,
  onConfirm,
}: {
  automation: Automation
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}) {
  return (
    <AlertDialog open onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogMedia>
            <PlayIcon />
          </AlertDialogMedia>
          <AlertDialogTitle>创建一个真实测试沙箱？</AlertDialogTitle>
          <AlertDialogDescription>
            将按“{automation.name}
            ”当前选择的模板创建并启动沙箱，结果会写入运行记录。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>
            创建测试沙箱
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function RunStatusBadge({ status }: { status: AutomationRunStatus }) {
  const config: Record<
    AutomationRunStatus,
    {
      label: string
      variant: "default" | "secondary" | "destructive" | "outline"
      icon: ReactNode
    }
  > = {
    evaluating: {
      label: "正在校验",
      variant: "outline",
      icon: <LoaderCircleIcon className="animate-spin" />,
    },
    queued: {
      label: "等待 Worker",
      variant: "secondary",
      icon: <ActivityIcon />,
    },
    provisioning: {
      label: "正在预配",
      variant: "secondary",
      icon: <LoaderCircleIcon className="animate-spin" />,
    },
    succeeded: {
      label: "成功",
      variant: "default",
      icon: <CheckCircle2Icon />,
    },
    failed: {
      label: "失败",
      variant: "destructive",
      icon: <XCircleIcon />,
    },
  }
  const item = config[status]
  return (
    <Badge variant={item.variant}>
      {item.icon}
      {item.label}
    </Badge>
  )
}

function AutomationSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <Skeleton className="h-8 w-full max-w-sm" />
        <Skeleton className="h-5 w-52" />
      </div>
      <Card>
        <CardContent className="flex flex-col gap-4">
          {Array.from({ length: 4 }, (_, index) => (
            <div key={index} className="flex items-center gap-3">
              <Skeleton className="size-8 rounded-lg" />
              <Skeleton className="h-8 flex-1" />
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}

function defaultAutomationInput(
  projectId: string,
  template: ResourceOfKind<"runtime"> | undefined,
  credentials: ManagedCredential[]
): AutomationInput {
  return {
    projectId,
    name: "",
    description: "",
    enabled: true,
    trigger: { type: "webhook", authMode: "bearer" },
    templateId: template?.id ?? "",
    modelBindings: reconcileModelBindings(
      resourceStringList(template?.spec.credentialIds),
      credentials,
      resourceStringRecord(template?.spec.modelBindings)
    ),
  }
}

function resourceStringList(value: unknown) {
  if (!Array.isArray(value)) return []
  return value.filter((item): item is string => typeof item === "string")
}

function resourceStringRecord(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {}
  return Object.fromEntries(
    Object.entries(value).filter(
      (entry): entry is [string, string] => typeof entry[1] === "string"
    )
  )
}

function authModeLabel(mode: AutomationAuthMode) {
  return {
    bearer: "通用 Webhook",
    "hmac-sha256": "AgentBox 签名",
    "github-sha256": "GitHub",
    "gitlab-token": "GitLab",
    "standard-webhooks": "Standard Webhooks",
  }[mode]
}

function authModeDescription(mode: AutomationAuthMode) {
  return {
    bearer: "适合 Jenkins、n8n 等通用系统，通过生成的令牌调用。",
    "hmac-sha256": "适合需要请求签名与防重放保护的自建系统。",
    "github-sha256": "使用 GitHub Webhook 的原生签名。",
    "gitlab-token": "使用 GitLab Webhook 的原生令牌。",
    "standard-webhooks": "适合支持 Standard Webhooks 签名规范的系统。",
  }[mode]
}

function webhookSample(
  automation: Automation,
  webhookURL: string,
  secret: string
) {
  const value = secret || "{webhook-secret}"
  const variables = `WEBHOOK_URL=${shellSingleQuote(webhookURL)}
WEBHOOK_SECRET=${shellSingleQuote(value)}`
  if (automation.trigger.authMode === "bearer") {
    return `${variables}
curl -X POST "$WEBHOOK_URL" \\
  -H "Authorization: Bearer $WEBHOOK_SECRET" \\
  -H 'Idempotency-Key: event-123' \\
  -H 'Content-Type: application/json' \\
  -d '{}'`
  }
  if (automation.trigger.authMode === "gitlab-token") {
    return `${variables}
curl -X POST "$WEBHOOK_URL" \\
  -H "X-Gitlab-Token: $WEBHOOK_SECRET" \\
  -H 'X-Gitlab-Event-UUID: event-123' \\
  -H 'Content-Type: application/json' \\
  -d '{}'`
  }
  if (automation.trigger.authMode === "github-sha256") {
    return `${variables}
BODY='{}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -hex | awk '{print $2}')
curl -X POST "$WEBHOOK_URL" \\
  -H "X-Hub-Signature-256: sha256=$SIG" \\
  -H 'X-GitHub-Delivery: event-123' \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`
  }
  if (automation.trigger.authMode === "standard-webhooks") {
    return `${variables}
BODY='{}'
ID='event-123'
TS=$(date +%s)
SIG=$(printf '%s.%s.%s' "$ID" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -binary | openssl base64 -A)
curl -X POST "$WEBHOOK_URL" \\
  -H "webhook-id: $ID" \\
  -H "webhook-timestamp: $TS" \\
  -H "webhook-signature: v1,$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`
  }
  return `${variables}
BODY='{}'
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
curl -X POST "$WEBHOOK_URL" \\
  -H "X-AgentBox-Timestamp: $TS" \\
  -H "X-AgentBox-Signature: v1=$SIG" \\
  -H 'Idempotency-Key: event-123' \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`
}

async function copyText(value: string, message: string) {
  try {
    await navigator.clipboard.writeText(value)
    toast.success(message)
  } catch {
    toast.error("复制失败", { description: "请手动选择并复制。" })
  }
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value))
}

function formatRelativeTime(value: string) {
  const difference = Date.now() - new Date(value).getTime()
  if (difference < 60_000) return "刚刚"
  if (difference < 3_600_000) return `${Math.floor(difference / 60_000)} 分钟前`
  if (difference < 86_400_000)
    return `${Math.floor(difference / 3_600_000)} 小时前`
  return formatDateTime(value)
}
