# AgentBox 传输与反向代理兼容矩阵

AgentBox 的浏览器、Worker 和沙箱共用一个公开入口。生产环境应只代理 `app` 的 3000 端口，由 Next.js 将 `/api/*` 转发到内部 Server；不要把 PostgreSQL 或 Server 的 8091 端口直接暴露到公网。

## 传输矩阵

| 链路 | 入口 | 传输 | 代理要求 |
| --- | --- | --- | --- |
| 控制台与普通 API | `/`、`/api/*` | HTTP/1.1 或 HTTP/2 | 保留 `Host`、`X-Forwarded-For`、`X-Forwarded-Proto` |
| Webhook 与运行状态 | `/api/webhooks/*` | 普通 HTTP | 不改写请求体；保留认证和签名头 |
| 模型流式响应 | `/api/runtime/sandboxes/*/llm/*` | SSE/分块响应 | 关闭响应缓冲与缓存；逐块刷新；读写超时长于最长模型调用 |
| Worker 轮询、心跳和结果上报 | `/api/servers/*` | 普通 HTTP | Worker 必须能访问同一公开地址；不要缓存响应 |
| Worker 会话总线 | `/api/servers/*/sessions/connect` | WebSocket | 转发 `Upgrade`/`Connection`；空闲超时长于最长会话 |
| 浏览器终端与文件操作 | `/api/sandboxes/*/session` | WebSocket，文本帧 | 与 Worker 会话使用相同的 WebSocket 配置 |
| 浏览器桌面 | `/api/sandboxes/*/desktop` | WebSocket，文本及二进制帧 | 禁止响应缓冲；允许二进制帧；不要做内容转换 |
| Worker 与驱动下载 | `/api/worker/*` | 普通 HTTP 下载 | 允许较大的响应体和足够的下载超时 |

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
```

只有在 AgentBox 确实位于可信反向代理之后时才开启 `AGENTBOX_TRUSTED_PROXY`。否则客户端可以伪造 `X-Forwarded-*` 头，影响来源判断和 WebSocket Origin 校验。

## 验收

部署或更换代理后，至少完成以下真实检查：

1. 登录控制台并完成一个普通 API 操作。
2. 从外部发送一次带认证的 Webhook，并轮询到终态。
3. 让模型输出持续流式返回，确认首个事件及时出现且中途没有固定时长断流。
4. 打开终端，执行命令并上传、下载一个文件。
5. 打开桌面，确认键盘、鼠标和连续二进制画面可用。
6. 保持会话超过代理的默认空闲时间，确认 Worker、终端和桌面没有被提前关闭。
