"use client"

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type DragEvent,
} from "react"
import {
  ArrowLeftIcon,
  BoxIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  ClipboardPasteIcon,
  Code2Icon,
  CopyIcon,
  FileIcon,
  FileUpIcon,
  FolderIcon,
  FolderOpenIcon,
  InfoIcon,
  MonitorIcon,
  PanelBottomIcon,
  PanelLeftIcon,
  PanelRightIcon,
  PanelTopCloseIcon,
  PanelTopIcon,
  RefreshCwIcon,
  SaveIcon,
  SearchIcon,
  ShieldCheckIcon,
  TerminalIcon,
  WifiIcon,
  XIcon,
} from "lucide-react"
import Link from "next/link"

import { usePolling } from "@/hooks/use-polling"
import { LoadState } from "@/components/load-state"
import { requestJson } from "@/lib/api-client"
import {
  resourceResponseSchema,
  type ResourceOfKind,
} from "@/lib/platform-schema"
import { serversResponseSchema, type ManagedServer } from "@/lib/server-schema"
import { SandboxCodeEditor } from "@/components/sandbox-code-editor"
import { SandboxDesktop } from "@/components/sandbox-desktop"
import { SandboxModelSourceSwitcher } from "@/components/sandbox-model-source-switcher"
import {
  SandboxTerminal,
  type SandboxTerminalHandle,
} from "@/components/sandbox-terminal"
import { SiteHeader, useSiteHeaderUser } from "@/components/site-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuGroup,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { Progress } from "@/components/ui/progress"
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { appToast as toast } from "@/lib/app-toast"
import { supportedAgentToolList } from "@/lib/agent-tools"
import { errorMessage } from "@/lib/api-client"
import { writeClipboardText } from "@/lib/clipboard"
import {
  credentialsResponseSchema,
  type ManagedCredential,
} from "@/lib/credential-schema"
import {
  sandboxFileEntriesSchema,
  type SandboxFileEntry,
} from "@/lib/sandbox-file-schema"
import { isSandboxDesktopEnabled } from "@/lib/sandbox-desktop"
import { createLatestByKeyQueue, enqueueLatestByKey } from "@/lib/latest-by-key"
import { useNavigationBlocker } from "@/lib/navigation-blocker"
import {
  SandboxSessionClient,
  sandboxFileReadMaxSize,
  sandboxUploadMaxSize,
  type SandboxSessionState,
} from "@/lib/sandbox-session"
import { cn } from "@/lib/utils"

type WorkspaceInput =
  | { kind: "list"; path: string }
  | { kind: "read"; path: string }
  | { kind: "write"; path: string; content: string }

type OpenFile = {
  path: string
  name: string
  content: string
  savedContent: string
  size: number
}

type UploadProgress = {
  fileName: string
  fileIndex: number
  fileCount: number
  percent: number
}

export function SandboxWorkspace({ sandboxId }: { sandboxId: string }) {
  const sandboxData = usePolling({
    queryKey: `workspace:${sandboxId}`,
    interval: 15000,
    load: async (signal) => {
      const resource = resourceResponseSchema.parse(
        await requestJson<unknown>(
          `/api/resources/${encodeURIComponent(sandboxId)}`,
          { signal }
        )
      ).resource
      if (resource.kind !== "sandbox") throw new Error("该资源不是沙箱")
      return resource
    },
  })
  const runtimeId = sandboxData.data?.spec.runtimeId
  const runtimeData = usePolling({
    queryKey: `workspace-runtime:${runtimeId ?? ""}`,
    enabled: typeof runtimeId === "string" && runtimeId !== "",
    load: async (signal) => {
      const resource = resourceResponseSchema.parse(
        await requestJson<unknown>(
          `/api/resources/${encodeURIComponent(String(runtimeId))}`,
          { signal }
        )
      ).resource
      if (resource.kind !== "runtime") throw new Error("该资源不是沙箱模板")
      return resource
    },
  })
  const serverData = usePolling({
    queryKey: `workspace-servers:${sandboxId}`,
    enabled: sandboxData.data !== undefined,
    load: async (signal) =>
      serversResponseSchema.parse(
        await requestJson<unknown>("/api/servers", { signal })
      ).servers,
  })
  const credentialsData = usePolling({
    queryKey: `workspace-credentials:${sandboxId}`,
    enabled: sandboxData.data?.spec.status === "running",
    load: async (signal) =>
      credentialsResponseSchema.parse(
        await requestJson<unknown>("/api/credentials", { signal })
      ).credentials,
  })
  if (!sandboxData.data)
    return (
      <LoadState label="沙箱" {...sandboxData} onRetry={sandboxData.refresh} />
    )
  return (
    <>
      {Boolean(sandboxData.error) && (
        <LoadState
          label="沙箱"
          {...sandboxData}
          onRetry={sandboxData.refresh}
        />
      )}
      {Boolean(runtimeData.error) && (
        <LoadState
          label="沙箱模板"
          {...runtimeData}
          onRetry={runtimeData.refresh}
        />
      )}
      {Boolean(serverData.error) && (
        <LoadState
          label="运行服务器"
          {...serverData}
          onRetry={serverData.refresh}
        />
      )}
      <SandboxWorkspaceContent
        key={sandboxId}
        sandboxId={sandboxId}
        sandbox={sandboxData.data}
        runtime={runtimeData.data}
        server={serverData.data?.find(
          (item) => item.id === sandboxData.data?.spec.serverId
        )}
        credentials={credentialsData.data ?? []}
        credentialsLoading={
          credentialsData.loading ||
          (credentialsData.data === undefined && !credentialsData.error)
        }
        credentialsError={credentialsData.error}
        onRetryCredentials={() => void credentialsData.refresh()}
        onSandboxChange={sandboxData.setData}
      />
    </>
  )
}

