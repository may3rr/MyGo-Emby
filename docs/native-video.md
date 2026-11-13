# macOS 原生视频输出

播放器在 macOS 使用 `internal/player/engine_darwin.go`：`AVPlayerItem → AVPlayer → AVPlayerLayer → NSView → ui.NativeView`。AVFoundation 统一负责解码、受支持的 Dolby Vision 处理、色彩管理、HDR/EDR 显示和 SDR 屏幕适配；应用不请求 NV12 帧，也不将电影送入 MyGo 的 SDR shader。其他平台保留 `ui/video` 路径。

所有原生对象操作在主线程执行。`ui.PlatformViewHandle.Close()` 先将视图从所有 MyGo 窗口中移除，再解绑播放器、停止播放并释放 NSView/AVPlayerLayer/AVPlayer/AVPlayerItem；重复关闭无副作用。视频由系统独立显示，UI 在播放时每 50ms 检查进度与字幕；暂停后不持续轮询。

原生播放器只在第一次构建视频视图时启动播放时钟。在窗口被完全遮挡或位于其他 Space、尚未呈现首帧时，加载可以继续，但不会静音消耗播放进度；显示后仍从正确的开始位置或续播点起播。

## 固定 MyGo 版本的局部补丁

依赖仍是 `go.mod` 固定的 `v0.8.4-0.20261111053122-9171337c021b`。`vendor/` 本来已有 HDR shader 试验修改，本次不将其当作 macOS 电影播放输出。

本轮补丁在 `patches/mygo-native-video.patch`，新增或修改：

- `ui/platform_view.go`：Go 持有的 NSView 的托管句柄，支持先脱离窗口再释放原生对象。
- `ui/webview.go`：无需 Swift native 模块即可使用平台已有的 EmbedView；句柄关闭时移除所有注册，避免切片后遗留视图。
- `internal/darwin/surface.go`：明确启用 NSWindow 的鼠标移动事件，维持全屏控制条唤起。
- `ui/platform_view_test.go`：跨窗口注册释放、重复关闭回归测试。

直接运行 `go mod vendor` 会覆盖本地补丁，也会覆盖原有 HDR shader 试验代码。需要重新生成 vendor 时，先保存其他本地修改，再从固定依赖生成并应用本轮补丁：

```sh
patch -d vendor/github.com/egoist/mygo -p1 < patches/mygo-native-video.patch
```

## 验证

```sh
CGO_ENABLED=0 go test ./... -timeout=90s
CGO_ENABLED=0 go test github.com/egoist/mygo/ui -run TestHostedPlatformView -count=1
CGO_ENABLED=0 go vet ./...
go tool mygo vet .
MYGO_NATIVE_TEST_FILE=/绝对路径/至少30秒测试片.mp4 CGO_ENABLED=0 go test ./internal/player -run TestNativeWindowPlayback -count=1 -v -timeout=90s
go tool mygo build -skip-dmg -o build/native-hdr
```

原生集成测试创建自己的 AppKit 窗口，不需要辅助功能权限，不移动用户真实鼠标；测试发送窗口内 AppKit 鼠标事件，验证全屏控制条唤起。它需要一个可显示窗口的桌面会话；系统权限面板、锁屏或其他 Space 导致窗口完全遮挡时，无法验收原生图层。

`MYGO_NATIVE_TEST_SHOT` 可保存 MyGo 控制条/字幕截图；该截图不包含 AVPlayerLayer 视频。`MYGO_NATIVE_TEST_SCREENSHOT` 使用系统 `screencapture -l` 只截取测试窗口，包含系统视频层，需要已有屏幕捕获权限。HDR 最终画质需与系统播放器对照，并在有 HDR 能力的显示器上验收，不能仅凭 PNG 截图或 `Ready=true` 判断 HDR 亮度正确。

