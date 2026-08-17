export async function requestJson<T>(
  url: string,
  options?: RequestInit
): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: { "Content-Type": "application/json", ...options?.headers },
  })
  if (response.status === 401) {
    window.location.assign("/login")
    throw new Error("登录状态已过期")
  }
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as {
      error?: string
    } | null
    const fallback = response.status === 403 ? "没有权限执行此操作" : "请求失败"
    throw new Error(body?.error || fallback)
  }
  return response.status === 204 ? (undefined as T) : response.json()
}

export function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "请稍后重试"
}
