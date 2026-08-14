"use client"

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  ArchiveIcon,
  BotIcon,
  CheckCircle2Icon,
  CopyIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  RotateCcwIcon,
  Trash2Icon,
} from "lucide-react"
import { useRouter } from "next/navigation"

import { AgentEditorDialog } from "@/components/agent-editor-dialog"
import { AccessManagement } from "@/components/access-management"
import { AppSidebar } from "@/components/app-sidebar"
import {
  CollectionContent,
  CollectionTablePrimaryContent,
} from "@/components/collection-list"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import {
  DashboardView,
  EnvironmentTemplatesView,
  SandboxesView,
} from "@/components/environment-views"
import { ImageManagement } from "@/components/image-management"
import {
  AutomationsView,
  CollectionHeader,
  ResourceView,
} from "@/components/control-plane-view"
import { ResourceEditorDialog } from "@/components/resource-editor-dialog"
import { ServerManagement } from "@/components/server-management"
import { SettingsView } from "@/components/settings-view"
import { SiteHeaderProvider } from "@/components/site-header"
import { UserManagement } from "@/components/user-management"
import {
  agentResponseSchema,
  type Agent,
  type AgentInput,
  type AgentStatus,
} from "@/lib/agent-schema"
import type {
  Credential,
  McpServerDefinition,
  Provider,
  SkillDefinition,
} from "@/lib/catalog"
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
import {
  agentsForProject,
  PROJECT_COOKIE_NAME,
  resourcesForProject,
} from "@/lib/project-scope"
import type { ManagedServer } from "@/lib/server-schema"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
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
  project: { id: string; name: string }
  providers: Provider[]
  credentials: Credential[]
  skills: SkillDefinition[]
  mcpServers: McpServerDefinition[]
}

const statusMeta: Record<AgentStatus, { label: string }> = {
  active: { label: "在线" },
  draft: { label: "草稿" },
  archived: { label: "已归档" },
}

type SectionRenderer = (section: AppSection) => ReactNode

const SectionRendererContext = createContext<SectionRenderer | null>(null)

export function AgentManagementSection({ section }: { section: AppSection }) {
  const renderSection = useContext(SectionRendererContext)
  if (!renderSection) {
    throw new Error(
      "AgentManagementSection must be rendered inside AgentManagement"
    )
  }
  return renderSection(section)
}

