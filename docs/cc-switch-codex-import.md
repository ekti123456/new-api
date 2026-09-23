# Codex 一键导入 CC Switch

NewAPI 的 Codex 导入链接传入 `name=OpenAI` 和 `codexModelProvider=OpenAI`，用于生成：

```toml
model_provider = "OpenAI"

[model_providers.OpenAI]
name = "OpenAI"
# base_url、模型和密钥沿用当前导入内容
```

`codexModelProvider` 是配套 CC Switch 修改版增加的可选参数。需要使用支持此参数的 CC Switch；未适配的版本仍会将 ID 写成 `custom`，仅修改 NewAPI 无法改变旧导入器的行为。

参数仅用于 Codex，Claude、Gemini 的导入名称、地址和模型参数保持不变。CC Switch 未收到该参数的旧链接仍使用 `custom`。已导入的配置不会被自动迁移，需要重新导入或手动调整对应配置。
