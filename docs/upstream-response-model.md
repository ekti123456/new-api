# 上游响应模型日志

管理员在「系统设置 → 运维 → 日志维护」开启「记录并显示实际响应模型」并保存后生效。配置键为 `UpstreamResponseModelLogEnabled`，默认关闭。

- 开启：消费日志和错误日志可记录上游响应信封实际声明的模型，并在模型标签下以灰色小字显示 `↳ 上游自报: 模型名`。日志详情也显示同一字段。与请求模型同名时仍显示。
- 关闭：不记录该诊断；管理员日志、用户日志和按令牌查看日志的接口均隐藏已有日志中的该字段。已有数据库记录不会删除，重新开启可再次查看。更新设置会使当前页面的日志查询缓存失效。
- 没有自报模型的响应、旧日志和开启前的请求不会用请求模型或映射模型补填。

三种模型互相独立：`model_name` 是原请求/计费模型，`other.upstream_model_name` 是渠道映射后的请求目标，`other.upstream_response_model` 是上游响应自报值。本功能不改变渠道选择、计费模型、用量或回传的响应内容；自报值不是对提供商实际执行模型的独立验证。

## 采集

在公共 HTTP 请求出口观察原始 JSON/SSE 字节，位于协议转换之前；也观察 OpenAI Realtime 上游 WebSocket 事件。可识别 `response.model`、顶层 `model`、Gemini `modelVersion`、Claude `message_start.message.model` 和 Realtime `session.model`。不搜索消息文本、工具输出字符串、请求元数据，也不复制协议转换器生成的模型名。

同一次响应初始和结束模型不一致时记录冲突标记 `other.upstream_response_model_conflict`，优先使用结束事件的声明。每次上游尝试重新采集，失败重试不会把旧渠道的模型带入新响应。

诊断缓冲最多保留 2 MiB 的单个 JSON/事件；超过限制只跳过该诊断，不影响响应转发。SSE 后续的小事件仍可采集；例如大图片输出事件之后的 `response.completed`。不走公共 HTTP 出口、也不走上述 Realtime 处理器的供应商专用传输暂不采集。

模型名经过长度和字符检查。诊断中不保存响应正文、凭据或工具内容。数据存入日志现有 `other` 字段，无需新增数据库列。

## 验证

- 后端：`go test ./common ./relay/channel ./relay/channel/openai ./controller ./model -count=1`。
- 覆盖原始 JSON、分片/多行 SSE、结束事件模型冲突、重试隔离、诊断关闭、日志存取、旧日志隐藏、计费字段保持及超大事件后继续采集。
- 前端：模型标签箭头行显示/缺省/同名/长内容转义；开关默认关闭、点击保存前不提交、开启与关闭保存。
- `bun run typecheck`、变更文件 lint 和 `bun run build`。