export function AgentManagement({
  initialAgents,
  initialResources,
  initialServers,
  initialCredentials,
  currentUser,
  initialUsers,
  initialProjectId,
  catalog,
  children,
}: {
  initialAgents: Agent[]
  initialResources: Resource[]
  initialServers: ManagedServer[]
  initialCredentials: ManagedCredential[]
  currentUser: ManagedUser
  initialUsers: ManagedUser[]
  initialProjectId: string
  catalog: Catalog
  children: ReactNode
}) {
  const router = useRouter()
  const [agents, setAgents] = useState(() => ordered(initialAgents))
  const [resources, setResources] = useState(initialResources)
  const [servers, setServers] = useState(initialServers)
  const [credentials, setCredentials] = useState(initialCredentials)
  const [users, setUsers] = useState(initialUsers)
  const [sessionUser, setSessionUser] = useState(currentUser)
  const [projectId, setProjectId] = useState(initialProjectId)
  const [editor, setEditor] = useState<{ open: boolean; agent: Agent | null }>({
    open: false,
    agent: null,
  })
  const [deleting, setDeleting] = useState<Agent | null>(null)
  const [resourceEditor, setResourceEditor] = useState<{
    kind: ResourceKind
    resource: Resource | null
    initialSpec?: Record<string, unknown>
  } | null>(null)
  const [deletingResource, setDeletingResource] = useState<Resource | null>(
    null
  )
  const [busyId, setBusyId] = useState<string | null>(null)
  const [sandboxBusyId, setSandboxBusyId] = useState<string | null>(null)
  const [userBusyId, setUserBusyId] = useState<string | null>(null)

  const projectResources = resources.filter((item) => item.kind === "project")
  const currentProject = projectResources.find((item) => item.id === projectId)
  const scopedAgents = agentsForProject(agents, projectId)
  const scopedResources = resourcesForProject(resources, projectId)
  const agentCatalog = {
    ...catalog,
    credentials: [
      ...credentials.map((credential) => ({
        id: credential.id,
        name: credential.name,
        providerId: credential.providerId,
        environment: credential.endpoint || "AgentBox 加密密钥库",
        status: credential.enabled
          ? ("configured" as const)
          : ("attention" as const),
        modelId: credential.modelId,
        models: credential.models.map((model) => ({
          id: model.id,
          name: model.name,
        })),
      })),
      ...catalog.credentials.filter(
        (item) => !credentials.some((credential) => credential.id === item.id)
      ),
    ],
    projects: currentProject ? [currentProject] : [],
    runtimes: scopedResources.filter(
      (item) => item.kind === "runtime" && item.enabled
    ),
    variables: scopedResources.filter(
      (item) => item.kind === "variable" && item.enabled
    ),
    skills: scopedResources
      .filter((item) => item.kind === "skill" && item.enabled)
      .map((item) => ({
        id: item.id,
        name: item.name,
        description: item.description,
        version: String(item.spec.version ?? "1.0.0"),
        category: String(item.spec.category ?? "通用"),
      })),
    mcpServers: scopedResources
      .filter((item) => item.kind === "mcp")
      .map((item) => ({
        id: item.id,
        name: item.name,
        description: item.description,
        transport:
          item.spec.transport === "http"
            ? ("http" as const)
            : ("stdio" as const),
        toolCount: Number(item.spec.toolCount ?? 0),
        status: item.enabled ? ("ready" as const) : ("attention" as const),
      })),
  }

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

  async function saveAgent(input: AgentInput, editing: Agent | null) {
    const result = agentResponseSchema.parse(
      await requestJson<unknown>(
        editing ? `/api/agents/${editing.id}` : "/api/agents",
        {
          method: editing ? "PATCH" : "POST",
          body: JSON.stringify(
            editing ? { ...input, version: editing.version } : input
          ),
        }
      )
    )
    setAgents((current) =>
      ordered(
        editing
          ? current.map((item) =>
              item.id === result.agent.id ? result.agent : item
            )
          : [result.agent, ...current]
      )
    )
    setEditor({ open: false, agent: null })
    toast.success(editing ? "智能体已更新" : "智能体已创建", {
      description: result.agent.name,
    })
  }

  async function changeStatus(agent: Agent, status: AgentStatus) {
    setBusyId(agent.id)
    try {
      const result = agentResponseSchema.parse(
        await requestJson<unknown>(`/api/agents/${agent.id}`, {
          method: "PATCH",
          body: JSON.stringify({
            ...agentInput(agent),
            status,
            version: agent.version,
          }),
        })
      )
      setAgents((current) =>
        ordered(
          current.map((item) => (item.id === agent.id ? result.agent : item))
        )
      )
      toast.success(`智能体已设为${statusMeta[status].label}`)
    } catch (error) {
      toast.error("状态更新失败", { description: errorMessage(error) })
    } finally {
      setBusyId(null)
    }
  }

  async function duplicate(agent: Agent) {
    setBusyId(agent.id)
    try {
      const result = agentResponseSchema.parse(
        await requestJson<unknown>(`/api/agents/${agent.id}/duplicate`, {
          method: "POST",
        })
      )
      setAgents((current) => ordered([result.agent, ...current]))
      toast.success("已创建草稿副本", { description: result.agent.name })
    } catch (error) {
      toast.error("复制失败", { description: errorMessage(error) })
    } finally {
      setBusyId(null)
    }
  }

  async function permanentlyDeleteAgent() {
    if (!deleting) return
    const target = deleting
    setBusyId(target.id)
    try {
      await requestJson<void>(`/api/agents/${target.id}`, { method: "DELETE" })
      setAgents((current) => current.filter((item) => item.id !== target.id))
      setDeleting(null)
      toast.success("智能体已永久删除", { description: target.name })
    } catch (error) {
      toast.error("删除失败", { description: errorMessage(error) })
    } finally {
      setBusyId(null)
    }
  }

  async function saveResource(input: ResourceInput) {
    const editing = resourceEditor?.resource
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
    action: "start" | "stop" | "delete" | "login-codex"
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
            : action === "delete"
              ? "删除任务已提交"
              : "Codex 登录已发起",
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
        onCreateSandbox={() =>
          setResourceEditor({ kind: "sandbox", resource: null })
        }
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
          setResourceEditor({
            kind: "sandbox",
            resource: null,
            initialSpec: {
              runtimeId: environment.id,
              serverId: environment.spec.serverId,
            },
          })
        }
      />
    ) : section === "sandboxes" ? (
      <SandboxesView
        key={projectId}
        resources={scopedResources}
        servers={servers}
        onCreate={() => setResourceEditor({ kind: "sandbox", resource: null })}
        busyId={sandboxBusyId}
        onAction={operateSandbox}
        onDelete={setDeletingResource}
      />
    ) : section === "agents" ? (
      <AgentsView
        key={projectId}
        agents={scopedAgents}
        resources={scopedResources}
        busyId={busyId}
        onCreate={() => setEditor({ open: true, agent: null })}
        onEdit={(agent) => setEditor({ open: true, agent })}
        onDuplicate={duplicate}
        onStatusChange={changeStatus}
        onDelete={setDeleting}
      />
    ) : section === "automations" ? (
      <AutomationsView
        key={projectId}
        resources={scopedResources}
        agents={scopedAgents}
        onCreate={(automationKind) =>
          setResourceEditor({ kind: automationKind, resource: null })
        }
        onEdit={(resource) =>
          setResourceEditor({ kind: resource.kind, resource })
        }
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
        agents={scopedAgents}
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
        agents={kind === "project" ? agents : scopedAgents}
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

          {editor.open && (
            <AgentEditorDialog
              key={editor.agent?.id ?? `new:${projectId}`}
              agent={editor.agent}
              catalog={agentCatalog}
              projectId={projectId}
              onOpenChange={(open) =>
                setEditor({ open, agent: open ? editor.agent : null })
              }
              onSave={saveAgent}
            />
          )}

          {resourceEditor && (
            <ResourceEditorDialog
              key={resourceEditor.resource?.id ?? resourceEditor.kind}
              kind={resourceEditor.kind}
              resource={resourceEditor.resource}
              projectId={projectId}
              resources={resources}
              agents={agents}
              servers={servers}
              credentials={credentials}
              initialSpec={resourceEditor.initialSpec}
              onOpenChange={(open) => !open && setResourceEditor(null)}
              onSave={saveResource}
            />
          )}

          <AlertDialog
            open={Boolean(deleting)}
            onOpenChange={(open) => !open && setDeleting(null)}
          >
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogMedia>
                  <Trash2Icon />
                </AlertDialogMedia>
                <AlertDialogTitle>永久删除 {deleting?.name}？</AlertDialogTitle>
                <AlertDialogDescription>
                  此操作会同时删除该智能体的修订历史，无法恢复。仍被沙箱、定时任务或
                  Webhook 引用时，平台会阻止删除。
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>取消</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={permanentlyDeleteAgent}
                  disabled={Boolean(busyId)}
                >
                  {busyId ? (
                    <Spinner data-icon="inline-start" />
                  ) : (
                    <Trash2Icon data-icon="inline-start" />
                  )}
                  永久删除
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>

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

function AgentsView({
  agents,
  resources,
  busyId,
  onCreate,
  onEdit,
  onDuplicate,
  onStatusChange,
  onDelete,
}: {
  agents: Agent[]
  resources: Resource[]
  busyId: string | null
  onCreate: () => void
  onEdit: (agent: Agent) => void
  onDuplicate: (agent: Agent) => void
  onStatusChange: (agent: Agent, status: AgentStatus) => void
  onDelete: (agent: Agent) => void
}) {
  const columns = useMemo(
    () =>
      agentColumns({
        resources,
        busyId,
        onEdit,
        onDuplicate,
        onStatusChange,
        onDelete,
      }),
    [busyId, onDelete, onDuplicate, onEdit, onStatusChange, resources]
  )

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="智能体"
        count={agents.length}
        action={
          <Button size="sm" onClick={onCreate}>
            <PlusIcon data-icon="inline-start" />
            新建智能体
          </Button>
        }
      />

      <CollectionContent>
        {agents.length === 0 ? (
          <Empty className="min-h-72 flex-1 border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BotIcon />
              </EmptyMedia>
              <EmptyTitle>还没有智能体</EmptyTitle>
              <EmptyDescription>
                创建一个智能体，并为它选择默认环境模板与能力。
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" size="sm" onClick={onCreate}>
                创建第一个智能体
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <DataTable
            data={agents}
            columns={columns}
            getRowId={(agent) => agent.id}
            searchPlaceholder="搜索智能体…"
            searchValue={(agent) =>
              `${agent.name} ${agent.slug} ${agent.description}`
            }
            filters={[
              {
                columnId: "status",
                title: "状态",
                options: [
                  { label: "在线", value: "active" },
                  { label: "草稿", value: "draft" },
                  { label: "已归档", value: "archived" },
                ],
              },
            ]}
          />
        )}
      </CollectionContent>
    </section>
  )
}

