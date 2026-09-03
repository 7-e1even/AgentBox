# Webhook 自动创建沙箱接入指南

AgentBox Webhook 把一个外部事件转换为一个可追踪的 Run，并按自动化中固定的项目、沙箱模板和模型绑定创建、启动一个沙箱。

Webhook Payload 不会覆盖模板，也不会作为命令执行。沙箱创建后的命令、销毁、审批、条件分支、重试策略、并行矩阵和跨系统步骤继续由 GitHub Actions、GitLab CI、Jenkins、n8n 等上游负责。

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
    succeeded) break ;;
    failed)
      printf '%s\n' "$RUN" | jq -r '.run.errorMessage // .run.provisioning.message // "沙箱创建失败"' >&2
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

## 事件记录与模板边界

AgentBox 会读取请求正文和必要的事件头，用于：

- 验证来源签名并识别事件来源、事件 ID 和事件类型；
- 计算 Payload 摘要，执行幂等冲突检查；
- 在 Run 中保存必要的审计元数据。

请求正文、请求头和查询参数不会渲染到沙箱配置中。每条自动化始终使用控制台中保存的项目、模板与模型绑定；需要按分支或事件选择不同模板时，请在上游工作流中选择对应的 Webhook。

Run 只会处于 `evaluating`、`queued`、`provisioning`、`succeeded` 或 `failed`。`succeeded` 表示沙箱已经创建并进入运行状态；`failed` 会携带创建阶段、是否建议重试和错误信息。

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
