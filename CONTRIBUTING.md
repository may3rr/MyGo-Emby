# 贡献指南

欢迎参与 MyGo Emby 的开发。提交 PR 前请先阅读本仓库的设计文档与以下工程约定，避免破坏 UI 生命周期或原生视频补丁。

## 开发环境

- **Go**：需要 Go **1.27.4** 或更高版本（无需 Xcode/CGO，`CGO_ENABLED=0` 即可构建与测试）。
- **可选运行依赖**：本机无损转封装后降级音频转码时需要系统安装 `ffmpeg`（或设置 `MYGO_FFMPEG`）。

## 严禁覆盖 `vendor/`

本仓库提交了带有 macOS 原生视频（`AVPlayerLayer`）与窗口生命周期局部补丁的 `vendor/` 目录：

- **不要运行 `go mod vendor`**，否则会丢失 `vendor/github.com/egoist/mygo` 中的局部修复。
- 不要在未讨论的情况下随意修改 `vendor/`、`go.mod`、`internal/player/engine_darwin.go` 与 `internal/player/black_bars_darwin_test.go`。

## 包边界与架构原则

依赖方向必须保持单向：

```
main.go -> internal/app -> internal/player -> internal/remux
                                          \-> internal/emby
```

- `internal/emby`：纯 Emby / Jellyfin HTTP 客户端、媒体模型与字幕解析（SRT / WebVTT / ASS / SSA），禁止依赖任何 UI 包。
- `internal/remux`：纯 Go MKV/TS/MP4 到 fMP4 HLS 的本机转封装管线（可选 FFmpeg 仅用于不兼容音频转 AAC），禁止导入 `internal/app`。
- `internal/player`：播放器状态机、控制栏、诊断 HUD、字幕/章节/外部播放器回调与系统媒体集成，禁止导入 `internal/app`。
- `internal/app`：应用页面、导航、设置、凭据存储与窗口生命周期。

## MyGo UI 与并发规范

1. **所有 UI 状态只在主线程读写**：后台 goroutine 通过 `app.update` / `Player` 的 `Post` 回调切回主线程，并检查世代号（`epoch` / `gen` / `sgen`）丢弃过期响应。
2. **禁止在 View 中执行阻塞操作或文件系统探测**：文件探测（如外部播放器路径扫描、磁盘缓存统计）必须在后台初始化完成后再同步给主线程。
3. **无边框列表必须使用唯一 `c.Key(...)`**：同一容器内动态列表元素、徽章、卡片必须稳定键控。
4. **保持播放器逐帧零分配约束**：控件隐藏时的播放画面渲染需通过 `TestSteadyFrameAllocations`。
5. **无窗口环境安全**：调用系统单实例功能（如 `mygo.Power`、`mygo.NowPlaying`、`mygo.Dialog`）前必须检查 `Window != nil`，以保障 headless `ui.NewTester` 测试可运行。

## 提交前检查

```sh
CGO_ENABLED=0 go vet ./...
go tool mygo vet .
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
CGO_ENABLED=0 go test -race ./internal/...
```

更多界面与架构设计细节见 [DESIGN.md](DESIGN.md)。
