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

同一台机器上的本地体验无需创建 `.env`，直接启动即可。Compose 会拉取 GHCR 中已构建的镜像，不需要在目标机编译 Go 或前端：

```sh
docker compose up -d
docker compose ps
```

全新数据库首次启动时，Server 会生成一次性管理员初始化码。用 `docker compose logs server` 在本机日志中读取它，再在登录页完成初始化；初始化成功后该码立即失效。自动化部署可以通过 `AGENTBOX_SETUP_CODE` 提供至少 24 随机字节的无填充 Base64URL 值，并应在初始化完成后从环境中删除。可用 `openssl rand -base64 24 | tr '+/' '-_' | tr -d '='` 安全生成；短值、带填充或非 Base64URL 值会使 Server 拒绝启动。

长期或生产部署请先将 `.env.example` 复制为 `.env`，至少修改 `POSTGRES_PASSWORD`；需要固定版本、端口或公开地址时也在该文件中覆盖。

Server 当前必须保持 **1 个副本**：会话票据、Worker 和浏览器 WebSocket 均属于同一进程，不支持多副本或重叠滚动发布。重启后 Worker 会重连，浏览器需要重新打开终端/桌面。部署约束与 Worker v1 协议兼容规则见[传输兼容矩阵](docs/transport-compatibility.md)。

生产环境建议由反向代理（如 Nginx、Caddy）终结 TLS，以 HTTPS 对外提供访问。位于反向代理之后时，在 `.env` 中同时设置 `AGENTBOX_TRUSTED_PROXY=true` 和 Server 直接连接方的精确 `AGENTBOX_TRUSTED_PROXY_CIDRS`，Server 才会信任代理传入的 `X-Forwarded-*` 头；开启信任但 CIDR 缺失或无效会拒绝启动。不在可信反向代理之后时保持默认 `false`；这只表示忽略转发头，并不豁免任何远程链路的 TLS 要求。代理还必须关闭流式响应缓冲、保留 WebSocket Upgrade，并把读写超时设为长于最长 Agent 会话，完整配置见[传输兼容矩阵](docs/transport-compatibility.md)。

同机开发可打开 `http://localhost:3000`；任何远程浏览器或 Linux Worker 必须使用配置了有效 TLS 的 `https://<服务器地址>`。再到“服务器”页面复制命令安装并配对 Worker。安装脚本会经由 AgentBox Server 下载当前 Release 中对应 `amd64` / `arm64` 的单个 Go Worker 二进制并强制校验 SHA-256；目标机不需要 Python，也不需要直接访问 GitHub。

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

源码构建的 Server 版本默认为 `dev`，不会提供 Web 在线更新；镜像会同时内置 amd64 和 arm64 Worker，服务器页面生成的安装命令仍可直接使用。

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

启动脚本不会关闭鉴权。首次打开时，从 Server 终端的本地启动日志复制一次性初始化码，再按页面提示创建管理员账号；之后使用用户名和密码登录，邮箱仅作为个人资料，不作为登录凭据。关闭脚本打开的两个 AgentBox 终端窗口即可停止 API 与 Web。

## 备份与恢复

升级 AgentBox 前请先备份。需要备份两部分：PostgreSQL 数据，以及 `agentbox-secrets` 卷中的凭据加密主密钥。为保证两份备份对应同一时刻，先停止会写入数据的 App 和 Server；备份完成后再启动它们。