function agentColumns({
  resources,
  busyId,
  onEdit,
  onDuplicate,
  onStatusChange,
  onDelete,
}: {
  resources: Resource[]
  busyId: string | null
  onEdit: (agent: Agent) => void
  onDuplicate: (agent: Agent) => void
  onStatusChange: (agent: Agent, status: AgentStatus) => void
  onDelete: (agent: Agent) => void
}): ColumnDef<Agent>[] {
  return [
    {
      id: "name",
      accessorFn: (agent) => agent.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="智能体" />
      ),
      cell: ({ row }) => (
        <CollectionTablePrimaryContent
          title={row.original.name}
          description={row.original.description || `/${row.original.slug}`}
          onClick={() => onEdit(row.original)}
          media={
            <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-xs font-semibold text-muted-foreground">
              {row.original.avatar}
            </span>
          }
        />
      ),
      meta: { label: "智能体" },
      enableHiding: false,
    },
    {
      id: "status",
      accessorFn: (agent) => agent.status,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="状态" />
      ),
      cell: ({ row }) => (
        <Badge
          variant={
            row.original.status === "active"
              ? "default"
              : row.original.status === "draft"
                ? "secondary"
                : "outline"
          }
        >
          {statusMeta[row.original.status].label}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "状态" },
    },
    {
      id: "project",
      accessorFn: (agent) =>
        resources.find(
          (item) => item.kind === "project" && item.id === agent.projectId
        )?.name ?? agent.projectId,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="项目" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: { label: "项目", className: "hidden lg:table-cell" },
    },
    {
      id: "runtime",
      accessorFn: (agent) =>
        resources.find(
          (item) => item.kind === "runtime" && item.id === agent.runtimeId
        )?.name ?? agent.runtimeId,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="环境模板" />
      ),
      cell: ({ getValue }) => (
        <span className="block max-w-40 truncate text-muted-foreground">
          {String(getValue())}
        </span>
      ),
      meta: { label: "环境模板", className: "hidden xl:table-cell" },
    },
    {
      id: "updatedAt",
      accessorFn: (agent) => agent.updatedAt,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="最近更新" />
      ),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {updatedLabel(row.original.updatedAt)}
        </span>
      ),
      meta: { label: "最近更新", className: "hidden md:table-cell" },
    },
    {
      id: "actions",
      cell: ({ row }) => (
        <AgentActions
          agent={row.original}
          busy={busyId === row.original.id}
          onEdit={() => onEdit(row.original)}
          onDuplicate={() => onDuplicate(row.original)}
          onStatusChange={(status) => onStatusChange(row.original, status)}
          onDelete={() => onDelete(row.original)}
        />
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-10" },
    },
  ]
}

