"use client"

import { useMemo, useState } from "react"
import {
  CableIcon,
  InfoIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { appToast as toast } from "@/lib/app-toast"
import { ApiError, errorMessage, requestJson } from "@/lib/api-client"
import type { ManagedCredential } from "@/lib/credential-schema"
import {
  resourceResponseSchema,
  type ResourceOfKind,
  type RuntimeModelSource,
} from "@/lib/platform-schema"
import {
  describeModelSource,
  filterModelSourceOptions,
  findModelSourceOption,
  modelSourceOptions,
  runtimeModelSourceSlots,
  sameModelSource,
  type ModelSourceOption,
} from "@/lib/sandbox-model-sources"
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"

type SourceSelection = Pick<RuntimeModelSource, "credentialId" | "modelId">

const MODEL_SOURCE_BATCH_SIZE = 100

export function SandboxModelSourceSwitcher({
  sandbox,
  runtime,
  credentials,
  credentialsLoading,
  credentialsError,
  canMutate,
  onRetryCredentials,
  onResourceChange,
}: {
  sandbox: ResourceOfKind<"sandbox">
  runtime?: ResourceOfKind<"runtime">
  credentials: ManagedCredential[]
  credentialsLoading: boolean
  credentialsError: unknown
  canMutate: boolean
  onRetryCredentials: () => void
  onResourceChange: (resource: ResourceOfKind<"sandbox">) => void
}) {
  const [open, setOpen] = useState(false)
  const [selectedSlotCredentialId, setSelectedSlotCredentialId] = useState("")
  const [target, setTarget] = useState<SourceSelection | null>(null)
  const [expectedSource, setExpectedSource] = useState<SourceSelection | null>(
    null
  )
  const [modelQuery, setModelQuery] = useState("")
  const [visibleOptionLimit, setVisibleOptionLimit] = useState(
    MODEL_SOURCE_BATCH_SIZE
  )
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState("")
  const slots = useMemo(
    () =>
      runtimeModelSourceSlots(
        sandbox.spec.runtimeModelSources,
        {
          credentialIds:
            sandbox.spec.credentialIds ?? runtime?.spec.credentialIds,
          modelBindings:
            sandbox.spec.modelBindings ?? runtime?.spec.modelBindings,
        },
        credentials,
        sandbox.spec.runtimeModelSourcesComplete === true
      ),
    [
      credentials,
      runtime?.spec.credentialIds,
      runtime?.spec.modelBindings,
      sandbox.spec.credentialIds,
      sandbox.spec.modelBindings,
      sandbox.spec.runtimeModelSources,
      sandbox.spec.runtimeModelSourcesComplete,
    ]
  )
  const options = useMemo(() => modelSourceOptions(credentials), [credentials])
  const matchingOptions = useMemo(
    () => filterModelSourceOptions(options, modelQuery),
    [modelQuery, options]
  )
  const visibleOptions = matchingOptions.slice(0, visibleOptionLimit)
  const selectedSlot =
    slots.find((slot) => slot.slotCredentialId === selectedSlotCredentialId) ??
    slots[0] ??
    null
  const selectedOption = findModelSourceOption(options, target)
  const canSwitch = canMutate && sandbox.spec.status === "running"
  const changed = !sameModelSource(selectedSlot?.source ?? null, target)
  const sourceSummary = selectedSlot
    ? describeModelSource(selectedSlot.source, credentials)
    : "未绑定"
  const triggerSummary =
    slots.length > 1 ? `${slots.length} 个通道` : sourceSummary

  function handleOpenChange(nextOpen: boolean) {
    if (submitting) return
    if (nextOpen) {
      onRetryCredentials()
      const initialSlot = slots[0] ?? null
      setSelectedSlotCredentialId(initialSlot?.slotCredentialId ?? "")
      setTarget(
        initialSlot
          ? {
              credentialId: initialSlot.source.credentialId,
              modelId: initialSlot.source.modelId,
            }
          : null
      )
      setExpectedSource(
        initialSlot
          ? {
              credentialId: initialSlot.source.credentialId,
              modelId: initialSlot.source.modelId,
            }
          : null
      )
      setModelQuery("")
      setVisibleOptionLimit(MODEL_SOURCE_BATCH_SIZE)
      setSubmitError("")
    }
    setOpen(nextOpen)
  }

  function selectSlot(slotCredentialId: string) {
    const slot = slots.find(
      (item) => item.slotCredentialId === slotCredentialId
    )
    setSelectedSlotCredentialId(slotCredentialId)
    setTarget(
      slot
        ? {
            credentialId: slot.source.credentialId,
            modelId: slot.source.modelId,
          }
        : null
    )
    setExpectedSource(
      slot
        ? {
            credentialId: slot.source.credentialId,
            modelId: slot.source.modelId,
          }
        : null
    )
    setModelQuery("")
    setVisibleOptionLimit(MODEL_SOURCE_BATCH_SIZE)
    setSubmitError("")
  }

  function selectTarget(option: ModelSourceOption | null) {
    setTarget(
      option
        ? { credentialId: option.credentialId, modelId: option.modelId }
        : null
    )
    setModelQuery("")
    setVisibleOptionLimit(MODEL_SOURCE_BATCH_SIZE)
    setSubmitError("")
  }

  async function switchSource() {
    if (
      !canSwitch ||
      !selectedSlot ||
      !selectedOption ||
      !expectedSource ||
      !changed ||
      submitting
    )
      return

    setSubmitting(true)
    setSubmitError("")
    try {
      const result = resourceResponseSchema.parse(
        await requestJson<unknown>(
          `/api/sandboxes/${encodeURIComponent(sandbox.id)}/model-source`,
          {
            method: "PATCH",
            body: JSON.stringify({
              slotCredentialId: selectedSlot.slotCredentialId,
              credentialId: selectedOption.credentialId,
              modelId: selectedOption.modelId,
              expectedCredentialId: expectedSource.credentialId,
              expectedModelId: expectedSource.modelId,
            }),
          }
        )
      )
      if (
        result.resource.kind !== "sandbox" ||
        result.resource.id !== sandbox.id
      )
        throw new Error("模型源响应与当前沙箱不匹配")

      onResourceChange(result.resource)
      setOpen(false)
      toast.success("模型源已切换", {
        description: `本次运行的下一次请求将使用 ${selectedOption.credentialName} · ${selectedOption.modelName}；当前在途请求继续使用原模型。`,
      })
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        try {
          const latest = resourceResponseSchema.parse(
            await requestJson<unknown>(
              `/api/resources/${encodeURIComponent(sandbox.id)}`
            )
          ).resource
          if (latest.kind !== "sandbox" || latest.id !== sandbox.id)
            throw new Error("刷新结果与当前沙箱不匹配")
          const refreshedSlot = runtimeModelSourceSlots(
            latest.spec.runtimeModelSources,
            {
              credentialIds:
                latest.spec.credentialIds ?? runtime?.spec.credentialIds,
              modelBindings:
                latest.spec.modelBindings ?? runtime?.spec.modelBindings,
            },
            credentials,
            latest.spec.runtimeModelSourcesComplete === true
          ).find(
            (slot) => slot.slotCredentialId === selectedSlot.slotCredentialId
          )
          if (!refreshedSlot)
            throw new Error("最新运行快照中不存在所选模型通道")
          onResourceChange(latest)
          setExpectedSource({
            credentialId: refreshedSlot.source.credentialId,
            modelId: refreshedSlot.source.modelId,
          })
          setSubmitError("模型源已被其他操作更新，已加载最新绑定，请确认后重试")
        } catch (refreshCause) {
          setSubmitError(
            `模型源已被其他操作更新，自动刷新失败：${errorMessage(refreshCause)}`
          )
        }
      } else {
        setSubmitError(errorMessage(cause))
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button
          type="button"
          variant="outline"
          size="sm"
          title={
            canSwitch
              ? `切换模型源：${triggerSummary}`
              : `模型源：${triggerSummary}（只读）`
          }
        >
          <CableIcon aria-hidden="true" data-icon="inline-start" />
          <span className="max-w-52 truncate">模型源 · {triggerSummary}</span>
        </Button>
      </DialogTrigger>
      <DialogContent showCloseButton={!submitting} className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{canSwitch ? "切换模型源" : "模型源"}</DialogTitle>
          <DialogDescription>
            {canSwitch
              ? "为当前沙箱的模型通道选择新的服务和具体模型。"
              : "当前账号为只读权限，可查看沙箱正在使用的模型绑定。"}
          </DialogDescription>
        </DialogHeader>

        {selectedSlot ? (
          <Alert role="note">
            <CableIcon aria-hidden="true" />
            <AlertTitle>
              {slots.length > 1
                ? `通道 · ${selectedSlot.slotName}`
                : "当前绑定"}
            </AlertTitle>
            <AlertDescription>
              {describeModelSource(selectedSlot.source, credentials)}
              <span className="block">
                {selectedSlot.recorded && selectedSlot.source.updatedAt
                  ? `运行快照更新于 ${formatUpdatedAt(selectedSlot.source.updatedAt)}`
                  : "旧沙箱未记录运行快照，当前绑定按保存配置推断"}
              </span>
            </AlertDescription>
          </Alert>
        ) : (
          <Alert role="note">
            <InfoIcon aria-hidden="true" />
            <AlertTitle>没有可切换的模型通道</AlertTitle>
            <AlertDescription>
              这个沙箱启动时没有注入模型绑定，需先在沙箱配置中添加模型服务。
            </AlertDescription>
          </Alert>
        )}

        {canSwitch && selectedSlot ? (
          <FieldGroup className="gap-4">
            {slots.length > 1 ? (
              <Field>
                <FieldLabel htmlFor="sandbox-model-source-slot">
                  作用通道
                </FieldLabel>
                <Select
                  value={selectedSlot.slotCredentialId}
                  disabled={submitting}
                  onValueChange={selectSlot}
                >
                  <SelectTrigger
                    id="sandbox-model-source-slot"
                    className="w-full"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectLabel>模型通道</SelectLabel>
                      {slots.map((slot) => (
                        <SelectItem
                          key={slot.slotCredentialId}
                          value={slot.slotCredentialId}
                        >
                          {slot.slotName} · {slot.slotCredentialId}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FieldDescription>
                  通道标识固定不变，只替换该通道下一次请求使用的模型源。
                </FieldDescription>
              </Field>
            ) : null}

            <Field data-invalid={Boolean(submitError)}>
              <FieldLabel htmlFor="sandbox-model-source-target">
                目标模型
              </FieldLabel>
              <Combobox
                items={options}
                filteredItems={visibleOptions}
                filter={null}
                value={selectedOption}
                disabled={
                  submitting ||
                  credentialsLoading ||
                  Boolean(credentialsError) ||
                  options.length === 0
                }
                autoComplete="off"
                itemToStringLabel={(option) => option.label}
                itemToStringValue={(option) => option.value}
                isItemEqualToValue={(option, value) =>
                  option.value === value.value
                }
                onValueChange={selectTarget}
                onInputValueChange={(inputValue, eventDetails) => {
                  if (
                    eventDetails.reason === "input-change" ||
                    eventDetails.reason === "input-clear"
                  ) {
                    setModelQuery(inputValue)
                    setVisibleOptionLimit(MODEL_SOURCE_BATCH_SIZE)
                  }
                }}
              >
                <ComboboxInput
                  id="sandbox-model-source-target"
                  placeholder={
                    credentialsLoading
                      ? "正在加载模型目录…"
                      : "搜索服务、模型名称或模型 ID"
                  }
                  disabled={
                    submitting ||
                    credentialsLoading ||
                    Boolean(credentialsError) ||
                    options.length === 0
                  }
                  aria-invalid={Boolean(submitError) || undefined}
                  triggerAriaLabel="显示模型选项"
                  className="w-full"
                />
                <ComboboxContent>
                  <ComboboxEmpty>没有匹配的可用模型</ComboboxEmpty>
                  <ComboboxList className="[scrollbar-width:thin] [scrollbar-color:var(--border)_transparent]">
                    {(option: ModelSourceOption) => (
                      <ComboboxItem
                        key={option.value}
                        value={option}
                        className="px-2.5 py-2 pr-8"
                      >
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-medium">
                            {option.modelName}
                          </span>
                          <span className="block truncate text-xs text-muted-foreground">
                            {option.credentialName} · {option.modelId}
                          </span>
                        </span>
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {option.protocol}
                        </span>
                      </ComboboxItem>
                    )}
                  </ComboboxList>
                  {matchingOptions.length > visibleOptions.length ? (
                    <div className="flex items-center justify-between gap-3 border-t px-2.5 py-2">
                      <span
                        role="status"
                        className="text-xs text-muted-foreground"
                      >
                        已显示 {visibleOptions.length.toLocaleString("zh-CN")}{" "}
                        个，共 {matchingOptions.length.toLocaleString("zh-CN")}{" "}
                        个
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        onClick={() =>
                          setVisibleOptionLimit((current) =>
                            Math.min(
                              current + MODEL_SOURCE_BATCH_SIZE,
                              matchingOptions.length
                            )
                          )
                        }
                      >
                        继续显示
                      </Button>
                    </div>
                  ) : null}
                </ComboboxContent>
              </Combobox>
              <FieldDescription>
                仅列出已启用且已获取模型目录的服务。
              </FieldDescription>
            </Field>
          </FieldGroup>
        ) : null}

        {canSwitch && selectedSlot && credentialsLoading ? (
          <Alert role="status" aria-live="polite">
            <Spinner aria-hidden="true" />
            <AlertTitle>正在加载模型目录</AlertTitle>
            <AlertDescription>
              正在读取已启用的模型服务和可用模型。
            </AlertDescription>
          </Alert>
        ) : canSwitch && selectedSlot && credentialsError ? (
          <Alert variant="destructive">
            <TriangleAlertIcon aria-hidden="true" />
            <AlertTitle>模型目录加载失败</AlertTitle>
            <AlertDescription>
              {errorMessage(credentialsError)}
            </AlertDescription>
            <AlertAction>
              <Button
                type="button"
                variant="outline"
                size="xs"
                disabled={credentialsLoading}
                onClick={onRetryCredentials}
              >
                {credentialsLoading ? (
                  <Spinner aria-hidden="true" data-icon="inline-start" />
                ) : (
                  <RefreshCwIcon aria-hidden="true" data-icon="inline-start" />
                )}
                重试
              </Button>
            </AlertAction>
          </Alert>
        ) : canSwitch &&
          selectedSlot &&
          !credentialsLoading &&
          options.length === 0 ? (
          <Alert role="note">
            <InfoIcon aria-hidden="true" />
            <AlertTitle>没有可选模型</AlertTitle>
            <AlertDescription>
              请先启用模型服务并获取模型列表，再回到这里切换。
            </AlertDescription>
          </Alert>
        ) : null}

        {canSwitch && selectedSlot ? (
          <Alert role="note">
            <InfoIcon aria-hidden="true" />
            <AlertTitle>无中断切换</AlertTitle>
            <AlertDescription>
              当前在途请求继续使用原模型；下一次新请求使用新模型源。终端与图形桌面不会停止或重连。切换仅影响当前运行周期；下次重启按沙箱保存配置恢复。
            </AlertDescription>
          </Alert>
        ) : null}

        {submitError ? (
          <Alert variant="destructive">
            <TriangleAlertIcon aria-hidden="true" />
            <AlertTitle>切换失败</AlertTitle>
            <AlertDescription>
              {submitError}。当前选择已保留，可直接重试。
            </AlertDescription>
          </Alert>
        ) : null}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={submitting}
            onClick={() => handleOpenChange(false)}
          >
            关闭
          </Button>
          {canSwitch && selectedSlot ? (
            <Button
              type="button"
              disabled={
                submitting ||
                credentialsLoading ||
                Boolean(credentialsError) ||
                !selectedOption ||
                !expectedSource ||
                !changed
              }
              onClick={() => void switchSource()}
            >
              {submitting ? <Spinner data-icon="inline-start" /> : null}
              {submitting ? "正在切换…" : changed ? "确认切换" : "当前正在使用"}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function formatUpdatedAt(value: string) {
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(timestamp)
}
