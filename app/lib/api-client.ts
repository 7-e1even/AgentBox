export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly retryable: boolean
  readonly retryAfter: string | null

  constructor(
    message: string,
    options: {
      status: number
      code?: string
      retryable?: boolean
      retryAfter?: string | null
    }
  ) {
    super(message)
    this.name = "ApiError"
    this.status = options.status
    this.code = options.code ?? ""
    this.retryable = options.retryable ?? false
    this.retryAfter = options.retryAfter ?? null
  }
}

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
    throw new ApiError("登录状态已过期", { status: 401, code: "unauthorized" })
  }
  if (!response.ok) {
    const body = await response.json().catch(() => null)
    const fallback = response.status === 403 ? "没有权限执行此操作" : "请求失败"
    const details = apiErrorDetails(body)
    throw new ApiError(details.message || fallback, {
      status: response.status,
      code: details.code,
      retryable: details.retryable,
      retryAfter: response.headers.get("Retry-After"),
    })
  }
  return response.status === 204 ? (undefined as T) : response.json()
}

export function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "请稍后重试"
}

export function apiErrorMessage(body: unknown, fallback: string) {
  return apiErrorDetails(body).message || fallback
}

function apiErrorDetails(body: unknown) {
  if (!body || typeof body !== "object" || !("error" in body)) {
    return { message: "", code: "", retryable: false }
  }
  const error = body.error
  if (typeof error === "string") {
    return { message: error, code: "", retryable: false }
  }
  if (!error || typeof error !== "object") {
    return { message: "", code: "", retryable: false }
  }
  return {
    message:
      "message" in error && typeof error.message === "string"
        ? error.message
        : "",
    code: "code" in error && typeof error.code === "string" ? error.code : "",
    retryable: "retryable" in error && error.retryable === true,
  }
}
