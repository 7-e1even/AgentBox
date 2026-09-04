# 控制面契约与实现边界

本轮保留 Go 模块化单体、PostgreSQL 和现有 React 状态组织，不增加消息中间件、通用工作流引擎或自动配置调和器。

## 资源与任务版本

- `Resource.spec` 按 `kind` 校验字段和类型；当前 `specVersion` 为 `1`，旧请求省略时按 `1` 处理，未知版本拒绝写入。
- `generation` 和 `observedGeneration` 是服务端只读字段。配置写入推进 `generation`；沙箱创建、启动或重启成功后，记录该任务确认的版本。它不是对外部运行环境持续无漂移的保证。
- 沙箱有排队或执行中的任务时，拒绝普通配置修改。任务记录入队时的资源版本；认领、进度和完成回写均检查版本，过期任务不能修改新版本资源。
- 沙箱的服务器、模板、驱动、镜像、CPU、内存、桌面、网络策略、工作目录和初始化命令属于创建时配置，普通更新不能改变。需要改变这些配置时创建新沙箱；代理使用已有专用操作接口。新建沙箱保存有效创建配置，后续模板编辑不隐式改变它。
- 旧沙箱缺省字段保持可读取、可原样提交，不回溯历史任务推断实例配置。编辑器不会把显示用的模板默认值自动写入这些锁定字段。
- `GET /api/resources?kind=…&projectId=…` 在数据库中筛选；`GET /api/resources/{id}` 返回单项。控制台持久布局只加载登录与项目导航，各业务域独立加载、展示错误和重试。

轮询共用轻量控制器：同查询单次请求、取消与过期结果隔离、可重试错误退避、页面隐藏/离线暂停；刷新失败保留上次数据并明确提示。配置依赖未就绪时保留编辑草稿、禁用提交。

## Skills、Variables 与 MCP

- Skill 写入以 `SKILL.md` 为权威：服务端校验 YAML frontmatter、资源 ID 与 `name` 一致、路径/文件数/解码后大小，并根据规范化正文和排序后的附件生成 SHA-256 bundle digest。列表 API 只返回摘要和大小统计，详情 API 才返回正文与附件。
- Variable 只保存 Worker 来源引用。`env://NAME` 由 Worker 服务环境解析，`secret://NAME` 由 root-only 的 `/etc/agentbox-worker-secrets/NAME` 解析；解析后的值只写入沙箱内权限受限的受管配置文件，不进入沙箱 manifest、任务结果或审计详情。MCP Header 只引用同一环境已绑定 Variable 的目标 key。
- Runtime、Sandbox 与其引用的 Skill、Variable、MCP 必须属于同一 Project 且处于启用状态；服务端在写入和任务组装时都重新校验，不依赖前端过滤。重复 Variable key、与直接环境变量同名以及 MCP Header 缺少对应 Variable 都会拒绝。
- MCP 持久层使用 `stdio {command,args[],cwd}` 或 `http {url,headers[{name,valueFrom}]}` 的单一结构。读取兼容旧字符串格式，但成功写入总会规范化；含明文 Header 的旧数据不会返回客户端或下发 Worker。
- Worker 用只含资源 ID、目标和摘要的受管 manifest 做 desired-state 调和。Variable、Skill 和 MCP 各自在沙箱内暂存并以一组受管路径原子替换；该组任一步失败会恢复该组旧配置。不同配置组之间采用失败可见、下次重试收敛的顺序调和，不宣称跨组事务。成功的重复执行没有额外副作用，删除或清空绑定会只移除 AgentBox 上次管理的内容。
- Worker heartbeat 的 `managed-capability-config` 与 `mcp-managed-config` capability 是受管配置格式门禁。通过输出安全门禁但缺少新格式能力的 Worker，只允许服务端确认可无损串行化的初次配置；清理旧配置、Variable 注入等不能证明等价的任务会在入队前要求升级。
- `fail-closed-job-output` 是业务任务的安全门禁：未声明该能力的旧 Worker 只能领取自身升级任务。升级前已经租出的任务仍由 Server 在写库前二次规范化，任意 guest 输出不进入结果、审计或 Automation；错误 code/stage、重试属性、外部实例 ID 和 Agent 状态也只接受动作相关的固定词表。
- 沙箱详情中的 `generation` 是 desired 版本，`observedGeneration` 和 `capabilityDigest` 只在创建、启动或重启任务成功后推进。能力资源发生变化时，引用它的沙箱标为待重启；“资源已启用”不等同于“运行时已应用”。
- BoxLite `restricted` 的网络允许列表在实例创建时冻结。已有实例若增删 HTTP MCP 主机，或被引用 MCP 改到另一主机，服务端会拒绝并要求新建沙箱；不能通过一次看似成功的重启伪装成网络策略已更新。同一主机下的路径、Header 或 STDIO 配置变化仍可通过重启调和。

## 审计与运行遥测

账号/会话、资源、凭据、代理、服务器、自动化和任务认领/完成等关键变更，在业务事务内追加审计 outbox；审计追加失败则业务一并回滚。现有日志刷新循环投递 outbox，日志写入和投递确认在同一事务，事件 ID 唯一约束防止重复。

审计仅记录操作、操作者、资源标识和允许的简短元数据，不记录密码、令牌、资源 spec 或请求体。已投递 outbox 保留七天，热记录投递到 `system_logs`：系统每五分钟发起维护；每次成功维护后，`delivery: transactional` 收敛到 365 天内且不超过 1,000,000 行，`delivery: best-effort` 收敛到 30 天内且不超过 100,000 行；两次维护之间可暂时超出行数边界，维护时优先淘汰最旧记录。需要更长期合规留存时，应在到期前导出到独立审计存储。失败请求、心跳和连通性遥测仍可丢弃；公共状态轮询与未匹配路由不写入数据库日志。

Worker 脚本属于中立的 `workerscript` 包，Worker 不依赖 HTTP API 或数据库包。协议握手与 Session 的单 Server 副本限制见 [传输兼容说明](transport-compatibility.md)。
