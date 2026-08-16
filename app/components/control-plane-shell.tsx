"use client"

import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react"
import { Trash2Icon } from "lucide-react"
import { useRouter } from "next/navigation"

import { AccessManagement } from "@/components/access-management"
import { AppSidebar } from "@/components/app-sidebar"
import { AutomationManagement } from "@/components/automation-management"
import {
  DashboardView,
  EnvironmentTemplatesView,
  SandboxesView,
} from "@/components/environment-views"
import { ImageManagement } from "@/components/image-management"
import { NetworkProxyManagement } from "@/components/network-proxy-management"
import { ResourceView } from "@/components/control-plane-view"
import { ResourceEditorDialog } from "@/components/resource-editor-dialog"
import { SandboxEditorDialog } from "@/components/sandbox-editor-dialog"
import { ServerManagement } from "@/components/server-management"
import { SettingsView } from "@/components/settings-view"
import { SiteHeaderProvider } from "@/components/site-header"
import { UserManagement } from "@/components/user-management"
import type { Provider } from "@/lib/catalog"
import {
  credentialModelsResponseSchema,
  credentialResponseSchema,
  type CredentialInput,
  type CredentialModel,
  type ManagedCredential,
} from "@/lib/credential-schema"
import {
  resourceResponseSchema,
  resourcesResponseSchema,
  type Resource,
  type ResourceInput,
  type ResourceKind,
} from "@/lib/platform-schema"
import { PROJECT_COOKIE_NAME, resourcesForProject } from "@/lib/project-scope"
import type { ManagedServer } from "@/lib/server-schema"
import {
  networkProxyResponseSchema,
  type ManagedNetworkProxy,
  type NetworkProxyInput,
} from "@/lib/network-proxy-schema"
import { appToast as toast } from "@/lib/app-toast"
import {
  userResponseSchema,
  type ManagedUser,
  type UserInput,
  type UserPreferences,
} from "@/lib/user-schema"
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
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { appSectionPath, type AppSection } from "@/lib/app-section"

const sectionKinds: Partial<
  Record<AppSection, "project" | "runtime" | "skill" | "variable">
> = {
  projects: "project",
  runtimes: "runtime",
  skills: "skill",
  variables: "variable",
}

type Catalog = {
  providers: Provider[]
}

type SectionRenderer = (section: AppSection) => ReactNode

const SectionRendererContext = createContext<SectionRenderer | null>(null)

export function ControlPlaneSection({ section }: { section: AppSection }) {
  const renderSection = useContext(SectionRendererContext)
  if (!renderSection) {
    throw new Error(
      "ControlPlaneSection must be rendered inside ControlPlaneShell"
    )
  }
  return renderSection(section)
}

