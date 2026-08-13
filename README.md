# AgentBox

AgentBox 是一个平台侧的 Agent 环境与沙箱控制面，定位类似 agent-compose，但把“提前配好环境”作为第一主线。它不做任务或工作流编排，也不接入 Multica。

核心操作路径：

1. 接入 Linux 物理服务器并维护 OCI 镜像。
2. 创建可复用的环境模板：选择 Docker / VM、镜像、Agent 工具、Skills、MCP、API Keys、变量和初始化命令。
3. 创建沙箱时选择环境模板、智能体和目标服务器。
4. Worker 在物理服务器上实例化环境，并在沙箱的凭据卷中保存 Agent 登录态。

## 当前能力

- Overview：展示服务器、镜像、智能体、凭据和环境模板的真实准备度
- Projects：Agent、环境模板、Skills 与触发配置的归属边界
- Servers：Linux 物理机的一次性配对、稳定设备身份、能力发现和心跳状态
- Agents：Provider、模型、指令、默认环境模板、Sandbox 策略、并发、启动参数和凭据引用
- Images：按物理服务器展示 Worker 实时盘点的 Docker image store 与 VM 镜像目录
- Environment Templates：绑定运行服务器及其真实镜像，并声明 Docker / VM、Codex / Claude Code / Gemini CLI / OpenCode、Skills、MCP、API Keys、变量与初始化命令
- Skills、MCP Servers、环境变量与 Secret 引用的独立配置和 Agent 绑定；创建沙箱时会投影到所选 Agent 工具的原生配置目录
- Credentials：API Key 使用 AES-256-GCM 加密后写入 PostgreSQL，API 只返回末四位；实例主密钥可由环境变量提供，也可由服务首次启动自动生成
- Sandboxes：选择 Agent、环境模板、目标服务器和工作区；Worker 可真实创建、启动、停止和删除 Docker 沙箱及其独立工作卷
- Agent 登录：运行中的 Codex 沙箱支持由平台发起设备登录；登录文件留在该沙箱内。Claude Code 与 Gemini CLI 暂保留官方终端登录入口
- Schedules 与 Webhooks：保存“直接触发一个 Agent”的声明，不串联任务节点
- PostgreSQL 持久化、引用校验、Agent 乐观锁版本与修订快照
- Next.js 16 + shadcn/ui 响应式操作平台
- 独立 Go API，包含严格 JSON 边界、输入校验、数据库迁移和种子数据

## 架构边界

| 层 | 当前部署位置 | 职责 |
| --- | --- | --- |
| 操作平台 | Windows | Next.js 前端与 Go 控制面 API |
| 数据层 | PostgreSQL | 控制面声明、状态、引用与 Agent 修订历史 |
| AgentBox Worker | Linux 物理服务器 | 配对注册、能力发现、独立心跳、任务租约和沙箱生命周期 |
| 环境模板 | PostgreSQL 控制面 | 绑定服务器镜像清单的预配声明，不代表已经创建 VM / 容器 |
| Sandbox | Worker 所在物理服务器 | 环境模板实例化后的隔离环境；当前真实执行后端为 Docker |

Worker 会在领取创建任务时临时取得解密后的凭据，并在沙箱内写入权限为 `0600` 的环境文件和各 Agent 原生配置；队列持久化内容和沙箱清单都不保存明文密钥。Codex 与 OpenCode 已覆盖真实安装、配置、Skills、MCP 和生命周期验证。

VM 仍是环境模板的声明类型，但 Worker 的 KVM/libvirt 创建后端尚未实现；没有对应能力的服务器不会在界面提供 VM 选项。Schedule 和 Webhook 目前也只保存触发声明，不会伪装成已运行任务。

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

3. 打开 `http://localhost:3000`。Go API 默认监听 `http://127.0.0.1:8091`。首次启动会引导创建管理员账号；后续访问需要登录。管理员可在左侧“用户管理”中维护账号、角色与状态。

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

Provider 连通性测试默认拒绝回环、私网和链路本地地址，避免控制面被用作 SSRF 跳板。如果确实要连接可信的内网模型网关，可在 Go API 环境中显式设置 `AGENTBOX_ALLOW_PRIVATE_PROVIDER_ENDPOINTS=true`。