function AgentActions({
  agent,
  busy,
  onEdit,
  onDuplicate,
  onStatusChange,
  onDelete,
}: {
  agent: Agent
  busy: boolean
  onEdit: () => void
  onDuplicate: () => void
  onStatusChange: (status: AgentStatus) => void
  onDelete: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`${agent.name} 操作`}
          disabled={busy}
        >
          {busy ? <Spinner /> : <MoreHorizontalIcon />}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuGroup>
          <DropdownMenuItem onClick={onEdit}>
            <PencilIcon />
            编辑
          </DropdownMenuItem>
          <DropdownMenuItem onClick={onDuplicate}>
            <CopyIcon />
            创建副本
          </DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          {agent.status !== "active" && agent.status !== "archived" ? (
            <DropdownMenuItem onClick={() => onStatusChange("active")}>
              <CheckCircle2Icon />
              启用
            </DropdownMenuItem>
          ) : null}
          {agent.status === "active" ? (
            <DropdownMenuItem onClick={() => onStatusChange("draft")}>
              <PencilIcon />
              转为草稿
            </DropdownMenuItem>
          ) : null}
          {agent.status !== "archived" ? (
            <DropdownMenuItem onClick={() => onStatusChange("archived")}>
              <ArchiveIcon />
              归档
            </DropdownMenuItem>
          ) : (
            <DropdownMenuItem onClick={() => onStatusChange("draft")}>
              <RotateCcwIcon />
              恢复为草稿
            </DropdownMenuItem>
          )}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={onDelete}>
          <Trash2Icon />
          永久删除
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function agentInput(agent: Agent): AgentInput {
  return {
    projectId: agent.projectId,
    runtimeId: agent.runtimeId,
    name: agent.name,
    slug: agent.slug,
    description: agent.description,
    avatar: agent.avatar,
    providerId: agent.providerId,
    modelId: agent.modelId,
    credentialId: agent.credentialId,
    systemPrompt: agent.systemPrompt,
    skillIds: agent.skillIds,
    mcpServerIds: agent.mcpServerIds,
    variableIds: agent.variableIds,
    customArgs: agent.customArgs,
    temperature: agent.temperature,
    maxSteps: agent.maxSteps,
    concurrency: agent.concurrency,
    sandboxPolicy: agent.sandboxPolicy,
    status: agent.status,
  }
}

function ordered(agents: Agent[]) {
  const weight: Record<AgentStatus, number> = {
    active: 0,
    draft: 1,
    archived: 2,
  }
  return [...agents].sort(
    (a, b) =>
      weight[a.status] - weight[b.status] ||
      b.updatedAt.localeCompare(a.updatedAt)
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

function updatedLabel(value: string) {
  const minutes = Math.floor((Date.now() - new Date(value).getTime()) / 60_000)
  if (minutes < 1) return "刚刚"
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}