```sh
set -eu

backup_name=agentbox-backup-$(date -u +%Y%m%dT%H%M%SZ)
backup_tmp=$(mktemp -d "./.${backup_name}.tmp.XXXXXX")
backup_final=./$backup_name
backup_complete=false
cleanup_backup() {
  rm -rf "$backup_tmp"
  if [ "$backup_complete" != true ]; then
    docker compose start server app >/dev/null 2>&1 || true
  fi
}
trap cleanup_backup 0 1 2 15
test ! -e "$backup_final"

docker compose stop app server

# 备份数据库（默认库名与用户均为 agentbox）
docker compose exec -T postgres pg_dump --format=plain --no-owner --no-privileges \
  -U agentbox -d agentbox > "$backup_tmp/database.sql"
test -s "$backup_tmp/database.sql"

# 备份凭据加密主密钥（卷名带 Compose 项目名前缀，可用 docker volume ls 确认）
docker run --rm -v agentbox_agentbox-secrets:/data:ro -v "$PWD":/backup alpine \
  tar czf "/backup/${backup_tmp#./}/agentbox-secrets.tar.gz" -C /data .

# 错误卷名会被 Docker 创建为空卷，所以发布备份前也按恢复契约验证唯一主密钥。
docker run --rm -v "$PWD/${backup_tmp#./}":/backup:ro alpine sh -eu -c '
  staging=$(mktemp -d)
  trap "rm -rf $staging" 0 1 2 15
  tar tzf /backup/agentbox-secrets.tar.gz > "$staging/archive.list"
  secret_member=
  while IFS= read -r entry; do
    case "$entry" in
      /*|..|../*|*/../*|*/..) exit 1 ;;
      secret-key|./secret-key)
        test -z "$secret_member" || exit 1
        secret_member=$entry
        ;;
    esac
  done < "$staging/archive.list"
  test -n "$secret_member"
  tar xzf /backup/agentbox-secrets.tar.gz -C "$staging" "$secret_member"
  test -f "$staging/secret-key"
  test ! -L "$staging/secret-key"
  encoded=$(tr -d "\r\n" < "$staging/secret-key")
  case "$encoded" in *[!A-Za-z0-9+/=]*) exit 1 ;; esac
  case "${#encoded}:$encoded" in
    43:*=*) exit 1 ;;
    43:*) padded="$encoded=" ;;
    44:*=)
      body=${encoded%=}
      case "$body" in *=*) exit 1 ;; esac
      padded=$encoded
      ;;
    *) exit 1 ;;
  esac
  printf %s "$padded" | base64 -d > "$staging/decoded-key"
  test "$(wc -c < "$staging/decoded-key" | tr -d " ")" = 32
'

# 清单把数据库与密钥绑定到同一代际；目录 rename 是唯一发布点。
(
  cd "$backup_tmp"
  sha256sum database.sql agentbox-secrets.tar.gz > SHA256SUMS
  test -s SHA256SUMS
)
mv "$backup_tmp" "$backup_final"
docker compose start server app
backup_complete=true
trap - 0 1 2 15
printf 'Backup published at %s\n' "$backup_final"
```

恢复会替换目标实例的数据库和主密钥。请先额外备份目标实例，并确认下面的卷名、数据库名和用户与 `.env` 一致。整个过程中保持 App 和 Server 停止；主密钥归档会先验证，再清空这个明确指定的目标卷，避免新旧密钥文件混合。

```sh
set -eu

# 替换为备份命令实际输出的代际目录；不要混用两个目录中的文件。
backup_dir=./agentbox-backup-20260903T120000Z
test -d "$backup_dir"
(
  cd "$backup_dir"
  sha256sum -c SHA256SUMS
)
test -s "$backup_dir/database.sql"
test -s "$backup_dir/agentbox-secrets.tar.gz"
docker compose stop app server

# 先把归档中的 secret-key 单独解到临时目录并完整验证，再清空明确指定的目标卷。
docker run --rm -v agentbox_agentbox-secrets:/data \
  -v "$PWD/${backup_dir#./}":/backup:ro alpine sh -eu -c '
  staging=$(mktemp -d)
  trap "rm -rf $staging" 0 1 2 15
  tar tzf /backup/agentbox-secrets.tar.gz > "$staging/archive.list"
  secret_member=
  while IFS= read -r entry; do
    case "$entry" in
      /*|..|../*|*/../*|*/..) echo "unsafe path in secret archive: $entry" >&2; exit 1 ;;
      secret-key|./secret-key)
        test -z "$secret_member" || { echo "duplicate secret-key in archive" >&2; exit 1; }
        secret_member=$entry
        ;;
    esac
  done < "$staging/archive.list"
  test -n "$secret_member"
  tar xzf /backup/agentbox-secrets.tar.gz -C "$staging" "$secret_member"
  test -f "$staging/secret-key"
  test ! -L "$staging/secret-key"
  test -s "$staging/secret-key"
  encoded=$(tr -d "\r\n" < "$staging/secret-key")
  case "$encoded" in *[!A-Za-z0-9+/=]*) echo "secret-key is not standard base64" >&2; exit 1 ;; esac
  case "${#encoded}:$encoded" in
    43:*=*) echo "43-character secret-key must be unpadded" >&2; exit 1 ;;
    43:*) padded="$encoded=" ;;
    44:*=)
      body=${encoded%=}
      case "$body" in *=*) echo "secret-key padding is invalid" >&2; exit 1 ;; esac
      padded=$encoded
      ;;
    *) echo "secret-key must be 43 raw or 44 padded base64 characters" >&2; exit 1 ;;
  esac
  printf %s "$padded" | base64 -d > "$staging/decoded-key"
  test "$(wc -c < "$staging/decoded-key" | tr -d " ")" = 32
  rm -f "$staging/archive.list" "$staging/decoded-key"
  find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  test -z "$(find /data -mindepth 1 -maxdepth 1 -print -quit)"
  cp -a "$staging/secret-key" /data/secret-key
  test -s /data/secret-key
'

# 断开残留连接并重建空数据库；恢复本身是单事务且遇到首个错误立即终止。
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U agentbox -d postgres <<'SQL'
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = 'agentbox' AND pid <> pg_backend_pid();
DROP DATABASE IF EXISTS agentbox;
CREATE DATABASE agentbox OWNER agentbox;
SQL
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 --single-transaction \
  -U agentbox -d agentbox < "$backup_dir/database.sql"

# 两部分都成功后才重新启动，并验证数据库健康和主密钥哨兵解密。
if ! docker compose up -d --wait server app; then
  docker compose logs --tail=100 server
  exit 1
fi
docker compose exec -T server wget -qO- http://127.0.0.1:8091/healthz
docker compose logs --tail=100 server
```