架构依据：[Apple WWDC22：Display HDR video in EDR with AVFoundation and Metal](https://developer.apple.com/videos/play/wwdc2022/110565/)。

## 2026-11-12 验收状态

- 本地 HDR10 灰阶、HDR10 彩条和 HLG 彩条均通过原生窗口集成测试，包括全屏 AppKit 鼠标移动、Seek、暂停/继续和视图释放。
- 相同 10-bit BT.2020 彩条与独立 Swift/AVKit AVPlayerView 作系统参照，8 个色块的屏幕截图 RGB 最大通道差值：HDR10 为 1，HLG 为 0。本机显示器最大潜在 EDR 分量为 16。这是应用合成链路对照，不是显示器色度校准。
- 真实 Dolby Vision Profile 5 MKV 样片通过项目自己的 remux 变成 fMP4 HLS，验证 dvh1.05 / PQ 标记保留，并在原生图层中显示正常肤色，未见整幅发绿。
- 普通项目测试、托管视图生命周期测试、`go vet`、`mygo vet` 通过。生产 `.app` 已构建并通过签名校验，位于 `build/native-hdr/darwin-arm64/MyGo Emby.app`。
- 之前系统屏幕访问权限提示导致的桌面遮挡已由用户处理；最新重跑时窗口可见，测试通过。没有将遮挡时失败的运行计为通过。
- 移除原生验收窗口、应用截图模式、playerdemo 的强制置顶；新增回归步骤验证播放器是普通窗口、全屏退出后层级正常、另一普通窗口可以盖住它。
- 《点石成金》第 1 集有多个媒体源。服务器标注的 HDR10 源播放地址，独立 `ffprobe` 实际读出 HEVC Main / BT.709 SDR；改变媒体源参数大小写和使用版本条目地址，仍得到相同格式。同一地址经 FFmpeg 解码的 60 秒处画面明显发绿，图像保存在 `build/hdrtest/diashichengjin-source.png`。随后已确认三个版本返回同一文件，详见下表；这部片的原生播放颜色验收仍未完成，没有通过手动调色掩盖它。

### 已确认的服务器版本映射异常

对服务器返回的三个 `DirectStreamUrl` 原样保留各自 `MediaSourceId`，请求 `Range: bytes=0-4095` 并跟随 302，结果如下：

| 服务器声明的源 | 元数据文件大小 | 实际 Range 响应的总大小 |
|---|---:|---:|
| MOV / SDR | 308,909,862 | 308,909,862 |
| MKV 条目 | 214 | 308,909,862 |
| MP4 / HDR10 | 1,666,768,782 | 308,909,862 |

三者均跳到相同 CDN 对象路径，前 4096 字节的 SHA-256 也完全相同：`9aa7e8f9c7d3efa1ce23e07c2c14210fd2f3df98e22589ad2c399ca2c024d0a4`。因此可以确认：当前服务器提供的 HDR10 版本播放地址，实际仍指向那份 MOV / SDR 文件，而非元数据声明的 1.66GB HDR10 文件。无令牌的完整探测结果保存在 `build/hdrtest/version-source-proof.json`。

在获得真正的 HDR 版本之前，不能用此地址完成《点石成金》的 HDR 颜色验收；这个 8-bit SDR 返回文件未包含版本元数据声称的原始 10-bit HDR 信号，不能靠调整 shader 补回。需要服务器修正版本映射，或提供可读的原始 HDR 片源。原生 HDR 链路仍可用独立的正确 HDR10/HLG/Dolby Vision 测试片继续验证。

## 随仓库保存的彩色回归样片

`internal/player/testdata/mygo-native-hdr-colors.mp4` 与 `mygo-native-hlg-colors.mp4` 是本项目生成的 30 秒、640×360、10 fps、10-bit BT.2020 测试彩条，分别包含正确的 PQ / HLG VUI 与容器标记。无外部影片内容；真实 DV 片段仅用于本地验收，不进入仓库。

```sh
MYGO_NATIVE_TEST_FILE="$PWD/internal/player/testdata/mygo-native-hdr-colors.mp4" CGO_ENABLED=0 go test ./internal/player -run TestNativeWindowPlayback -v -count=1 -timeout=60s
MYGO_NATIVE_TEST_FILE="$PWD/internal/player/testdata/mygo-native-hlg-colors.mp4" CGO_ENABLED=0 go test ./internal/player -run TestNativeWindowPlayback -v -count=1 -timeout=60s
```

`internal/player/testdata/native-reference.swift` 是独立 AVKit 对照程序，窗口也使用普通层级。编译后传入测试片路径与输出 PNG 路径，可以对照系统视频层；调用系统窗口截图需要屏幕捕获权限。测试截图保存在 `build/hdrtest/native-verification/`。

## 播放交互收尾

- 全屏鼠标移动显示进度条、播放/暂停控件和光标；播放中停止操作后隐藏，暂停/菜单/拖动时保持可见。
- 正常窗口允许其他窗口覆盖，退出全屏恢复普通层级；生产截图模式与演示/测试入口也不强制置顶。
- 进度条悬停/拖动显示目标时间气泡，限制在滑轨可见范围内；悬停不触发网络跳转，拖动释放才提交一次，系统取消拖动不误跳。
- `TestFullscreenStageMovementAndControlClicks`、`TestProgressPreviewDoesNotSeekUntilReleased`、`TestCancellingProgressDragDoesNotJump` 锁定上述交互；原生窗口集成测试额外验证 AppKit 鼠标与普通窗口叠放。
