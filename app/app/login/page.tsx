import { cookies } from "next/headers"
import { redirect } from "next/navigation"

import { BackendUnavailable } from "@/components/backend-unavailable"
import { LoginForm } from "@/components/login-form"
import { authStatusSchema, userResponseSchema } from "@/lib/user-schema"

export const dynamic = "force-dynamic"

export default async function LoginPage() {
  const apiOrigin = process.env.AGENTBOX_API_URL || "http://127.0.0.1:8091"
  const cookieHeader = (await cookies()).toString()
  const sessionResponse = await fetch(`${apiOrigin}/api/auth/me`, {
    cache: "no-store",
    headers: cookieHeader ? { Cookie: cookieHeader } : undefined,
  }).catch(() => null)
  if (sessionResponse?.ok) {
    const sessionBody = userResponseSchema.safeParse(await sessionResponse.json())
    if (!sessionBody.success) return <BackendUnavailable variant="error" />
    redirect("/")
  }
  const statusResponse = await fetch(`${apiOrigin}/api/auth/status`, {
    cache: "no-store",
  }).catch(() => null)
  if (!statusResponse) return <BackendUnavailable variant="unreachable" />
  if (!statusResponse.ok) return <BackendUnavailable variant="error" />

  const authStatus = authStatusSchema.parse(await statusResponse.json())
  return (
    <LoginForm
      needsSetup={authStatus.needsSetup}
      setupCodeRequired={authStatus.setupCodeRequired}
    />
  )
}
