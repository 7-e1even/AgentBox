# AgentBox

AgentBox 是面向 Coding Agent 的沙箱控制面：连接 Linux 服务器，预先配置运行环境、模型凭据、Skills 和 MCP，然后快速创建可操作的隔离沙箱。

它负责“准备并运行 Agent 环境”，不负责项目任务或工作流编排。

第一次使用请按[《AgentBox 使用指南》](docs/user-guide.md)完成部署、Worker 配对、模型服务、沙箱模板和工作台操作；Webhook 对接见[《Webhook 流水线接入指南》](docs/webhook-automation.md)，生产反向代理见[《传输与反向代理兼容矩阵》](docs/transport-compatibility.md)。

## 快速部署

需要 Docker Engine 和 Docker Compose v2。先下载项目：

```sh
git clone https://github.com/7-e1even/AgentBox.git
cd AgentBox
```

本机或可信内网体验无需创建 `.env`，直接启动即可。Compose 会拉取 GHCR 中已构建的镜像，不需要在目标机编译 Go 或前端：

```sh
docker compose up -d
docker compose ps
```

长期或生产部署请先将 `.env.example` 复制为 `.env`，至少修改 `POSTGRES_PASSWORD`；需要固定版本、端口或公开地址时也在该文件中覆盖。

生产环境建议由反向代理（如 Nginx、Caddy）终结 TLS，以 HTTPS 对外提供访问。位于反向代理之后时，在 `.env` 中设置 `AGENTBOX_TRUSTED_PROXY=true`，Server 才会信任代理传入的 `X-Forwarded-*` 头；直接暴露端口或可信内网保持默认即可。代理还必须关闭流式响应缓冲、保留 WebSocket Upgrade，并把读写超时设为长于最长 Agent 会话，完整配置见[传输兼容矩阵](docs/transport-compatibility.md)。

打开 `http://<服务器地址>:3000` 创建首个管理员，再到“服务器”页面复制命令安装并配对 Linux Worker。安装脚本会经由 AgentBox Server 下载当前 Release 中对应 `amd64` / `arm64` 的单个 Go Worker 二进制并校验 SHA-256；目标机不需要 Python，也不需要直接访问 GitHub。

Worker 上线后，服务器详情页会显示当前版本。发布新版本并升级 AgentBox Server 后，可在同一页面点击“更新 Worker”：Server 会缓存 Release 资产，Worker 校验 SHA-256 后原子替换二进制并重启；新版不能正常启动时会自动恢复上一版。

从旧版迁移时，需要在物理服务器上重新运行一次安装命令，让旧 Worker 获得自更新能力；安装器会保留已有配对配置。完成这一次迁移后，后续升级均可在 Web 中执行。

更新、查看日志或停止服务：

```sh
docker compose pull && docker compose up -d
docker compose logs -f app server postgres
docker compose down
```

数据、Worker 缓存和凭据加密主密钥保存在 Docker 命名卷中。普通更新不要执行 `docker compose down -v`。

仅在需要使用当前源码构建镜像时添加 `--build`：

```sh
docker compose up -d --build
```

源码构建的 Server 版本默认为 `dev`，不会提供 Web 在线更新。

需要限制容器资源占用时，可添加 `compose.override.yaml`（Compose 会自动合并），按需调整：

```yaml
services:
  server:
    deploy:
      resources:
        limits:
          cpus: "2.0"
          memory: 1G
  app:
    deploy:
      resources:
        limits:
          cpus: "1.0"
          memory: 512M
```

### Windows 本地源码启动

已安装 Go 1.24+、Node.js 20.9+ 和 pnpm 后，双击根目录的 `start-agentbox.bat` 即可同时启动 API 与 Web。脚本优先使用 `server/.env` 中已有的 `DATABASE_URL`；未配置数据库时，会使用 Docker 在本机启动开发用 PostgreSQL。服务就绪后会自动打开 `http://127.0.0.1:3000`。

启动脚本不会关闭鉴权。首次打开时按页面提示创建管理员账号，之后使用用户名和密码登录；邮箱仅作为个人资料，不作为登录凭据。关闭脚本打开的两个 AgentBox 终端窗口即可停止 API 与 Web。

