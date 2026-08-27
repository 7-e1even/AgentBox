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
  RefreshCwIcon,
  SaveIcon,
  SearchIcon,
  ShieldCheckIcon,
  TerminalIcon,
  WifiIcon,
  XIcon,
} from "lucide-react"
import Link from "next/link"

import { SandboxCodeEditor } from "@/components/sandbox-code-editor"
import { SandboxDesktop } from "@/components/sandbox-desktop"
import {
  SandboxTerminal,
  type SandboxTerminalHandle,
} from "@/components/sandbox-terminal"
import { SiteHeader } from "@/components/site-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
import { errorMessage, requestJson } from "@/lib/api-client"
import { writeClipboardText } from "@/lib/clipboard"
import {
  resourceSchema,
  resourcesResponseSchema,
  type Resource,
} from "@/lib/platform-schema"
import {
  sandboxFileEntriesSchema,
  type SandboxFileEntry,
} from "@/lib/sandbox-file-schema"
import { isSandboxDesktopEnabled } from "@/lib/sandbox-desktop"
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
  const terminalRef = useRef<SandboxTerminalHandle>(null)
  const uploadInputRef = useRef<HTMLInputElement>(null)
  const uploadDirectoryRef = useRef("/")
  const session = useMemo(
    () => new SandboxSessionClient(sandboxId),
    [sandboxId]
  )
  const [sandbox, setSandbox] = useState<Resource | null>(null)
  const [resources, setResources] = useState<Resource[]>([])
  const [initializing, setInitializing] = useState(true)
  const [loadError, setLoadError] = useState("")
  const [directoryEntries, setDirectoryEntries] = useState<
    Record<string, SandboxFileEntry[]>
  >({})
  const [expandedDirectories, setExpandedDirectories] = useState(
    () => new Set<string>(["/"])
  )
  const [loadingDirectories, setLoadingDirectories] = useState(
    () => new Set<string>()
  )
  const [fileFilter, setFileFilter] = useState("")
  const [openFiles, setOpenFiles] = useState<OpenFile[]>([])
  const [activeFilePath, setActiveFilePath] = useState<string | null>(null)
  const [openingFilePath, setOpeningFilePath] = useState<string | null>(null)
  const [savingFilePath, setSavingFilePath] = useState<string | null>(null)
  const [sessionState, setSessionState] =
    useState<SandboxSessionState>("disconnected")
  const [propertiesTab, setPropertiesTab] = useState("sandbox")
  const [workspaceMode, setWorkspaceMode] = useState<"code" | "desktop">("code")
  const [showExplorer, setShowExplorer] = useState(true)
  const [showInspector, setShowInspector] = useState(false)
  const [showTerminal, setShowTerminal] = useState(true)
  const [showSearch, setShowSearch] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(
    null
  )
  const [dragUploadTarget, setDragUploadTarget] = useState<string | null>(null)

  const isRunning = sandbox?.spec.status === "running"
  const runtime = resources.find((item) => item.id === sandbox?.spec.runtimeId)
  const server = resources.find((item) => item.id === sandbox?.spec.serverId)
  const inheritedAgents = runtime?.spec.agentTools
  const sandboxAgents = stringList(
    Array.isArray(sandbox?.spec.agentTools)
      ? sandbox.spec.agentTools
      : inheritedAgents
  )
  const desktopEnabled = isSandboxDesktopEnabled(sandbox?.spec, runtime?.spec)
  const activeWorkspaceMode = desktopEnabled ? workspaceMode : "code"
  const activeFile = useMemo(
    () => openFiles.find((file) => file.path === activeFilePath) ?? null,
    [activeFilePath, openFiles]
  )
  const hasDirtyFiles = openFiles.some(
    (file) => file.content !== file.savedContent
  )
  const showEditorPanel = openFiles.length > 0 || !showTerminal
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

  const loadDirectory = useCallback(
    async (path: string, showToast = true) => {
      setLoadingDirectories((current) => new Set(current).add(path))
      try {
        const entries = sandboxFileEntriesSchema.parse(
          await runFileOperation({ kind: "list", path })
        )
        setDirectoryEntries((current) => ({ ...current, [path]: entries }))
      } catch (error) {
        if (showToast) {
          toast.error("无法打开目录", { description: errorMessage(error) })
        }
      } finally {
        setLoadingDirectories((current) => {
          const next = new Set(current)
          next.delete(path)
          return next
        })
      }
    },
    [runFileOperation]
  )

  useEffect(() => {
    const unsubscribe = session.onState((state) => setSessionState(state))
    if (isRunning) session.start()
    else session.stop()
    return () => {
      unsubscribe()
      session.stop()
    }
  }, [isRunning, session])

  useEffect(() => {
    let cancelled = false
    async function initialize() {
      setInitializing(true)
      setLoadError("")
      try {
        const nextResources = resourcesResponseSchema.parse(
          await requestJson<unknown>("/api/resources")
        ).resources
        const nextSandbox = nextResources.find(
          (resource) => resource.kind === "sandbox" && resource.id === sandboxId
        )
        if (!nextSandbox) throw new Error("沙箱不存在")
        if (cancelled) return
        setResources(nextResources)
        setSandbox(resourceSchema.parse(nextSandbox))
        setInitializing(false)
      } catch (error) {
        if (!cancelled) setLoadError(errorMessage(error))
      } finally {
        if (!cancelled) setInitializing(false)
      }
    }
    void initialize()
    return () => {
      cancelled = true
    }
  }, [loadDirectory, sandboxId])

  useEffect(() => {
    if (
      isRunning &&
      sessionState === "ready" &&
      !directoryEntries["/"] &&
      !loadingDirectories.has("/")
    ) {
      const timeout = window.setTimeout(() => void loadDirectory("/", false), 0)
      return () => window.clearTimeout(timeout)
    }
  }, [
    directoryEntries,
    isRunning,
    loadDirectory,
    loadingDirectories,
    sessionState,
  ])

  useEffect(() => {
    if (!hasDirtyFiles) return
    const warnBeforeLeaving = (event: BeforeUnloadEvent) => {
      event.preventDefault()
    }
    window.addEventListener("beforeunload", warnBeforeLeaving)
    return () => window.removeEventListener("beforeunload", warnBeforeLeaving)
  }, [hasDirtyFiles])

  async function openEntry(entry: SandboxFileEntry) {
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
    } catch (error) {
      toast.error("无法打开文件", { description: errorMessage(error) })
    } finally {
      setOpeningFilePath(null)
    }
  }

  async function saveActiveFile() {
    if (!activeFile || activeFile.content === activeFile.savedContent) return
    const content = activeFile.content
    setSavingFilePath(activeFile.path)
    try {
      await runFileOperation({
        kind: "write",
        path: activeFile.path,
        content,
      })
      setOpenFiles((current) =>
        current.map((file) =>
          file.path === activeFile.path
            ? { ...file, savedContent: content }
            : file
        )
      )
      toast.success("文件已保存", { description: activeFile.path })
    } catch (error) {
      toast.error("保存失败", { description: errorMessage(error) })
    } finally {
      setSavingFilePath(null)
    }
  }

  function closeFile(path: string) {
    const file = openFiles.find((item) => item.path === path)
    if (!file) return
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

    const directory = uploadDirectoryRef.current
    let uploaded = 0
    const failures: string[] = []
    try {
      const currentEntries = sandboxFileEntriesSchema.parse(
        await runFileOperation({ kind: "list", path: directory })
      )
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
            setUploadProgress({
              fileName: file.name,
              fileIndex: index + 1,
              fileCount: selectedFiles.length,
              percent,
            })
          )
          existingNames.add(file.name)
          uploaded += 1
        } catch (error) {
          failures.push(`${file.name}：${uploadErrorMessage(error)}`)
        }
      }

      setExpandedDirectories((current) => new Set(current).add(directory))
      await loadDirectory(directory, false)
      if (uploaded > 0) {
        toast.success(`已上传 ${uploaded} 个文件`, { description: directory })
      }
      if (failures.length > 0) {
        toast.error(`${failures.length} 个文件未上传`, {
          description: failures.slice(0, 3).join("；"),
        })
      }
    } catch (error) {
      toast.error("上传失败", { description: errorMessage(error) })
    } finally {
      setUploadProgress(null)
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
    try {
      await writeClipboardText(path)
      toast.success("路径已复制", { description: path })
    } catch {
      toast.error("复制失败", { description: "浏览器未授权访问剪贴板。" })
    }
  }

  function renderEntry(entry: SandboxFileEntry, depth = 0) {
    const expanded = expandedDirectories.has(entry.path)
    const loading = loadingDirectories.has(entry.path)
    const active = activeFilePath === entry.path
    return (
      <div
        key={entry.path}
        style={{ contentVisibility: "auto", containIntrinsicSize: "24px" }}
      >
        <ContextMenu>
          <ContextMenuTrigger asChild>
            <button
              type="button"
              aria-expanded={entry.type === "directory" ? expanded : undefined}
              className={cn(
                "flex h-6 w-full items-center gap-1 pr-2 text-left text-[13px] hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
                active && "bg-accent text-accent-foreground",
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
      <aside className="flex h-full min-w-0 flex-col bg-muted/10">
        <div className="flex h-9 shrink-0 items-center gap-1 border-b px-2">
          <h2 className="min-w-0 flex-1 truncate text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
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
                disabled={loadingDirectories.has("/")}
                onClick={() => {
                  setDirectoryEntries({})
                  setExpandedDirectories(new Set(["/"]))
                  void loadDirectory("/")
                }}
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
        <div className="flex h-7 shrink-0 items-center gap-1 border-b px-2 text-[11px] font-semibold tracking-[0.06em] uppercase">
          <ChevronDownIcon aria-hidden="true" className="size-3" />
          <span className="truncate">{sandbox?.id}</span>
        </div>
        <ContextMenu>
          <ContextMenuTrigger asChild>
            <div
              aria-label="资源管理器文件列表"
              className={cn(
                "relative min-h-0 flex-1 overflow-auto py-1",
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
              {fileFilter.trim() ? (
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
                disabled={loadingDirectories.has("/")}
                onSelect={() => {
                  setDirectoryEntries({})
                  setExpandedDirectories(new Set(["/"]))
                  void loadDirectory("/")
                }}
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
          className="flex h-9 shrink-0 items-stretch overflow-x-auto border-b bg-muted/20"
        >
          {openFiles.length > 0 ? (
            openFiles.map((file) => {
              const active = activeFilePath === file.path
              const dirty = file.content !== file.savedContent
              return (
                <div
                  key={file.path}
                  className={cn(
                    "group flex max-w-52 min-w-0 shrink-0 items-center border-r",
                    active && "bg-background"
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
            <div className="flex items-center px-3 text-xs text-muted-foreground">
              未打开文件
            </div>
          )}
          <div className="ml-auto flex shrink-0 items-center border-l px-1">
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="保存当前文件"
              disabled={
                !activeFile || activeFile.content === activeFile.savedContent
              }
              onClick={() => void saveActiveFile()}
            >
              {savingFilePath === activeFile?.path ? (
                <Spinner />
              ) : (
                <SaveIcon aria-hidden="true" />
              )}
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
            <Empty className="h-full rounded-none border-0">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Code2Icon aria-hidden="true" />
                </EmptyMedia>
                <EmptyTitle>打开文件开始编辑</EmptyTitle>
                <EmptyDescription>
                  文件通过 root 权限从当前沙箱读取。
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
        <div className="flex h-8 shrink-0 items-center border-b bg-muted/20">
          <div className="flex h-full items-center border-b-2 border-foreground px-3 text-[11px] font-medium tracking-wide uppercase">
            终端
          </div>
          <div className="ml-auto flex items-center px-1">
            <Badge variant="outline" className="h-5 rounded-sm text-[10px]">
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
              onClick={() => setShowTerminal(false)}
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
              activeFile.content === activeFile.savedContent
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
      <aside className="flex h-full min-w-0 flex-col bg-muted/10">
        <div className="flex h-9 shrink-0 items-center border-b px-2">
          <h2 className="min-w-0 flex-1 truncate text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
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
      </aside>
    )
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <SiteHeader
        title={
          <Link
            href="/sandboxes"
            className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
          >
            <ArrowLeftIcon aria-hidden="true" className="size-3.5" />
            沙箱
          </Link>
        }
        center={
          <WorkspaceModeToggle
            value={activeWorkspaceMode}
            desktopEnabled={desktopEnabled}
            onChange={setWorkspaceMode}
            className="pointer-events-auto mx-auto"
          />
        }
        action={
          <>
            <WorkspaceModeToggle
              value={activeWorkspaceMode}
              desktopEnabled={desktopEnabled}
              onChange={setWorkspaceMode}
              className="lg:hidden"
            />
            {activeWorkspaceMode === "code" ? (
              <>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      variant="ghost"
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
                      variant="ghost"
                      size="icon-sm"
                      aria-label={showTerminal ? "隐藏终端" : "显示终端"}
                      aria-pressed={showTerminal}
                      onClick={() => setShowTerminal((visible) => !visible)}
                    >
                      <PanelBottomIcon aria-hidden="true" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>终端</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      variant="ghost"
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
              </>
            ) : null}
          </>
        }
      />

      {initializing ? (
        <div className="flex min-h-0 flex-1 items-center justify-center gap-2 text-muted-foreground">
          <Spinner />
          正在连接沙箱…
        </div>
      ) : loadError || !sandbox ? (
        <Empty className="min-h-0 flex-1 rounded-none border-0">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InfoIcon aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>无法打开沙箱</EmptyTitle>
            <EmptyDescription>{loadError || "沙箱不存在"}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : !isRunning ? (
        <Empty className="min-h-0 flex-1 rounded-none border-0">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BoxIcon aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>沙箱未运行</EmptyTitle>
            <EmptyDescription>
              先启动沙箱，再进入 IDE 操作文件和终端。
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
          <div className="min-h-0 flex-1 overflow-hidden">
            {activeWorkspaceMode === "desktop" ? (
              <SandboxDesktop
                sandboxId={sandbox.id}
                active
                running={isRunning}
              />
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
                      {renderExplorer()}
                    </ResizablePanel>
                    <ResizableHandle aria-label="调整资源管理器宽度" />
                  </>
                ) : null}
                <ResizablePanel id="sandbox-editor" minSize="320px">
                  <ResizablePanelGroup orientation="vertical">
                    {showEditorPanel ? (
                      <ResizablePanel
                        id="sandbox-code"
                        defaultSize={showTerminal ? "65%" : "100%"}
                        minSize="180px"
                      >
                        {renderEditor()}
                      </ResizablePanel>
                    ) : null}
                    {showTerminal ? (
                      <>
                        {showEditorPanel ? (
                          <ResizableHandle aria-label="调整终端高度" />
                        ) : null}
                        <ResizablePanel
                          id="sandbox-terminal"
                          defaultSize={showEditorPanel ? "35%" : "100%"}
                          minSize="120px"
                          maxSize={showEditorPanel ? "70%" : "100%"}
                        >
                          {renderTerminal()}
                        </ResizablePanel>
                      </>
                    ) : null}
                  </ResizablePanelGroup>
                </ResizablePanel>
                {showInspector ? (
                  <>
                    <ResizableHandle aria-label="调整检查器宽度" />
                    <ResizablePanel
                      id="sandbox-inspector"
                      defaultSize="270px"
                      minSize="220px"
                      maxSize="40%"
                    >
                      {renderInspector()}
                    </ResizablePanel>
                  </>
                ) : null}
              </ResizablePanelGroup>
            )}
          </div>
          <footer className="flex h-6 shrink-0 items-center gap-3 border-t bg-sidebar px-2 text-[11px] text-sidebar-foreground">
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
  onChange,
  className,
}: {
  value: "code" | "desktop"
  desktopEnabled: boolean
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
        代码
      </ToggleGroupItem>
      <ToggleGroupItem
        value="desktop"
        disabled={!desktopEnabled}
        title={desktopEnabled ? "打开图形桌面" : "此沙箱未启用图形桌面"}
      >
        <MonitorIcon aria-hidden="true" data-icon="inline-start" />
        桌面
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

function stringList(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : []
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
