# Responses 流结束与交付诊断

原生 `/v1/responses` 流处理区分响应生成状态与本地写回状态。

- `response.completed`、`response.done`、`response.incomplete` 均结束当前流，不等待额外 `[DONE]` 或 EOF。失败、取消终态也停止读取；上游失败仍保留错误，不标为生成成功。
- 终态中的实际 token 用量在写回前提取，包括 `response.incomplete`。只有上游没有 usage 对象时才尝试估算已收到的文本；明确返回的零用量不覆盖成估算值。
- 原有未完成响应的图片工具收费规则不变；不能因修复 token 用量而额外收取图片调用费用。
- 完成帧已经成功写入并调用本地 Flush 后，迟到的客户端取消不再抢占正常流结束状态。只有看到终态、但未写出或写入失败时，仍保留断线／错误状态。
- 不延长断开后的 NewAPI 上游读取、不自动重放请求、不变更账号绑定与定价规则。

使用日志 `other.stream_status.delivery` 包含：

| 字段 | 含义 |
| --- | --- |
| `terminal_event` | 最后处理的终态类型 |
| `response_status` | 生成状态；`incomplete` 不改写为 `completed` |
| `incomplete_reason` | 未完成原因；未知原因记为 `other`，不保存任意正文 |
| `terminal_write` | `pending`、`flushed` 或 `failed` |
| `usage_source` | `upstream`、`estimated` 或 `missing` |
| `terminal_received_at_unix_ms` | 本地处理终态的时间 |
| `terminal_flushed_at_unix_ms` | 本地 Flush 返回后的时间 |
| `client_canceled_at_unix_ms` | 扫描器观察到请求取消的时间 |

日志详情同时显示上述字段，成功流也可查看。不完整生成与流传输成功可以同时出现。

`flushed` 不是客户端确认收到了数据；普通 HTTP/SSE 没有应用层接收确认。时间字段是服务端观察时刻，不是对端行为的精确发生时刻。

无可计费用量时仅提示未扣费，不再推测“上游超时”。历史记录不会回填，也不会据此补扣费用。
