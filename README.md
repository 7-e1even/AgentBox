# AgentBox

AgentBox 是一个平台侧的 Agent 沙箱控制面，定位类似 agent-compose：声明 Agent、准备隔离环境并管理生命周期，但不做任务或工作流编排，也不接入 Multica。

## 当前能力

- Projects：仓库、本地工作区与配置归属边界
- Agents：Provider、模型、指令、Runtime、Sandbox 策略、并发、启动参数和凭据引用
- Runtimes：Docker、主机虚拟环境、BoxLite 与 Microsandbox 模板
- Skills、MCP Servers、环境变量与 Secret 引用的独立配置和 Agent 绑定
- Sandboxes：声明 Agent、Runtime、工作区、实例策略和期望状态
- Schedules 与 Webhooks：直接触发一个 Agent，不串联任务节点
- PostgreSQL 持久化、引用校验、Agent 乐观锁版本与修订快照
- Next.js 16 + shadcn/ui 响应式操作平台
- 独立 Go API，包含严格 JSON 边界、输入校验、数据库迁移和种子数据

## 架构边界

| 层 | 当前部署位置 | 职责 |
| --- | --- | --- |
| 操作平台 | Windows | Next.js 前端与 Go 控制面 API |
| 数据层 | PostgreSQL | 控制面声明、状态、引用与 Agent 修订历史 |
| Runtime Worker | 后续部署到 SSH 服务器 | 消费已启用声明，创建 Docker、虚拟环境或微虚机并执行 Agent |

当前版本完成控制面与持久化；SSH Worker 尚未部署，因此 Sandbox、Schedule 和 Webhook 目前保存期望配置，不会伪装成已经启动远端环境。后续由 Worker 主动注册、发送心跳并领取单次 Agent 运行请求。

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
