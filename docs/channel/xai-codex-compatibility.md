# Codex → xAI 请求兼容

检查日期：2026-09-10。此次修复只作用于 NewAPI 的 **xAI 渠道**，包括配置为其他中转地址的 xAI 渠道；不按模型名修改 OpenAI、Codex 等其他渠道。

## 官方 Codex 会发送什么

公开 `openai/codex` main 的 `build_responses_request` 会构造 `model`、`instructions`、`input`、`tools`、`tool_choice: auto`、`parallel_tool_calls`、`reasoning`、`store: false`、`stream: true`、`include: [reasoning.encrypted_content]`、`prompt_cache_key` 和 `client_metadata`。`text`、`service_tier`、`reasoning.summary/context`、`stream_options` 取决于模型能力、配置和功能开关，不是每个请求都会出现。WS 的增量请求还可以带 `previous_response_id`。

来源：
- [Codex 请求结构](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/common.rs)
- [Codex 请求构造](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs)

这是当日读取的公开 main，不是用户已安装客户端的固定版本，也不是故障请求抓包。

## 清理规则

清理位于 xAI 适配器最终 `DoRequest`，覆盖 `/v1/responses` 和 `/v1/chat/completions`，在普通转换和参数覆盖之后执行。**开启全局或渠道请求体透传时，也执行这一小范围兼容清理**，避免透传绕过修复。图片、音频等端点不受影响；这不是新增 WS 或 `/responses/compact` 端点支持。

| 字段 | 处理 |
| --- | --- |
| `client_metadata`、`access_programs`、`safety_identifier` | 删除出站 Codex/OpenAI 专属控制与身份字段 |
| `prompt_cache_options`、`prompt_cache_retention` | 删除未列入当前 xAI 缓存请求契约的扩展配置 |
| `reasoning.context/mode`、`text.verbosity` | 删除未列入当前 xAI 推理/文本控制契约的扩展；保留其他子字段 |
| `stream_options.include_obfuscation/reasoning_summary_delivery` | 删除 OpenAI 流控制扩展；保留 `include_usage` 等其他子字段 |
| 无工具时的 `tool_choice: auto/none` | 删除；有工具时保留。显式 `required` 或指定函数不悄悄降级，仍由上游校验 |
| Grok 4.5 及之后的 `grok-4.<版本>` 推理模型 | 同时处理 `reasoning.effort` 与 `reasoning_effort`：`none/minimal → low`；`max/ultra → xhigh`（4.5 为 `high`）；4.5 的 `xhigh → high`；有效档位保留 |
| 上述推理模型的 `stop/presence_penalty/frequency_penalty` | xAI 文档明确不支持，删除，包括显式零值 |

不套用上述模型档位规则到 Grok 4.3、旧型号、非推理型号或未知别名；未知档位保持原样，不能把任意错误输入当成合法请求。只清理列出的字段，不以全字段白名单误删未来能力。

## 必须保留的字段

`prompt_cache_key` **受 xAI 官方支持，并用于缓存路由**，不能照搬旧清理逻辑删掉。`include: [reasoning.encrypted_content]` 同样受支持。

保留 `store: false`（不擅自开启上游存储）、有效 `service_tier`（仍遵守 NewAPI 原有渠道费用控制）、`previous_response_id`、`text.format`、工具声明/调用/结果、历史消息、图片及已有加密内容。不会修改正文路径或用户显式业务 `metadata`，不会清空整个会话历史来规避 400。

清理使用独立出站 JSON，入站请求和已解析的会话信息不变；原有会话头和签名策略不改。更新出站 Content-Length，保留请求体大小限制和磁盘缓冲机制。

官方参考：
- [xAI 推理参数和不支持的参数](https://docs.x.ai/developers/model-capabilities/text/reasoning)
- [xAI Responses 请求](https://docs.x.ai/developers/rest-api-reference/inference/responses)
- [xAI 缓存键](https://docs.x.ai/developers/advanced-api-usage/prompt-caching/maximizing-cache-hits)
- [xAI 流式用量](https://docs.x.ai/developers/advanced-api-usage/cost-tracking)

## 验证与边界

回归测试覆盖字段保留、档位转换、空工具、重复清理、普通/透传/参数覆盖路径、出站实际请求长度和其他渠道不变。全部使用虚拟请求和本地回环上游，不调用真实计费接口。

未列入契约的字段是保守兼容清理，**不能据此断言每个字段都会触发 xAI 400，或认定它就是这次故障原因**。如果其他中转重新注入字段、工具协议不兼容或存在跨服务商加密历史，仍需查看该中转收到的原始 xAI `error/message`。不应记录或索取密钥、完整对话正文来代替必要的脱敏错误详情。
