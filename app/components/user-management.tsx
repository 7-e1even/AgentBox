"use client"

import { useMemo, useState, type FormEvent } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react"

import {
  CollectionContent,
  CollectionTablePrimaryContent,
} from "@/components/collection-list"
import { CollectionHeader } from "@/components/control-plane-view"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
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
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import {
  userInputSchema,
  type ManagedUser,
  type UserInput,
  type UserRole,
  type UserStatus,
} from "@/lib/user-schema"

const roleLabels: Record<UserRole, string> = {
  admin: "管理员",
  operator: "运维人员",
  viewer: "只读成员",
}

export function UserManagement({
  users,
  currentUser,
  busyId,
  onSave,
  onDelete,
}: {
  users: ManagedUser[]
  currentUser: ManagedUser
  busyId: string | null
  onSave: (input: UserInput, editing: ManagedUser | null) => Promise<void>
  onDelete: (user: ManagedUser) => Promise<void>
}) {
  const [editing, setEditing] = useState<ManagedUser | null | undefined>()
  const [deleting, setDeleting] = useState<ManagedUser | null>(null)
  const columns = useMemo(
    () =>
      userColumns({
        currentUser,
        onEdit: setEditing,
        onDelete: setDeleting,
      }),
    [currentUser]
  )

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <CollectionHeader
        title="用户管理"
        count={users.length}
        action={
          <Button size="sm" onClick={() => setEditing(null)}>
            <PlusIcon data-icon="inline-start" />
            添加用户
          </Button>
        }
      />
      <CollectionContent>
        <div className="flex flex-col gap-1">
          <h2 className="text-xl font-semibold tracking-tight">团队成员</h2>
          <p className="text-sm text-muted-foreground">
            管理登录账号、角色和访问状态。只有管理员可以访问本页。
          </p>
        </div>
        {users.length > 0 ? (
          <DataTable
            data={users}
            columns={columns}
            getRowId={(user) => user.id}
            searchPlaceholder="搜索名称或邮箱…"
            searchValue={(user) =>
              `${user.name} ${user.email} ${roleLabels[user.role]}`
            }
            filters={[
              {
                columnId: "role",
                title: "角色",
                options: Object.entries(roleLabels).map(([value, label]) => ({
                  value,
                  label,
                })),
              },
              {
                columnId: "status",
                title: "状态",
                options: [
                  { label: "已启用", value: "active" },
                  { label: "已停用", value: "disabled" },
                ],
              },
            ]}
          />
        ) : (
          <Empty className="min-h-72 border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <UsersIcon />
              </EmptyMedia>
              <EmptyTitle>还没有用户</EmptyTitle>
              <EmptyDescription>
                创建账号后，团队成员即可登录 AgentBox。
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
      </CollectionContent>

      {editing !== undefined && (
        <UserEditorDialog
          key={editing?.id ?? "new-user"}
          user={editing}
          currentUser={currentUser}
          onOpenChange={(open) => !open && setEditing(undefined)}
          onSave={async (input) => {
            await onSave(input, editing)
            setEditing(undefined)
          }}
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
            <AlertDialogTitle>删除 {deleting?.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              该用户的全部登录会话会立即失效，此操作无法恢复。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={Boolean(busyId)}
              onClick={async () => {
                if (!deleting) return
                await onDelete(deleting)
                setDeleting(null)
              }}
            >
              {busyId && <Spinner data-icon="inline-start" />}
              永久删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function userColumns({
  currentUser,
  onEdit,
  onDelete,
}: {
  currentUser: ManagedUser
  onEdit: (user: ManagedUser) => void
  onDelete: (user: ManagedUser) => void
}): ColumnDef<ManagedUser>[] {
  return [
    {
      id: "name",
      accessorFn: (user) => user.name,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="用户" />
      ),
      cell: ({ row }) => {
        const user = row.original
        const isCurrent = user.id === currentUser.id
        return (
          <CollectionTablePrimaryContent
            title={
              <span className="flex items-center gap-2">
                {user.name}
                {isCurrent ? <Badge variant="outline">当前</Badge> : null}
              </span>
            }
            description={user.email}
            media={
              <Avatar className="size-8 rounded-lg">
                <AvatarFallback className="rounded-lg">
                  {initials(user.name)}
                </AvatarFallback>
              </Avatar>
            }
          />
        )
      },
      meta: { label: "用户" },
      enableHiding: false,
    },
    {
      id: "role",
      accessorFn: (user) => user.role,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="角色" />
      ),
      cell: ({ row }) => roleLabels[row.original.role],
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "角色" },
    },
    {
      id: "status",
      accessorFn: (user) => user.status,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="状态" />
      ),
      cell: ({ row }) => (
        <Badge
          variant={row.original.status === "active" ? "secondary" : "outline"}
        >
          {row.original.status === "active" ? "已启用" : "已停用"}
        </Badge>
      ),
      filterFn: (row, columnId, filterValue) =>
        (filterValue as string[]).includes(row.getValue(columnId)),
      meta: { label: "状态" },
    },
    {
      id: "lastLoginAt",
      accessorFn: (user) => user.lastLoginAt ?? "",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="最近登录" />
      ),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {formatDate(row.original.lastLoginAt)}
        </span>
      ),
      meta: { label: "最近登录", className: "hidden lg:table-cell" },
    },
    {
      id: "createdAt",
      accessorFn: (user) => user.createdAt,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="创建时间" />
      ),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {formatDate(row.original.createdAt)}
        </span>
      ),
      meta: { label: "创建时间", className: "hidden xl:table-cell" },
    },
    {
      id: "actions",
      cell: ({ row }) => {
        const user = row.original
        const isCurrent = user.id === currentUser.id
        return (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`${user.name} 操作`}
              >
                <MoreHorizontalIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuGroup>
                <DropdownMenuItem onClick={() => onEdit(user)}>
                  <PencilIcon />
                  编辑
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem
                  variant="destructive"
                  disabled={isCurrent}
                  onClick={() => onDelete(user)}
                >
                  <Trash2Icon />
                  删除
                </DropdownMenuItem>
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        )
      },
      enableSorting: false,
      enableHiding: false,
      meta: { className: "w-10" },
    },
  ]
}

