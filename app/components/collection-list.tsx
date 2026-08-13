import * as React from "react"
import { SearchIcon, type LucideIcon } from "lucide-react"

import { Input } from "@/components/ui/input"
import { Item, ItemGroup, ItemSeparator } from "@/components/ui/item"
import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination"
import { Separator } from "@/components/ui/separator"
import { Table, TableCell } from "@/components/ui/table"
import { cn } from "@/lib/utils"

function CollectionContent({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div className="min-h-0 flex-1 overflow-auto p-4 sm:p-6">
      <div
        className={cn(
          "mx-auto flex w-full max-w-[1600px] flex-col gap-3",
          className
        )}
        {...props}
      />
    </div>
  )
}

function CollectionToolbar({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between",
        className
      )}
      {...props}
    />
  )
}

function CollectionSearch({
  className,
  ...props
}: React.ComponentProps<typeof Input>) {
  return (
    <div className="relative w-full max-w-sm">
      <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input className={cn("pl-9", className)} {...props} />
    </div>
  )
}

function CollectionPanel({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn("overflow-hidden rounded-lg border bg-card", className)}
      {...props}
    />
  )
}

function CollectionTable({
  pagination,
  ...props
}: React.ComponentProps<typeof Table> & { pagination?: React.ReactNode }) {
  return (
    <CollectionPanel>
      <Table {...props} />
      {pagination}
    </CollectionPanel>
  )
}

function CollectionTablePrimary({
  icon: Icon,
  media,
  title,
  description,
  onClick,
  className,
}: {
  icon?: LucideIcon
  media?: React.ReactNode
  title: React.ReactNode
  description?: React.ReactNode
  onClick?: () => void
  className?: string
}) {
  return (
    <TableCell className={className}>
      <CollectionTablePrimaryContent
        icon={Icon}
        media={media}
        title={title}
        description={description}
        onClick={onClick}
      />
    </TableCell>
  )
}

function CollectionTablePrimaryContent({
  icon: Icon,
  media,
  title,
  description,
  onClick,
}: {
  icon?: LucideIcon
  media?: React.ReactNode
  title: React.ReactNode
  description?: React.ReactNode
  onClick?: () => void
}) {
  const content = (
    <>
      {media ??
        (Icon ? (
          <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
            <Icon className="size-4" />
          </span>
        ) : null)}
      <span className="min-w-0">
        <span className="block max-w-72 truncate font-medium">{title}</span>
        {description ? (
          <span className="block max-w-72 truncate text-xs text-muted-foreground">
            {description}
          </span>
        ) : null}
      </span>
    </>
  )

  return onClick ? (
    <button
      type="button"
      className="flex min-w-0 items-center gap-3 text-left"
      onClick={onClick}
    >
      {content}
    </button>
  ) : (
    <div className="flex min-w-0 items-center gap-3">{content}</div>
  )
}

function CollectionList({
  className,
  children,
  ...props
}: React.ComponentProps<typeof ItemGroup>) {
  const items = React.Children.toArray(children)
  return (
    <ItemGroup
      className={cn(
        "gap-0 overflow-hidden rounded-lg border bg-card",
        className
      )}
      {...props}
    >
      {items.map((item, index) => (
        <React.Fragment key={index}>
          {item}
          {index < items.length - 1 && <ItemSeparator className="m-0" />}
        </React.Fragment>
      ))}
    </ItemGroup>
  )
}

function CollectionListItem({
  className,
  ...props
}: React.ComponentProps<typeof Item>) {
  return (
    <Item
      role="listitem"
      className={cn(
        "rounded-none border-0 px-4 py-3 sm:flex-nowrap",
        className
      )}
      {...props}
    />
  )
}

function CollectionPagination({
  currentPage,
  pageSize,
  totalItems,
  onPageChange,
}: {
  currentPage: number
  pageSize: number
  totalItems: number
  onPageChange: (page: number) => void
}) {
  if (totalItems === 0) return null

  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize))
  const page = Math.min(currentPage, totalPages)
  const firstItem = (page - 1) * pageSize + 1
  const lastItem = Math.min(page * pageSize, totalItems)

  return (
    <>
      <Separator />
      <div className="flex min-h-14 flex-col gap-2 bg-muted/20 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-center text-xs text-muted-foreground sm:text-left">
          共 {totalItems} 个 · 当前 {firstItem}–{lastItem}
        </p>
        <Pagination className="mx-0 w-auto justify-end">
          <PaginationContent>
            <PaginationItem>
              <PaginationPrevious
                href="#"
                text="上一页"
                aria-label="上一页"
                aria-disabled={page === 1}
                tabIndex={page === 1 ? -1 : undefined}
                className={cn(page === 1 && "pointer-events-none opacity-50")}
                onClick={(event) => {
                  event.preventDefault()
                  if (page > 1) onPageChange(page - 1)
                }}
              />
            </PaginationItem>
            {paginationItems(page, totalPages).map((item, index) =>
              item === "ellipsis" ? (
                <PaginationItem key={`ellipsis-${index}`}>
                  <PaginationEllipsis />
                </PaginationItem>
              ) : (
                <PaginationItem key={item}>
                  <PaginationLink
                    href="#"
                    isActive={item === page}
                    aria-label={`第 ${item} 页`}
                    onClick={(event) => {
                      event.preventDefault()
                      onPageChange(item)
                    }}
                  >
                    {item}
                  </PaginationLink>
                </PaginationItem>
              )
            )}
            <PaginationItem>
              <PaginationNext
                href="#"
                text="下一页"
                aria-label="下一页"
                aria-disabled={page === totalPages}
                tabIndex={page === totalPages ? -1 : undefined}
                className={cn(
                  page === totalPages && "pointer-events-none opacity-50"
                )}
                onClick={(event) => {
                  event.preventDefault()
                  if (page < totalPages) onPageChange(page + 1)
                }}
              />
            </PaginationItem>
          </PaginationContent>
        </Pagination>
      </div>
    </>
  )
}

function paginationItems(currentPage: number, totalPages: number) {
  if (totalPages <= 5) {
    return Array.from({ length: totalPages }, (_, index) => index + 1)
  }

  const pages = [1, currentPage - 1, currentPage, currentPage + 1, totalPages]
    .filter((page) => page >= 1 && page <= totalPages)
    .filter((page, index, all) => all.indexOf(page) === index)
    .sort((a, b) => a - b)
  const items: Array<number | "ellipsis"> = []
  pages.forEach((page, index) => {
    if (index > 0 && page - pages[index - 1] > 1) items.push("ellipsis")
    items.push(page)
  })
  return items
}

export {
  CollectionContent,
  CollectionList,
  CollectionListItem,
  CollectionPagination,
  CollectionPanel,
  CollectionSearch,
  CollectionTable,
  CollectionTablePrimary,
  CollectionTablePrimaryContent,
  CollectionToolbar,
}
