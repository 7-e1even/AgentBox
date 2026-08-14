# AgentBox

AgentBox 是面向 Coding Agent 的沙箱控制面：连接 Linux 服务器，预先配置运行环境、模型凭据、Skills 和 MCP，然后快速创建可操作的隔离沙箱。

它负责“准备并运行 Agent 环境”，不负责项目任务或工作流编排。

## 快速部署

需要 Docker Engine 和 Docker Compose v2。先下载项目：

```sh
git clone https://github.com/7-e1even/AgentBox.git
cd AgentBox
```

首次启动前复制 `.env.example` 为 `.env`，正式环境只需确认以下配置：

```sh
cp .env.example .env
```

```env
POSTGRES_PASSWORD=请替换为强密码
AGENTBOX_PUBLIC_URL=http://<服务器地址>:3000
AGENTBOX_ALLOWED_ORIGINS=http://<服务器地址>:3000
# 建议固定到 Releases 页面中的版本标签
AGENTBOX_VERSION=latest
```

需要可重复部署时，将 `latest` 替换为 Releases 页面中实际存在的 `v*` 标签。

正式部署直接拉取 GHCR 中已构建的镜像，不需要在目标机编译 Go 或前端：

```sh
docker compose pull
docker compose up -d
docker compose ps
```

打开 `AGENTBOX_PUBLIC_URL` 创建首个管理员，再到“服务器”页面复制命令安装并配对 Linux Worker。安装脚本会经由 AgentBox Server 下载当前 Release 中对应 `amd64` / `arm64` 的单个 Go Worker 二进制并校验 SHA-256；目标机不需要 Python，也不需要直接访问 GitHub。

Worker 上线后，服务器详情页会显示当前版本。发布新版本并升级 AgentBox Server 后，可在同一页面点击“更新 Worker”：Server 会缓存 Release 资产，Worker 原子替换二进制并重启；新版不能正常启动时会自动恢复上一版。

从旧版迁移时，需要在物理服务器上重新运行一次安装命令，让旧 Worker 获得自更新能力；安装器会保留已有配对配置。完成这一次迁移后，后续升级均可在 Web 中执行。

更新、查看日志或停止服务：

```sh
docker compose pull && docker compose up -d
docker compose logs -f app server postgres
docker compose down
```

数据、Worker 缓存和凭据加密主密钥保存在 Docker 命名卷中。普通更新不要执行 `docker compose down -v`。

仅在本机从源码体验或开发时才需要构建镜像：

```sh
docker compose up -d --build
```

源码构建的 Server 版本默认为 `dev`，不会提供 Web 在线更新。需要联调发布版 Worker 时，在 `.env` 中将 `AGENTBOX_VERSION` 固定到已有的 Release 标签。

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

Linux Worker 的调度、交互会话和文件操作收敛在同一个 Go 二进制中，不依赖宿主机 Python。BoxLite 使用官方 CLI，Microsandbox 使用 Go SDK 驱动。

## 发布

推送 `v*` 标签会触发发布流水线：先运行 Go 测试，再生成两个架构的 Worker、`SHA256SUMS` 和 GitHub Release，最后将 Server/Web 多架构镜像发布到 GHCR。部署端固定同一个版本标签，Server 与它分发的 Worker 就会保持一致。

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
