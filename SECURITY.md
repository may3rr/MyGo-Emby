# 安全策略

## 报告安全漏洞

如果你发现了凭据泄露、命令执行、路径穿越或越权等安全问题，**请不要直接提交公开 Issue**。请通过仓库维护者私密联系方式（GitHub Security Advisory / 私信维护者）提交：

- 受影响的平台与版本或 Commit
- 复现步骤与最小验证示例（如有 HTTP 请求或 URL，请先手动脱敏 Token / `api_key`）
- 预期行为与实际安全影响

## 凭据与令牌存储说明

- **首选系统钥匙串（Keychain）**：会话令牌默认通过 `mygo.Secrets` 写入系统凭据存储，`settings.json` 中仅保留服务器地址、用户名与 `TokenRef`。
- **降级明文行为**：当系统钥匙串不可用或写入失败时，`internal/app` 会将 `plainTokens` 置为 `true`，此时令牌会降级保存于本地 `settings.json`，并在「设置 -> 关于 -> 凭据存储」行中明确提示用户。

## 日志与诊断信息脱敏

- 所有包含 `api_key`、`apikey`、`token`、`x-emby-token`、`access_token` 或 URL UserInfo（`user:pass@host`）的地址在进入播放诊断面板（`emby.RedactURL`）和外部播放器错误日志前都会被替换为 `REDACTED`。
- 外部播放器（IINA / mpv / VLC）始终通过 `exec.Command` 参数数组直接启动，禁止通过 shell 拼接字符串。
- 请勿在反馈日志、截图或 Issue 中粘贴原始带鉴权参数的串流 URL 或 `settings.json`。
