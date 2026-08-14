"use client"

import { useState, type FormEvent, type ReactNode } from "react"
import {
  BellIcon,
  KeyRoundIcon,
  MonitorIcon,
  PaletteIcon,
  SaveIcon,
  UserCogIcon,
  WrenchIcon,
  type LucideIcon,
} from "lucide-react"
import { useTheme } from "next-themes"

import { SiteHeader } from "@/components/site-header"
import { useAccentTheme, type AccentTheme } from "@/components/theme-provider"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
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
import { Separator } from "@/components/ui/separator"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { appToast as toast } from "@/lib/app-toast"
import {
  userInputSchema,
  type ManagedUser,
  type UserInput,
  type UserPreferences,
} from "@/lib/user-schema"

type SettingsSection =
  | "profile"
  | "account"
  | "appearance"
  | "notifications"
  | "display"
  | "environment"

type SettingsNavItem = {
  id: SettingsSection
  title: string
  icon: LucideIcon
}

const settingsNavItems: SettingsNavItem[] = [
  { id: "profile", title: "个人资料", icon: UserCogIcon },
  { id: "account", title: "账号安全", icon: WrenchIcon },
  { id: "appearance", title: "外观", icon: PaletteIcon },
  { id: "notifications", title: "通知", icon: BellIcon },
  { id: "display", title: "显示", icon: MonitorIcon },
  { id: "environment", title: "环境变量", icon: KeyRoundIcon },
]

