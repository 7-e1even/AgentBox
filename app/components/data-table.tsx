"use client"

import * as React from "react"
import {
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  type Column,
  type ColumnDef,
  type ColumnFiltersState,
  type FilterFn,
  type SortingState,
  type Table as TanStackTable,
  type VisibilityState,
  useReactTable,
} from "@tanstack/react-table"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  ChevronsLeftIcon,
  ChevronsRightIcon,
  ChevronsUpDownIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  EyeOffIcon,
  PlusCircleIcon,
  RotateCcwIcon,
  SearchIcon,
  SlidersHorizontalIcon,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
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
import { cn } from "@/lib/utils"

type DataTableColumnMeta = {
  label?: string
  className?: string
}

type DataTableFilterOption = {
  label: string
  value: string
}

type DataTableFilter = {
  columnId: string
  title: string
  options: DataTableFilterOption[]
}

type DataTableProps<TData, TValue> = {
  columns: ColumnDef<TData, TValue>[]
  data: TData[]
  filters?: DataTableFilter[]
  getRowId?: (row: TData) => string
  initialPageSize?: number
  searchPlaceholder?: string
  searchValue: (row: TData) => string
  emptyMessage?: string
}

const arrayFilter: FilterFn<unknown> = (row, columnId, filterValue) => {
  const selected = filterValue as string[]
  return selected.length === 0 || selected.includes(String(row.getValue(columnId)))
}

function DataTable<TData, TValue>({
  columns,
  data,
  filters = [],
  getRowId,
  initialPageSize = 10,
  searchPlaceholder = "搜索...",
  searchValue,
  emptyMessage = "没有符合条件的数据。",
}: DataTableProps<TData, TValue>) {
  const [sorting, setSorting] = React.useState<SortingState>([])
  const [columnFilters, setColumnFilters] =
    React.useState<ColumnFiltersState>([])
  const [columnVisibility, setColumnVisibility] =
    React.useState<VisibilityState>({})
  const [globalFilter, setGlobalFilter] = React.useState("")

  // TanStack Table exposes mutable table methods by design.
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data,
    columns,
    filterFns: { array: arrayFilter },
    getRowId,
    state: { sorting, columnFilters, columnVisibility, globalFilter },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    onColumnVisibilityChange: setColumnVisibility,
    onGlobalFilterChange: setGlobalFilter,
    globalFilterFn: (row, _columnId, value) =>
      searchValue(row.original)
        .toLocaleLowerCase()
        .includes(String(value).trim().toLocaleLowerCase()),
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    initialState: { pagination: { pageIndex: 0, pageSize: initialPageSize } },
  })

  return (
    <div className="space-y-3">
      <DataTableToolbar
        table={table}
        filters={filters}
        searchPlaceholder={searchPlaceholder}
        globalFilter={globalFilter}
        onGlobalFilterChange={setGlobalFilter}
      />
      <div className="overflow-hidden rounded-lg border bg-card">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => {
                  const meta = header.column.columnDef.meta as
                    | DataTableColumnMeta
                    | undefined
                  return (
                    <TableHead key={header.id} className={meta?.className}>
                      {header.isPlaceholder
                        ? null
                        : flexRender(
                            header.column.columnDef.header,
                            header.getContext()
                          )}
                    </TableHead>
                  )
                })}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length ? (
              table.getRowModel().rows.map((row) => (
                <TableRow key={row.id}>
                  {row.getVisibleCells().map((cell) => {
                    const meta = cell.column.columnDef.meta as
                      | DataTableColumnMeta
                      | undefined
                    return (
                      <TableCell key={cell.id} className={meta?.className}>
                        {flexRender(
                          cell.column.columnDef.cell,
                          cell.getContext()
                        )}
                      </TableCell>
                    )
                  })}
                </TableRow>
              ))
            ) : (
              <TableRow>
                <TableCell
                  colSpan={table.getVisibleLeafColumns().length}
                  className="h-28 text-center text-muted-foreground"
                >
                  {emptyMessage}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <DataTablePagination table={table} />
    </div>
  )
}

