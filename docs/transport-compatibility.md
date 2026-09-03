# AgentBox 传输与反向代理兼容矩阵

AgentBox 的浏览器、Worker 和沙箱共用一个公开入口。生产环境应只代理 `app` 的 3000 端口，由 Next.js 将 `/api/*` 转发到内部 Server；不要把 PostgreSQL 或 Server 的 8091 端口直接暴露到公网。

## Session 单实例边界

目前 Server 必须保持 **1 个副本**。Worker WebSocket、浏览器 WebSocket 和 30 秒单次会话票据都保存在同一 Server 进程内；PostgreSQL 不共享这些连接或票据。仅对浏览器配置粘性路由不足以解决 Worker 被路由到另一副本的问题。

`/api/sandboxes/*/session-ticket`、`/desktop-ticket`、浏览器 `/session`、`/desktop` 和 Worker `/api/servers/*/sessions/connect` 必须由同一 Server 处理。当前不支持多副本或重叠滚动发布；升级时先停止旧 Server，再启动新 Server。重启会中断现有终端/桌面，Worker 会重连，浏览器必须重新申请票据。

启动日志及 `/healthz` 的 `sessionTopology: "single-instance"` 声明这一部署约束，不代表系统会自动检测或阻止错误的多副本部署。

## Worker 协议

Worker 协议版本独立于发布版本。当前为 v1，覆盖现有任务和会话消息，包括第一批的 `leaseGeneration`。Worker 在任务认领、进度、完成及会话升级请求中发送 `X-AgentBox-Worker-Protocol-Min/Max`；Server 用 `X-AgentBox-Worker-Protocol` 返回双方支持的最高版本。

相邻旧版本没有这些头，按既有 v1 消息兼容；这不会放宽租约代次校验。明确不兼容的请求返回 426 `worker_protocol_incompatible`，不会认领任务或建立会话。Worker 不处理超出其支持范围的成功响应，需安装与 Server 配套的 Worker。此兼容承诺不覆盖早于现有 v1 消息格式的历史版本。

## 传输矩阵

| 链路 | 入口 | 传输 | 代理要求 |
| --- | --- | --- | --- |
| 控制台与普通 API | `/`、`/api/*` | 公网 HTTPS；反代上游 HTTP/1.1 或 HTTP/2 | 保留 `Host`、`X-Forwarded-For`、`X-Forwarded-Proto` |
| Webhook 与运行状态 | `/api/webhooks/*` | 公网 HTTPS；反代上游 HTTP | 不改写请求体；保留认证和签名头 |
| 模型流式响应 | `/api/runtime/sandboxes/*/llm/*` | SSE/分块响应 | 关闭响应缓冲与缓存；逐块刷新；读写超时长于最长模型调用 |
| Worker 轮询、心跳和结果上报 | `/api/servers/*` | 远程必须 HTTPS；仅精确回环可用 HTTP | Worker 必须能访问同一公开地址；不要缓存响应 |
| Worker 会话总线 | `/api/servers/*/sessions/connect` | 远程 WSS；仅精确回环可用 WS | 转发 `Upgrade`/`Connection`；空闲超时长于最长会话 |
| 浏览器终端与文件操作 | `/api/sandboxes/*/session` | 远程 WSS；文本帧 | 与 Worker 会话使用相同的 WebSocket 配置 |
| 浏览器桌面 | `/api/sandboxes/*/desktop` | 远程 WSS；文本及二进制帧 | 禁止响应缓冲；允许二进制帧；不要做内容转换 |
| Worker 与驱动下载 | `/api/worker/*` | 远程必须 HTTPS；仅精确回环可用 HTTP | 允许较大的响应体和足够的下载超时；不得降级重定向 |

浏览器到反向代理可以使用 HTTP/2 或 HTTP/3，但反向代理连接 AgentBox 3000 端口时必须支持 HTTP/1.1 Upgrade。SSE 不要求 HTTP/2，关键是代理不能聚合响应块。

## Nginx 最小配置

下面的配置把 TLS 细节留给现有证书方案，只展示 AgentBox 必需的转发行为：

```nginx
map $http_upgrade $agentbox_connection {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name agentbox.example.com;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $agentbox_connection;

        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;
    }
}
```

Caddy 的 `reverse_proxy` 默认处理 WebSocket；仍需确认上游链路没有额外启用响应缓冲，并让任何 CDN、负载均衡器和防火墙的空闲超时都长于预期会话。

## 环境变量

公网地址为 `https://agentbox.example.com` 时，至少设置：

```dotenv
AGENTBOX_PUBLIC_URL=https://agentbox.example.com
AGENTBOX_ALLOWED_ORIGINS=https://agentbox.example.com
AGENTBOX_TRUSTED_PROXY=true
AGENTBOX_TRUSTED_PROXY_CIDRS=172.20.0.0/24
```

只有在 AgentBox 确实位于可信反向代理之后时才开启 `AGENTBOX_TRUSTED_PROXY`。`AGENTBOX_TRUSTED_PROXY_CIDRS` 必须替换为 **Server 直接连接方** 所在的精确网段（Compose 架构下通常是 `app` 与 `server` 所在 Docker 网络，而不是浏览器地址）；示例网段不能直接照搬。Server 只在直接对端受信任时解析转发头，并从 `X-Forwarded-For` 右侧向左剥离受信任代理。开启信任但 CIDR 缺失或无效会拒绝启动。不开启或直接对端不受信任时，转发头会被忽略，避免客户端伪造来源、协议或 WebSocket Origin。

## 验收

部署或更换代理后，至少完成以下真实检查：

1. 登录控制台并完成一个普通 API 操作。
2. 从外部发送一次带认证的 Webhook，并轮询到终态。
3. 让模型输出持续流式返回，确认首个事件及时出现且中途没有固定时长断流。
4. 打开终端，执行命令并上传、下载一个文件。
5. 打开桌面，确认键盘、鼠标和连续二进制画面可用。
6. 保持会话超过代理的默认空闲时间，确认 Worker、终端和桌面没有被提前关闭。