## 备份与恢复

升级 AgentBox 前请先备份。需要备份两部分：PostgreSQL 数据，以及 `agentbox-secrets` 卷中的凭据加密主密钥。

```sh
# 备份数据库（默认库名与用户均为 agentbox）
docker compose exec postgres pg_dump -U agentbox -d agentbox > agentbox-backup.sql

# 备份凭据加密主密钥（卷名带 Compose 项目名前缀，可用 docker volume ls 确认）
docker run --rm -v agentbox_agentbox-secrets:/data:ro -v "$PWD":/backup alpine \
  tar czf /backup/agentbox-secrets.tar.gz -C /data .
```

恢复时反向执行：

```sh
docker compose exec -T postgres psql -U agentbox -d agentbox < agentbox-backup.sql
docker run --rm -v agentbox_agentbox-secrets:/data -v "$PWD":/backup alpine \
  tar xzf /backup/agentbox-secrets.tar.gz -C /data
```

主密钥一旦丢失，数据库中已加密保存的模型凭据将无法解密，只能逐个重新录入，请与数据库备份分开妥善保管。

## 核心能力

- 接入 Linux 物理机或 VM，并通过 Worker 管理沙箱生命周期
- 使用沙箱模板复用镜像、Agent 工具、变量、Skills、MCP 和初始化命令，支持多项目分组管理
- 支持 Docker、BoxLite 和 Microsandbox；实际可用类型以服务器能力检测为准（VM 运行时暂不支持，仅保留存量数据只读盘点）
- 预装和配置 Codex、Claude Code、Gemini CLI、OpenCode、Kimi、Pi、Reasonix 等 Agent 工具
- 通过自动化 Webhook 联动外部系统，通过网络代理控制沙箱出入流量
- 内置运行时 LLM 网关：模型凭据只保存在 Server，由 Server 代理协议转换，不下发到沙箱
- 在浏览器中使用 root 终端、文件管理器和代码编辑器运维沙箱
- 使用 PostgreSQL 持久化配置，并加密保存模型凭据

## Webhook 与流水线

“自动化”页面可以把 GitHub、GitLab、Jenkins、n8n 或其他系统的事件转换为持久 Run，执行创建沙箱、在隔离沙箱中运行命令或销毁沙箱。首次请求返回独立的 `statusUrl` 和 `runToken`，调用方无需登录控制台即可轮询最终状态、退出码、输出和清理结果。

完整的鉴权方式、事件字段、幂等语义、轮询脚本和各平台接入步骤见 [Webhook 流水线接入指南](docs/webhook-automation.md)。AgentBox 只提供一个可靠的隔离任务原语；条件分支、并行矩阵和跨步骤 DAG 仍应留在现有 CI/CD 或工作流系统中。

## 架构

| 组件            | 职责                                      |
| --------------- | ----------------------------------------- |
| Web + Go API    | 用户界面、配置管理、鉴权和 Worker 调度    |
| PostgreSQL      | 保存服务器、模板、沙箱、凭据和运行状态    |
| AgentBox Worker | 运行在 Linux 服务器上，实际创建和操作沙箱 |

浏览器和 Worker 统一访问平台端口 `3000`；Go API 与 PostgreSQL 默认只在内部网络中通信。

Linux Worker 的调度、交互会话和文件操作收敛在同一个 Go 二进制中，不依赖宿主机 Python。BoxLite 使用官方 CLI，Microsandbox 使用 Go SDK 驱动。

## 发布

推送 `v*` 标签会触发发布流水线：先运行 Go 测试与 Web 的 lint、typecheck、test 和 build，全部通过后再生成两个架构的 Worker、`SHA256SUMS` 和 GitHub Release，最后将 Server/Web 多架构镜像发布到 GHCR。同一 Release 中的 Server 与 Worker 会保持版本一致。

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

推送到 `main` 分支或发起 Pull Request 时，日常 CI 会自动执行上述检查，Go 侧还包括 `go build ./...` 与 `go vet ./...`。

## 开源协议

AgentBox 使用 [MIT License](LICENSE)。