function DataTableColumnHeader<TData, TValue>({
  column,
  title,
  className,
}: {
  column: Column<TData, TValue>
  title: string
  className?: string
}) {
  if (!column.getCanSort()) {
    return <span className={className}>{title}</span>
  }

  const sorted = column.getIsSorted()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className={cn("-ml-2", className)}
        >
          <span>{title}</span>
          {sorted === "desc" ? (
            <ArrowDownIcon data-icon="inline-end" />
          ) : sorted === "asc" ? (
            <ArrowUpIcon data-icon="inline-end" />
          ) : (
            <ChevronsUpDownIcon data-icon="inline-end" />
          )}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuGroup>
          <DropdownMenuItem onSelect={() => column.toggleSorting(false)}>
            <ArrowUpIcon />
            升序
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => column.toggleSorting(true)}>
            <ArrowDownIcon />
            降序
          </DropdownMenuItem>
        </DropdownMenuGroup>
        {column.getCanHide() ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuItem onSelect={() => column.toggleVisibility(false)}>
                <EyeOffIcon />
                隐藏列
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function DataTableToolbar<TData>({
  table,
  filters,
  searchPlaceholder,
  globalFilter,
  onGlobalFilterChange,
}: {
  table: TanStackTable<TData>
  filters: DataTableFilter[]
  searchPlaceholder: string
  globalFilter: string
  onGlobalFilterChange: (value: string) => void
}) {
  const isFiltered = globalFilter.length > 0 || table.getState().columnFilters.length > 0

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
        <div className="relative w-full sm:w-64">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={globalFilter}
            onChange={(event) => onGlobalFilterChange(event.target.value)}
            placeholder={searchPlaceholder}
            className="h-8 pl-8"
          />
        </div>
        {filters.map((filter) => {
          const column = table.getColumn(filter.columnId)
          return column ? (
            <DataTableFacetedFilter
              key={filter.columnId}
              column={column}
              title={filter.title}
              options={filter.options}
            />
          ) : null
        })}
        {isFiltered ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              onGlobalFilterChange("")
              table.resetColumnFilters()
            }}
          >
            重置
            <RotateCcwIcon data-icon="inline-end" />
          </Button>
        ) : null}
      </div>
      <DataTableViewOptions table={table} />
    </div>
  )
}

