"use client"

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
  type SetStateAction,
} from "react"
import dynamic from "next/dynamic"
import { ShieldXIcon, Trash2Icon } from "lucide-react"
import { usePathname, useRouter } from "next/navigation"

import { AppSidebar } from "@/components/app-sidebar"
import { ResourceEditorDialog } from "@/components/resource-editor-dialog"
import { SandboxEditorDialog } from "@/components/sandbox-editor-dialog"
import { SettingsView } from "@/components/settings-view"
import { SiteHeaderProvider } from "@/components/site-header"
import { catalogSchema } from "@/lib/catalog"
import { observePollingVisibility, usePolling } from "@/hooks/use-polling"
import { PollingController } from "@/lib/polling-controller"
import { LoadState } from "@/components/load-state"
import { Button } from "@/components/ui/button"
import { domainsForEditor, domainsForSection } from "@/lib/console-domains"
import {
  credentialModelsResponseSchema,
  credentialsResponseSchema,
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
import {
  PROJECT_COOKIE_NAME,
  resolveProjectId,
  resourcesForProject,
} from "@/lib/project-scope"
import { serversResponseSchema, type ManagedServer } from "@/lib/server-schema"
import {
  networkProxyCheckResponseSchema,
  networkProxiesResponseSchema,
  networkProxyResponseSchema,
  type ManagedNetworkProxy,
  type NetworkProxyCheckResult,
  type NetworkProxyInput,
} from "@/lib/network-proxy-schema"
import { appToast as toast } from "@/lib/app-toast"
import {
  userResponseSchema,
  usersResponseSchema,
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
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { ApiError, errorMessage, requestJson } from "@/lib/api-client"
import {
  APP_SECTION_PATHS,
  appSectionPath,
  type AppSection,
} from "@/lib/app-section"

// 各管理视图按需加载，避免所有 section 的代码打进首屏 chunk。
const AccessManagement = dynamic(() =>
  import("@/components/access-management").then((mod) => mod.AccessManagement)
)
const AutomationManagement = dynamic(() =>
  import("@/components/automation-management").then(
    (mod) => mod.AutomationManagement
  )
)
const AutomationRunHistory = dynamic(() =>
  import("@/components/automation-management").then(
    (mod) => mod.AutomationRunHistory
  )
)
const ImageManagement = dynamic(() =>
  import("@/components/image-management").then((mod) => mod.ImageManagement)
)
const LogsView = dynamic(() =>
  import("@/components/logs-view").then((mod) => mod.LogsView)
)
const NetworkProxyManagement = dynamic(() =>
  import("@/components/network-proxy-management").then(
    (mod) => mod.NetworkProxyManagement
  )
)
const ServerManagement = dynamic(() =>
  import("@/components/server-management").then((mod) => mod.ServerManagement)
)
const UserManagement = dynamic(() =>
  import("@/components/user-management").then((mod) => mod.UserManagement)
)
const ResourceView = dynamic(() =>
  import("@/components/control-plane-view").then((mod) => mod.ResourceView)
)
const DashboardView = dynamic(() =>
  import("@/components/environment-views").then((mod) => mod.DashboardView)
)
const EnvironmentTemplatesView = dynamic(() =>
  import("@/components/environment-views").then(
    (mod) => mod.EnvironmentTemplatesView
  )
)
const SandboxesView = dynamic(() =>
  import("@/components/environment-views").then((mod) => mod.SandboxesView)
)
const sectionKinds: Partial<
  Record<AppSection, "project" | "runtime" | "skill" | "variable">
> = {
  projects: "project",
  runtimes: "runtime",
  skills: "skill",
  variables: "variable",
}

type SectionRenderer = (section: AppSection) => ReactNode

const SectionRendererContext = createContext<SectionRenderer | null>(null)

function useListPolling<T>(options: Parameters<typeof usePolling<T[]>>[0]) {
  const state = usePolling(options)
  const setData = state.setData
  const setItems = useCallback(
    (update: SetStateAction<T[]>) => {
      setData((current) =>
        typeof update === "function" ? update(current ?? []) : update
      )
    },
    [setData]
  )
  return { ...state, items: state.data ?? [], setItems }
}

function resourcesPollingInterval(
  resources: Resource[] | undefined
): number | false {
  return resources?.some(
    (item) =>
      item.kind === "sandbox" &&
      (["requested", "starting", "stopping", "restarting", "deleting"].includes(
        String(item.spec.status)
      ) ||
        ["queued", "running"].includes(
          String(item.spec.proxyOperation?.status)
        ) ||
        ["queued", "running"].includes(
          String(item.spec.agentToolOperation?.status)
        ))
  )
    ? 1000
    : false
}

const domainLabels = {
  resources: "项目资源",
  servers: "服务器",
  credentials: "模型服务",
  proxies: "网络代理",
  users: "用户",
  catalog: "模型服务目录",
}

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
  initialProjects,
  currentUser,
  initialProjectId,
  children,
}: {
  initialProjects?: Resource[]
  currentUser: ManagedUser
  initialProjectId: string
  children: ReactNode
}) {
  const router = useRouter()
  const pathname = usePathname()
  const [sessionUser, setSessionUser] = useState(currentUser)
  const [selectedProjectId, setProjectId] = useState(initialProjectId)
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

  const isViewer = sessionUser.role === "viewer"
  const isAdmin = sessionUser.role === "admin"
  // viewer 只读；operator 可变更沙箱/模板/自动化/镜像/项目，不能变更服务器/凭据/代理/用户
  const canMutateResources = !isViewer

  const section = (Object.keys(APP_SECTION_PATHS) as AppSection[]).find(
    (key) => APP_SECTION_PATHS[key] === pathname
  )
  const sectionNeeds = domainsForSection(section, isAdmin)
  const editorNeeds = domainsForEditor(
    sandboxEditor ? "sandbox" : resourceEditor?.kind
  )
  const needs = new Set([...sectionNeeds, ...editorNeeds])
  const projectsDomain = useListPolling({
    queryKey: "projects",
    initialData: initialProjects,
    load: async (signal) =>
      resourcesResponseSchema.parse(
        await requestJson<unknown>("/api/resources?kind=project", { signal })
      ).resources,
  })
  const projectResources = projectsDomain.items
  const projectId = resolveProjectId(projectsDomain.data, selectedProjectId)
  const allProjects = section === "projects" || section === "servers"
  const resourcesDomain = useListPolling({
    queryKey: `resources:${allProjects ? "all" : projectId}`,
    enabled: needs.has("resources"),
    interval: resourcesPollingInterval,
    load: async (signal) => {
      if (allProjects)
        return resourcesResponseSchema.parse(
          await requestJson<unknown>("/api/resources", { signal })
        ).resources
      const [scoped, images] = await Promise.all([
        requestJson<unknown>(
          `/api/resources?projectId=${encodeURIComponent(projectId)}`,
          { signal }
        ),
        requestJson<unknown>("/api/resources?kind=image", { signal }),
      ])
      return [
        ...resourcesResponseSchema.parse(scoped).resources,
        ...resourcesResponseSchema.parse(images).resources,
      ]
    },
  })
  const serversDomain = usePolling({
    queryKey: "servers",
    enabled: needs.has("servers"),
    interval: 15000,
    load: async (signal) =>
      serversResponseSchema.parse(
        await requestJson<unknown>("/api/servers", { signal })
      ),
  })
  const credentialsDomain = useListPolling({
    queryKey: "credentials",
    enabled: needs.has("credentials"),
    load: async (signal) =>
      credentialsResponseSchema.parse(
        await requestJson<unknown>("/api/credentials", { signal })
      ).credentials,
  })
  const proxiesDomain = useListPolling({
    queryKey: "proxies",
    enabled: needs.has("proxies"),
    load: async (signal) =>
      networkProxiesResponseSchema.parse(
        await requestJson<unknown>("/api/network-proxies", { signal })
      ).proxies,
  })
  const usersDomain = useListPolling({
    queryKey: "users",
    enabled: isAdmin && needs.has("users"),
    load: async (signal) =>
      usersResponseSchema.parse(
        await requestJson<unknown>("/api/users", { signal })
      ).users,
  })
  const catalogDomain = usePolling({
    queryKey: "catalog",
    enabled: needs.has("catalog"),
    load: async (signal) =>
      catalogSchema.parse(
        await requestJson<unknown>("/api/catalog", { signal })
      ),
  })
  const domains = {
    resources: resourcesDomain,
    servers: serversDomain,
    credentials: credentialsDomain,
    proxies: proxiesDomain,
    users: usersDomain,
    catalog: catalogDomain,
  }
  const resources = [
    ...projectResources,
    ...resourcesDomain.items.filter((item) => item.kind !== "project"),
  ]
  const setResources = resourcesDomain.setItems
  const servers = serversDomain.data?.servers ?? []
  const setServerData = serversDomain.setData
  const setServers = useCallback(
    (update: SetStateAction<ManagedServer[]>) =>
      setServerData((current) => ({
        workerVersion: current?.workerVersion ?? "",
        servers:
          typeof update === "function"
            ? update(current?.servers ?? [])
            : update,
      })),
    [setServerData]
  )
  const refreshServerData = serversDomain.refresh
  const refreshServers = useCallback(async () => {
    if (!(await refreshServerData()))
      throw new Error("服务器信息未能刷新，请重试")
  }, [refreshServerData])
  const credentials = credentialsDomain.items
  const setCredentials = credentialsDomain.setItems
  const proxies = proxiesDomain.items
  const setProxies = proxiesDomain.setItems
  const users = usersDomain.items
  const setUsers = usersDomain.setItems
  const currentProject = projectResources.find((item) => item.id === projectId)
  const scopedResources = resourcesForProject(resources, projectId)
  const editorReady = editorNeeds.every(
    (key) => domains[key].data !== undefined && !domains[key].error
  )
  const editorIssue = editorNeeds.find(
    (key) => domains[key].data === undefined || domains[key].error
  )
  const editorWaiting = editorNeeds.some(
    (key) => domains[key].data === undefined
  )
  const editorDependencyStatus = editorIssue ? (
    <LoadState
      label={domainLabels[editorIssue]}
      {...domains[editorIssue]}
      onRetry={domains[editorIssue].refresh}
    />
  ) : undefined

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
    if (!editorReady) throw new Error("相关配置尚未加载完成，请先重试后再保存")
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
    if (result.resource.kind === "project") {
      projectsDomain.setItems((current) =>
        editing
          ? current.map((item) =>
              item.id === result.resource.id ? result.resource : item
            )
          : [...current, result.resource]
      )
    }
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

  async function checkNetworkProxy(
    proxy: ManagedNetworkProxy,
    serverId: string,
    signal: AbortSignal
  ): Promise<NetworkProxyCheckResult> {
    const result = networkProxyCheckResponseSchema.parse(
      await requestJson<unknown>(`/api/network-proxies/${proxy.id}/check`, {
        method: "POST",
        body: JSON.stringify({ serverId }),
        signal,
      })
    ).result
    signal.throwIfAborted()
    if (result.status === "completed") return result
    const polling = new PollingController(result)
    polling.configure(
      async (requestSignal) =>
        networkProxyCheckResponseSchema.parse(
          await requestJson<unknown>(
            `/api/network-proxies/${proxy.id}/checks/${result.checkId}`,
            { signal: requestSignal }
          )
        ).result,
      750
    )
    const stopObserving = observePollingVisibility(polling)
    try {
      return await new Promise<NetworkProxyCheckResult>((resolve, reject) => {
        const finish = (error?: unknown, value?: NetworkProxyCheckResult) => {
          clearTimeout(timeout)
          unsubscribe()
          signal.removeEventListener("abort", abort)
          polling.stop()
          if (error) reject(error)
          else resolve(value!)
        }
        const abort = () =>
          finish(signal.reason || new DOMException("已取消检测", "AbortError"))
        const timeout = setTimeout(
          () => finish(new Error("Worker 检测超时，请确认 Worker 在线状态")),
          30000
        )
        const unsubscribe = polling.subscribe(() => {
          const state = polling.getSnapshot()
          if (state.data?.status === "completed") finish(undefined, state.data)
          else if (
            state.error &&
            !(state.error instanceof TypeError) &&
            !(state.error instanceof ApiError && state.error.retryable)
          )
            finish(state.error)
        })
        signal.addEventListener("abort", abort, { once: true })
        polling.start()
      })
    } finally {
      stopObserving()
      polling.stop()
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
        `/api/credentials/${credential.id}/models/${encodeURIComponent(model.id)}`,
        { method: "DELETE" }
      )
    )
    updateCredentialModels(credential.id, result.models)
    toast.success("模型已删除", { description: model.name })
    return result.models
  }

  const resourceRefresh = resourcesDomain.refresh
  const refreshResources = useCallback(async () => {
    await resourceRefresh()
  }, [resourceRefresh])

  const handleSandboxResourceChange = useCallback(
    (resource: Resource) => {
      setResources((current) =>
        current.map((item) => (item.id === resource.id ? resource : item))
      )
    },
    [setResources]
  )

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
      if (target.kind === "project")
        projectsDomain.setItems((current) =>
          current.filter((item) => item.id !== target.id)
        )
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
      description: `@${result.user.username}`,
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
      toast.success("用户已删除", { description: `@${user.username}` })
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
    const missing = domainsForSection(section, isAdmin).find(
      (key) => domains[key].data === undefined
    )
    if (missing)
      return (
        <LoadState
          label={domainLabels[missing]}
          {...domains[missing]}
          onRetry={domains[missing].refresh}
        />
      )

    return section === "overview" ? (
      <DashboardView
        key={projectId}
        resources={scopedResources}
        servers={servers}
        configuredCredentials={
          credentials.filter((credential) => credential.enabled).length
        }
        canMutate={canMutateResources}
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
        canMutate={canMutateResources}
        dependenciesReady={!resourcesDomain.error && !credentialsDomain.error}
      />
    ) : section === "automationRuns" ? (
      <AutomationRunHistory key={projectId} projectId={projectId} />
    ) : section === "runtimes" ? (
      <EnvironmentTemplatesView
        key={projectId}
        resources={scopedResources}
        servers={servers}
        canMutate={canMutateResources}
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
        canMutate={canMutateResources}
        canOpenWorkspace={!isViewer}
        onCreate={() => setSandboxEditor({ resource: null })}
        onEdit={(resource) => setSandboxEditor({ resource })}
        busyId={sandboxBusyId}
        onAction={operateSandbox}
        onDelete={setDeletingResource}
        onResourceChange={handleSandboxResourceChange}
        onRefresh={refreshResources}
      />
    ) : section === "servers" ? (
      <ServerManagement
        servers={servers}
        runtimes={resources.filter((item) => item.kind === "runtime")}
        canMutate={isAdmin}
        canMutateRuntimes={canMutateResources}
        onServersChange={setServers}
        onRefresh={refreshServers}
        targetWorkerVersion={serversDomain.data?.workerVersion ?? ""}
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
        canMutate={canMutateResources}
        onRefresh={refreshServers}
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
        providers={catalogDomain.data?.providers ?? []}
        canMutate={isAdmin}
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
        servers={servers}
        canMutate={isAdmin}
        onSave={saveNetworkProxy}
        onCheck={checkNetworkProxy}
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
        canMutate={canMutateResources}
        onCreate={() => setResourceEditor({ kind: "mcp", resource: null })}
        onEdit={(resource) =>
          setResourceEditor({ kind: resource.kind, resource })
        }
        onDelete={setDeletingResource}
      />
    ) : section === "logs" ? (
      isAdmin ? (
        <LogsView />
      ) : (
        <section className="flex min-h-0 flex-1 flex-col">
          <Empty className="min-h-0 flex-1 rounded-none border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ShieldXIcon />
              </EmptyMedia>
              <EmptyTitle>没有权限访问日志</EmptyTitle>
              <EmptyDescription>
                当前角色为
                {sessionUser.role === "operator" ? "运维人员" : "只读成员"}
                ，日志仅对管理员开放。
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        </section>
      )
    ) : section === "users" ? (
      isAdmin ? (
        <UserManagement
          users={users}
          currentUser={sessionUser}
          busyId={userBusyId}
          onSave={saveUser}
          onDelete={deleteUser}
        />
      ) : (
        <section className="flex min-h-0 flex-1 flex-col">
          <Empty className="min-h-0 flex-1 rounded-none border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ShieldXIcon />
              </EmptyMedia>
              <EmptyTitle>没有权限访问用户管理</EmptyTitle>
              <EmptyDescription>
                当前角色为
                {sessionUser.role === "operator" ? "运维人员" : "只读成员"}
                ，用户管理仅对管理员开放。
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        </section>
      )
    ) : kind ? (
      <ResourceView
        key={kind === "project" ? "projects" : `${kind}:${projectId}`}
        kind={kind}
        resources={kind === "project" ? resources : scopedResources}
        servers={servers}
        canMutate={canMutateResources}
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
              {(projectsDomain.data === undefined ||
                Boolean(projectsDomain.error)) && (
                <LoadState
                  label="项目"
                  {...projectsDomain}
                  onRetry={projectsDomain.refresh}
                />
              )}
              {sectionNeeds
                .filter((key) => domains[key].stale)
                .map((key) => (
                  <LoadState
                    key={key}
                    label={domainLabels[key]}
                    {...domains[key]}
                    onRetry={domains[key].refresh}
                  />
                ))}
              {editorWaiting && (
                <div className="px-4">
                  {editorDependencyStatus}
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setResourceEditor(null)
                      setSandboxEditor(null)
                    }}
                  >
                    取消编辑
                  </Button>
                </div>
              )}
              {children}
            </div>
          </SidebarInset>

          {resourceEditor &&
            editorNeeds.every((key) => domains[key].data !== undefined) && (
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
                dependenciesReady={editorReady}
                dependencyStatus={editorDependencyStatus}
              />
            )}

          {sandboxEditor &&
            editorNeeds.every((key) => domains[key].data !== undefined) && (
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
                dependenciesReady={editorReady}
                dependencyStatus={editorDependencyStatus}
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