Server 启动时会使用恢复的主密钥解密数据库中的加密哨兵；密钥不匹配时 Server 不会进入健康状态，并会在日志中报告错误。主密钥一旦丢失，数据库中已加密保存的模型凭据将无法解密，只能逐个重新录入，请与数据库备份分开妥善保管。

## 核心能力

- 接入 Linux 物理机或 VM，并通过 Worker 管理沙箱生命周期
- 使用沙箱模板复用镜像、Agent 工具、变量、Skills、MCP 和初始化命令，支持多项目分组管理
- 支持 Docker、BoxLite 和 Microsandbox；实际可用类型以服务器能力检测为准（VM 运行时暂不支持，仅保留存量数据只读盘点）
- 预装和配置 Codex、Claude Code、Gemini CLI、OpenCode、Kimi、Pi、Reasonix 等 Agent 工具
- 通过自动化 Webhook 从固定模板创建沙箱；网络策略支持完全隔离或出站访问，BoxLite 额外支持受限网络
- 内置运行时 LLM 网关：模型凭据只保存在 Server，由 Server 代理协议转换；运行中可切换模型源，不停止沙箱、终端或图形桌面
- 在浏览器中使用 root 终端、文件管理器和代码编辑器运维沙箱
- 使用 PostgreSQL 持久化配置，并加密保存模型凭据

## Webhook 与流水线

“自动化”页面可以把 GitHub、GitLab、Jenkins、n8n 或其他系统的事件转换为持久 Run，并按自动化中固定的模板和模型绑定创建、启动一个沙箱。首次请求返回独立的 `statusUrl` 和 `runToken`，调用方无需登录控制台即可轮询创建进度与最终结果。

完整的鉴权方式、幂等语义、轮询脚本和各平台接入步骤见 [Webhook 自动创建沙箱接入指南](docs/webhook-automation.md)。Webhook Payload 不会覆盖模板或作为命令执行；后续命令、清理、条件分支、并行矩阵和跨步骤 DAG 仍应留在现有 CI/CD 或工作流系统中。

## 架构

| 组件            | 职责                                      |
| --------------- | ----------------------------------------- |
| Web + Go API    | 用户界面、配置管理、鉴权和 Worker 调度    |
| PostgreSQL      | 保存服务器、模板、沙箱、凭据和运行状态    |
| AgentBox Worker | 运行在 Linux 服务器上，实际创建和操作沙箱 |

浏览器和 Worker 统一访问平台端口 `3000`；Go API 与 PostgreSQL 默认只在内部网络中通信。

Linux Worker 的调度、交互会话和文件操作收敛在同一个 Go 二进制中，不依赖宿主机 Python。BoxLite 使用官方 CLI，Microsandbox 使用 Go SDK 驱动。

## 发布

推送 `v*` 标签会触发发布流水线：先运行 Go 测试与 Web 的 lint、typecheck、test 和 build，再生成两个架构的 Worker 与 `SHA256SUMS`，并将 Server/Web 多架构镜像分别发布为对应版本标签。两个镜像均成功后，流水线才统一推进 `latest` 并创建 GitHub Release；推进或 Release 发布失败会尝试恢复上一组 `latest`。同一 Release 中的 Server、Web 与 Worker 会保持版本一致。

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