function DataTableFacetedFilter<TData, TValue>({
  column,
  title,
  options,
}: {
  column: Column<TData, TValue>
  title: string
  options: DataTableFilterOption[]
}) {
  const selected = new Set((column.getFilterValue() as string[]) ?? [])
  const facets = column.getFacetedUniqueValues()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="border-dashed">
          <PlusCircleIcon data-icon="inline-start" />
          {title}
          {selected.size ? (
            <>
              <span className="mx-0.5 h-4 w-px bg-border" />
              <span className="rounded-sm bg-secondary px-1 font-normal lg:hidden">
                {selected.size}
              </span>
              <span className="hidden max-w-36 truncate font-normal lg:inline">
                {selected.size === 1
                  ? options.find((option) => selected.has(option.value))?.label
                  : `已选 ${selected.size} 项`}
              </span>
            </>
          ) : null}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-52">
        <DropdownMenuLabel>{title}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          {options.map((option) => (
            <DropdownMenuCheckboxItem
              key={option.value}
              checked={selected.has(option.value)}
              onSelect={(event) => event.preventDefault()}
              onCheckedChange={(checked) => {
                const next = new Set(selected)
                if (checked) next.add(option.value)
                else next.delete(option.value)
                column.setFilterValue(next.size ? Array.from(next) : undefined)
              }}
            >
              <span className="flex-1">{option.label}</span>
              {facets.get(option.value) ? (
                <span className="text-xs tabular-nums text-muted-foreground">
                  {facets.get(option.value)}
                </span>
              ) : null}
            </DropdownMenuCheckboxItem>
          ))}
        </DropdownMenuGroup>
        {selected.size ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuItem
                className="justify-center"
                onSelect={() => column.setFilterValue(undefined)}
              >
                清除筛选
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function DataTableViewOptions<TData>({
  table,
}: {
  table: TanStackTable<TData>
}) {
  const columns = table
    .getAllColumns()
    .filter((column) => column.getCanHide() && column.accessorFn)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="ml-auto">
          <SlidersHorizontalIcon data-icon="inline-start" />
          列
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-40">
        <DropdownMenuLabel>显示列</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          {columns.map((column) => {
            const meta = column.columnDef.meta as DataTableColumnMeta | undefined
            return (
              <DropdownMenuCheckboxItem
                key={column.id}
                checked={column.getIsVisible()}
                onSelect={(event) => event.preventDefault()}
                onCheckedChange={(value) => column.toggleVisibility(Boolean(value))}
              >
                {meta?.label ?? column.id}
              </DropdownMenuCheckboxItem>
            )
          })}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function DataTablePagination<TData>({ table }: { table: TanStackTable<TData> }) {
  const total = table.getFilteredRowModel().rows.length
  const { pageIndex, pageSize } = table.getState().pagination
  const pageCount = table.getPageCount()
  const first = total === 0 ? 0 : pageIndex * pageSize + 1
  const last = Math.min((pageIndex + 1) * pageSize, total)
  const pages = visiblePages(pageIndex + 1, pageCount)

  if (pageCount <= 1) {
    return (
      <p className="px-1 text-xs text-muted-foreground">共 {total} 项</p>
    )
  }

  return (
    <div className="flex flex-col gap-3 px-1 sm:flex-row sm:items-center sm:justify-between">
      <p className="text-xs text-muted-foreground">
        共 {total} 项 · 当前 {first}–{last}
      </p>
      <div className="flex flex-wrap items-center justify-end gap-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span>每页</span>
          <Select
            value={`${pageSize}`}
            onValueChange={(value) => table.setPageSize(Number(value))}
          >
            <SelectTrigger size="sm" aria-label="每页显示条数">
              <SelectValue />
            </SelectTrigger>
            <SelectContent side="top">
              <SelectGroup>
                {[8, 10, 20, 30, 50].map((size) => (
                  <SelectItem key={size} value={`${size}`}>
                    {size}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
        <span className="min-w-16 text-center text-xs text-muted-foreground">
          第 {pageCount ? pageIndex + 1 : 0} / {pageCount} 页
        </span>
        <div className="flex items-center gap-1">
          <Button
            variant="outline"
            size="icon-sm"
            className="hidden sm:inline-flex"
            aria-label="第一页"
            disabled={!table.getCanPreviousPage()}
            onClick={() => table.setPageIndex(0)}
          >
            <ChevronsLeftIcon />
          </Button>
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="上一页"
            disabled={!table.getCanPreviousPage()}
            onClick={() => table.previousPage()}
          >
            <ChevronLeftIcon />
          </Button>
          {pages.map((page) => (
            <Button
              key={page}
              variant={page === pageIndex + 1 ? "default" : "outline"}
              size="icon-sm"
              aria-label={`第 ${page} 页`}
              aria-current={page === pageIndex + 1 ? "page" : undefined}
              onClick={() => table.setPageIndex(page - 1)}
            >
              {page}
            </Button>
          ))}
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="下一页"
            disabled={!table.getCanNextPage()}
            onClick={() => table.nextPage()}
          >
            <ChevronRightIcon />
          </Button>
          <Button
            variant="outline"
            size="icon-sm"
            className="hidden sm:inline-flex"
            aria-label="最后一页"
            disabled={!table.getCanNextPage()}
            onClick={() => table.setPageIndex(Math.max(0, pageCount - 1))}
          >
            <ChevronsRightIcon />
          </Button>
        </div>
      </div>
    </div>
  )
}

function visiblePages(currentPage: number, totalPages: number) {
  if (totalPages <= 3) {
    return Array.from({ length: totalPages }, (_, index) => index + 1)
  }
  if (currentPage <= 2) return [1, 2, 3]
  if (currentPage >= totalPages - 1) {
    return [totalPages - 2, totalPages - 1, totalPages]
  }
  return [currentPage - 1, currentPage, currentPage + 1]
}

export {
  DataTable,
  DataTableColumnHeader,
  type DataTableFilter,
  type DataTableFilterOption,
}
