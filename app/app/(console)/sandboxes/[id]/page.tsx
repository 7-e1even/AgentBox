import { SandboxWorkspaceLazy } from "@/components/sandbox-workspace-loader"

export default async function SandboxWorkspacePage({
  params,
}: {
  params: Promise<{ id: string }>
}) {
  const { id } = await params
  return <SandboxWorkspaceLazy sandboxId={id} />
}
