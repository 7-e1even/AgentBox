# Webhook 流水线接入指南

AgentBox Webhook 把一个外部事件转换为一个可追踪的 Run。Run 可以：

- 创建沙箱，供预览环境或后续人工操作使用；
- 创建沙箱并执行一条有超时的 POSIX shell 命令，保存退出码和最多 512 KiB 输出；
- 从 Payload 中解析沙箱 ID 并销毁该沙箱。

它不是 DAG 编排器。重试策略、并行矩阵、审批和跨系统步骤继续由 GitHub Actions、GitLab CI、Jenkins、n8n 等上游负责。

## 通用调用与状态轮询

在“自动化”页面选择 Bearer Token 后，任意流水线都可以使用同一套协议：

```sh
RESPONSE=$(curl --fail-with-body -sS -X POST "$AGENTBOX_WEBHOOK_URL" \
  -H "Authorization: Bearer $AGENTBOX_WEBHOOK_SECRET" \
  -H "Idempotency-Key: $CI_PIPELINE_ID-$CI_JOB_ID" \
  -H 'Content-Type: application/json' \
  -d "{\"commit\":\"$GIT_COMMIT\",\"branch\":\"$GIT_BRANCH\"}")

STATUS_URL=$(printf '%s' "$RESPONSE" | jq -r .statusUrl)
RUN_TOKEN=$(printf '%s' "$RESPONSE" | jq -r .runToken)

while :; do
  RUN=$(curl --fail-with-body -sS "$STATUS_URL" \
    -H "Authorization: Bearer $RUN_TOKEN")
  STATUS=$(printf '%s' "$RUN" | jq -r .run.status)
  case "$STATUS" in
    succeeded|skipped) break ;;
    failed|expired)
      printf '%s\n' "$RUN" | jq -r '.run.output, .run.errorMessage' >&2
      exit 1
      ;;
  esac
  sleep "$(printf '%s' "$RESPONSE" | jq -r .pollAfterSeconds)"
done
```

首次 POST 通常返回 `202 Accepted`。这只表示 Run 已持久化或排队，不代表任务成功。响应格式为：

```json
{
  "runId": "...",
  "sandboxId": "...",
  "status": "queued",
  "duplicate": false,
  "statusUrl": "https://agentbox.example/api/webhooks/.../runs/...",
  "runToken": "...",
  "pollAfterSeconds": 2
}
```

`runToken` 只允许读取对应 Run，作用域不扩展到控制台或其他 Run。它应按流水线密钥处理，不要写入公开日志。

## 鉴权适配器

| 模式 | 适用来源 | 验证字段 | 事件 ID / 类型 |
| --- | --- | --- | --- |
| Bearer Token | Jenkins、n8n、自定义服务 | `Authorization: Bearer ...` | `Idempotency-Key`、`X-Event-*` 或 CloudEvents 头 |
| AgentBox HMAC-SHA256 | 自定义签名调用方 | `X-AgentBox-Timestamp`、`X-AgentBox-Signature` | `Idempotency-Key` |
| GitHub | Repository / Organization Webhook | `X-Hub-Signature-256` | `X-GitHub-Delivery`、`X-GitHub-Event` |
| GitLab | Project / Group Webhook | `X-Gitlab-Token` | `X-Gitlab-Event-UUID`、`X-Gitlab-Event` |
| Standard Webhooks | 遵循 Standard Webhooks 的服务 | `webhook-id`、`webhook-timestamp`、`webhook-signature` | `webhook-id`、`webhook-type` |

GitHub 接入时，在仓库的 Webhooks 设置中填写 AgentBox URL，Content type 选 `application/json`，Secret 填 AgentBox 创建时显示的密钥，并选择需要的事件。GitLab 接入时，在 Webhooks 设置中填写 URL，将同一密钥填入 Secret token。

AgentBox HMAC 的签名原文是 `<unix_timestamp>.<raw_body>`，算法为 HMAC-SHA256，结果使用无填充的 base64url，并放入 `X-AgentBox-Signature: v1=<signature>`。GitHub、GitLab 和 Standard Webhooks 均按各自原生格式验证，不需要转换成 AgentBox 私有签名。

## 事件、条件和模板

无论来源如何，模板都能读取同一组变量：

- `.payload`：JSON 请求正文；
- `.headers`：已移除认证和签名字段的小写请求头；
- `.query`：URL 查询参数；
- `.event.id`、`.event.type`、`.event.source`、`.event.time`、`.event.receivedAt`；
- `.run.id`、`.run.shortId`、`.automation.id`、`.automation.name`；
- 创建类动作还可读取 `.project` 和 `.template`。

执行条件必须渲染为 `true` 或 `false`。例如只处理 GitHub Pull Request：

```gotemplate
{{ and (eq .event.source "github") (eq .event.type "pull_request") }}
```

条件为 false 的事件会生成 `skipped` Run，但不会提交 Worker Job。这样调用方仍能确认事件已被安全接收。

“执行隔离任务”的命令同样是模板，例如：

```gotemplate
go test ./{{ .payload.package }}
```

命令在模板配置的工作目录中执行。超时范围为 10 到 3600 秒；清理策略可以是保留、仅成功后清理或始终清理。创建类动作还可设置 60 秒到 30 天的 TTL，由后台自动回收。

## 幂等、重试和错误

优先使用来源自带的事件 ID；通用调用方应显式发送最多 255 字节的 `Idempotency-Key`。同一自动化中：

- 相同 Key 和相同 Payload 返回原 Run，`duplicate` 为 true；
- 相同 Key 但不同 Payload 返回 `409 idempotency_conflict`；
- 每分钟最多接收 30 次，每条自动化最多同时执行 5 个 Run；超限返回 `429` 和 `Retry-After`；
- Worker 长时间离线或租约过期后，后台会将 Job 和 Run 标记失败，避免永久占满并发额度。

Webhook 错误使用稳定结构：

```json
{
  "error": {
    "code": "rate_limited",
    "message": "自动化触发过于频繁，请稍后重试",
    "retryable": true
  }
}
```

上游只应自动重试 `retryable: true` 的错误，并复用原 `Idempotency-Key`。
