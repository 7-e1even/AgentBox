"use client"

import { useMemo, useState } from "react"
import {
  ActivityIcon,
  CheckIcon,
  ChevronsUpIcon,
  ChevronDownIcon,
  EyeIcon,
  EyeOffIcon,
  LockKeyholeIcon,
  PlusIcon,
  RefreshCwIcon,
  SaveIcon,
  SearchIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react"

import {
  CollectionList,
  CollectionListItem,
  CollectionPagination,
} from "@/components/collection-list"
import { CollectionHeader } from "@/components/control-plane-view"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import type { Provider } from "@/lib/catalog"
import {
  credentialInputSchema,
  type CredentialInput,
  type CredentialModel,
  type ManagedCredential,
} from "@/lib/credential-schema"
import { cn } from "@/lib/utils"

type AccessManagementProps = {
  credentials: ManagedCredential[]
  providers: Provider[]
  onSave: (
    input: CredentialInput,
    editing: ManagedCredential | null
  ) => Promise<ManagedCredential>
  onCheck: (credential: ManagedCredential) => Promise<void>
  onPullModels: (credential: ManagedCredential) => Promise<CredentialModel[]>
  onAddModel: (
    credential: ManagedCredential,
    input: { id: string; name: string }
  ) => Promise<CredentialModel[]>
  onDeleteModel: (
    credential: ManagedCredential,
    model: CredentialModel
  ) => Promise<CredentialModel[]>
  onDelete: (credential: ManagedCredential) => Promise<void>
}

const MODEL_PAGE_SIZE = 10

export function AccessManagement({
  credentials,
  providers,
  onSave,
  onCheck,
  onPullModels,
  onAddModel,
  onDeleteModel,
  onDelete,
}: AccessManagementProps) {
  const [selection, setSelection] = useState<string | "new" | null>(
    credentials[0]?.id ?? null
  )
  const [query, setQuery] = useState("")
  const [deleting, setDeleting] = useState<ManagedCredential | null>(null)
  const [deletingBusy, setDeletingBusy] = useState(false)
  const visibleCredentials = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    if (!normalized) return credentials
    return credentials.filter((credential) => {
      const provider = providers.find(
        (item) => item.id === credential.providerId
      )
      return `${credential.name} ${provider?.name ?? ""}`
        .toLowerCase()
        .includes(normalized)
    })
  }, [credentials, providers, query])
  const selectedCredential =
    selection && selection !== "new"
      ? (credentials.find((item) => item.id === selection) ?? null)
      : null

  async function confirmDelete() {
    if (!deleting) return
    setDeletingBusy(true)
    try {
      await onDelete(deleting)
      if (selection === deleting.id) {
        const next = credentials.find((item) => item.id !== deleting.id)
        setSelection(next?.id ?? null)
      }
      setDeleting(null)
    } finally {
      setDeletingBusy(false)
    }
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="模型服务"
        count={credentials.length}
        action={
          <Button size="sm" onClick={() => setSelection("new")}>
            <PlusIcon data-icon="inline-start" />
            添加服务
          </Button>
        }
      />

      <div className="mx-auto grid min-h-0 w-full max-w-[1600px] flex-1 md:grid-cols-[17rem_minmax(0,1fr)]">
        <aside className="flex min-h-0 flex-col border-b bg-muted/20 md:border-r md:border-b-0">
          <div className="shrink-0 p-3">
            <div className="relative">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索模型平台…"
                className="bg-background pl-9"
              />
            </div>
          </div>

          <ScrollArea className="h-52 px-2 pb-2 md:h-auto md:min-h-0 md:flex-1">
            <ItemGroup className="gap-1">
              {selection === "new" && (
                <ServiceListItem
                  selected
                  mark="+"
                  name="新模型服务"
                  detail="尚未保存"
                />
              )}
              {visibleCredentials.map((credential) => {
                const provider = providers.find(
                  (item) => item.id === credential.providerId
                )
                const status = credentialStatus(credential)
                return (
                  <ServiceListItem
                    key={credential.id}
                    selected={selection === credential.id}
                    mark={
                      provider?.mark ??
                      credential.providerId.slice(0, 2).toUpperCase()
                    }
                    name={credential.name}
                    detail={provider?.name ?? credential.providerId}
                    status={status}
                    onClick={() => setSelection(credential.id)}
                  />
                )
              })}
              {visibleCredentials.length === 0 && selection !== "new" && (
                <p className="px-3 py-8 text-center text-sm text-muted-foreground">
                  {credentials.length === 0 ? "还没有模型服务" : "没有匹配结果"}
                </p>
              )}
            </ItemGroup>
          </ScrollArea>

          <div className="shrink-0 border-t p-3">
            <Button
              variant="outline"
              className="w-full"
              onClick={() => setSelection("new")}
            >
              <PlusIcon data-icon="inline-start" />
              添加服务
            </Button>
          </div>
        </aside>

        <div className="flex min-h-0 overflow-hidden">
          {selection === "new" || selectedCredential ? (
            <CredentialEditor
              key={selection}
              credential={selectedCredential}
              credentials={credentials}
              providers={providers}
              onSave={onSave}
              onCheck={onCheck}
              onPullModels={onPullModels}
              onAddModel={onAddModel}
              onDeleteModel={onDeleteModel}
              onSaved={(credential) => setSelection(credential.id)}
              onDelete={() =>
                selectedCredential && setDeleting(selectedCredential)
              }
            />
          ) : (
            <Empty className="min-h-full border-0">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <LockKeyholeIcon />
                </EmptyMedia>
                <EmptyTitle>添加第一个模型服务</EmptyTitle>
                <EmptyDescription>
                  保存 API 连接后，再获取或手动添加这个服务可用的模型。
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button size="sm" onClick={() => setSelection("new")}>
                  <PlusIcon data-icon="inline-start" />
                  添加服务
                </Button>
              </EmptyContent>
            </Empty>
          )}
        </div>
      </div>

      <AlertDialog
        open={Boolean(deleting)}
        onOpenChange={(open) => !open && setDeleting(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除 {deleting?.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              密钥和已保存的模型列表会永久删除。仍被智能体或环境模板引用时不能删除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deletingBusy}
              onClick={() => void confirmDelete()}
            >
              {deletingBusy ? "正在删除…" : "永久删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function ServiceListItem({
  selected,
  mark,
  name,
  detail,
  status,
  onClick,
}: {
  selected: boolean
  mark: string
  name: string
  detail: string
  status?: { label: string; dotClassName: string }
  onClick?: () => void
}) {
  return (
    <CollectionListItem
      asChild
      variant={selected ? "muted" : "default"}
      className="rounded-lg border-0 px-3 py-2.5 hover:bg-muted/70"
    >
      <button
        type="button"
        aria-current={selected ? "page" : undefined}
        onClick={onClick}
      >
        <ItemMedia className="relative flex size-9 items-center justify-center rounded-lg bg-background text-xs font-semibold ring-1 ring-foreground/10">
          {mark}
          {status && (
            <span
              className={cn(
                "absolute right-0 bottom-0 size-2.5 rounded-full border-2 border-background",
                status.dotClassName
              )}
              title={status.label}
            />
          )}
        </ItemMedia>
        <ItemContent className="min-w-0">
          <ItemTitle>{name}</ItemTitle>
          <ItemDescription>{detail}</ItemDescription>
        </ItemContent>
      </button>
    </CollectionListItem>
  )
}

function CredentialEditor({
  credential,
  credentials,
  providers,
  onSave,
  onCheck,
  onPullModels,
  onAddModel,
  onDeleteModel,
  onSaved,
  onDelete,
}: Omit<AccessManagementProps, "credentials" | "onDelete"> & {
  credential: ManagedCredential | null
  credentials: ManagedCredential[]
  onSaved: (credential: ManagedCredential) => void
  onDelete: () => void
}) {
  const [input, setInput] = useState<CredentialInput>(() =>
    credential
      ? inputFromCredential(credential)
      : newCredentialInput(providers, credentials)
  )
  const [identityEdited, setIdentityEdited] = useState(Boolean(credential))
  const [slugEdited, setSlugEdited] = useState(Boolean(credential))
  const [showSecret, setShowSecret] = useState(false)
  const [error, setError] = useState("")
  const [saving, setSaving] = useState(false)
  const [checking, setChecking] = useState(false)
  const [pullingModels, setPullingModels] = useState(false)
  const provider = providers.find((item) => item.id === input.providerId)
  const dirty = credential ? isCredentialDirty(input, credential) : true

  function update<K extends keyof CredentialInput>(
    key: K,
    value: CredentialInput[K]
  ) {
    setInput((current) => ({ ...current, [key]: value }))
    setError("")
  }

  function updateProvider(providerId: string) {
    const nextProvider = providers.find((item) => item.id === providerId)
    setInput((current) => ({
      ...current,
      providerId,
      protocol: defaultProtocol(providerId),
      modelId: "",
      ...(!credential && !identityEdited
        ? {
            name: defaultCredentialName(nextProvider),
            id: nextCredentialId(providerId, credentials),
          }
        : {}),
    }))
    setError("")
  }

  function validate() {
    const parsed = credentialInputSchema.safeParse(input)
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "请检查表单")
      return null
    }
    if (!credential && !parsed.data.secret) {
      setError("请填写 API Key")
      return null
    }
    return parsed.data
  }

  async function persist() {
    const parsed = validate()
    if (!parsed) return null
    setSaving(true)
    try {
      const saved = await onSave(parsed, credential)
      setInput(inputFromCredential(saved))
      setShowSecret(false)
      onSaved(saved)
      return saved
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存失败")
      return null
    } finally {
      setSaving(false)
    }
  }

  async function checkConnection() {
    setChecking(true)
    setError("")
    try {
      const target = dirty ? await persist() : credential
      if (target) await onCheck(target)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "连接检测失败")
    } finally {
      setChecking(false)
    }
  }

  async function pullModels() {
    setPullingModels(true)
    setError("")
    try {
      const target = dirty ? await persist() : credential
      if (target) await onPullModels(target)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "获取模型失败")
    } finally {
      setPullingModels(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <div className="flex min-h-20 flex-wrap items-center justify-between gap-4 border-b px-4 py-4 sm:px-6">
        <div className="flex min-w-0 items-center gap-3">
          <span className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-muted text-sm font-semibold">
            {provider?.mark ||
              input.providerId.slice(0, 2).toUpperCase() ||
              "AI"}
          </span>
          <div className="min-w-0">
            <h2 className="truncate font-semibold">
              {credential ? credential.name : "新模型服务"}
            </h2>
            <p className="truncate text-sm text-muted-foreground">
              {credentialStatusText(credential)}
            </p>
          </div>
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-sm">
          <Checkbox
            checked={input.enabled}
            onCheckedChange={(checked) => update("enabled", checked === true)}
          />
          启用
        </label>
      </div>

      <div className="mx-auto flex min-h-0 w-full max-w-5xl flex-1 flex-col gap-6 overflow-y-auto p-4 sm:p-6 md:overflow-hidden lg:p-8">
        <section className="grid shrink-0 gap-4">
          <h3 className="text-sm font-semibold">连接配置</h3>
          <FieldGroup>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel>Agent Provider</FieldLabel>
                <Select value={input.providerId} onValueChange={updateProvider}>
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="选择 Provider" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectLabel>Providers</SelectLabel>
                      {providers.map((item) => (
                        <SelectItem key={item.id} value={item.id}>
                          {item.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel>接口协议</FieldLabel>
                <Select
                  value={input.protocol}
                  onValueChange={(value) =>
                    update("protocol", value as CredentialInput["protocol"])
                  }
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="openai-responses">
                      OpenAI Responses
                    </SelectItem>
                    <SelectItem value="openai-chat">
                      OpenAI Chat Completions
                    </SelectItem>
                    <SelectItem value="anthropic">
                      Anthropic Messages
                    </SelectItem>
                    <SelectItem value="gemini">Gemini API</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>

            <Field>
              <FieldLabel htmlFor="credential-secret">API Key</FieldLabel>
              <div className="flex flex-col gap-2 sm:flex-row">
                <div className="relative min-w-0 flex-1">
                  <Input
                    id="credential-secret"
                    type={showSecret ? "text" : "password"}
                    autoComplete="new-password"
                    value={input.secret}
                    placeholder={
                      credential
                        ? `当前 ${credential.maskedSecret}；输入新值可替换`
                        : "输入 API Key"
                    }
                    className="pr-10 font-mono"
                    onChange={(event) => update("secret", event.target.value)}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    className="absolute top-1/2 right-1 -translate-y-1/2"
                    aria-label={showSecret ? "隐藏 API Key" : "显示 API Key"}
                    onClick={() => setShowSecret((current) => !current)}
                  >
                    {showSecret ? <EyeOffIcon /> : <EyeIcon />}
                  </Button>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  disabled={saving || checking || pullingModels}
                  onClick={() => void checkConnection()}
                >
                  <ActivityIcon data-icon="inline-start" />
                  {checking ? "正在检测…" : "检测连接"}
                </Button>
              </div>
            </Field>

            <Field>
              <FieldLabel htmlFor="credential-endpoint">API 地址</FieldLabel>
              <Input
                id="credential-endpoint"
                value={input.endpoint}
                placeholder={defaultEndpointHint(input.providerId)}
                onChange={(event) => update("endpoint", event.target.value)}
              />
              <FieldDescription>
                留空使用 Provider 官方地址；兼容服务可以填写自定义地址。
              </FieldDescription>
            </Field>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="credential-name">配置名称</FieldLabel>
                <Input
                  id="credential-name"
                  value={input.name}
                  onChange={(event) => {
                    setIdentityEdited(true)
                    update("name", event.target.value)
                    if (!credential && !slugEdited) {
                      update("id", slug(event.target.value))
                    }
                  }}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="credential-id">唯一标识</FieldLabel>
                <Input
                  id="credential-id"
                  value={input.id}
                  disabled={Boolean(credential)}
                  onChange={(event) => {
                    setIdentityEdited(true)
                    setSlugEdited(true)
                    update("id", event.target.value.toLowerCase())
                  }}
                />
              </Field>
            </div>

            {error && <FieldError>{error}</FieldError>}
          </FieldGroup>
        </section>

        <Separator className="shrink-0" />

        <CredentialModels
          credential={credential}
          models={credential?.models ?? []}
          defaultModelId={input.modelId}
          busy={saving || pullingModels}
          pulling={pullingModels}
          onSelect={(modelId) => update("modelId", modelId)}
          onPull={() => void pullModels()}
          onAdd={onAddModel}
          onDelete={onDeleteModel}
          onError={setError}
        />

        <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t pt-5">
          <div>
            {credential && (
              <Button variant="ghost" onClick={onDelete}>
                <Trash2Icon data-icon="inline-start" />
                删除服务
              </Button>
            )}
          </div>
          <div className="flex items-center gap-3">
            {credential && !dirty && (
              <span className="text-xs text-muted-foreground">已保存</span>
            )}
            <Button
              disabled={
                saving ||
                checking ||
                pullingModels ||
                (Boolean(credential) && !dirty)
              }
              onClick={() => void persist()}
            >
              <SaveIcon data-icon="inline-start" />
              {saving ? "正在保存…" : "保存配置"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

function CredentialModels({
  credential,
  models,
  defaultModelId,
  busy,
  pulling,
  onSelect,
  onPull,
  onAdd,
  onDelete,
  onError,
}: {
  credential: ManagedCredential | null
  models: CredentialModel[]
  defaultModelId: string
  busy: boolean
  pulling: boolean
  onSelect: (modelId: string) => void
  onPull: () => void
  onAdd: AccessManagementProps["onAddModel"]
  onDelete: AccessManagementProps["onDeleteModel"]
  onError: (message: string) => void
}) {
  const [query, setQuery] = useState("")
  const [searching, setSearching] = useState(false)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const [adding, setAdding] = useState(false)
  const [deletingId, setDeletingId] = useState("")
  const [page, setPage] = useState(1)
  const normalized = query.trim().toLowerCase()
  const visibleModels = normalized
    ? models.filter((model) =>
        `${model.name} ${model.id} ${model.group}`
          .toLowerCase()
          .includes(normalized)
      )
    : models
  const totalPages = Math.max(
    1,
    Math.ceil(visibleModels.length / MODEL_PAGE_SIZE)
  )
  const currentPage = Math.min(page, totalPages)
  const currentModels = visibleModels.slice(
    (currentPage - 1) * MODEL_PAGE_SIZE,
    currentPage * MODEL_PAGE_SIZE
  )
  const groups = Object.entries(
    currentModels.reduce<Record<string, CredentialModel[]>>((result, model) => {
      const group = model.group || "models"
      result[group] = [...(result[group] ?? []), model]
      return result
    }, {})
  )

  async function deleteModel(model: CredentialModel) {
    if (!credential) return
    setDeletingId(model.id)
    onError("")
    try {
      await onDelete(credential, model)
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "删除模型失败")
    } finally {
      setDeletingId("")
    }
  }

  return (
    <section className="flex min-h-[28rem] flex-none flex-col gap-4 md:min-h-0 md:flex-1">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-1">
          <h3 className="mr-1 text-sm font-semibold">模型</h3>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="全部折叠"
                disabled={groups.length === 0}
                onClick={() =>
                  setCollapsed(
                    Object.fromEntries(groups.map(([group]) => [group, true]))
                  )
                }
              >
                <ChevronsUpIcon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>全部折叠</TooltipContent>
          </Tooltip>
          {searching ? (
            <div className="relative ml-1 w-52 max-w-[45vw]">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                autoFocus
                value={query}
                placeholder="搜索模型…"
                className="h-8 pr-8 pl-8"
                onChange={(event) => {
                  setQuery(event.target.value)
                  setPage(1)
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                className="absolute top-1/2 right-1 -translate-y-1/2"
                aria-label="关闭搜索"
                onClick={() => {
                  setSearching(false)
                  setQuery("")
                  setPage(1)
                }}
              >
                <XIcon />
              </Button>
            </div>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="搜索模型"
                  onClick={() => setSearching(true)}
                >
                  <SearchIcon />
                </Button>
              </TooltipTrigger>
              <TooltipContent>搜索模型</TooltipContent>
            </Tooltip>
          )}
        </div>

        <ButtonGroup aria-label="模型操作">
          <Button
            type="button"
            variant="outline"
            disabled={!credential || busy}
            onClick={onPull}
          >
            <RefreshCwIcon
              data-icon="inline-start"
              className={pulling ? "animate-spin" : undefined}
            />
            {pulling ? "正在获取…" : "获取模型列表"}
          </Button>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="icon"
                aria-label="添加模型"
                disabled={!credential || busy}
                onClick={() => setAdding(true)}
              >
                <PlusIcon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>手动添加模型</TooltipContent>
          </Tooltip>
        </ButtonGroup>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto pr-1">
        {!credential ? (
          <Empty className="min-h-52 rounded-xl border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <SaveIcon />
              </EmptyMedia>
              <EmptyTitle>先保存模型服务</EmptyTitle>
              <EmptyDescription>
                保存连接后，就可以获取远程模型或手动添加模型。
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : models.length === 0 ? (
          <Empty className="min-h-52 rounded-xl border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <RefreshCwIcon />
              </EmptyMedia>
              <EmptyTitle>还没有模型</EmptyTitle>
              <EmptyDescription>
                从服务端获取模型列表，或使用右上角的加号手动添加。
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" disabled={busy} onClick={onPull}>
                <RefreshCwIcon data-icon="inline-start" />
                获取模型列表
              </Button>
            </EmptyContent>
          </Empty>
        ) : groups.length === 0 ? (
          <Empty className="min-h-40 rounded-xl border">
            <EmptyHeader>
              <EmptyTitle>没有匹配的模型</EmptyTitle>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="grid gap-3">
            {groups.map(([group, groupModels]) => (
              <Collapsible
                key={group}
                open={normalized ? true : !collapsed[group]}
                onOpenChange={(open) =>
                  setCollapsed((current) => ({ ...current, [group]: !open }))
                }
                className="overflow-hidden rounded-xl border bg-card"
              >
                <CollapsibleTrigger asChild>
                  <Button
                    type="button"
                    variant="ghost"
                    className="group h-11 w-full justify-between rounded-none px-4"
                  >
                    <span className="flex min-w-0 items-center gap-2">
                      <ChevronDownIcon className="transition-transform group-data-[state=closed]:-rotate-90" />
                      <span className="truncate font-medium">{group}</span>
                      <span className="text-xs text-muted-foreground">
                        {groupModels.length}
                      </span>
                    </span>
                  </Button>
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <CollectionList className="rounded-none border-x-0 border-b-0">
                    {groupModels.map((model) => {
                      const selected = model.id === defaultModelId
                      return (
                        <CollectionListItem
                          key={model.id}
                          variant={selected ? "muted" : "default"}
                        >
                          <button
                            type="button"
                            className="flex min-w-0 flex-1 items-center gap-3 text-left"
                            onClick={() => onSelect(model.id)}
                          >
                            <ItemMedia
                              className={cn(
                                "flex size-8 shrink-0 items-center justify-center rounded-lg border text-xs font-semibold",
                                selected &&
                                  "border-primary bg-primary text-primary-foreground"
                              )}
                            >
                              {selected ? (
                                <CheckIcon />
                              ) : (
                                model.name.slice(0, 1)
                              )}
                            </ItemMedia>
                            <ItemContent className="min-w-0">
                              <ItemTitle>
                                {model.name}
                                {selected && (
                                  <Badge variant="secondary">默认</Badge>
                                )}
                                {model.source === "manual" && (
                                  <Badge variant="outline">手动</Badge>
                                )}
                              </ItemTitle>
                              {model.name !== model.id && (
                                <ItemDescription>{model.id}</ItemDescription>
                              )}
                            </ItemContent>
                          </button>
                          <ItemActions>
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon-sm"
                                  aria-label={`删除 ${model.name}`}
                                  disabled={selected || deletingId === model.id}
                                  onClick={() => void deleteModel(model)}
                                >
                                  <Trash2Icon />
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>
                                {selected ? "默认模型不能删除" : "删除模型"}
                              </TooltipContent>
                            </Tooltip>
                          </ItemActions>
                        </CollectionListItem>
                      )
                    })}
                  </CollectionList>
                </CollapsibleContent>
              </Collapsible>
            ))}
          </div>
        )}
      </div>
      <CollectionPagination
        currentPage={currentPage}
        pageSize={MODEL_PAGE_SIZE}
        totalItems={visibleModels.length}
        onPageChange={setPage}
      />

      {credential && (
        <AddModelDialog
          open={adding}
          credential={credential}
          onOpenChange={setAdding}
          onAdd={onAdd}
          onError={onError}
        />
      )}
    </section>
  )
}

function AddModelDialog({
  open,
  credential,
  onOpenChange,
  onAdd,
  onError,
}: {
  open: boolean
  credential: ManagedCredential
  onOpenChange: (open: boolean) => void
  onAdd: AccessManagementProps["onAddModel"]
  onError: (message: string) => void
}) {
  const [id, setId] = useState("")
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit() {
    const modelId = id.trim()
    if (!modelId) {
      onError("请填写模型 ID")
      return
    }
    setBusy(true)
    onError("")
    try {
      await onAdd(credential, { id: modelId, name: name.trim() })
      setId("")
      setName("")
      onOpenChange(false)
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "添加模型失败")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>添加模型</DialogTitle>
          <DialogDescription>
            添加服务端未返回、但当前 API 地址可以调用的模型。
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="manual-model-id">模型 ID</FieldLabel>
            <Input
              id="manual-model-id"
              autoFocus
              value={id}
              placeholder="例如 gpt-5.5"
              onChange={(event) => setId(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="manual-model-name">模型名称</FieldLabel>
            <Input
              id="manual-model-name"
              value={name}
              placeholder="例如 GPT-5.5"
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button type="button" disabled={busy} onClick={() => void submit()}>
            {busy ? "正在添加…" : "添加模型"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function inputFromCredential(credential: ManagedCredential): CredentialInput {
  return {
    id: credential.id,
    name: credential.name,
    providerId: credential.providerId,
    protocol: credential.protocol,
    endpoint: credential.endpoint,
    modelId: credential.modelId,
    secret: "",
    enabled: credential.enabled,
  }
}

function newCredentialInput(
  providers: Provider[],
  credentials: ManagedCredential[]
): CredentialInput {
  const provider = providers[0]
  const providerId = provider?.id ?? ""
  return {
    id: nextCredentialId(providerId, credentials),
    name: defaultCredentialName(provider),
    providerId,
    protocol: defaultProtocol(providerId),
    endpoint: "",
    modelId: "",
    secret: "",
    enabled: true,
  }
}

function defaultCredentialName(provider?: Provider) {
  return provider ? `${provider.name} 主连接` : "新模型服务"
}

function nextCredentialId(
  providerId: string,
  credentials: ManagedCredential[]
) {
  const prefix = providerId ? `${providerId}-primary` : "provider-primary"
  if (!credentials.some((item) => item.id === prefix)) return prefix
  let index = 2
  while (credentials.some((item) => item.id === `${prefix}-${index}`)) {
    index += 1
  }
  return `${prefix}-${index}`
}

function isCredentialDirty(
  input: CredentialInput,
  credential: ManagedCredential
) {
  return (
    input.name !== credential.name ||
    input.providerId !== credential.providerId ||
    input.protocol !== credential.protocol ||
    input.endpoint !== credential.endpoint ||
    input.modelId !== credential.modelId ||
    input.enabled !== credential.enabled ||
    input.secret.length > 0
  )
}

function credentialStatus(credential: ManagedCredential) {
  if (!credential.enabled) {
    return { label: "已停用", dotClassName: "bg-muted-foreground" }
  }
  if (credential.lastCheckOk === true) {
    return { label: "连接正常", dotClassName: "bg-primary" }
  }
  if (credential.lastCheckOk === false) {
    return { label: "连接失败", dotClassName: "bg-destructive" }
  }
  return { label: "尚未检测", dotClassName: "bg-muted-foreground" }
}

function credentialStatusText(credential: ManagedCredential | null) {
  if (!credential) return "填写连接信息后保存"
  const status = credentialStatus(credential)
  return `${status.label} · ${credential.models.length} 个模型`
}

function slug(value: string) {
  return (
    value
      .normalize("NFKD")
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9\s-]/g, "")
      .replace(/[\s_-]+/g, "-")
      .replace(/^-+|-+$/g, "") || "credential"
  )
}

function defaultProtocol(providerId: string): CredentialInput["protocol"] {
  if (providerId === "anthropic") return "anthropic"
  if (providerId === "google") return "gemini"
  if (providerId === "openai") return "openai-responses"
  return "openai-chat"
}

function defaultEndpointHint(providerId: string) {
  return (
    {
      openai: "留空使用 https://api.openai.com/v1",
      anthropic: "留空使用 https://api.anthropic.com/v1",
      google: "留空使用 Google Gemini API",
      deepseek: "留空使用 https://api.deepseek.com",
    }[providerId] ?? "输入兼容服务的 API 地址"
  )
}
