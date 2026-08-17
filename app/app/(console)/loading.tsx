import { Spinner } from "@/components/ui/spinner"

export default function ConsoleLoading() {
  return (
    <div className="flex min-h-0 flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
      <Spinner />
      正在加载控制台…
    </div>
  )
}
