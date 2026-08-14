# AgentBox

AgentBox 是面向 Coding Agent 的沙箱控制面：连接 Linux 服务器，预先配置运行环境、模型凭据、Skills 和 MCP，然后快速创建可操作的隔离沙箱。

它负责“准备并运行 Agent 环境”，不负责项目任务或工作流编排。

## 快速部署

需要 Docker Engine 和 Docker Compose v2。先下载项目：

```sh
git clone https://github.com/7-e1even/AgentBox.git
cd AgentBox
```

仅在本机体验时，直接启动：

```sh
docker compose up -d --build
```

正式部署到服务器时，首次启动前复制 `.env.example` 为 `.env`，只需修改以下 3 项：

```sh
cp .env.example .env
```

```env
POSTGRES_PASSWORD=请替换为强密码
AGENTBOX_PUBLIC_URL=http://<服务器地址>:3000
AGENTBOX_ALLOWED_ORIGINS=http://<服务器地址>:3000
```

然后启动：

```sh
docker compose up -d --build
docker compose ps
```

打开 `AGENTBOX_PUBLIC_URL` 创建首个管理员，再到“服务器”页面按提示安装并配对 Linux Worker。

更新、查看日志或停止服务：

```sh
docker compose up -d --build
docker compose logs -f app server postgres
docker compose down
```

数据和凭据加密主密钥保存在 Docker 命名卷中。普通更新不要执行 `docker compose down -v`。如果无法访问 `proxy.golang.org`，可在 `.env` 中修改 `GOPROXY`。

## 核心能力

- 接入 Linux 物理机或 VM，并通过 Worker 管理沙箱生命周期
- 使用环境模板复用镜像、Agent 工具、变量、Skills、MCP 和初始化命令
- 支持 Docker、BoxLite 和 Microsandbox；实际可用类型以服务器能力检测为准
- 预装和配置 Codex、Claude Code、Gemini CLI、OpenCode 等 Agent 工具
- 在浏览器中使用 root 终端、文件管理器和代码编辑器运维沙箱
- 使用 PostgreSQL 持久化配置，并加密保存模型凭据

## 架构

| 组件            | 职责                                      |
| --------------- | ----------------------------------------- |
| Web + Go API    | 用户界面、配置管理、鉴权和 Worker 调度    |
| PostgreSQL      | 保存服务器、模板、沙箱、凭据和运行状态    |
| AgentBox Worker | 运行在 Linux 服务器上，实际创建和操作沙箱 |

浏览器和 Worker 统一访问平台端口 `3000`；Go API 与 PostgreSQL 默认只在内部网络中通信。

## 本地开发

启动 Go API：

```powershell
Copy-Item server/.env.example server/.env
cd server
go run ./cmd/agentbox
```

启动 Next.js：

```powershell
cd app
Copy-Item .env.example .env.local
pnpm install
pnpm dev
```

访问 `http://localhost:3000`。本地免登录调试可在 `server/.env` 中同时设置：

```env
AGENTBOX_ENV=development
AGENTBOX_DISABLE_AUTH=true
```

该开关在生产环境下会被拒绝。

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

## 开源协议

AgentBox 使用 [MIT License](LICENSE)。
