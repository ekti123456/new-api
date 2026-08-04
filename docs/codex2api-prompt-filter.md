# Codex2API Prompt 检查联动

NewAPI 可以只向明确绑定的 Codex2API 上游发送签名身份与策略元数据。出站请求体完成模型映射、参数覆盖等处理后再计算 SHA-256，因此 Codex2API 验证的是实际收到的请求体。

## 推荐配置

先在 Codex2API 的 Prompt Filter 管理页中，为 NewAPI 渠道实际使用的 Codex2API API Key 创建绑定。两端必须使用相同的 `platform_id` 和绑定密钥；密钥至少 32 个字符。

NewAPI 环境变量：

```env
CODEX2API_POLICY_ENABLED=true
CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED=true
CODEX2API_POLICY_BINDINGS=[{"platform_id":"primary-newapi","target":"http://127.0.0.1:18095","codex_key_fingerprint":"<Codex2API API Key 的 SHA-256 小写十六进制值>","secret":"<与 Codex2API 绑定完全相同的密钥>","enabled":true}]
CODEX2API_POLICY_AUDIT_ENABLED=true
CODEX2API_POLICY_STRIKE_ENABLED=false
CODEX2API_POLICY_ACCOUNT_BAN_ENABLED=false
CODEX2API_POLICY_IP_BLOCK_ENABLED=false
CODEX2API_POLICY_BAN_AFTER=2
CODEX2API_POLICY_WINDOW_SECONDS=604800
```

每个绑定同时校验目标地址和 Codex2API API Key 指纹。原始 API Key 不写入绑定配置。`target` 可以是主机根地址或带路径前缀的地址；路径匹配遵守分段边界，例如 `/v1` 不会匹配 `/v10`。

PowerShell 计算 Key 指纹示例：

```powershell
$key = '<Codex2API API Key>'
$bytes = [Text.Encoding]::UTF8.GetBytes($key.Trim())
$sha256 = [Security.Cryptography.SHA256]::Create()
try { ([BitConverter]::ToString($sha256.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant() } finally { $sha256.Dispose() }
```

Codex2API 的 Prompt Filter 总开关、运行模式与绑定的 `require_signed_identity` 仍需在 Codex2API 侧按部署策略配置。NewAPI 发送的 `mode`、`profile` 只是已签名审计元数据，不覆盖 Codex2API 的全局 GuardPipeline 策略。

## 违规决策联动

Codex2API 拒绝 Prompt 后会返回 `policy-decision-v1` HMAC 签名决策。NewAPI 仅接受 Request ID 与当前签名请求一致、且能用该目标和 Key 绑定密钥完成验签的决策。验签成功后会停止渠道重试，防止通过切换上游绕过 Prompt 检查，并只保存规则版本与 Prompt 证据的 SHA-256，不保存 Prompt 原文。

- `CODEX2API_POLICY_AUDIT_ENABLED`：保存已验签决策，默认 `true`。
- `CODEX2API_POLICY_STRIKE_ENABLED`：在时间窗口内累计可处罚决策，默认 `false`。
- `CODEX2API_POLICY_ACCOUNT_BAN_ENABLED`：达到阈值后禁用普通用户账号，默认 `false`。
- `CODEX2API_POLICY_IP_BLOCK_ENABLED`：达到阈值后临时限制来源 IP，默认 `false`。
- `CODEX2API_POLICY_BAN_AFTER`：时间窗口内触发处罚所需次数，默认 `2`。
- `CODEX2API_POLICY_WINDOW_SECONDS`：CYB 累计窗口及 IP 限制时长，默认 `604800` 秒（7 天）。

账号或 IP 处罚必须同时显式启用 `CODEX2API_POLICY_STRIKE_ENABLED=true`。管理员和 Root 用户只记录审计，不累计处罚。普通 HTTP、表单请求、WebSocket 握手失败以及 WebSocket 建连后的逐事件违规决策均支持验签；逐事件还会额外验证 `policy-event-v1` 签名。Decision ID 具有数据库唯一约束，用于防止重放导致重复累计。

## 请求协议

身份签名原文为：

```text
v1\n<timestamp>\n<request_id>\n<user_id>\n<client_ip>\n<http_method>\n<request_path>\n<body_sha256>
```

使用绑定密钥计算 HMAC-SHA256，并发送：

```text
X-NewAPI-User-ID
X-NewAPI-Client-IP
X-NewAPI-Request-ID
X-NewAPI-Timestamp
X-NewAPI-Method
X-NewAPI-Path
X-NewAPI-Body-SHA256
X-NewAPI-Signature-Version: 1
X-NewAPI-Signature
X-NewAPI-Policy-Meta
X-NewAPI-Policy-Meta-Signature
```

`X-NewAPI-Policy-Meta` 是无填充 Base64URL JSON。其独立签名原文为：

```text
policy-meta-v1\n<request_id>\n<body_sha256>\n<base64url_meta>
```

NewAPI 会先删除客户端透传或渠道 Header Override 中的全部同名头，再写入服务端生成的值。未命中目标和 Key 双重绑定时不会发送身份头。

## 旧配置兼容

为兼容早期部署，也支持：

```env
CODEX2API_POLICY_ENABLED=true
CODEX2API_POLICY_TARGETS=http://127.0.0.1:18095
CODEX2API_POLICY_SECRET=<至少 32 个字符>
CODEX2API_POLICY_PLATFORM_ID=newapi
```

旧配置只有目标隔离，没有逐 Key 隔离，建议迁移到 `CODEX2API_POLICY_BINDINGS`。当前 Codex2API 已采用逐 API Key 绑定；使用旧配置时，也必须在 Codex2API 管理页为实际 API Key 创建相同 platform 与 secret 的绑定。旧版 `PROMPT_FILTER_NEWAPI_SECRET` 不能替代当前逐 Key 绑定。