function SandboxWorkspaceContent({
  sandboxId,
  sandbox,
  runtime,
  server,
  credentials,
  credentialsLoading,
  credentialsError,
  onRetryCredentials,
  onSandboxChange,
}: {
  sandboxId: string
  sandbox: ResourceOfKind<"sandbox">
  runtime?: ResourceOfKind<"runtime">
  server?: ManagedServer
  credentials: ManagedCredential[]
  credentialsLoading: boolean
  credentialsError: unknown
  onRetryCredentials: () => void
  onSandboxChange: (resource: ResourceOfKind<"sandbox">) => void
}) {
  const currentUser = useSiteHeaderUser()
  const {
    confirmNavigation,
    isBlocked: isNavigationBlocked,
    setBlocked: setNavigationBlocked,
  } = useNavigationBlocker()
  const terminalRef = useRef<SandboxTerminalHandle>(null)
  const uploadInputRef = useRef<HTMLInputElement>(null)
  const uploadDirectoryRef = useRef("/")
  const mountedRef = useRef(true)
  const operationGenerationRef = useRef(0)
  const session = useMemo(
    () => new SandboxSessionClient(sandboxId),
    [sandboxId]
  )
  const [directoryEntries, setDirectoryEntries] = useState<
    Record<string, SandboxFileEntry[]>
  >({})
  const [expandedDirectories, setExpandedDirectories] = useState(
    () => new Set<string>(["/"])
  )
  const [loadingDirectories, setLoadingDirectories] = useState(
    () => new Set<string>()
  )
  const [directoryErrors, setDirectoryErrors] = useState<
    Record<string, string>
  >({})
  const [fileFilter, setFileFilter] = useState("")
  const [openFiles, setOpenFiles] = useState<OpenFile[]>([])
  const [activeFilePath, setActiveFilePath] = useState<string | null>(null)
  const [openingFilePath, setOpeningFilePath] = useState<string | null>(null)
  const fileSaveQueueRef = useRef(createLatestByKeyQueue<string>())
  const [savingFilePaths, setSavingFilePaths] = useState(
    () => new Set<string>()
  )
  const [sessionState, setSessionState] =
    useState<SandboxSessionState>("disconnected")
  const [sessionStateDetail, setSessionStateDetail] = useState("")
  const [propertiesTab, setPropertiesTab] = useState("sandbox")
  const [workspaceMode, setWorkspaceMode] = useState<"code" | "desktop">("code")
  const [showExplorer, setShowExplorer] = useState(true)
  const [showEditor, setShowEditor] = useState(false)
  const [showInspector, setShowInspector] = useState(false)
  const [showTerminal, setShowTerminal] = useState(true)
  const [showSearch, setShowSearch] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(
    null
  )
  const [dragUploadTarget, setDragUploadTarget] = useState<string | null>(null)

  const isRunning = sandbox?.spec.status === "running"
  const creationCancelled =
    sandbox?.spec.status === "cancelled" ||
    Boolean(sandbox?.spec.provisioning?.cancelRequested)
  const inheritedAgents = runtime?.spec.agentTools
  const sandboxAgents = supportedAgentToolList(
    Array.isArray(sandbox?.spec.agentTools)
      ? sandbox.spec.agentTools
      : inheritedAgents
  )
  const desktopEnabled = isSandboxDesktopEnabled(sandbox?.spec)
  const desktopUploadDirectory =
    typeof sandbox?.spec.workdir === "string" &&
    sandbox.spec.workdir.trim().startsWith("/")
      ? sandbox.spec.workdir.trim()
      : "/workspace"
  const desktopUnavailableReason =
    typeof sandbox?.spec.desktop !== "boolean" && runtime?.spec.desktop === true
      ? "此旧沙箱尚未应用模板的图形桌面配置；请从沙箱列表选择“重启并应用配置”"
      : "此沙箱创建时未启用图形桌面；请重新创建沙箱"
  const activeWorkspaceMode = desktopEnabled ? workspaceMode : "code"
  const activeFile = useMemo(
    () => openFiles.find((file) => file.path === activeFilePath) ?? null,
    [activeFilePath, openFiles]
  )
  const hasDirtyFiles =
    savingFilePaths.size > 0 ||
    openFiles.some((file) => file.content !== file.savedContent)
  const activeFileSaving = activeFile
    ? savingFilePaths.has(activeFile.path)
    : false
  const visibleSearchFiles = useMemo(() => {
    const query = fileFilter.trim().toLowerCase()
    if (!query) return []
    const seen = new Set<string>()
    return Object.values(directoryEntries)
      .flat()
      .filter((entry) => {
        if (seen.has(entry.path)) return false
        seen.add(entry.path)
        return entry.type === "file" && entry.name.toLowerCase().includes(query)
      })
  }, [directoryEntries, fileFilter])

  const runFileOperation = useCallback(
    async (input: WorkspaceInput) => {
      if (input.kind === "list") {
        return session.request<SandboxFileEntry[]>("list", input.path)
      }
      if (input.kind === "read") {
        return session.request<string>("read", input.path)
      }
      return session.request<string>("write", input.path, input.content)
    },
    [session]
  )
  const isOperationCurrent = useCallback(
    (generation: number) =>
      mountedRef.current && operationGenerationRef.current === generation,
    []
  )

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    const generation = operationGenerationRef.current
    return () => {
      if (operationGenerationRef.current === generation) {
        operationGenerationRef.current += 1
      }
    }
  }, [sandboxId, session])

  const loadDirectory = useCallback(
    async (path: string, showToast = true) => {
      const operationGeneration = operationGenerationRef.current
      setLoadingDirectories((current) => new Set(current).add(path))
      setDirectoryErrors((current) => {
        if (!(path in current)) return current
        const next = { ...current }
        delete next[path]
        return next
      })
      try {
        const entries = sandboxFileEntriesSchema.parse(
          await runFileOperation({ kind: "list", path })
        )
        if (!isOperationCurrent(operationGeneration)) return
        setDirectoryEntries((current) => ({ ...current, [path]: entries }))
      } catch (error) {
        if (!isOperationCurrent(operationGeneration)) return
        const detail = errorMessage(error) || "无法读取目录"
        setDirectoryErrors((current) => ({ ...current, [path]: detail }))
        if (showToast) {
          toast.error("无法打开目录", { description: detail })
        }
      } finally {
        if (isOperationCurrent(operationGeneration)) {
          setLoadingDirectories((current) => {
            const next = new Set(current)
            next.delete(path)
            return next
          })
        }
      }
    },
    [isOperationCurrent, runFileOperation]
  )

  useEffect(() => {
    const unsubscribe = session.onState((state, detail) => {
      setSessionState(state)
      setSessionStateDetail(detail ?? "")
    })
    const startTimer = isRunning
      ? window.setTimeout(() => session.start(), 0)
      : null
    if (!isRunning) session.stop()
    return () => {
      if (startTimer !== null) window.clearTimeout(startTimer)
      unsubscribe()
      session.stop()
    }
  }, [isRunning, session])

  useEffect(() => {
    if (
      isRunning &&
      sessionState === "ready" &&
      !directoryEntries["/"] &&
      !directoryErrors["/"] &&
      !loadingDirectories.has("/")
    ) {
      const timeout = window.setTimeout(() => void loadDirectory("/", false), 0)
      return () => window.clearTimeout(timeout)
    }
  }, [
    directoryEntries,
    directoryErrors,
    isRunning,
    loadDirectory,
    loadingDirectories,
    sessionState,
  ])

  useEffect(() => {
    setNavigationBlocked(hasDirtyFiles)
    if (!hasDirtyFiles) return
    const warnBeforeLeaving = (event: BeforeUnloadEvent) => {
      if (isNavigationBlocked()) event.preventDefault()
    }
    window.addEventListener("beforeunload", warnBeforeLeaving)
    return () => {
      window.removeEventListener("beforeunload", warnBeforeLeaving)
      setNavigationBlocked(false)
    }
  }, [hasDirtyFiles, isNavigationBlocked, setNavigationBlocked])

  async function openEntry(entry: SandboxFileEntry) {
    const operationGeneration = operationGenerationRef.current
    if (entry.type === "directory") {
      const expanded = expandedDirectories.has(entry.path)
      setExpandedDirectories((current) => {
        const next = new Set(current)
        if (expanded) {
          next.delete(entry.path)
        } else {
          next.add(entry.path)
        }
        return next
      })
      if (!expanded && !directoryEntries[entry.path]) {
        await loadDirectory(entry.path)
      }
      return
    }
    if (openFiles.some((file) => file.path === entry.path)) {
      setActiveFilePath(entry.path)
      setPropertiesTab("file")
      setShowEditor(true)
      return
    }
    setOpeningFilePath(entry.path)
    if (entry.size > sandboxFileReadMaxSize) {
      toast.error("文件过大，无法在线查看", {
        description: `${entry.path}（${formatBytes(entry.size)}）超过 5 MiB 上限，请在终端中处理。`,
      })
      setOpeningFilePath(null)
      return
    }
    try {
      const content = await runFileOperation({ kind: "read", path: entry.path })
      if (!isOperationCurrent(operationGeneration)) return
      if (typeof content !== "string") throw new Error("文件响应格式无效")
      const file: OpenFile = {
        path: entry.path,
        name: entry.name,
        content,
        savedContent: content,
        size: entry.size,
      }
      setOpenFiles((current) => [...current, file])
      setActiveFilePath(entry.path)
      setPropertiesTab("file")
      setShowEditor(true)
    } catch (error) {
      if (!isOperationCurrent(operationGeneration)) return
      toast.error("无法打开文件", { description: errorMessage(error) })
    } finally {
      if (isOperationCurrent(operationGeneration)) setOpeningFilePath(null)
    }
  }

  async function saveActiveFile() {
    if (
      !activeFile ||
      (activeFile.content === activeFile.savedContent && !activeFileSaving)
    ) {
      return
    }
    const operationGeneration = operationGenerationRef.current
    const { path, content } = activeFile
    const save = enqueueLatestByKey(
      fileSaveQueueRef.current,
      path,
      content,
      async (nextContent) => {
        await runFileOperation({ kind: "write", path, content: nextContent })
        if (!isOperationCurrent(operationGeneration)) return
        setOpenFiles((current) =>
          current.map((file) =>
            file.path === path ? { ...file, savedContent: nextContent } : file
          )
        )
      }
    )
    if (!save) return

    setSavingFilePaths((current) => new Set(current).add(path))
    try {
      await save
      if (isOperationCurrent(operationGeneration)) {
        toast.success("文件已保存", { description: path })
      }
    } catch (error) {
      if (isOperationCurrent(operationGeneration)) {
        toast.error("保存失败", { description: errorMessage(error) })
      }
    } finally {
      if (isOperationCurrent(operationGeneration)) {
        setSavingFilePaths((current) => {
          const next = new Set(current)
          next.delete(path)
          return next
        })
      }
    }
  }

  function refreshFileTree() {
    setDirectoryEntries({})
    setExpandedDirectories(new Set(["/"]))
    void loadDirectory("/")
  }

  function closeFile(path: string) {
    const file = openFiles.find((item) => item.path === path)
    if (!file) return
    if (savingFilePaths.has(path)) {
      toast.error("文件正在保存", { description: "保存完成后再关闭该文件。" })
      return
    }
    if (file.content !== file.savedContent) {
      toast.error("文件尚未保存", { description: "保存后再关闭该文件。" })
      return
    }
    const index = openFiles.findIndex((item) => item.path === path)
    const remaining = openFiles.filter((item) => item.path !== path)
    setOpenFiles(remaining)
    if (activeFilePath === path) {
      setActiveFilePath(remaining[Math.max(0, index - 1)]?.path ?? null)
    }
  }

  function updateActiveFile(content: string) {
    if (!activeFilePath) return
    setOpenFiles((current) =>
      current.map((file) =>
        file.path === activeFilePath ? { ...file, content } : file
      )
    )
  }

  function chooseFiles(directory: string) {
    if (sessionState !== "ready" || uploadProgress) return
    uploadDirectoryRef.current = directory
    if (uploadInputRef.current) {
      uploadInputRef.current.value = ""
      uploadInputRef.current.click()
    }
  }

  function uploadDesktopFiles(files: FileList) {
    uploadDirectoryRef.current = desktopUploadDirectory
    void uploadSelectedFiles(files)
  }

  function markUploadDropTarget(
    event: DragEvent<HTMLElement>,
    directory: string
  ) {
    if (!event.dataTransfer.types.includes("Files") || uploadProgress) return
    event.preventDefault()
    event.stopPropagation()
    event.dataTransfer.dropEffect = "copy"
    setDragUploadTarget(directory)
  }

  function clearUploadDropTarget(
    event: DragEvent<HTMLElement>,
    directory: string
  ) {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return
    setDragUploadTarget((current) => (current === directory ? null : current))
  }

  function uploadDroppedFiles(
    event: DragEvent<HTMLElement>,
    directory: string
  ) {
    if (!event.dataTransfer.types.includes("Files") || uploadProgress) return
    event.preventDefault()
    event.stopPropagation()
    uploadDirectoryRef.current = directory
    setDragUploadTarget(null)
    void uploadSelectedFiles(event.dataTransfer.files)
  }

  async function uploadSelectedFiles(files: FileList | null) {
    const selectedFiles = Array.from(files ?? [])
    if (selectedFiles.length === 0) return

    const operationGeneration = operationGenerationRef.current
    const directory = uploadDirectoryRef.current
    let uploaded = 0
    const failures: string[] = []
    try {
      const currentEntries = sandboxFileEntriesSchema.parse(
        await runFileOperation({ kind: "list", path: directory })
      )
      if (!isOperationCurrent(operationGeneration)) return
      setDirectoryEntries((current) => ({
        ...current,
        [directory]: currentEntries,
      }))
      const existingNames = new Set(currentEntries.map((entry) => entry.name))

      for (const [index, file] of selectedFiles.entries()) {
        const invalidName = uploadNameError(file.name)
        if (invalidName) {
          failures.push(`${file.name || "未命名文件"}：${invalidName}`)
          continue
        }
        if (file.size > sandboxUploadMaxSize) {
          failures.push(`${file.name}：单个文件不能超过 50 MiB`)
          continue
        }
        if (existingNames.has(file.name)) {
          failures.push(`${file.name}：目标目录已存在同名文件`)
          continue
        }

        const destination = joinSandboxPath(directory, file.name)
        setUploadProgress({
          fileName: file.name,
          fileIndex: index + 1,
          fileCount: selectedFiles.length,
          percent: 0,
        })
        try {
          await uploadFile(file, destination, (percent) =>
            isOperationCurrent(operationGeneration)
              ? setUploadProgress({
                  fileName: file.name,
                  fileIndex: index + 1,
                  fileCount: selectedFiles.length,
                  percent,
                })
              : undefined
          )
          if (!isOperationCurrent(operationGeneration)) return
          existingNames.add(file.name)
          uploaded += 1
        } catch (error) {
          if (!isOperationCurrent(operationGeneration)) return
          failures.push(`${file.name}：${uploadErrorMessage(error)}`)
        }
      }

      if (!isOperationCurrent(operationGeneration)) return
      setExpandedDirectories((current) => new Set(current).add(directory))
      await loadDirectory(directory, false)
      if (!isOperationCurrent(operationGeneration)) return
      if (uploaded > 0) {
        toast.success(`已上传 ${uploaded} 个文件`, { description: directory })
      }
      if (failures.length > 0) {
        toast.error(`${failures.length} 个文件未上传`, {
          description: failures.slice(0, 3).join("；"),
        })
      }
    } catch (error) {
      if (!isOperationCurrent(operationGeneration)) return
      toast.error("上传失败", { description: errorMessage(error) })
    } finally {
      if (isOperationCurrent(operationGeneration)) setUploadProgress(null)
    }
  }

  async function uploadFile(
    file: File,
    destination: string,
    onProgress: (percent: number) => void
  ) {
    if (file.size <= 512 * 1024) {
      const bytes = new Uint8Array(await file.arrayBuffer())
      let text: string | null = null
      try {
        text = new TextDecoder("utf-8", { fatal: true }).decode(bytes)
      } catch {
        // Binary files use the chunked upload protocol below.
      }
      if (text !== null) {
        await runFileOperation({
          kind: "write",
          path: destination,
          content: text,
        })
        onProgress(100)
        return
      }
    }
    await session.uploadFile(file, destination, onProgress)
  }

  async function copyPath(path: string) {
    const operationGeneration = operationGenerationRef.current
    try {
      await writeClipboardText(path)
      if (!isOperationCurrent(operationGeneration)) return
      toast.success("路径已复制", { description: path })
    } catch {
      if (!isOperationCurrent(operationGeneration)) return
      toast.error("复制失败", { description: "浏览器未授权访问剪贴板。" })
    }
  }

  function toggleEditorPanel() {
    if (showEditor && !showTerminal) setShowTerminal(true)
    setShowEditor((visible) => !visible)
  }

  function toggleTerminalPanel() {
    if (showTerminal && !showEditor) setShowEditor(true)
    setShowTerminal((visible) => !visible)
  }

  function renderEntry(entry: SandboxFileEntry, depth = 0) {
    const expanded = expandedDirectories.has(entry.path)
    const loading = loadingDirectories.has(entry.path)
    const active = activeFilePath === entry.path
    return (
      <div
        key={entry.path}
        style={{ contentVisibility: "auto", containIntrinsicSize: "28px" }}
      >
        <ContextMenu>
          <ContextMenuTrigger asChild>
            <button
              type="button"
              aria-expanded={entry.type === "directory" ? expanded : undefined}
              className={cn(
                "mx-1 flex h-7 w-[calc(100%-0.5rem)] items-center gap-1 rounded-md pr-2 text-left text-[13px] hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
                active && "bg-sidebar-accent text-sidebar-accent-foreground",
                dragUploadTarget === entry.path &&
                  "bg-primary/10 text-foreground ring-1 ring-primary/60 ring-inset"
              )}
              style={{ paddingLeft: 8 + depth * 12 }}
              onClick={() => void openEntry(entry)}
              onDragEnter={(event) => {
                if (entry.type === "directory") {
                  markUploadDropTarget(event, entry.path)
                }
              }}
              onDragOver={(event) => {
                if (entry.type === "directory") {
                  markUploadDropTarget(event, entry.path)
                }
              }}
              onDragLeave={(event) => {
                if (entry.type === "directory") {
                  clearUploadDropTarget(event, entry.path)
                }
              }}
              onDrop={(event) => {
                if (entry.type === "directory") {
                  uploadDroppedFiles(event, entry.path)
                }
              }}
            >
              {entry.type === "directory" ? (
                expanded ? (
                  <ChevronDownIcon
                    aria-hidden="true"
                    className="size-3 shrink-0"
                  />
                ) : (
                  <ChevronRightIcon
                    aria-hidden="true"
                    className="size-3 shrink-0"
                  />
                )
              ) : (
                <span className="size-3 shrink-0" />
              )}
              {entry.type === "directory" ? (
                expanded ? (
                  <FolderOpenIcon
                    aria-hidden="true"
                    className="size-3.5 shrink-0 text-muted-foreground"
                  />
                ) : (
                  <FolderIcon
                    aria-hidden="true"
                    className="size-3.5 shrink-0 text-muted-foreground"
                  />
                )
              ) : openingFilePath === entry.path ? (
                <Spinner className="size-3.5 shrink-0" />
              ) : (
                <FileIcon
                  aria-hidden="true"
                  className="size-3.5 shrink-0 text-muted-foreground"
                />
              )}
              <span className="min-w-0 flex-1 truncate">{entry.name}</span>
            </button>
          </ContextMenuTrigger>
          <ContextMenuContent className="w-56">
            <ContextMenuLabel className="truncate">
              {entry.name}
            </ContextMenuLabel>
            <ContextMenuGroup>
              <ContextMenuItem onSelect={() => void openEntry(entry)}>
                {entry.type === "directory" ? (
                  expanded ? (
                    <ChevronRightIcon aria-hidden="true" />
                  ) : (
                    <ChevronDownIcon aria-hidden="true" />
                  )
                ) : (
                  <FileIcon aria-hidden="true" />
                )}
                {entry.type === "directory"
                  ? expanded
                    ? "折叠目录"
                    : "展开目录"
                  : "打开文件"}
              </ContextMenuItem>
              {entry.type === "directory" ? (
                <>
                  <ContextMenuItem
                    disabled={sessionState !== "ready" || !!uploadProgress}
                    onSelect={() => chooseFiles(entry.path)}
                  >
                    <FileUpIcon aria-hidden="true" />
                    上传到此目录
                  </ContextMenuItem>
                  <ContextMenuItem
                    disabled={loading}
                    onSelect={() => void loadDirectory(entry.path)}
                  >
                    <RefreshCwIcon aria-hidden="true" />
                    刷新目录
                  </ContextMenuItem>
                </>
              ) : null}
            </ContextMenuGroup>
            <ContextMenuSeparator />
            <ContextMenuGroup>
              <ContextMenuItem onSelect={() => void copyPath(entry.path)}>
                <CopyIcon aria-hidden="true" />
                复制路径
              </ContextMenuItem>
            </ContextMenuGroup>
          </ContextMenuContent>
        </ContextMenu>
        {entry.type === "directory" && expanded && (
          <div>
            {loading ? (
              <div
                className="flex h-6 items-center gap-2 text-xs text-muted-foreground"
                style={{ paddingLeft: 28 + depth * 12 }}
              >
                <Spinner />
                加载中…
              </div>
            ) : (
              renderTree(entry.path, depth + 1)
            )}
          </div>
        )}
      </div>
    )
  }

  function renderTree(path: string, depth = 0) {
    return (directoryEntries[path] ?? []).map((entry) =>
      renderEntry(entry, depth)
    )
  }

  function renderExplorer() {
    return (
      <aside className="flex h-full min-w-0 flex-col bg-sidebar text-sidebar-foreground">
        <div className="flex h-10 shrink-0 items-center gap-1 border-b px-2.5">
          <h2 className="min-w-0 flex-1 truncate text-xs font-medium">
            资源管理器
          </h2>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label="上传文件到根目录"
                disabled={sessionState !== "ready" || !!uploadProgress}
                onClick={() => chooseFiles("/")}
              >
                <FileUpIcon aria-hidden="true" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>上传文件到根目录</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={showSearch ? "关闭文件搜索" : "搜索文件"}
                aria-pressed={showSearch}
                onClick={() => setShowSearch((visible) => !visible)}
              >
                <SearchIcon aria-hidden="true" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>搜索文件</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label="刷新文件树"
                disabled={
                  sessionState !== "ready" || loadingDirectories.has("/")
                }
                onClick={refreshFileTree}
              >
                {loadingDirectories.has("/") ? (
                  <Spinner />
                ) : (
                  <RefreshCwIcon aria-hidden="true" />
                )}
              </Button>
            </TooltipTrigger>
            <TooltipContent>刷新文件树</TooltipContent>
          </Tooltip>
        </div>
        {showSearch ? (
          <div className="border-b p-2">
            <div className="relative">
              <SearchIcon
                aria-hidden="true"
                className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                aria-label="搜索文件"
                name="sandbox-file-search"
                autoComplete="off"
                value={fileFilter}
                placeholder="在已展开目录中搜索…"
                className="h-7 rounded-sm pl-7 text-xs"
                onChange={(event) => setFileFilter(event.target.value)}
              />
            </div>
          </div>
        ) : null}
        <div className="mx-2 mt-2 flex h-8 shrink-0 items-center gap-1.5 rounded-lg border bg-sidebar-accent/50 px-2.5 text-[11px] font-medium">
          <FolderOpenIcon aria-hidden="true" className="size-3.5" />
          <span className="min-w-0 flex-1 truncate">{sandbox?.id}</span>
          <span className="shrink-0 text-sidebar-foreground/60">
            {directoryEntries["/"]?.length ?? 0} 项
          </span>
        </div>
        <ContextMenu>
          <ContextMenuTrigger asChild>
            <div
              aria-label="资源管理器文件列表"
              className={cn(
                "relative min-h-0 flex-1 overflow-auto py-1.5",
                dragUploadTarget === "/" &&
                  "bg-primary/5 ring-1 ring-primary/60 ring-inset"
              )}
              onDragEnter={(event) => markUploadDropTarget(event, "/")}
              onDragOver={(event) => markUploadDropTarget(event, "/")}
              onDragLeave={(event) => clearUploadDropTarget(event, "/")}
              onDrop={(event) => uploadDroppedFiles(event, "/")}
            >
              {dragUploadTarget === "/" ? (
                <div className="pointer-events-none sticky top-1 z-10 mx-2 flex h-8 items-center justify-center gap-2 rounded-sm border border-primary/40 bg-background/95 text-xs font-medium shadow-sm">
                  <FileUpIcon aria-hidden="true" className="size-3.5" />
                  松开上传到根目录
                </div>
              ) : null}
              {directoryErrors["/"] && !directoryEntries["/"] ? (
                <div
                  role="alert"
                  className="mx-2 mt-1 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-xs"
                >
                  <p className="font-medium text-foreground">无法读取根目录</p>
                  <p
                    className="mt-1 line-clamp-2 break-words text-muted-foreground"
                    title={directoryErrors["/"]}
                  >
                    {directoryErrors["/"]}
                  </p>
                  <Button
                    variant="outline"
                    size="xs"
                    className="mt-2"
                    disabled={
                      sessionState !== "ready" || loadingDirectories.has("/")
                    }
                    onClick={refreshFileTree}
                  >
                    {loadingDirectories.has("/") ? (
                      <Spinner />
                    ) : (
                      <RefreshCwIcon aria-hidden="true" />
                    )}
                    重试
                  </Button>
                </div>
              ) : fileFilter.trim() ? (
                visibleSearchFiles.length > 0 ? (
                  visibleSearchFiles.map((entry) => renderEntry(entry))
                ) : (
                  <p className="px-3 py-2 text-xs text-muted-foreground">
                    已展开目录中没有匹配文件
                  </p>
                )
              ) : loadingDirectories.has("/") && !directoryEntries["/"] ? (
                <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
                  <Spinner />
                  正在读取根文件系统…
                </div>
              ) : (
                renderTree("/")
              )}
            </div>
          </ContextMenuTrigger>
          <ContextMenuContent className="w-56">
            <ContextMenuLabel>沙箱根目录</ContextMenuLabel>
            <ContextMenuGroup>
              <ContextMenuItem
                disabled={sessionState !== "ready" || !!uploadProgress}
                onSelect={() => chooseFiles("/")}
              >
                <FileUpIcon aria-hidden="true" />
                上传文件
              </ContextMenuItem>
              <ContextMenuItem
                disabled={
                  sessionState !== "ready" || loadingDirectories.has("/")
                }
                onSelect={refreshFileTree}
              >
                <RefreshCwIcon aria-hidden="true" />
                刷新文件树
              </ContextMenuItem>
            </ContextMenuGroup>
          </ContextMenuContent>
        </ContextMenu>
        {uploadProgress ? (
          <div className="shrink-0 border-t bg-background px-2.5 py-2">
            <div className="mb-1.5 flex items-center gap-2 text-[11px]">
              <FileUpIcon
                aria-hidden="true"
                className="size-3 shrink-0 text-muted-foreground"
              />
              <span className="min-w-0 flex-1 truncate">
                {uploadProgress.fileName}
              </span>
              <span className="shrink-0 text-muted-foreground tabular-nums">
                {uploadProgress.fileIndex}/{uploadProgress.fileCount} ·{" "}
                {uploadProgress.percent}%
              </span>
            </div>
            <Progress
              aria-label={`正在上传 ${uploadProgress.fileName}`}
              value={uploadProgress.percent}
            />
          </div>
        ) : null}
        <input
          ref={uploadInputRef}
          type="file"
          multiple
          className="sr-only"
          aria-label="选择要上传到沙箱的文件"
          onChange={(event) => {
            void uploadSelectedFiles(event.currentTarget.files)
          }}
        />
      </aside>
    )
  }

  function renderEditor() {
    return (
      <section className="flex h-full min-h-0 flex-col bg-background">
        <div
          role="tablist"
          aria-label="已打开文件"
          className="flex h-10 shrink-0 items-stretch overflow-x-auto border-b bg-sidebar/80"
        >
          {openFiles.length > 0 ? (
            openFiles.map((file) => {
              const active = activeFilePath === file.path
              const dirty = file.content !== file.savedContent
              return (
                <div
                  key={file.path}
                  className={cn(
                    "group my-1 ml-1 flex h-8 max-w-52 min-w-0 shrink-0 items-center rounded-lg border border-transparent",
                    active && "border-border bg-background shadow-xs"
                  )}
                >
                  <button
                    type="button"
                    role="tab"
                    aria-selected={active}
                    className={cn(
                      "flex h-full min-w-0 flex-1 items-center gap-1.5 px-3 text-xs text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset",
                      active && "text-foreground"
                    )}
                    onClick={() => {
                      setActiveFilePath(file.path)
                      setPropertiesTab("file")
                    }}
                  >
                    <FileIcon
                      aria-hidden="true"
                      className="size-3.5 shrink-0"
                    />
                    <span className="truncate">{file.name}</span>
                    {dirty ? (
                      <span
                        className="shrink-0 text-[10px]"
                        aria-label="未保存"
                      >
                        ●
                      </span>
                    ) : null}
                  </button>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label={`关闭 ${file.name}`}
                    className="mr-1 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                    onClick={() => closeFile(file.path)}
                  >
                    <XIcon aria-hidden="true" />
                  </Button>
                </div>
              )
            })
          ) : (
            <div className="flex items-center gap-1.5 px-3 text-xs font-medium text-muted-foreground">
              <Code2Icon aria-hidden="true" className="size-3.5" />
              编辑器
            </div>
          )}
          <div className="ml-auto flex shrink-0 items-center border-l px-1">
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="保存当前文件"
              disabled={
                !activeFile ||
                activeFile.content === activeFile.savedContent ||
                activeFileSaving
              }
              onClick={() => void saveActiveFile()}
            >
              {activeFileSaving ? <Spinner /> : <SaveIcon aria-hidden="true" />}
            </Button>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="收起编辑器"
              onClick={toggleEditorPanel}
            >
              <PanelTopCloseIcon aria-hidden="true" />
            </Button>
          </div>
        </div>
        {activeFile ? (
          <div className="flex h-7 shrink-0 items-center border-b px-3 font-mono text-[11px] text-muted-foreground">
            <span className="truncate" title={activeFile.path}>
              {activeFile.path}
            </span>
          </div>
        ) : null}
        <div className="min-h-0 flex-1 overflow-hidden">
          {activeFile ? (
            <SandboxCodeEditor
              path={activeFile.path}
              value={activeFile.content}
              onChange={updateActiveFile}
              onSave={() => void saveActiveFile()}
            />
          ) : (
            <Empty className="h-full rounded-none border-0 bg-muted/10">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Code2Icon aria-hidden="true" />
                </EmptyMedia>
                <EmptyTitle>选择一个文件开始编辑</EmptyTitle>
                <EmptyDescription>
                  从左侧资源管理器打开文件，或继续使用下方终端。文件读写使用
                  root 权限。
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </div>
      </section>
    )
  }

  function renderTerminal() {
    if (!sandbox) return null
    return (
      <section className="flex h-full min-h-0 flex-col bg-background">
        <div className="flex h-10 shrink-0 items-center border-b bg-sidebar/80">
          <div className="m-1 flex h-8 items-center gap-1.5 rounded-md bg-background px-2.5 text-xs font-medium shadow-xs">
            <TerminalIcon aria-hidden="true" className="size-3.5" />
            终端
          </div>
          <div className="ml-auto flex items-center gap-1 px-1.5">
            <Badge variant="secondary" className="h-5 text-[10px]">
              {sessionStateLabel(sessionState)}
            </Badge>
            <Badge variant="outline" className="h-5 text-[10px]">
              root
            </Badge>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  aria-label="粘贴到终端"
                  disabled={sessionState !== "ready"}
                  onClick={() => {
                    void terminalRef.current
                      ?.pasteFromClipboard()
                      .then((pasted) => {
                        if (!pasted) toast.error("剪贴板为空")
                      })
                      .catch(() => {
                        toast.error("无法读取剪贴板", {
                          description:
                            "请允许浏览器读取剪贴板，或点击终端后按 Ctrl+V。",
                        })
                      })
                  }}
                >
                  <ClipboardPasteIcon aria-hidden="true" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>粘贴到终端（Ctrl+V）</TooltipContent>
            </Tooltip>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="关闭终端面板"
              onClick={toggleTerminalPanel}
            >
              <XIcon aria-hidden="true" />
            </Button>
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-hidden">
          <SandboxTerminal ref={terminalRef} session={session} />
        </div>
      </section>
    )
  }

  function renderInspector() {
    const rows =
      propertiesTab === "file" && activeFile
        ? [
            ["路径", activeFile.path],
            ["类型", languageForPath(activeFile.path)],
            ["大小", formatBytes(activeFile.size)],
            ["行数", String(activeFile.content.split("\n").length)],
            [
              "状态",
              activeFileSaving
                ? "正在保存"
                : activeFile.content === activeFile.savedContent
                  ? "已保存"
                  : "未保存",
            ],
          ]
        : [
            ["状态", String(sandbox?.spec.status ?? "未知")],
            [
              "运行时",
              runtime?.name ?? String(sandbox?.spec.runtimeId ?? "未绑定"),
            ],
            [
              "服务器",
              server?.name ?? String(sandbox?.spec.serverId ?? "创建时选择"),
            ],
            ["Agent", sandboxAgents.join(" · ") || "未安装"],
            ["图形桌面", desktopEnabled ? "已启用" : "未启用"],
            ["终端用户", "root"],
            ["实时会话", sessionStateLabel(sessionState)],
          ]

    return (
      <aside className="flex h-full min-w-0 flex-col bg-sidebar text-sidebar-foreground">
        <div className="flex h-10 shrink-0 items-center border-b px-2.5">
          <h2 className="min-w-0 flex-1 truncate text-xs font-medium">
            检查器
          </h2>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="关闭检查器"
            onClick={() => setShowInspector(false)}
          >
            <XIcon aria-hidden="true" />
          </Button>
        </div>
        <ToggleGroup
          type="single"
          value={propertiesTab}
          onValueChange={(value) => value && setPropertiesTab(value)}
          size="sm"
          variant="outline"
          spacing={0}
          aria-label="检查器内容"
          className="m-2 grid w-auto grid-cols-2"
        >
          <ToggleGroupItem value="sandbox">沙箱</ToggleGroupItem>
          <ToggleGroupItem value="file" disabled={!activeFile}>
            文件
          </ToggleGroupItem>
        </ToggleGroup>
        <>
          <div className="border-y px-3 py-3">
            <h3 className="truncate text-sm font-semibold">
              {propertiesTab === "file" && activeFile
                ? activeFile.name
                : (sandbox?.name ?? "沙箱")}
            </h3>
            <p className="mt-1 line-clamp-2 text-xs break-all text-muted-foreground">
              {propertiesTab === "file" && activeFile
                ? activeFile.path
                : sandbox?.description || sandbox?.id}
            </p>
          </div>
          <div className="min-h-0 flex-1 overflow-auto p-3">
            <dl className="grid grid-cols-[5rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs">
              {rows.map(([label, value]) => (
                <div key={label} className="contents">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd
                    className="min-w-0 break-words text-foreground"
                    title={value}
                  >
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        </>
      </aside>
    )
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col bg-background">
      <SiteHeader
        title={
          <span className="flex min-w-0 items-center gap-2">
            <Link
              href="/sandboxes"
              className="flex shrink-0 items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
              onNavigate={(event) => {
                if (!confirmNavigation()) event.preventDefault()
              }}
            >
              <ArrowLeftIcon aria-hidden="true" className="size-3.5" />
              <span className="hidden sm:inline">沙箱</span>
            </Link>
            <span aria-hidden="true" className="text-muted-foreground/50">
              /
            </span>
            <span className="flex min-w-0 items-center gap-1.5">
              <BoxIcon aria-hidden="true" className="size-3.5 shrink-0" />
              <span className="truncate">{sandbox?.name ?? sandboxId}</span>
            </span>
            {sandbox ? (
              <Badge variant={isRunning ? "secondary" : "outline"}>
                {isRunning ? "运行中" : "未运行"}
              </Badge>
            ) : null}
          </span>
        }
        center={
          <WorkspaceModeToggle
            value={activeWorkspaceMode}
            desktopEnabled={desktopEnabled}
            desktopUnavailableReason={desktopUnavailableReason}
            onChange={setWorkspaceMode}
            className="pointer-events-auto mx-auto"
          />
        }
        action={
          <>
            {isRunning ? (
              <SandboxModelSourceSwitcher
                sandbox={sandbox}
                runtime={runtime}
                credentials={credentials}
                credentialsLoading={credentialsLoading}
                credentialsError={credentialsError}
                canMutate={
                  currentUser?.role === "admin" ||
                  currentUser?.role === "operator"
                }
                onRetryCredentials={onRetryCredentials}
                onResourceChange={onSandboxChange}
              />
            ) : null}
            <WorkspaceModeToggle
              value={activeWorkspaceMode}
              desktopEnabled={desktopEnabled}
              desktopUnavailableReason={desktopUnavailableReason}
              onChange={setWorkspaceMode}
              className="lg:hidden"
            />
            {activeWorkspaceMode === "code" ? (
              <ButtonGroup
                aria-label="工作区面板"
                className="rounded-lg border bg-muted/30 p-0.5"
              >
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      variant={showExplorer ? "secondary" : "ghost"}
                      size="icon-sm"
                      aria-label={
                        showExplorer ? "隐藏资源管理器" : "显示资源管理器"
                      }
                      aria-pressed={showExplorer}
                      onClick={() => setShowExplorer((visible) => !visible)}
                    >
                      <PanelLeftIcon aria-hidden="true" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>资源管理器</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      variant={showEditor ? "secondary" : "ghost"}
                      size="icon-sm"
                      aria-label={showEditor ? "隐藏编辑器" : "显示编辑器"}
                      aria-pressed={showEditor}
                      onClick={toggleEditorPanel}
                    >
                      <PanelTopIcon aria-hidden="true" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>编辑器</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      variant={showTerminal ? "secondary" : "ghost"}
                      size="icon-sm"
                      aria-label={showTerminal ? "隐藏终端" : "显示终端"}
                      aria-pressed={showTerminal}
                      onClick={toggleTerminalPanel}
                    >
                      <PanelBottomIcon aria-hidden="true" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>终端</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      variant={showInspector ? "secondary" : "ghost"}
                      size="icon-sm"
                      aria-label={showInspector ? "隐藏检查器" : "显示检查器"}
                      aria-pressed={showInspector}
                      onClick={() => setShowInspector((visible) => !visible)}
                    >
                      <PanelRightIcon aria-hidden="true" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>检查器</TooltipContent>
                </Tooltip>
              </ButtonGroup>
            ) : null}
          </>
        }
      />

      {!sandbox ? (
        <Empty className="min-h-0 flex-1 rounded-none border-0">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InfoIcon aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>无法打开沙箱</EmptyTitle>
            <EmptyDescription>沙箱不存在</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : !isRunning ? (
        <Empty className="min-h-0 flex-1 rounded-none border-0">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BoxIcon aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>
              {creationCancelled
                ? sandbox.spec.status === "cancelling"
                  ? "正在取消安装"
                  : "沙箱创建已中止"
                : "沙箱未运行"}
            </EmptyTitle>
            <EmptyDescription>
              {creationCancelled
                ? "请返回沙箱列表查看取消进度或清理结果。需要使用时请重新创建沙箱。"
                : "先启动沙箱，再进入 IDE 操作文件和终端。"}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="m-2 flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
          {sessionStateDetail && sessionState !== "ready" ? (
            <div
              role="alert"
              className="flex shrink-0 items-center gap-3 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs"
            >
              <div className="min-w-0 flex-1">
                <p className="font-medium text-foreground">
                  {sessionState === "connecting"
                    ? "实时会话正在重连"
                    : "实时会话连接异常"}
                </p>
                <p
                  className="truncate text-muted-foreground"
                  title={sessionStateDetail || "无法连接 Worker 实时会话"}
                >
                  {sessionStateDetail || "无法连接 Worker 实时会话"}
                </p>
              </div>
              <Button
                variant="outline"
                size="xs"
                onClick={() => session.restart()}
              >
                <RefreshCwIcon aria-hidden="true" />
                重新连接
              </Button>
            </div>
          ) : null}
          <div className="min-h-0 flex-1 overflow-hidden">
            {activeWorkspaceMode === "desktop" ? (
              <div className="h-full overflow-hidden rounded-xl border bg-background shadow-sm">
                <SandboxDesktop
                  sandboxId={sandbox.id}
                  active
                  running={isRunning}
                  uploadDestination={desktopUploadDirectory}
                  uploadEnabled={sessionState === "ready"}
                  uploadProgress={uploadProgress}
                  onUploadFiles={uploadDesktopFiles}
                />
              </div>
            ) : (
              <ResizablePanelGroup orientation="horizontal">
                {showExplorer ? (
                  <>
                    <ResizablePanel
                      id="sandbox-explorer"
                      defaultSize="250px"
                      minSize="190px"
                      maxSize="40%"
                    >
                      <div className="h-full overflow-hidden rounded-xl border bg-background shadow-sm">
                        {renderExplorer()}
                      </div>
                    </ResizablePanel>
                    <ResizableHandle
                      aria-label="调整资源管理器宽度"
                      className="w-2 bg-transparent after:w-2 hover:bg-muted/50 focus-visible:bg-muted/50"
                    />
                  </>
                ) : null}
                <ResizablePanel id="sandbox-editor" minSize="320px">
                  <ResizablePanelGroup orientation="vertical">
                    {showEditor ? (
                      <ResizablePanel
                        id="sandbox-code"
                        defaultSize={showTerminal ? "64%" : "100%"}
                        minSize="180px"
                      >
                        <div className="h-full overflow-hidden rounded-xl border bg-background shadow-sm">
                          {renderEditor()}
                        </div>
                      </ResizablePanel>
                    ) : null}
                    {showTerminal ? (
                      <>
                        {showEditor ? (
                          <ResizableHandle
                            aria-label="调整终端高度"
                            className="bg-transparent hover:bg-muted/50 focus-visible:bg-muted/50 aria-[orientation=horizontal]:h-2 aria-[orientation=horizontal]:after:h-2"
                          />
                        ) : null}
                        <ResizablePanel
                          id="sandbox-terminal"
                          defaultSize={showEditor ? "36%" : "100%"}
                          minSize="120px"
                          maxSize={showEditor ? "70%" : "100%"}
                        >
                          <div className="h-full overflow-hidden rounded-xl border bg-background shadow-sm">
                            {renderTerminal()}
                          </div>
                        </ResizablePanel>
                      </>
                    ) : null}
                  </ResizablePanelGroup>
                </ResizablePanel>
                {showInspector ? (
                  <>
                    <ResizableHandle
                      aria-label="调整检查器宽度"
                      className="w-2 bg-transparent after:w-2 hover:bg-muted/50 focus-visible:bg-muted/50"
                    />
                    <ResizablePanel
                      id="sandbox-inspector"
                      defaultSize="320px"
                      minSize="280px"
                      maxSize="45%"
                    >
                      <div className="h-full overflow-hidden rounded-xl border bg-background shadow-sm">
                        {renderInspector()}
                      </div>
                    </ResizablePanel>
                  </>
                ) : null}
              </ResizablePanelGroup>
            )}
          </div>
          <footer className="flex h-7 shrink-0 items-center gap-3 rounded-lg border bg-sidebar px-2.5 text-[11px] text-sidebar-foreground shadow-xs">
            <span
              className="flex min-w-0 items-center gap-1"
              aria-live="polite"
            >
              {activeWorkspaceMode === "desktop" ? (
                <MonitorIcon aria-hidden="true" className="size-3" />
              ) : (
                <WifiIcon aria-hidden="true" className="size-3" />
              )}
              {activeWorkspaceMode === "desktop"
                ? "图形桌面"
                : sessionStateLabel(sessionState)}
            </span>
            <span className="flex min-w-0 items-center gap-1">
              <ShieldCheckIcon aria-hidden="true" className="size-3" />
              root
            </span>
            <span className="flex min-w-0 items-center gap-1 truncate">
              <TerminalIcon aria-hidden="true" className="size-3" />
              {sandbox.id}
            </span>
            <span className="ml-auto hidden items-center gap-1 sm:flex">
              <BoxIcon aria-hidden="true" className="size-3" />
              {runtime?.name ?? "沙箱"}
            </span>
            {activeFile ? (
              <>
                <span className="hidden tabular-nums md:inline">
                  {activeFile.content.split("\n").length} 行
                </span>
                <span className="hidden md:inline">UTF-8</span>
                <span>{languageForPath(activeFile.path)}</span>
              </>
            ) : null}
          </footer>
        </div>
      )}
    </section>
  )
}

function WorkspaceModeToggle({
  value,
  desktopEnabled,
  desktopUnavailableReason,
  onChange,
  className,
}: {
  value: "code" | "desktop"
  desktopEnabled: boolean
  desktopUnavailableReason: string
  onChange: (value: "code" | "desktop") => void
  className?: string
}) {
  return (
    <ToggleGroup
      type="single"
      value={value}
      onValueChange={(next) => next && onChange(next as "code" | "desktop")}
      variant="outline"
      size="sm"
      spacing={0}
      aria-label="工作区视图"
      className={className}
    >
      <ToggleGroupItem value="code">
        <Code2Icon aria-hidden="true" data-icon="inline-start" />
        <span className="hidden sm:inline">代码</span>
      </ToggleGroupItem>
      <ToggleGroupItem
        value="desktop"
        disabled={!desktopEnabled}
        aria-label={desktopEnabled ? "打开图形桌面" : desktopUnavailableReason}
        title={desktopEnabled ? "打开图形桌面" : desktopUnavailableReason}
      >
        <MonitorIcon aria-hidden="true" data-icon="inline-start" />
        <span className="hidden sm:inline">桌面</span>
      </ToggleGroupItem>
    </ToggleGroup>
  )
}

function languageForPath(path: string) {
  const extension = path.split(".").pop()?.toLowerCase() ?? ""
  const languages: Record<string, string> = {
    bash: "Shell",
    c: "C",
    conf: "配置",
    cpp: "C++",
    css: "CSS",
    env: "环境变量",
    go: "Go",
    html: "HTML",
    ini: "INI",
    java: "Java",
    js: "JavaScript",
    json: "JSON",
    jsx: "JavaScript React",
    md: "Markdown",
    php: "PHP",
    ps1: "PowerShell",
    py: "Python",
    rb: "Ruby",
    rs: "Rust",
    sh: "Shell",
    sql: "SQL",
    toml: "TOML",
    ts: "TypeScript",
    tsx: "TypeScript React",
    xml: "XML",
    yaml: "YAML",
    yml: "YAML",
  }
  return languages[extension] ?? "文本"
}

function sessionStateLabel(state: SandboxSessionState) {
  switch (state) {
    case "ready":
      return "已连接"
    case "connecting":
      return "连接中"
    case "error":
      return "异常"
    default:
      return "未连接"
  }
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KiB`
  return `${(value / 1024 / 1024).toFixed(1)} MiB`
}

function uploadNameError(name: string) {
  if (!name || name === "." || name === "..") return "文件名无效"
  if (name.includes("/") || name.includes("\\") || name.includes("\0")) {
    return "文件名不能包含路径分隔符"
  }
  return ""
}

function joinSandboxPath(directory: string, name: string) {
  return directory === "/"
    ? `/${name}`
    : `${directory.replace(/\/+$/, "")}/${name}`
}

function uploadErrorMessage(error: unknown) {
  const message = errorMessage(error)
  return message.includes("unsupported file operation")
    ? "当前 Worker 版本不支持二进制或大文件上传，请重新运行 Worker 安装脚本"
    : message
}
