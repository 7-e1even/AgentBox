"use client"

import dynamic from "next/dynamic"

// 工作台依赖 xterm 等浏览器专用库，仅在客户端按需加载。
export const SandboxWorkspaceLazy = dynamic(
  () =>
    import("@/components/sandbox-workspace").then((mod) => mod.SandboxWorkspace),
  { ssr: false }
)
