# AgentBox Web

AgentBox 的 Windows 操作平台，使用 Next.js 16、React 19、Tailwind CSS 4 和 shadcn/ui。

前端不直接访问 PostgreSQL。服务端渲染和浏览器请求统一通过 `AGENTBOX_API_URL` 或 `/api` 代理访问独立 Go API。

```powershell
Copy-Item .env.example .env.local
pnpm install
pnpm dev
```

完整启动和架构说明见仓库根目录 `README.md`。
