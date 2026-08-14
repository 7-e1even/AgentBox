# AgentBox Web

AgentBox 的 Windows 操作平台，使用 Next.js 16、React 19、Tailwind CSS 4 和 shadcn/ui。

前端不直接访问 PostgreSQL。服务端渲染通过 `AGENTBOX_API_URL` 访问独立 Go API；浏览器、Worker 和交互 WebSocket 统一走前端 `/api` 代理，因此部署时只需公开平台入口端口。

```powershell
Copy-Item .env.example .env.local
pnpm install
pnpm dev
```

完整启动和架构说明见仓库根目录 `README.md`。
