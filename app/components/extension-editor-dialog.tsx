"use client"

import { useRef, useState, type ReactNode } from "react"
import { ChevronDownIcon, SaveIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
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
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldSet,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { errorMessage } from "@/lib/api-client"
import {
  resourceInputSchema,
  type Resource,
  type ResourceInput,
} from "@/lib/platform-schema"

type ExtensionEditorDialogProps = {
  resource: Resource | null
  projectId: string | null
  resources?: Resource[]
  dependenciesReady?: boolean
  dependencyStatus?: ReactNode
  onOpenChange: (open: boolean) => void
  onSave: (input: ResourceInput) => Promise<void>
}

function extensionIdForName(name: string, resources: Resource[]) {
  const slug = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 58)
    .replace(/-$/, "")
  const base = slug.length >= 2 ? slug : "extension"
  let id = base
  let suffix = 2
  while (resources.some((item) => item.id === id)) id = base + "-" + suffix++
  return id
}

export function ExtensionEditorDialog(props: ExtensionEditorDialogProps) {
  return (
    <ExtensionEditorForm
      key={props.resource?.id ?? "new-extension"}
      {...props}
    />
  )
}

function ExtensionEditorForm({
  resource,
  projectId,
  resources = [],
  dependenciesReady = true,
  dependencyStatus,
  onOpenChange,
  onSave,
}: ExtensionEditorDialogProps) {
  const formRef = useRef<HTMLFormElement>(null)
  const extension = resource?.kind === "extension" ? resource : null
  const [name, setName] = useState(extension?.name ?? "")
  const [id, setId] = useState(
    () => extension?.id ?? extensionIdForName("", resources)
  )
  const [idEdited, setIdEdited] = useState(Boolean(extension))
  const [description, setDescription] = useState(extension?.description ?? "")
  const [enabled, setEnabled] = useState(extension?.enabled ?? true)
  const [version, setVersion] = useState(extension?.spec.version ?? "1.0.0")
  const source = extension?.spec.source ?? "custom"
  const [installScript, setInstallScript] = useState(
    extension?.spec.installScript ?? ""
  )
  const [verifyScript, setVerifyScript] = useState(
    extension?.spec.verifyScript ?? ""
  )
  const [timeout, setTimeout] = useState(
    String(extension?.spec.timeoutSeconds ?? 600)
  )
  const [requiresNetwork, setRequiresNetwork] = useState(
    extension?.spec.requiresNetwork ?? true
  )
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saveError, setSaveError] = useState("")
  const [busy, setBusy] = useState(false)

  function updateName(value: string) {
    setName(value)
    if (!idEdited) setId(extensionIdForName(value, resources))
  }

  async function submit() {
    if (busy || !dependenciesReady) return
    setSaveError("")
    const result = resourceInputSchema.safeParse({
      id,
      kind: "extension",
      projectId: extension?.projectId ?? projectId,
      name,
      description,
      enabled,
      specVersion: extension?.specVersion ?? 1,
      spec: {
        version,
        source,
        installScript,
        verifyScript,
        timeoutSeconds: Number(timeout),
        requiresNetwork,
      },
    })
    if (!result.success) {
      const issues = result.error.issues
      setErrors(
        Object.fromEntries(
          issues.map((issue) => [issue.path.join("."), issue.message])
        )
      )
      if (
        issues.some((issue) =>
          ["id", "description", "spec.version", "spec.timeoutSeconds"].includes(
            issue.path.join(".")
          )
        )
      ) {
        setAdvancedOpen(true)
      }
      requestAnimationFrame(() => {
        const firstControl = issues
          .map((issue) =>
            formRef.current?.elements.namedItem(issue.path.join("."))
          )
          .find((control) => control instanceof HTMLElement)
        if (firstControl instanceof HTMLElement) firstControl.focus()
      })
      return
    }
    setErrors({})
    setBusy(true)
    try {
      await onSave(result.data)
      onOpenChange(false)
    } catch (cause) {
      setSaveError(errorMessage(cause))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!busy) onOpenChange(open)
      }}
    >
      <DialogContent
        className="flex max-h-[calc(100dvh-2rem)] flex-col overflow-hidden p-0 sm:max-w-xl"
        showCloseButton={!busy}
      >
        <DialogHeader className="shrink-0 border-b px-5 py-4 pr-12">
          <DialogTitle>
            {extension ? "编辑沙箱扩展" : "新建沙箱扩展"}
          </DialogTitle>
          <DialogDescription>
            填写名称和命令，创建沙箱时自动安装。
          </DialogDescription>
        </DialogHeader>
        <form
          ref={formRef}
          id="extension-editor-form"
          noValidate
          className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-5"
          onSubmit={(event) => {
            event.preventDefault()
            void submit()
          }}
          aria-busy={busy}
        >
          {dependencyStatus}
          <FieldSet disabled={busy}>
            <FieldGroup className="gap-4">
              <Field data-invalid={Boolean(errors.name)}>
                <FieldLabel htmlFor="extension-name">名称</FieldLabel>
                <Input
                  id="extension-name"
                  name="name"
                  autoFocus
                  autoComplete="off"
                  placeholder="例如 Multica"
                  value={name}
                  onChange={(event) => updateName(event.target.value)}
                  aria-invalid={Boolean(errors.name)}
                  aria-describedby="extension-name-error"
                  maxLength={80}
                />
                <FieldError id="extension-name-error">{errors.name}</FieldError>
              </Field>
              <Field data-invalid={Boolean(errors["spec.installScript"])}>
                <FieldLabel htmlFor="extension-install-script">
                  安装命令
                </FieldLabel>
                <Textarea
                  id="extension-install-script"
                  name="spec.installScript"
                  rows={3}
                  className="max-h-64 min-h-24 overflow-y-auto font-mono"
                  spellCheck={false}
                  placeholder="粘贴软件的安装命令，可填写多行"
                  value={installScript}
                  onChange={(event) => setInstallScript(event.target.value)}
                  aria-invalid={Boolean(errors["spec.installScript"])}
                  aria-describedby="extension-install-error"
                />
                <FieldError id="extension-install-error">
                  {errors["spec.installScript"]}
                </FieldError>
              </Field>
              <Field data-invalid={Boolean(errors["spec.verifyScript"])}>
                <FieldLabel htmlFor="extension-verify-script">
                  验证命令
                </FieldLabel>
                <Textarea
                  id="extension-verify-script"
                  name="spec.verifyScript"
                  rows={2}
                  className="max-h-40 min-h-16 overflow-y-auto font-mono"
                  spellCheck={false}
                  placeholder="例如 my-tool --version"
                  value={verifyScript}
                  onChange={(event) => setVerifyScript(event.target.value)}
                  aria-invalid={Boolean(errors["spec.verifyScript"])}
                  aria-describedby="extension-verify-help extension-verify-error"
                />
                <FieldDescription id="extension-verify-help">
                  用于确认安装成功，例如查看软件版本。
                </FieldDescription>
                <FieldError id="extension-verify-error">
                  {errors["spec.verifyScript"]}
                </FieldError>
              </Field>
            </FieldGroup>
          </FieldSet>
          <FieldDescription>
            命令仅在新建沙箱内以 root
            执行。请使用可信命令，密钥通过沙箱环境变量配置。
          </FieldDescription>
          <Collapsible
            open={advancedOpen}
            onOpenChange={setAdvancedOpen}
            className="border-t pt-3"
          >
            <CollapsibleTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="group w-full justify-between"
                disabled={busy}
              >
                高级设置
                <ChevronDownIcon
                  data-icon="inline-end"
                  className="group-data-[state=open]:rotate-180"
                />
              </Button>
            </CollapsibleTrigger>
            <CollapsibleContent className="pt-4">
              <FieldSet disabled={busy}>
                <FieldGroup className="gap-4">
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field data-invalid={Boolean(errors.id)}>
                      <FieldLabel htmlFor="extension-id">标识</FieldLabel>
                      <Input
                        id="extension-id"
                        name="id"
                        autoComplete="off"
                        value={id}
                        onChange={(event) => {
                          setIdEdited(true)
                          setId(event.target.value)
                        }}
                        disabled={Boolean(extension)}
                        aria-invalid={Boolean(errors.id)}
                        aria-describedby="extension-id-error"
                        maxLength={64}
                      />
                      <FieldDescription>
                        已自动生成，创建后不可更改。
                      </FieldDescription>
                      <FieldError id="extension-id-error">
                        {errors.id}
                      </FieldError>
                    </Field>
                    <Field data-invalid={Boolean(errors["spec.version"])}>
                      <FieldLabel htmlFor="extension-version">
                        版本标签
                      </FieldLabel>
                      <Input
                        id="extension-version"
                        name="spec.version"
                        value={version}
                        onChange={(event) => setVersion(event.target.value)}
                        aria-invalid={Boolean(errors["spec.version"])}
                        aria-describedby="extension-version-error"
                      />
                      <FieldDescription>
                        仅作记录，不会改写安装命令。
                      </FieldDescription>
                      <FieldError id="extension-version-error">
                        {errors["spec.version"]}
                      </FieldError>
                    </Field>
                  </div>
                  <Field data-invalid={Boolean(errors.description)}>
                    <FieldLabel htmlFor="extension-description">
                      说明（可选）
                    </FieldLabel>
                    <Textarea
                      id="extension-description"
                      name="description"
                      rows={2}
                      value={description}
                      onChange={(event) => setDescription(event.target.value)}
                      aria-invalid={Boolean(errors.description)}
                      aria-describedby="extension-description-error"
                      maxLength={500}
                    />
                    <FieldError id="extension-description-error">
                      {errors.description}
                    </FieldError>
                  </Field>
                  <Field data-invalid={Boolean(errors["spec.timeoutSeconds"])}>
                    <FieldLabel htmlFor="extension-timeout">
                      每步超时（秒）
                    </FieldLabel>
                    <Input
                      id="extension-timeout"
                      name="spec.timeoutSeconds"
                      type="number"
                      min={30}
                      max={1800}
                      step={1}
                      value={timeout}
                      onChange={(event) => setTimeout(event.target.value)}
                      aria-invalid={Boolean(errors["spec.timeoutSeconds"])}
                      aria-describedby="extension-timeout-error"
                    />
                    <FieldDescription>
                      默认 600 秒，安装和验证分别计时；镜像需提供 GNU timeout。
                    </FieldDescription>
                    <FieldError id="extension-timeout-error">
                      {errors["spec.timeoutSeconds"]}
                    </FieldError>
                  </Field>
                  <Field orientation="horizontal">
                    <FieldContent>
                      <FieldLabel htmlFor="extension-network">
                        需要网络
                      </FieldLabel>
                      <FieldDescription>
                        用于下载依赖，不会改变沙箱网络策略。
                      </FieldDescription>
                    </FieldContent>
                    <Switch
                      id="extension-network"
                      checked={requiresNetwork}
                      onCheckedChange={setRequiresNetwork}
                      disabled={busy}
                    />
                  </Field>
                  <Field orientation="horizontal">
                    <FieldContent>
                      <FieldLabel htmlFor="extension-enabled">
                        启用扩展
                      </FieldLabel>
                      <FieldDescription>
                        关闭后可保存未完成的配置。
                      </FieldDescription>
                    </FieldContent>
                    <Switch
                      id="extension-enabled"
                      checked={enabled}
                      onCheckedChange={setEnabled}
                      disabled={busy}
                    />
                  </Field>
                </FieldGroup>
              </FieldSet>
            </CollapsibleContent>
          </Collapsible>
          {errors.projectId && <FieldError>{errors.projectId}</FieldError>}
          {saveError && (
            <Alert variant="destructive">
              <AlertTitle>保存失败</AlertTitle>
              <AlertDescription>{saveError}</AlertDescription>
            </Alert>
          )}
        </form>
        <DialogFooter className="mx-0 mb-0 shrink-0 px-5 py-4">
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button
            type="submit"
            form="extension-editor-form"
            disabled={busy || !dependenciesReady}
          >
            {busy ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <SaveIcon data-icon="inline-start" />
            )}
            {busy ? "正在保存…" : "保存扩展"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
