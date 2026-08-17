"use client"

import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"
import {
  ActivityIcon,
  BracesIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  ClipboardIcon,
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
  CollectionSearch,
  CollectionToolbar,
} from "@/components/collection-list"
import { SandboxCodeEditor } from "@/components/sandbox-code-editor"
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
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
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
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
  automationPreviewResponseSchema,
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
import type { Resource, ResourceInput } from "@/lib/platform-schema"

const DEFAULT_INPUT_TEMPLATE = `{
  "name": "{{ .automation.name }}-{{ .run.shortId }}",
  "description": "由自动化 {{ .automation.name }} 创建"
}`

export function AutomationManagement({
  projectId,
  resources,
  credentials,
  canMutate,
}: {
  projectId: string
  resources: Resource[]
  credentials: ManagedCredential[]
  canMutate: boolean
}) {
  const templates = useMemo(
    () =>
      resources.filter(
        (resource) =>
          resource.kind === "runtime" &&
          resource.projectId === projectId &&
          resource.enabled
      ),
    [projectId, resources]
  )
  const [automations, setAutomations] = useState<Automation[]>([])
  const [runs, setRuns] = useState<AutomationRun[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const [search, setSearch] = useState("")
  const [editing, setEditing] = useState<Automation | "new" | null>(null)
  const [secret, setSecret] = useState("")
  const [deleting, setDeleting] = useState<Automation | null>(null)
  const [testing, setTesting] = useState<Automation | null>(null)

  const loadData = useCallback(async () => {
    try {
      const [automationBody, runBody] = await Promise.all([
        requestJson<unknown>(
          `/api/automations?projectId=${encodeURIComponent(projectId)}`
        ),
        requestJson<unknown>(
          `/api/automation-runs?projectId=${encodeURIComponent(projectId)}&limit=50`
        ),
      ])
      setAutomations(
        automationsResponseSchema.parse(automationBody).automations
      )
      setRuns(automationRunsResponseSchema.parse(runBody).runs)
      setLoadError("")
    } catch (error) {
      setLoadError(errorMessage(error))
    } finally {
      setLoading(false)
    }
  }, [projectId])

  useEffect(() => {
    const initial = window.setTimeout(() => void loadData(), 0)
    return () => window.clearTimeout(initial)
  }, [loadData])

  const hasActiveRuns = runs.some(
    (run) => run.status === "queued" || run.status === "provisioning"
  )

  useEffect(() => {
    if (!hasActiveRuns) return
    const timer = window.setInterval(() => void loadData(), 5000)
    return () => window.clearInterval(timer)
  }, [hasActiveRuns, loadData])

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

  async function deleteAutomation() {
    if (!deleting) return
    try {
      await requestJson<void>(`/api/automations/${deleting.id}`, {
        method: "DELETE",
      })
      setAutomations((current) =>
        current.filter((automation) => automation.id !== deleting.id)
      )
      setDeleting(null)
      toast.success("自动化已删除", { description: deleting.name })
    } catch (error) {
      toast.error("删除失败", { description: errorMessage(error) })
    }
  }

  async function testAutomation(payload: unknown) {
    if (!testing) return
    try {
      const result = automationTriggerResponseSchema.parse(
        await requestJson<unknown>(`/api/automations/${testing.id}/test`, {
          method: "POST",
          body: JSON.stringify({ payload }),
        })
      )
      setRuns((current) => [result.run, ...current])
      setTesting(null)
      toast.success(
        result.run.status === "failed" ? "测试事件已记录" : "测试沙箱已提交",
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
                setSecret("")
              }}
            >
              返回列表
            </Button>
          ) : canMutate ? (
            <Button
              disabled={templates.length === 0}
              onClick={() => {
                setEditing("new")
                setSecret("")
              }}
            >
              <PlusIcon data-icon="inline-start" />
              新建自动化
            </Button>
          ) : undefined
        }
      />
      <CollectionContent className="gap-6">
        {editing ? (
          <AutomationEditor
            key={editing === "new" ? `new:${projectId}` : editing.id}
            projectId={projectId}
            templates={templates}
            credentials={credentials}
            automation={editing === "new" ? null : editing}
            secret={secret}
            runs={
              editing === "new"
                ? []
                : runs.filter((run) => run.automationId === editing.id)
            }
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
            }}
            onRotate={(automation, nextSecret) => {
              setAutomations((current) =>
                current.map((item) =>
                  item.id === automation.id ? automation : item
                )
              )
              setEditing(automation)
              setSecret(nextSecret)
            }}
            onTest={(automation) => setTesting(automation)}
            onDelete={(automation) => setDeleting(automation)}
          />
        ) : loading ? (
          <AutomationSkeleton />
        ) : loadError ? (
          <Alert variant="destructive">
            <XCircleIcon />
            <AlertTitle>自动化加载失败</AlertTitle>
            <AlertDescription>{loadError}</AlertDescription>
          </Alert>
        ) : (
          <>
            <CollectionToolbar>
              <CollectionSearch
                value={search}
                placeholder="搜索自动化"
                onChange={(event) => setSearch(event.target.value)}
              />
              <p className="text-sm text-muted-foreground">
                Webhook 事件会复用现有模板与 Worker 创建链路。
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
                      ? "创建一个 Webhook 规则，在外部事件到达时按模板创建沙箱。"
                      : "调整搜索词后再试。"}
                  </EmptyDescription>
                </EmptyHeader>
                {automations.length === 0 && canMutate && (
                  <EmptyContent>
                    <Button
                      disabled={templates.length === 0}
                      onClick={() => setEditing("new")}
                    >
                      <PlusIcon data-icon="inline-start" />
                      创建第一个自动化
                    </Button>
                  </EmptyContent>
                )}
              </Empty>
            ) : (
              <Card className="py-0">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="pl-4">自动化</TableHead>
                      <TableHead>触发器</TableHead>
                      <TableHead>沙箱模板</TableHead>
                      <TableHead>最近运行</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead className="pr-4 text-right">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visibleAutomations.map((automation) => {
                      const latestRun = latestRunByAutomation.get(automation.id)
                      const template = templates.find(
                        (item) => item.id === automation.action.templateId
                      )
                      return (
                        <TableRow key={automation.id}>
                          <TableCell className="max-w-72 pl-4">
                            <button
                              type="button"
                              className="flex min-w-0 items-center gap-3 text-left"
                              onClick={
                                canMutate
                                  ? () => {
                                      setEditing(automation)
                                      setSecret("")
                                    }
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
                          <TableCell>
                            <span className="flex items-center gap-2">
                              <WebhookIcon className="size-4 text-muted-foreground" />
                              {automation.trigger.authMode === "bearer"
                                ? "Bearer"
                                : "HMAC"}
                            </span>
                          </TableCell>
                          <TableCell>
                            {template?.name ?? automation.action.templateId}
                          </TableCell>
                          <TableCell>
                            {latestRun ? (
                              <div className="flex flex-col gap-0.5">
                                <RunStatusBadge status={latestRun.status} />
                                <span className="text-xs text-muted-foreground">
                                  {formatRelativeTime(latestRun.receivedAt)}
                                </span>
                              </div>
                            ) : (
                              <span className="text-muted-foreground">
                                尚未运行
                              </span>
                            )}
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                automation.enabled ? "default" : "secondary"
                              }
                            >
                              {automation.enabled ? "已启用" : "已停用"}
                            </Badge>
                          </TableCell>
                          <TableCell className="pr-4 text-right">
                            {canMutate ? (
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => {
                                  setEditing(automation)
                                  setSecret("")
                                }}
                              >
                                编辑
                              </Button>
                            ) : null}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </Card>
            )}

            <RunHistory runs={runs.slice(0, 12)} />
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
  runs,
  onSaved,
  onRotate,
  onTest,
  onDelete,
}: {
  projectId: string
  templates: Resource[]
  credentials: ManagedCredential[]
  automation: Automation | null
  secret: string
  runs: AutomationRun[]
  onSaved: (automation: Automation, secret: string) => void
  onRotate: (automation: Automation, secret: string) => void
  onTest: (automation: Automation) => void
  onDelete: (automation: Automation) => void
}) {
  const [input, setInput] = useState<AutomationInput>(() =>
    automation
      ? automationInputSchema.parse(automation)
      : defaultAutomationInput(projectId, templates[0]?.id ?? "")
  )
  const [saving, setSaving] = useState(false)
  const [rotating, setRotating] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [previewPayload, setPreviewPayload] = useState("{}")
  const [preview, setPreview] = useState<ResourceInput | null>(null)
  const [previewError, setPreviewError] = useState("")
  const [previewing, setPreviewing] = useState(false)
  const [formError, setFormError] = useState("")

  const selectedTemplate = templates.find(
    (template) => template.id === input.action.templateId
  )
  const templateCredentialIds = resourceStringList(
    selectedTemplate?.spec.credentialIds
  )

  const webhookURL = automation
    ? `${typeof window === "undefined" ? "" : window.location.origin}/api/webhooks/${automation.endpointId}`
    : ""

  async function save() {
    const parsed = automationInputSchema.safeParse(input)
    if (!parsed.success) {
      setFormError(parsed.error.issues[0]?.message ?? "请检查自动化配置")
      return
    }
    if (
      templateCredentialIds.some(
        (credentialID) => !parsed.data.action.modelBindings[credentialID]
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
        onSaved(result.automation, "")
        toast.success("自动化已保存")
      } else {
        const result = automationSecretResponseSchema.parse(
          await requestJson<unknown>("/api/automations", {
            method: "POST",
            body: JSON.stringify(parsed.data),
          })
        )
        onSaved(result.automation, result.secret)
        toast.success("自动化已创建", {
          description: "请立即保存 Webhook 密钥。",
        })
      }
    } catch (error) {
      setFormError(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  async function renderPreview() {
    const parsed = automationInputSchema.safeParse(input)
    if (!parsed.success) {
      setPreviewError(parsed.error.issues[0]?.message ?? "请检查自动化配置")
      return
    }
    let payload: unknown
    try {
      payload = JSON.parse(previewPayload)
    } catch {
      setPreviewError("测试 Payload 不是有效 JSON")
      return
    }
    setPreviewing(true)
    setPreviewError("")
    try {
      const result = automationPreviewResponseSchema.parse(
        await requestJson<unknown>("/api/automations/preview", {
          method: "POST",
          body: JSON.stringify({
            automation: parsed.data,
            payload,
            headers: {},
            query: {},
          }),
        })
      )
      setPreview(result.input)
    } catch (error) {
      setPreview(null)
      setPreviewError(errorMessage(error))
    } finally {
      setPreviewing(false)
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
            Webhook 到达后渲染沙箱输入，并通过既有 Worker 链路创建沙箱。
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {automation && (
            <>
              <Button variant="outline" onClick={() => onTest(automation)}>
                <PlayIcon data-icon="inline-start" />
                发送测试事件
              </Button>
              <Button variant="outline" onClick={() => onDelete(automation)}>
                <Trash2Icon data-icon="inline-start" />
                删除
              </Button>
            </>
          )}
          <Button disabled={saving} onClick={() => void save()}>
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

      {secret && automation && (
        <Alert>
          <KeyRoundIcon />
          <AlertTitle>立即保存新的 Webhook 密钥</AlertTitle>
          <AlertDescription>
            <div className="flex flex-col gap-2">
              <code className="overflow-x-auto rounded-md bg-muted px-2 py-1 font-mono text-xs text-foreground">
                {secret}
              </code>
              <span>完整密钥离开本页后无法再次读取，只能重新轮换。</span>
            </div>
          </AlertDescription>
        </Alert>
      )}

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.7fr)]">
        <Card>
          <CardHeader>
            <CardTitle>规则配置</CardTitle>
            <CardDescription>
              保持触发器和动作简单，复杂输入放在高级表达式中。
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
                      placeholder="例如：PR 预览环境"
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
                      placeholder="说明这个 Webhook 在什么情况下创建沙箱"
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
                        停用后 Webhook 返回 410，不会创建运行记录。
                      </FieldDescription>
                    </FieldContent>
                  </Field>
                </FieldGroup>
              </FieldSet>

              <FieldSet>
                <FieldLegend>Webhook 触发器</FieldLegend>
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="automation-auth">鉴权方式</FieldLabel>
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
                          <SelectItem value="bearer">Bearer Token</SelectItem>
                          <SelectItem value="hmac-sha256">
                            HMAC-SHA256
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {input.trigger.authMode === "bearer"
                        ? "适合通用 Webhook 工具，通过 Authorization 请求头发送。"
                        : "验证时间戳和原始正文签名，可检测篡改与短期重放。"}
                    </FieldDescription>
                  </Field>
                </FieldGroup>
              </FieldSet>

              <FieldSet>
                <FieldLegend>创建沙箱动作</FieldLegend>
                <FieldGroup>
                  <Field data-invalid={!input.action.templateId}>
                    <FieldLabel htmlFor="automation-template">
                      沙箱模板
                    </FieldLabel>
                    <Select
                      value={input.action.templateId}
                      onValueChange={(templateId) =>
                        setInput((current) => {
                          const nextTemplate = templates.find(
                            (template) => template.id === templateId
                          )
                          const credentialIDs = resourceStringList(
                            nextTemplate?.spec.credentialIds
                          )
                          return {
                            ...current,
                            action: {
                              ...current.action,
                              templateId,
                              modelBindings: Object.fromEntries(
                                credentialIDs.flatMap((credentialID) => {
                                  const modelID =
                                    current.action.modelBindings[credentialID]
                                  return modelID
                                    ? [[credentialID, modelID]]
                                    : []
                                })
                              ),
                            },
                          }
                        })
                      }
                    >
                      <SelectTrigger
                        id="automation-template"
                        className="w-full"
                        aria-invalid={!input.action.templateId}
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
                    {!input.action.templateId && (
                      <FieldError>请选择沙箱模板。</FieldError>
                    )}
                    <FieldDescription>
                      触发时读取模板最新配置，再应用高级 JSON Merge Patch。
                    </FieldDescription>
                  </Field>
                  {templateCredentialIds.map((credentialID) => {
                    const credential = credentials.find(
                      (item) => item.id === credentialID && item.enabled
                    )
                    const modelID =
                      input.action.modelBindings[credentialID] ?? ""
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
                              action: {
                                ...current.action,
                                modelBindings: {
                                  ...current.action.modelBindings,
                                  [credentialID]: nextModelID,
                                },
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
            rotating={rotating}
            onRotate={() => void rotateSecret()}
          />
          <GuardrailsCard />
        </div>
      </div>

      <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
        <Card>
          <CardHeader>
            <CardTitle>高级 Spec 表达式</CardTitle>
            <CardDescription>
              使用 Go Template 渲染 JSON Merge
              Patch。身份、项目、模板引用和生命周期字段由平台保留。
            </CardDescription>
            <CardAction>
              <CollapsibleTrigger asChild>
                <Button variant="ghost" size="sm">
                  <BracesIcon data-icon="inline-start" />
                  {advancedOpen ? "收起" : "展开编辑器"}
                  <ChevronDownIcon
                    data-icon="inline-end"
                    className={advancedOpen ? "rotate-180" : undefined}
                  />
                </Button>
              </CollapsibleTrigger>
            </CardAction>
          </CardHeader>
          <CollapsibleContent>
            <CardContent className="flex flex-col gap-4">
              <Alert>
                <BracesIcon />
                <AlertTitle>完整 Spec 能力</AlertTitle>
                <AlertDescription>
                  表达式可以覆盖服务器、镜像、网络、初始化命令、能力与模型绑定；外部
                  Payload 不能直接提交 Spec，渲染结果仍会经过完整沙箱校验。
                </AlertDescription>
              </Alert>
              <div className="grid min-h-[28rem] overflow-hidden rounded-lg border xl:grid-cols-2">
                <div className="min-h-80 border-b xl:border-r xl:border-b-0">
                  <SandboxCodeEditor
                    path="sandbox-input.json.tmpl"
                    value={input.action.inputTemplate}
                    onSave={() => void renderPreview()}
                    onChange={(inputTemplate) =>
                      setInput((current) => ({
                        ...current,
                        action: { ...current.action, inputTemplate },
                      }))
                    }
                  />
                </div>
                <div className="flex min-h-80 flex-col bg-muted/20">
                  <div className="flex h-10 items-center justify-between border-b px-3">
                    <span className="text-xs font-medium text-muted-foreground">
                      渲染预览
                    </span>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={previewing}
                      onClick={() => void renderPreview()}
                    >
                      {previewing ? (
                        <LoaderCircleIcon
                          className="animate-spin"
                          data-icon="inline-start"
                        />
                      ) : (
                        <RefreshCwIcon data-icon="inline-start" />
                      )}
                      验证并预览
                    </Button>
                  </div>
                  {previewError ? (
                    <div className="p-4 text-sm text-destructive">
                      {previewError}
                    </div>
                  ) : preview ? (
                    <pre className="min-h-0 flex-1 overflow-auto p-3 font-mono text-xs leading-5">
                      {JSON.stringify(preview, null, 2)}
                    </pre>
                  ) : (
                    <div className="flex flex-1 items-center justify-center p-6 text-center text-sm text-muted-foreground">
                      填写测试 Payload 后验证，预览最终沙箱输入。
                    </div>
                  )}
                </div>
              </div>
              <Field>
                <FieldLabel htmlFor="automation-preview-payload">
                  测试 Payload
                </FieldLabel>
                <Textarea
                  id="automation-preview-payload"
                  className="min-h-36 font-mono text-xs"
                  value={previewPayload}
                  spellCheck={false}
                  onChange={(event) => setPreviewPayload(event.target.value)}
                />
                <FieldDescription>
                  仅用于本页预览，不会保存到自动化或运行历史。
                </FieldDescription>
              </Field>
            </CardContent>
          </CollapsibleContent>
        </Card>
      </Collapsible>

      {automation && <RunHistory runs={runs} title="这条自动化的运行记录" />}
    </div>
  )
}

function WebhookConnectionCard({
  automation,
  webhookURL,
  secret,
  rotating,
  onRotate,
}: {
  automation: Automation | null
  webhookURL: string
  secret: string
  rotating: boolean
  onRotate: () => void
}) {
  if (!automation) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Webhook 接入</CardTitle>
          <CardDescription>保存自动化后生成独立 URL 和密钥。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex min-h-32 items-center justify-center rounded-lg border border-dashed text-sm text-muted-foreground">
            完成规则配置后创建
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
          {automation.trigger.authMode === "bearer"
            ? "Bearer Token"
            : "HMAC-SHA256"}{" "}
          · 密钥末四位 {automation.secretLastFour}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <CopyField label="Webhook URL" value={webhookURL} />
        <Field>
          <FieldLabel>请求示例</FieldLabel>
          <div className="relative overflow-hidden rounded-lg border bg-muted/20">
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
        </Field>
      </CardContent>
      <CardFooter className="justify-between gap-3">
        <p className="text-xs text-muted-foreground">轮换后旧密钥立即失效。</p>
        <Button
          variant="outline"
          size="sm"
          disabled={rotating}
          onClick={onRotate}
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

function CopyField({ label, value }: { label: string; value: string }) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <div className="flex gap-2">
        <Input readOnly value={value} className="font-mono text-xs" />
        <Button
          variant="outline"
          size="icon"
          aria-label={`复制${label}`}
          onClick={() => void copyText(value, `${label} 已复制`)}
        >
          <ClipboardIcon />
        </Button>
      </div>
    </Field>
  )
}

function GuardrailsCard() {
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>固定保护</CardTitle>
        <CardDescription>无需额外配置，所有 Webhook 自动生效。</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">请求正文</dt>
          <dd>最大 1 MiB</dd>
          <dt className="text-muted-foreground">触发频率</dt>
          <dd>30 次/分钟</dd>
          <dt className="text-muted-foreground">同时预配</dt>
          <dd>最多 5 个</dd>
          <dt className="text-muted-foreground">原始 Payload</dt>
          <dd>不保存</dd>
        </dl>
      </CardContent>
    </Card>
  )
}

function RunHistory({
  runs,
  title = "最近运行",
}: {
  runs: AutomationRun[]
  title?: string
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>Webhook 接收、Worker 预配和最终结果。</CardDescription>
      </CardHeader>
      <CardContent>
        {runs.length === 0 ? (
          <div className="flex min-h-32 items-center justify-center rounded-lg border border-dashed text-sm text-muted-foreground">
            尚无运行记录
          </div>
        ) : (
          <div className="overflow-hidden rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>状态</TableHead>
                  <TableHead>自动化</TableHead>
                  <TableHead>触发来源</TableHead>
                  <TableHead>关联沙箱</TableHead>
                  <TableHead>接收时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {runs.map((run) => (
                  <TableRow key={run.id}>
                    <TableCell>
                      <RunStatusBadge status={run.status} />
                    </TableCell>
                    <TableCell>
                      <div className="flex max-w-64 flex-col gap-0.5">
                        <span className="truncate font-medium">
                          {run.automationName}
                        </span>
                        {run.errorMessage && (
                          <span className="truncate text-xs text-destructive">
                            {run.errorMessage}
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      {run.triggerSource === "manual-test"
                        ? "控制台测试"
                        : "Webhook"}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {run.sandboxId ?? "—"}
                    </TableCell>
                    <TableCell>{formatDateTime(run.receivedAt)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function TestAutomationDialog({
  onOpenChange,
  onConfirm,
}: {
  onOpenChange: (open: boolean) => void
  onConfirm: (payload: unknown) => void
}) {
  const [payload, setPayload] = useState("{}")
  const [error, setError] = useState("")

  function confirm() {
    try {
      onConfirm(JSON.parse(payload))
    } catch {
      setError("测试 Payload 不是有效 JSON")
    }
  }

  return (
    <AlertDialog open onOpenChange={onOpenChange}>
      <AlertDialogContent className="sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogMedia>
            <PlayIcon />
          </AlertDialogMedia>
          <AlertDialogTitle>发送真实测试事件？</AlertDialogTitle>
          <AlertDialogDescription>
            这会执行表达式、提交 Worker
            Job，并创建一个真实沙箱，占用目标服务器资源。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Field data-invalid={Boolean(error)}>
          <FieldLabel htmlFor="automation-test-payload">
            测试 Payload
          </FieldLabel>
          <Textarea
            id="automation-test-payload"
            className="min-h-36 font-mono text-xs"
            value={payload}
            aria-invalid={Boolean(error)}
            spellCheck={false}
            onChange={(event) => {
              setPayload(event.target.value)
              setError("")
            }}
          />
          {error && <FieldError>{error}</FieldError>}
          <FieldDescription>
            Payload 只用于本次测试，不会保存原文。
          </FieldDescription>
        </Field>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction onClick={confirm}>创建测试沙箱</AlertDialogAction>
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
  templateId: string
): AutomationInput {
  return {
    projectId,
    name: "",
    description: "",
    enabled: true,
    trigger: { type: "webhook", authMode: "bearer" },
    action: {
      type: "create-sandbox",
      templateId,
      modelBindings: {},
      inputTemplate: DEFAULT_INPUT_TEMPLATE,
    },
  }
}

function resourceStringList(value: unknown) {
  if (!Array.isArray(value)) return []
  return value.filter((item): item is string => typeof item === "string")
}

function webhookSample(
  automation: Automation,
  webhookURL: string,
  secret: string
) {
  const value = secret || "{webhook-secret}"
  if (automation.trigger.authMode === "bearer") {
    return `curl -X POST '${webhookURL}' \\
  -H 'Authorization: Bearer ${value}' \\
  -H 'Idempotency-Key: event-123' \\
  -H 'Content-Type: application/json' \\
  -d '{}'`
  }
  return `BODY='{}'
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac '${value}' -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
curl -X POST '${webhookURL}' \\
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
