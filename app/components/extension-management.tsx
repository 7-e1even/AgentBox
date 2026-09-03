"use client"

import { useState } from "react"
import {
  LayoutGridIcon,
  ListIcon,
  MoreHorizontalIcon,
  PackageIcon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react"

import {
  CollectionContent,
  CollectionPagination,
  CollectionPanel,
  CollectionTablePrimary,
  CollectionToolbar,
} from "@/components/collection-list"
import { CollectionHeader } from "@/components/control-plane-view"
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuModalItem,
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
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Resource } from "@/lib/platform-schema"
import {
  extensionMatchesQuery,
  extensionSourceLabel,
  sandboxExtensions,
  type SandboxExtension,
} from "@/lib/sandbox-extensions"

type ExtensionManagementProps = {
  resources: Resource[]
  canMutate: boolean
  onCreate: () => void
  onEdit: (resource: Resource) => void
  onDelete: (resource: Resource) => void
}

export function ExtensionManagement({
  resources,
  canMutate,
  onCreate,
  onEdit,
  onDelete,
}: ExtensionManagementProps) {
  const [view, setView] = useState("list")
  const [query, setQuery] = useState("")
  const [source, setSource] = useState("all")
  const [status, setStatus] = useState("all")
  const [page, setPage] = useState(1)
  const extensions = sandboxExtensions(resources)
  const filtered = extensions.filter(
    (extension) =>
      extensionMatchesQuery(extension, query) &&
      (source === "all" || (extension.spec.source ?? "custom") === source) &&
      (status === "all" || extension.enabled === (status === "enabled"))
  )
  const pageSize = 12
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  if (page > pageCount) setPage(pageCount)
  const visible = filtered.slice(
    (currentPage - 1) * pageSize,
    currentPage * pageSize
  )
  const isFiltered = query !== "" || source !== "all" || status !== "all"

  function resetFilters() {
    setQuery("")
    setSource("all")
    setStatus("all")
    setPage(1)
  }

  function actions(extension: SandboxExtension) {
    return canMutate ? (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`${extension.name} 操作`}
          >
            <MoreHorizontalIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuGroup>
            <DropdownMenuModalItem onOpen={() => onEdit(extension)}>
              <PencilIcon />
              编辑
            </DropdownMenuModalItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuModalItem
              variant="destructive"
              onOpen={() => onDelete(extension)}
            >
              <Trash2Icon />
              删除
            </DropdownMenuModalItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    ) : null
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="沙箱扩展"
        count={extensions.length}
        action={
          canMutate ? (
            <Button size="sm" onClick={onCreate}>
              <PlusIcon data-icon="inline-start" />
              新建扩展
            </Button>
          ) : undefined
        }
      />
      <CollectionContent>
        <p className="text-sm text-muted-foreground">
          管理创建沙箱时安装的软件；在沙箱模板或创建页面中选择要使用的扩展。
        </p>
        <CollectionToolbar>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
            <InputGroup className="w-full sm:max-w-xs">
              <InputGroupInput
                aria-label="搜索沙箱扩展"
                placeholder="搜索名称、标识或说明…"
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value)
                  setPage(1)
                }}
              />
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              {query && (
                <InputGroupAddon align="inline-end">
                  <InputGroupButton
                    size="icon-xs"
                    aria-label="清除搜索"
                    onClick={() => {
                      setQuery("")
                      setPage(1)
                    }}
                  >
                    <XIcon />
                  </InputGroupButton>
                </InputGroupAddon>
              )}
            </InputGroup>
            <Select
              value={source}
              onValueChange={(value) => {
                setSource(value)
                setPage(1)
              }}
            >
              <SelectTrigger size="sm" aria-label="扩展来源">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="all">全部来源</SelectItem>
                  <SelectItem value="custom">自定义</SelectItem>
                  <SelectItem value="preset">预设</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            <Select
              value={status}
              onValueChange={(value) => {
                setStatus(value)
                setPage(1)
              }}
            >
              <SelectTrigger size="sm" aria-label="扩展状态">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="all">全部状态</SelectItem>
                  <SelectItem value="enabled">已启用</SelectItem>
                  <SelectItem value="disabled">已停用</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            {isFiltered && (
              <Button variant="ghost" size="sm" onClick={resetFilters}>
                重置
              </Button>
            )}
          </div>
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            spacing={0}
            value={view}
            onValueChange={(value) => value && setView(value)}
            aria-label="扩展展示方式"
          >
            <ToggleGroupItem value="list" aria-label="列表视图">
              <ListIcon data-icon="inline-start" />
              列表
            </ToggleGroupItem>
            <ToggleGroupItem value="cards" aria-label="卡片视图">
              <LayoutGridIcon data-icon="inline-start" />
              卡片
            </ToggleGroupItem>
          </ToggleGroup>
        </CollectionToolbar>

        {filtered.length === 0 ? (
          <Empty className="min-h-72 border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <PackageIcon />
              </EmptyMedia>
              <EmptyTitle>
                {extensions.length ? "没有匹配的扩展" : "还没有沙箱扩展"}
              </EmptyTitle>
              <EmptyDescription>
                {extensions.length
                  ? "尝试其他关键词，或清除来源与状态筛选。"
                  : "添加安装和验证脚本，再在创建沙箱时复用。"}
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              {extensions.length ? (
                <Button variant="outline" onClick={resetFilters}>
                  清除筛选
                </Button>
              ) : canMutate ? (
                <Button onClick={onCreate}>
                  <PlusIcon data-icon="inline-start" />
                  创建第一个扩展
                </Button>
              ) : null}
            </EmptyContent>
          </Empty>
        ) : (
          <>
            {view === "list" ? (
              <CollectionPanel>
                <Table className="table-fixed" aria-label="沙箱扩展列表">
                  <TableHeader>
                    <TableRow>
                      <TableHead>扩展</TableHead>
                      <TableHead className="hidden w-32 md:table-cell">
                        版本
                      </TableHead>
                      <TableHead className="hidden w-24 lg:table-cell">
                        来源
                      </TableHead>
                      <TableHead className="hidden w-52 xl:table-cell">
                        安装要求
                      </TableHead>
                      <TableHead className="w-20">状态</TableHead>
                      {canMutate && (
                        <TableHead className="w-10">
                          <span className="sr-only">操作</span>
                        </TableHead>
                      )}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((extension) => (
                      <TableRow key={extension.id}>
                        <CollectionTablePrimary
                          icon={PackageIcon}
                          title={extension.name}
                          description={[extension.id, extension.description]
                            .filter(Boolean)
                            .join(" · ")}
                          onClick={
                            canMutate ? () => onEdit(extension) : undefined
                          }
                        />
                        <TableCell className="hidden md:table-cell">
                          <span className="block max-w-40 truncate">
                            {extension.spec.version || "未配置"}
                          </span>
                        </TableCell>
                        <TableCell className="hidden lg:table-cell">
                          {extensionSourceLabel(extension.spec.source)}
                        </TableCell>
                        <TableCell className="hidden xl:table-cell">
                          <span className="text-muted-foreground">
                            {extension.spec.requiresNetwork
                              ? "需要网络"
                              : "不要求网络"}{" "}
                            · {extension.spec.timeoutSeconds ?? 600} 秒
                          </span>
                        </TableCell>
                        <TableCell>
                          <Badge
                            variant={
                              extension.enabled ? "secondary" : "outline"
                            }
                          >
                            {extension.enabled ? "已启用" : "已停用"}
                          </Badge>
                        </TableCell>
                        {canMutate && (
                          <TableCell>{actions(extension)}</TableCell>
                        )}
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
                <CollectionPagination
                  currentPage={currentPage}
                  pageSize={pageSize}
                  totalItems={filtered.length}
                  onPageChange={setPage}
                />
              </CollectionPanel>
            ) : (
              <>
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {visible.map((extension) => (
                    <Card key={extension.id} size="sm" className="min-w-0">
                      <CardHeader>
                        <CardTitle className="min-w-0 truncate">
                          {extension.name}
                        </CardTitle>
                        <CardDescription className="truncate">
                          {extension.id}
                        </CardDescription>
                        <CardAction>{actions(extension)}</CardAction>
                      </CardHeader>
                      <CardContent className="flex flex-1 flex-col gap-3">
                        <p className="line-clamp-2 min-h-10 text-sm text-muted-foreground">
                          {extension.description || "暂无说明"}
                        </p>
                        <div className="flex flex-wrap items-center gap-2">
                          <Badge
                            variant="outline"
                            className="max-w-full"
                            title={extension.spec.version}
                          >
                            <span className="truncate">
                              {extension.spec.version || "未配置版本"}
                            </span>
                          </Badge>
                          <Badge variant="outline">
                            {extensionSourceLabel(extension.spec.source)}
                          </Badge>
                          <Badge
                            variant={
                              extension.enabled ? "secondary" : "outline"
                            }
                          >
                            {extension.enabled ? "已启用" : "已停用"}
                          </Badge>
                        </div>
                      </CardContent>
                      <CardFooter className="justify-between gap-2">
                        <span className="text-xs text-muted-foreground">
                          {extension.spec.requiresNetwork
                            ? "需要网络"
                            : "不要求网络"}{" "}
                          · {extension.spec.timeoutSeconds ?? 600} 秒
                        </span>
                        {canMutate && (
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => onEdit(extension)}
                          >
                            配置
                          </Button>
                        )}
                      </CardFooter>
                    </Card>
                  ))}
                </div>
                <CollectionPanel>
                  <CollectionPagination
                    currentPage={currentPage}
                    pageSize={pageSize}
                    totalItems={filtered.length}
                    onPageChange={setPage}
                  />
                </CollectionPanel>
              </>
            )}
          </>
        )}
      </CollectionContent>
    </section>
  )
}
