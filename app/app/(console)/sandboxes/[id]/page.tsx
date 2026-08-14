import { SandboxWorkspace } from "@/components/sandbox-workspace"

export default async function SandboxWorkspacePage({
  params,
}: {
  params: Promise<{ id: string }>
}) {
  const { id } = await params
  return <SandboxWorkspace sandboxId={id} />
}
