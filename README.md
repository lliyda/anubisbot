# anubisbot

Anubis 链（6714）本机抢额度运维台：任务列表驱动，整点 / 每日 11:59 到点并发广播。默认 `MODE=sim`，不实际上链。

## 要求

- Go 1.22+
- 运维页 `http://127.0.0.1:47193`
- 本地面板：Windows 原生托盘 + 小窗（`anubisbot-panel.exe`，Walk，无需 CGO）

## 启动

```bash
cd anubisbot
copy config\wallets.example.json config\wallets.local.json
copy data\tasks.example.json data\tasks.json
go mod tidy
go build -o anubisbot.exe ./cmd/anubisbot
go build -o anubisbot-panel.exe ./cmd/anubisbot-panel
.\anubisbot-panel.exe
```

运行面板后看系统托盘图标：右键可打开运维页、启停重启、OTA；「打开控制窗」可改配置。面板会尝试自动拉起 `anubisbot.exe`。退出面板**不会**停止 bot。

调试用旧网页面板：`.\anubisbot-panel.exe -http`（`http://127.0.0.1:47192`，可选）。

## 版本与发布

```bash
go build -ldflags "-X anubisbot/internal/version.Version=1.0.14" -o anubisbot.exe ./cmd/anubisbot
go build -ldflags "-H windowsgui -X anubisbot/internal/version.Version=1.0.14" -o bin/anubisbot-panel.exe ./cmd/anubisbot-panel
```

GitHub Release 上传 `anubisbot-windows-amd64.zip`（内含上述两个 exe）。在控制窗配置里填写 `owner/repo` 后即可检查/应用 OTA。

## 模式

- `MODE=sim`（默认）：到点只记 `would_send` 日志，不上链
- `MODE=live`：需在 UI 二次确认后才会 `SendTransaction`

密钥只放环境变量（见 `.env.example`），不要提交 `*.local.json`。

## 任务类型（UI 中文）

| 内部 ID | 显示名 |
| --- | --- |
| fixed_price | 固定价销毁债券 |
| lgns_burn | LGNS 销毁债券 |
| helix | AI 算力机器人（Helix） |