export function SettingsView({
  user,
  projectName,
  variableCount,
  onSaveUser,
  onSavePreferences,
  onManageVariables,
}: {
  user: ManagedUser
  projectName: string
  variableCount: number
  onSaveUser: (input: UserInput) => Promise<void>
  onSavePreferences: (input: UserPreferences) => Promise<void>
  onManageVariables: () => void
}) {
  const [section, setSection] = useState<SettingsSection>("profile")

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <SiteHeader title="设置" />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-6xl flex-col px-4 py-6 sm:px-6 lg:px-8">
          <div className="flex flex-col gap-1">
            <h2 className="text-2xl font-bold tracking-tight sm:text-3xl">
              设置
            </h2>
            <p className="text-sm text-muted-foreground">
              管理账号、界面偏好，以及当前项目的运行配置。
            </p>
          </div>
          <Separator className="my-5" />

          <div className="grid min-h-0 gap-8 lg:grid-cols-[12rem_minmax(0,1fr)] lg:gap-12">
            <aside>
              <div className="lg:hidden">
                <Select
                  value={section}
                  onValueChange={(value) =>
                    setSection(value as SettingsSection)
                  }
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {settingsNavItems.map((item) => (
                        <SelectItem key={item.id} value={item.id}>
                          {item.title}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
              <nav
                className="hidden flex-col gap-1 lg:flex"
                aria-label="设置分区"
              >
                {settingsNavItems.map((item) => {
                  const Icon = item.icon
                  return (
                    <Button
                      key={item.id}
                      variant={section === item.id ? "secondary" : "ghost"}
                      className="justify-start"
                      onClick={() => setSection(item.id)}
                    >
                      <Icon data-icon="inline-start" />
                      {item.title}
                    </Button>
                  )
                })}
              </nav>
            </aside>

            <div className="max-w-2xl min-w-0">
              {section === "profile" ? (
                <ProfileSettings user={user} onSave={onSaveUser} />
              ) : section === "account" ? (
                <AccountSettings user={user} onSave={onSaveUser} />
              ) : section === "appearance" ? (
                <AppearanceSettings />
              ) : section === "notifications" ? (
                <NotificationSettings
                  preferences={user.preferences}
                  onSave={onSavePreferences}
                />
              ) : section === "display" ? (
                <DisplaySettings
                  preferences={user.preferences}
                  onSave={onSavePreferences}
                />
              ) : (
                <EnvironmentSettings
                  projectName={projectName}
                  variableCount={variableCount}
                  onManage={onManageVariables}
                />
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}

function SettingsContent({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <div className="flex flex-col">
      <div className="flex flex-col gap-1">
        <h3 className="text-lg font-medium">{title}</h3>
        <p className="text-sm text-muted-foreground">{description}</p>
      </div>
      <Separator className="my-4" />
      {children}
    </div>
  )
}

function ProfileSettings({
  user,
  onSave,
}: {
  user: ManagedUser
  onSave: (input: UserInput) => Promise<void>
}) {
  const [name, setName] = useState(user.name)
  const [email, setEmail] = useState(user.email)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const parsed = userInputSchema.safeParse({
      name,
      email,
      password: "",
      role: user.role,
      status: user.status,
    })
    if (!parsed.success) {
      setErrors(
        Object.fromEntries(
          parsed.error.issues.map((issue) => [
            String(issue.path[0]),
            issue.message,
          ])
        )
      )
      return
    }
    setSaving(true)
    try {
      await onSave(parsed.data)
      setErrors({})
    } catch (error) {
      setErrors({
        form: error instanceof Error ? error.message : "资料保存失败",
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingsContent
      title="个人资料"
      description="这些信息会显示在账户菜单和团队成员列表中。"
    >
      <form onSubmit={submit}>
        <FieldGroup>
          <Field data-invalid={Boolean(errors.name)}>
            <FieldLabel htmlFor="settings-name">显示名称</FieldLabel>
            <Input
              id="settings-name"
              value={name}
              autoComplete="name"
              aria-invalid={Boolean(errors.name)}
              onChange={(event) => setName(event.target.value)}
            />
            <FieldDescription>团队成员看到的名称。</FieldDescription>
            <FieldError>{errors.name}</FieldError>
          </Field>
          <Field data-invalid={Boolean(errors.email)}>
            <FieldLabel htmlFor="settings-email">登录邮箱</FieldLabel>
            <Input
              id="settings-email"
              type="email"
              value={email}
              autoComplete="email"
              aria-invalid={Boolean(errors.email)}
              onChange={(event) => setEmail(event.target.value)}
            />
            <FieldDescription>
              修改后，下次登录需要使用新的邮箱地址。
            </FieldDescription>
            <FieldError>{errors.email}</FieldError>
          </Field>
          {errors.form ? <FieldError>{errors.form}</FieldError> : null}
          <div>
            <Button type="submit" disabled={saving}>
              {saving ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <SaveIcon data-icon="inline-start" />
              )}
              保存资料
            </Button>
          </div>
        </FieldGroup>
      </form>
    </SettingsContent>
  )
}

function AccountSettings({
  user,
  onSave,
}: {
  user: ManagedUser
  onSave: (input: UserInput) => Promise<void>
}) {
  const [password, setPassword] = useState("")
  const [confirmation, setConfirmation] = useState("")
  const [error, setError] = useState("")
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (password.length < 8) {
      setError("新密码至少需要 8 个字符")
      return
    }
    if (password !== confirmation) {
      setError("两次输入的密码不一致")
      return
    }
    setSaving(true)
    try {
      await onSave({
        name: user.name,
        email: user.email,
        password,
        role: user.role,
        status: user.status,
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "密码更新失败")
      setSaving(false)
    }
  }

  return (
    <SettingsContent
      title="账号安全"
      description="查看账号状态并更新登录密码。"
    >
      <form onSubmit={submit}>
        <FieldGroup>
          <div className="grid gap-4 rounded-lg border p-4 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <span className="text-sm text-muted-foreground">账号角色</span>
              <span className="text-sm font-medium">
                {roleLabel(user.role)}
              </span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-sm text-muted-foreground">账号状态</span>
              <div>
                <Badge variant="secondary">
                  {user.status === "active" ? "已启用" : "已停用"}
                </Badge>
              </div>
            </div>
            <div className="flex flex-col gap-1 sm:col-span-2">
              <span className="text-sm text-muted-foreground">最近登录</span>
              <span className="text-sm font-medium">
                {formatDate(user.lastLoginAt)}
              </span>
            </div>
          </div>
          <Field data-invalid={Boolean(error)}>
            <FieldLabel htmlFor="settings-password">新密码</FieldLabel>
            <Input
              id="settings-password"
              type="password"
              value={password}
              autoComplete="new-password"
              aria-invalid={Boolean(error)}
              onChange={(event) => {
                setPassword(event.target.value)
                setError("")
              }}
            />
            <FieldDescription>密码需要 8 到 128 个字符。</FieldDescription>
          </Field>
          <Field data-invalid={Boolean(error)}>
            <FieldLabel htmlFor="settings-password-confirmation">
              确认新密码
            </FieldLabel>
            <Input
              id="settings-password-confirmation"
              type="password"
              value={confirmation}
              autoComplete="new-password"
              aria-invalid={Boolean(error)}
              onChange={(event) => {
                setConfirmation(event.target.value)
                setError("")
              }}
            />
            <FieldError>{error}</FieldError>
          </Field>
          <Alert>
            <KeyRoundIcon />
            <AlertTitle>密码修改后需要重新登录</AlertTitle>
            <AlertDescription>
              为保护账号，保存新密码会立即注销该账号的全部现有会话。
            </AlertDescription>
          </Alert>
          <div>
            <Button type="submit" disabled={saving}>
              {saving ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <SaveIcon data-icon="inline-start" />
              )}
              更新密码
            </Button>
          </div>
        </FieldGroup>
      </form>
    </SettingsContent>
  )
}

function AppearanceSettings() {
  const { theme, setTheme } = useTheme()
  const { accentTheme, setAccentTheme } = useAccentTheme()
  const [nextTheme, setNextTheme] = useState(theme ?? "system")
  const [nextAccent, setNextAccent] = useState<AccentTheme>(accentTheme)

  return (
    <SettingsContent title="外观" description="选择控制台的显示模式和主题色。">
      <FieldGroup>
        <Field>
          <FieldLabel>显示模式</FieldLabel>
          <FieldDescription>
            跟随系统会自动匹配设备的浅色或深色模式。
          </FieldDescription>
          <ToggleGroup
            type="single"
            variant="outline"
            value={nextTheme}
            onValueChange={(value) => value && setNextTheme(value)}
            className="justify-start"
          >
            <ToggleGroupItem value="light">浅色</ToggleGroupItem>
            <ToggleGroupItem value="dark">深色</ToggleGroupItem>
            <ToggleGroupItem value="system">跟随系统</ToggleGroupItem>
          </ToggleGroup>
        </Field>
        <Field>
          <FieldLabel>主题色</FieldLabel>
          <FieldDescription>
            用于主要按钮、选中状态和键盘焦点，不改变内容语义色。
          </FieldDescription>
          <ToggleGroup
            type="single"
            variant="outline"
            value={nextAccent}
            onValueChange={(value) =>
              value && setNextAccent(value as AccentTheme)
            }
            className="justify-start"
          >
            <ToggleGroupItem value="neutral">中性</ToggleGroupItem>
            <ToggleGroupItem value="blue">蓝色</ToggleGroupItem>
            <ToggleGroupItem value="green">绿色</ToggleGroupItem>
            <ToggleGroupItem value="orange">橙色</ToggleGroupItem>
          </ToggleGroup>
        </Field>
        <div>
          <Button
            onClick={() => {
              setTheme(nextTheme)
              setAccentTheme(nextAccent)
              toast.success("外观设置已保存")
            }}
          >
            <SaveIcon data-icon="inline-start" />
            保存外观
          </Button>
        </div>
      </FieldGroup>
    </SettingsContent>
  )
}

function NotificationSettings({
  preferences,
  onSave,
}: {
  preferences: UserPreferences
  onSave: (input: UserPreferences) => Promise<void>
}) {
  const [successNotifications, setSuccessNotifications] = useState(
    preferences.successNotifications
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  return (
    <SettingsContent
      title="通知"
      description="管理控制台内的操作反馈；错误与安全提示不会被静默。"
    >
      <FieldGroup>
        <Field orientation="horizontal" className="rounded-lg border p-4">
          <FieldContent>
            <FieldTitle>操作成功提示</FieldTitle>
            <FieldDescription>
              创建、更新、删除或切换成功后显示站内通知。
            </FieldDescription>
          </FieldContent>
          <Switch
            checked={successNotifications}
            onCheckedChange={setSuccessNotifications}
            aria-label="操作成功提示"
          />
        </Field>
        <Field
          orientation="horizontal"
          className="rounded-lg border p-4"
          data-disabled
        >
          <FieldContent>
            <FieldTitle>错误提示</FieldTitle>
            <FieldDescription>
              请求失败、配置冲突和运行错误始终显示。
            </FieldDescription>
          </FieldContent>
          <Switch checked disabled aria-label="错误提示始终开启" />
        </Field>
        <Field
          orientation="horizontal"
          className="rounded-lg border p-4"
          data-disabled
        >
          <FieldContent>
            <FieldTitle>账号安全提示</FieldTitle>
            <FieldDescription>
              登录失效和密码变更等安全事件始终显示。
            </FieldDescription>
          </FieldContent>
          <Switch checked disabled aria-label="账号安全提示始终开启" />
        </Field>
        <div>
          <Button
            disabled={saving}
            onClick={async () => {
              setSaving(true)
              try {
                await onSave({ ...preferences, successNotifications })
                setError("")
              } catch (cause) {
                setError(
                  cause instanceof Error ? cause.message : "通知设置保存失败"
                )
              } finally {
                setSaving(false)
              }
            }}
          >
            {saving ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <SaveIcon data-icon="inline-start" />
            )}
            保存通知设置
          </Button>
        </div>
        {error ? <FieldError>{error}</FieldError> : null}
      </FieldGroup>
    </SettingsContent>
  )
}

function DisplaySettings({
  preferences,
  onSave,
}: {
  preferences: UserPreferences
  onSave: (input: UserPreferences) => Promise<void>
}) {
  const [next, setNext] = useState(preferences)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  return (
    <SettingsContent
      title="显示"
      description="调整列表密度和主侧栏中显示的功能分组。"
    >
      <FieldGroup>
        <Field>
          <FieldLabel>列表密度</FieldLabel>
          <FieldDescription>
            紧凑模式减少表格行高，在同一屏幕显示更多数据。
          </FieldDescription>
          <ToggleGroup
            type="single"
            variant="outline"
            value={next.density}
            onValueChange={(value) =>
              value &&
              setNext((current) => ({
                ...current,
                density: value as UserPreferences["density"],
              }))
            }
            className="justify-start"
          >
            <ToggleGroupItem value="comfortable">舒适</ToggleGroupItem>
            <ToggleGroupItem value="compact">紧凑</ToggleGroupItem>
          </ToggleGroup>
        </Field>
        <FieldSet>
          <FieldLegend variant="label">侧栏分组</FieldLegend>
          <FieldGroup>
            <SidebarGroupSwitch
              label="能力配置"
              description="Skills、MCP Servers 和模型服务。"
              checked={next.showCapabilities}
              onCheckedChange={(checked) =>
                setNext((current) => ({
                  ...current,
                  showCapabilities: checked,
                }))
              }
            />
            <SidebarGroupSwitch
              label="基础设施"
              description="服务器和镜像。"
              checked={next.showInfrastructure}
              onCheckedChange={(checked) =>
                setNext((current) => ({
                  ...current,
                  showInfrastructure: checked,
                }))
              }
            />
            <SidebarGroupSwitch
              label="管理员入口"
              description="显示用户管理。"
              checked={next.showGovernance}
              onCheckedChange={(checked) =>
                setNext((current) => ({
                  ...current,
                  showGovernance: checked,
                }))
              }
            />
          </FieldGroup>
        </FieldSet>
        <div>
          <Button
            disabled={saving}
            onClick={async () => {
              setSaving(true)
              try {
                await onSave(next)
                setError("")
              } catch (cause) {
                setError(
                  cause instanceof Error ? cause.message : "显示设置保存失败"
                )
              } finally {
                setSaving(false)
              }
            }}
          >
            {saving ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <SaveIcon data-icon="inline-start" />
            )}
            保存显示设置
          </Button>
        </div>
        {error ? <FieldError>{error}</FieldError> : null}
      </FieldGroup>
    </SettingsContent>
  )
}

function SidebarGroupSwitch({
  label,
  description,
  checked,
  onCheckedChange,
}: {
  label: string
  description: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  const id = `settings-${label}`
  return (
    <Field orientation="horizontal" className="rounded-lg border p-4">
      <FieldContent>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <FieldDescription>{description}</FieldDescription>
      </FieldContent>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} />
    </Field>
  )
}

function EnvironmentSettings({
  projectName,
  variableCount,
  onManage,
}: {
  projectName: string
  variableCount: number
  onManage: () => void
}) {
  return (
    <SettingsContent
      title="环境变量"
      description={`管理当前项目“${projectName}”在 Agent 和沙箱运行时使用的变量引用。`}
    >
      <FieldGroup>
        <Alert>
          <KeyRoundIcon />
          <AlertTitle>平台保存引用，不保存宿主机明文</AlertTitle>
          <AlertDescription>
            例如 GITHUB_TOKEN 可以指向 env://GITHUB_TOKEN 或
            secret://GITHUB_TOKEN。环境模板或 Agent
            选中它后，创建沙箱时才解析并注入。
          </AlertDescription>
        </Alert>
        <div className="flex items-center justify-between gap-4 rounded-lg border p-4">
          <div className="flex min-w-0 flex-col gap-1">
            <span className="font-medium">当前项目变量</span>
            <span className="text-sm text-muted-foreground">
              已配置 {variableCount} 个变量引用
            </span>
          </div>
          <Button variant="outline" onClick={onManage}>
            管理环境变量
          </Button>
        </div>
      </FieldGroup>
    </SettingsContent>
  )
}

function roleLabel(role: ManagedUser["role"]) {
  if (role === "admin") return "管理员"
  if (role === "operator") return "运维人员"
  return "只读成员"
}

function formatDate(value: string | null) {
  if (!value) return "尚无记录"
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value))
}