export function ControlPlaneShell({
  initialResources,
  initialServers,
  initialCredentials,
  initialProxies,
  currentUser,
  initialUsers,
  initialProjectId,
  catalog,
  children,
}: {
  initialResources: Resource[]
  initialServers: ManagedServer[]
  initialCredentials: ManagedCredential[]
  initialProxies: ManagedNetworkProxy[]
  currentUser: ManagedUser
  initialUsers: ManagedUser[]
  initialProjectId: string
  catalog: Catalog
  children: ReactNode
}) {
  const router = useRouter()
  const [resources, setResources] = useState(initialResources)
  const [servers, setServers] = useState(initialServers)
  const [credentials, setCredentials] = useState(initialCredentials)
  const [proxies, setProxies] = useState(initialProxies)
  const [users, setUsers] = useState(initialUsers)
  const [sessionUser, setSessionUser] = useState(currentUser)
  const [projectId, setProjectId] = useState(initialProjectId)
  const [resourceEditor, setResourceEditor] = useState<{
    kind: ResourceKind
    resource: Resource | null
    initialSpec?: Record<string, unknown>
  } | null>(null)
  const [sandboxEditor, setSandboxEditor] = useState<{
    resource: Resource | null
    initialRuntimeId?: string
  } | null>(null)
  const [deletingResource, setDeletingResource] = useState<Resource | null>(
    null
  )
  const [sandboxBusyId, setSandboxBusyId] = useState<string | null>(null)
  const [userBusyId, setUserBusyId] = useState<string | null>(null)

  const projectResources = resources.filter((item) => item.kind === "project")
  const currentProject = projectResources.find((item) => item.id === projectId)
  const scopedResources = resourcesForProject(resources, projectId)

  useEffect(() => {
    const secure = window.location.protocol === "https:" ? "; Secure" : ""
    document.cookie = `${PROJECT_COOKIE_NAME}=${encodeURIComponent(projectId)}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`
  }, [projectId])

  useEffect(() => {
    document.documentElement.dataset.density = sessionUser.preferences.density
    document.documentElement.dataset.successNotifications = String(
      sessionUser.preferences.successNotifications
    )
  }, [sessionUser.preferences])

  function selectProject(nextProjectId: string, notify = true) {
    const project = projectResources.find((item) => item.id === nextProjectId)
    if (!project || project.id === projectId) return
    setProjectId(project.id)
    if (notify) {
      toast.success("已切换项目", { description: project.name })
    }
  }

  async function saveResource(
    input: ResourceInput,
    editing: Resource | null = resourceEditor?.resource ?? null
  ) {
    const result = resourceResponseSchema.parse(
      await requestJson<unknown>(
        editing ? `/api/resources/${editing.id}` : "/api/resources",
        {
          method: editing ? "PATCH" : "POST",
          body: JSON.stringify(input),
        }
      )
    )
    setResources((current) =>
      editing
        ? current.map((item) =>
            item.id === result.resource.id ? result.resource : item
          )
        : [...current, result.resource]
    )
    setResourceEditor(null)
    if (input.kind === "project" && !editing) {
      setProjectId(result.resource.id)
    }
    toast.success(editing ? "配置已更新" : "配置已创建", {
      description: result.resource.name,
    })
  }

  async function saveSandbox(input: ResourceInput) {
    await saveResource(input, sandboxEditor?.resource ?? null)
    setSandboxEditor(null)
  }

  async function saveCredential(
    input: CredentialInput,
    editing: ManagedCredential | null
  ): Promise<ManagedCredential> {
    const result = credentialResponseSchema.parse(
      await requestJson<unknown>(
        editing ? `/api/credentials/${editing.id}` : "/api/credentials",
        {
          method: editing ? "PATCH" : "POST",
          body: JSON.stringify(input),
        }
      )
    )
    setCredentials((current) =>
      editing
        ? current.map((item) =>
            item.id === result.credential.id ? result.credential : item
          )
        : [result.credential, ...current]
    )
    toast.success(editing ? "API Key 已更新" : "API Key 已加密保存", {
      description: result.credential.name,
    })
    return result.credential
  }

  async function deleteCredential(credential: ManagedCredential) {
    try {
      await requestJson<void>(`/api/credentials/${credential.id}`, {
        method: "DELETE",
      })
      setCredentials((current) =>
        current.filter((item) => item.id !== credential.id)
      )
      toast.success("API Key 已永久删除", { description: credential.name })
    } catch (error) {
      toast.error("无法删除 API Key", { description: errorMessage(error) })
      throw error
    }
  }

  async function saveNetworkProxy(
    input: NetworkProxyInput,
    editing: ManagedNetworkProxy | null
  ): Promise<ManagedNetworkProxy> {
    const result = networkProxyResponseSchema.parse(
      await requestJson<unknown>(
        editing ? `/api/network-proxies/${editing.id}` : "/api/network-proxies",
        {
          method: editing ? "PATCH" : "POST",
          body: JSON.stringify(input),
        }
      )
    )
    setProxies((current) =>
      editing
        ? current.map((item) =>
            item.id === result.proxy.id ? result.proxy : item
          )
        : [result.proxy, ...current]
    )
    toast.success(editing ? "代理已更新" : "代理凭据已加密保存", {
      description: result.proxy.name,
    })
    return result.proxy
  }

  async function deleteNetworkProxy(proxy: ManagedNetworkProxy) {
    try {
      await requestJson<void>(`/api/network-proxies/${proxy.id}`, {
        method: "DELETE",
      })
      setProxies((current) => current.filter((item) => item.id !== proxy.id))
      toast.success("代理已删除", { description: proxy.name })
    } catch (error) {
      toast.error("无法删除代理", { description: errorMessage(error) })
      throw error
    }
  }

  async function checkCredential(credential: ManagedCredential) {
    const result = credentialResponseSchema.parse(
      await requestJson<unknown>(`/api/credentials/${credential.id}/check`, {
        method: "POST",
      })
    )
    setCredentials((current) =>
      current.map((item) =>
        item.id === result.credential.id ? result.credential : item
      )
    )
    if (result.credential.lastCheckOk) {
      toast.success("Provider 连接正常", { description: credential.name })
    } else {
      toast.error("Provider 验证失败", {
        description: result.credential.lastCheckError || credential.name,
      })
    }
  }

  function updateCredentialModels(
    credentialId: string,
    models: CredentialModel[]
  ) {
    setCredentials((current) =>
      current.map((item) =>
        item.id === credentialId ? { ...item, models } : item
      )
    )
  }

  async function pullCredentialModels(
    credential: ManagedCredential
  ): Promise<CredentialModel[]> {
    const result = credentialModelsResponseSchema.parse(
      await requestJson<unknown>(
        `/api/credentials/${credential.id}/models/pull`,
        { method: "POST" }
      )
    )
    updateCredentialModels(credential.id, result.models)
    toast.success(`已获取 ${result.models.length} 个模型`, {
      description: credential.name,
    })
    return result.models
  }

  async function addCredentialModel(
    credential: ManagedCredential,
    input: { id: string; name: string }
  ): Promise<CredentialModel[]> {
    const result = credentialModelsResponseSchema.parse(
      await requestJson<unknown>(`/api/credentials/${credential.id}/models`, {
        method: "POST",
        body: JSON.stringify(input),
      })
    )
    updateCredentialModels(credential.id, result.models)
    toast.success("模型已添加", { description: input.name || input.id })
    return result.models
  }

  async function deleteCredentialModel(
    credential: ManagedCredential,
    model: CredentialModel
  ): Promise<CredentialModel[]> {
    const result = credentialModelsResponseSchema.parse(
      await requestJson<unknown>(
        `/api/credentials/${credential.id}/models?modelId=${encodeURIComponent(model.id)}`,
        { method: "DELETE" }
      )
    )
    updateCredentialModels(credential.id, result.models)
    toast.success("模型已删除", { description: model.name })
    return result.models
  }

  async function refreshResources() {
    const result = resourcesResponseSchema.parse(
      await requestJson<unknown>("/api/resources")
    )
    setResources(result.resources)
  }

  async function operateSandbox(
    sandbox: Resource,
    action: "start" | "stop" | "restart" | "delete"
  ) {
    setSandboxBusyId(sandbox.id)
    try {
      const result = resourceResponseSchema.parse(
        await requestJson<unknown>(
          `/api/sandboxes/${sandbox.id}/actions/${action}`,
          { method: "POST" }
        )
      )
      setResources((current) =>
        current.map((item) =>
          item.id === result.resource.id ? result.resource : item
        )
      )
      toast.success(
        action === "start"
          ? "启动任务已提交"
          : action === "stop"
            ? "停止任务已提交"
            : action === "restart"
              ? "重启任务已提交"
              : "删除任务已提交",
        { description: sandbox.name }
      )
      window.setTimeout(() => void refreshResources(), 6000)
      window.setTimeout(() => void refreshResources(), 15000)
    } catch (error) {
      toast.error("沙箱操作失败", { description: errorMessage(error) })
    } finally {
      setSandboxBusyId(null)
    }
  }

  async function permanentlyDeleteResource() {
    if (!deletingResource) return
    const target = deletingResource
    if (target.kind === "sandbox") {
      setDeletingResource(null)
      await operateSandbox(target, "delete")
      return
    }
    try {
      await requestJson<void>(`/api/resources/${target.id}`, {
        method: "DELETE",
      })
      setResources((current) => current.filter((item) => item.id !== target.id))
      if (target.kind === "project" && target.id === projectId) {
        const next = resources.find(
          (item) => item.kind === "project" && item.id !== target.id
        )
        setProjectId(next?.id ?? "default")
      }
      setDeletingResource(null)
      toast.success("配置已删除", { description: target.name })
    } catch (error) {
      toast.error("删除失败", { description: errorMessage(error) })
    }
  }

  async function saveUser(input: UserInput, editing: ManagedUser | null) {
    const result = userResponseSchema.parse(
      await requestJson<unknown>(
        editing ? `/api/users/${editing.id}` : "/api/users",
        {
          method: editing ? "PATCH" : "POST",
          body: JSON.stringify(input),
        }
      )
    )
    setUsers((current) =>
      editing
        ? current.map((user) =>
            user.id === result.user.id ? result.user : user
          )
        : [result.user, ...current]
    )
    if (result.user.id === sessionUser.id) {
      setSessionUser(result.user)
      if (input.password) {
        toast.success("密码已更新，请重新登录")
        await logout()
        return
      }
    }
    toast.success(editing ? "用户已更新" : "用户已创建", {
      description: result.user.email,
    })
  }

  async function saveCurrentUser(input: UserInput) {
    const result = userResponseSchema.parse(
      await requestJson<unknown>("/api/auth/me", {
        method: "PATCH",
        body: JSON.stringify(input),
      })
    )
    setSessionUser(result.user)
    setUsers((current) =>
      current.map((user) => (user.id === result.user.id ? result.user : user))
    )
    if (input.password) {
      toast.success("密码已更新，请重新登录")
      await logout()
      return
    }
    toast.success("个人资料已更新")
  }

  async function saveCurrentUserPreferences(input: UserPreferences) {
    const result = userResponseSchema.parse(
      await requestJson<unknown>("/api/auth/preferences", {
        method: "PATCH",
        body: JSON.stringify(input),
      })
    )
    setSessionUser(result.user)
    setUsers((current) =>
      current.map((user) => (user.id === result.user.id ? result.user : user))
    )
    if (result.user.preferences.successNotifications) {
      toast.success("设置已保存")
    }
  }

  async function deleteUser(user: ManagedUser) {
    setUserBusyId(user.id)
    try {
      await requestJson<void>(`/api/users/${user.id}`, { method: "DELETE" })
      setUsers((current) => current.filter((item) => item.id !== user.id))
      toast.success("用户已删除", { description: user.email })
    } catch (error) {
      toast.error("删除用户失败", { description: errorMessage(error) })
      throw error
    } finally {
      setUserBusyId(null)
    }
  }

  async function logout() {
    try {
      await fetch("/api/auth/logout", { method: "POST" })
    } finally {
      window.location.assign("/login")
    }
  }

  function navigate(section: AppSection) {
    router.push(appSectionPath(section))
  }

  function renderSection(section: AppSection) {
    const kind = sectionKinds[section]

    return section === "overview" ? (
      <DashboardView
        key={projectId}
        resources={scopedResources}
        servers={servers}
        configuredCredentials={
          credentials.filter((credential) => credential.enabled).length
        }
        onNavigate={navigate}
        onCreateEnvironment={() =>
          setResourceEditor({ kind: "runtime", resource: null })
        }
        onCreateSandbox={() => setSandboxEditor({ resource: null })}
      />
    ) : section === "automations" ? (
      <AutomationManagement
        key={projectId}
        projectId={projectId}
        resources={resources}
        credentials={credentials}
      />
    ) : section === "runtimes" ? (
      <EnvironmentTemplatesView
        key={projectId}
        resources={scopedResources}
        servers={servers}
        onCreate={() => setResourceEditor({ kind: "runtime", resource: null })}
        onEdit={(resource) => setResourceEditor({ kind: "runtime", resource })}
        onDelete={setDeletingResource}
        onLaunch={(environment) =>
          setSandboxEditor({
            resource: null,
            initialRuntimeId: environment.id,
          })
        }
      />
    ) : section === "sandboxes" ? (
      <SandboxesView
        key={projectId}
        resources={scopedResources}
        servers={servers}
        onCreate={() => setSandboxEditor({ resource: null })}
        onEdit={(resource) => setSandboxEditor({ resource })}
        busyId={sandboxBusyId}
        onAction={operateSandbox}
        onDelete={setDeletingResource}
      />
    ) : section === "servers" ? (
      <ServerManagement
        servers={servers}
        runtimes={resources.filter((item) => item.kind === "runtime")}
        onServersChange={setServers}
        onCreateRuntime={(serverId) =>
          setResourceEditor({
            kind: "runtime",
            resource: null,
            initialSpec: { serverId },
          })
        }
        onEditRuntime={(resource) =>
          setResourceEditor({ kind: "runtime", resource })
        }
      />
    ) : section === "images" ? (
      <ImageManagement
        servers={servers}
        onServersChange={setServers}
        onCreateRuntime={(serverId, imageReference, driver) =>
          setResourceEditor({
            kind: "runtime",
            resource: null,
            initialSpec: { serverId, imageReference, driver },
          })
        }
      />
    ) : section === "access" ? (
      <AccessManagement
        credentials={credentials}
        providers={catalog.providers}
        onSave={saveCredential}
        onCheck={checkCredential}
        onPullModels={pullCredentialModels}
        onAddModel={addCredentialModel}
        onDeleteModel={deleteCredentialModel}
        onDelete={deleteCredential}
      />
    ) : section === "proxies" ? (
      <NetworkProxyManagement
        proxies={proxies}
        onSave={saveNetworkProxy}
        onDelete={deleteNetworkProxy}
      />
    ) : section === "settings" ? (
      <SettingsView
        user={sessionUser}
        projectName={currentProject?.name ?? projectId}
        variableCount={
          scopedResources.filter((item) => item.kind === "variable").length
        }
        onSaveUser={saveCurrentUser}
        onSavePreferences={saveCurrentUserPreferences}
        onManageVariables={() => navigate("variables")}
      />
    ) : section === "mcp" ? (
      <ResourceView
        key={projectId}
        resources={scopedResources}
        kind="mcp"
        servers={servers}
        onCreate={() => setResourceEditor({ kind: "mcp", resource: null })}
        onEdit={(resource) =>
          setResourceEditor({ kind: resource.kind, resource })
        }
        onDelete={setDeletingResource}
      />
    ) : section === "users" && sessionUser.role === "admin" ? (
      <UserManagement
        users={users}
        currentUser={sessionUser}
        busyId={userBusyId}
        onSave={saveUser}
        onDelete={deleteUser}
      />
    ) : kind ? (
      <ResourceView
        key={kind === "project" ? "projects" : `${kind}:${projectId}`}
        kind={kind}
        resources={kind === "project" ? resources : scopedResources}
        servers={servers}
        onCreate={() => setResourceEditor({ kind, resource: null })}
        onEdit={(resource) =>
          setResourceEditor({ kind: resource.kind, resource })
        }
        onDelete={setDeletingResource}
      />
    ) : null
  }

  return (
    <SectionRendererContext.Provider value={renderSection}>
      <SidebarProvider className="h-svh min-h-0 overflow-hidden">
        <SiteHeaderProvider
          user={sessionUser}
          onSettings={() => navigate("settings")}
          onManageUsers={() => navigate("users")}
          onLogout={() => void logout()}
        >
          <a
            href="#main-content"
            className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-50 focus:rounded-lg focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:ring-2 focus:ring-ring"
          >
            跳到主要内容
          </a>
          <AppSidebar
            currentUser={sessionUser}
            projects={projectResources}
            projectId={projectId}
            onProjectChange={selectProject}
          />
          <SidebarInset
            id="main-content"
            tabIndex={-1}
            className="min-w-0 overflow-hidden focus:outline-none"
          >
            <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
              {children}
            </div>
          </SidebarInset>

          {resourceEditor && (
            <ResourceEditorDialog
              key={resourceEditor.resource?.id ?? resourceEditor.kind}
              kind={resourceEditor.kind}
              resource={resourceEditor.resource}
              projectId={projectId}
              resources={resources}
              servers={servers}
              credentials={credentials}
              proxies={proxies}
              initialSpec={resourceEditor.initialSpec}
              onOpenChange={(open) => !open && setResourceEditor(null)}
              onSave={saveResource}
            />
          )}

          {sandboxEditor && (
            <SandboxEditorDialog
              key={
                sandboxEditor.resource?.id ??
                sandboxEditor.initialRuntimeId ??
                "new"
              }
              resource={sandboxEditor.resource}
              projectId={projectId}
              resources={resources}
              servers={servers}
              credentials={credentials}
              proxies={proxies}
              initialRuntimeId={sandboxEditor.initialRuntimeId}
              onOpenChange={(open) => !open && setSandboxEditor(null)}
              onSave={saveSandbox}
            />
          )}

          <AlertDialog
            open={Boolean(deletingResource)}
            onOpenChange={(open) => !open && setDeletingResource(null)}
          >
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogMedia>
                  <Trash2Icon />
                </AlertDialogMedia>
                <AlertDialogTitle>
                  删除 {deletingResource?.name}？
                </AlertDialogTitle>
                <AlertDialogDescription>
                  {deletingResource?.kind === "sandbox"
                    ? "沙箱容器、独立工作区卷和控制面记录都会被永久删除，无法恢复。"
                    : "该配置会从 PostgreSQL 永久删除，无法恢复。"}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>取消</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={permanentlyDeleteResource}
                >
                  永久删除
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </SiteHeaderProvider>
      </SidebarProvider>
    </SectionRendererContext.Provider>
  )
}

async function requestJson<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: { "Content-Type": "application/json", ...options?.headers },
  })
  if (response.status === 401) {
    window.location.assign("/login")
    throw new Error("登录状态已过期")
  }
  if (!response.ok) {
    const body = (await response
      .json()
      .catch(() => ({ error: "请求失败" }))) as { error?: string }
    throw new Error(body.error || "请求失败")
  }
  return response.status === 204 ? (undefined as T) : response.json()
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "请稍后重试"
}