function UserEditorDialog({
  user,
  currentUser,
  onOpenChange,
  onSave,
}: {
  user: ManagedUser | null
  currentUser: ManagedUser
  onOpenChange: (open: boolean) => void
  onSave: (input: UserInput) => Promise<void>
}) {
  const [role, setRole] = useState<UserRole>(user?.role ?? "viewer")
  const [status, setStatus] = useState<UserStatus>(user?.status ?? "active")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const isCurrent = user?.id === currentUser.id

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError("")
    const form = new FormData(event.currentTarget)
    const result = userInputSchema.safeParse({
      name: form.get("name"),
      email: form.get("email"),
      password: form.get("password") ?? "",
      role,
      status,
    })
    if (!result.success) {
      setError(result.error.issues[0]?.message ?? "请检查用户信息")
      setBusy(false)
      return
    }
    if (!user && result.data.password === "") {
      setError("新用户必须设置密码")
      setBusy(false)
      return
    }
    try {
      await onSave(result.data)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存用户失败")
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{user ? "编辑用户" : "添加用户"}</DialogTitle>
          <DialogDescription>
            {user
              ? "更新账号资料、角色或访问状态。"
              : "创建一个可以登录 AgentBox 的账号。"}
          </DialogDescription>
        </DialogHeader>
        <form id="user-editor" onSubmit={submit}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="user-name">名称</FieldLabel>
              <Input
                id="user-name"
                name="name"
                defaultValue={user?.name}
                required
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="user-email">邮箱</FieldLabel>
              <Input
                id="user-email"
                name="email"
                type="email"
                defaultValue={user?.email}
                required
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="user-password">密码</FieldLabel>
              <Input
                id="user-password"
                name="password"
                type="password"
                autoComplete="new-password"
                minLength={8}
                required={!user}
              />
              <FieldDescription>
                {user
                  ? "留空表示不修改；修改后现有会话会失效。"
                  : "至少 8 个字符。"}
              </FieldDescription>
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field data-disabled={isCurrent}>
                <FieldLabel>角色</FieldLabel>
                <Select
                  value={role}
                  onValueChange={(value) => setRole(value as UserRole)}
                  disabled={isCurrent}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectItem value="admin">管理员</SelectItem>
                      <SelectItem value="operator">运维人员</SelectItem>
                      <SelectItem value="viewer">只读成员</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
              <Field data-disabled={isCurrent}>
                <FieldLabel>状态</FieldLabel>
                <Select
                  value={status}
                  onValueChange={(value) => setStatus(value as UserStatus)}
                  disabled={isCurrent}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectItem value="active">启用</SelectItem>
                      <SelectItem value="disabled">停用</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
            </div>
            {isCurrent && (
              <FieldDescription>
                当前账号不能停用或移除管理员角色。
              </FieldDescription>
            )}
            {error && <FieldError>{error}</FieldError>}
          </FieldGroup>
        </form>
        <DialogFooter>
          <Button
            variant="outline"
            type="button"
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button form="user-editor" type="submit" disabled={busy}>
            {busy && <Spinner data-icon="inline-start" />}
            {busy ? "正在保存…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function initials(name: string) {
  return name
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase()
}

function formatDate(value: string | null) {
  if (!value) return "从未登录"
  return new Intl.DateTimeFormat("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value))
}
