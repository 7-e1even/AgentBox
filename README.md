# AgentBox

AgentBox 是一个平台侧的 Agent 声明与管理系统。当前版本聚焦一个完整边界：创建和管理 Agent 配置，不创建 Runtime，也不接入 Multica。

## 当前能力

- Agent 创建、编辑、搜索、筛选、复制、启用、草稿、归档与永久删除
- Provider、模型和凭据引用配置
- Skills 与 MCP Servers 绑定
- PostgreSQL 持久化、乐观锁版本与修订快照
- Next.js 16 + shadcn/ui 响应式操作平台
- 独立 Go API，包含输入校验、数据库迁移和种子数据

## 架构边界

| 层 | 当前部署位置 | 职责 |
| --- | --- | --- |
| 操作平台 | Windows | Next.js 前端与 Go 控制面 API |
| 数据层 | PostgreSQL | Agent 声明、状态与修订历史 |
| Runtime Worker | 后续部署到 SSH 服务器 | Docker、虚拟环境、Agent 执行与资源隔离 |

SSH Worker 不属于当前版本。后续接入时由 Worker 主动注册、发送心跳并领取任务，平台不会直接把控制逻辑搬到远端。

## 本地启动

1. 配置 Go API：

   ```powershell
   Copy-Item server/.env.example server/.env
   # 编辑 server/.env 中的 DATABASE_URL
   cd server
   go run ./cmd/agentbox
   ```

2. 启动操作平台：

   ```powershell
   cd app
   Copy-Item .env.example .env.local
   pnpm install
   pnpm dev
   ```

3. 打开 `http://localhost:3000`。Go API 默认监听 `http://127.0.0.1:8091`。

## 验证

```powershell
cd server
go test ./...

cd ../app
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

数据库凭据只放在 `server/.env`，前端环境文件只保存 Go API 地址。两个本地环境文件都不会进入 Git。
