# MyGo Emby 顶层设计

用 [MyGo](https://github.com/egoist/mygo) 原生 UI 写的 Emby / Jellyfin 桌面客户端。纯 Go，无 WebView，无 cgo。目标是 macOS 原生应用的观感，功能覆盖日常观影的完整链路。

## 1. 技术约束

- Go 1.27，`github.com/egoist/mygo` 固定在 `go.mod` 里的伪版本，**不要升级或降级**。
- 框架文档和源码在模块缓存里，写代码前先读对应章节，API 以这个版本的源码为准：
  `D=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)`，文档在 `$D/docs/ui/*.md`，示例在 `$D/examples/`，`go doc github.com/egoist/mygo/ui <Symbol>` 可查签名。
- 必读：`.agents/skills/mygo-maintenance/SKILL.md`（元素生命周期、Key、线程规则）。
- 网络：Go 模块代理已设为 goproxy.cn。GitHub 直连不稳，不要新增需要从 GitHub 直接拉取的依赖，尽量只用标准库和 mygo 自带的包。
- 测试服务器：以 Emby 为准，主测服务器凭据只走环境变量 `EMBY_TEST_SERVER` / `EMBY_TEST_USER` / `EMBY_TEST_PASS`，不进仓库。Jellyfin 演示站 `https://demo.jellyfin.org/stable`（用户 `demo`，空密码）只在没有凭据时兜底，已有的 Jellyfin 兼容代码保留但不再专门打磨。
- 运行：`go tool mygo dev`（开发）、`go build ./... && go vet ./...`、`go tool mygo vet .`、`go test ./...`。

## 2. 目录与归属

```
main.go                 入口，只调用 app.Run()
mygo.json               应用名 "MyGo Emby"，identifier dev.jackie.mygo-emby
internal/emby/          Emby/Jellyfin API 客户端，不依赖 ui
internal/player/        播放器，独立的包：依赖 emby 与 ui，不依赖 app
internal/remux/         本机转封装：远程 MKV/TS/hev1-MP4 → 127.0.0.1 上的 fMP4 VOD HLS
cmd/remuxdemo/          转封装的独立调试入口
cmd/playerdemo/         播放器的独立调试入口，登录演示站直接播放
internal/app/           应用状态与除播放器外的全部界面
  app.go                状态、窗口、启动、会话恢复、路由
  theme.go              调色板、字号、间距常量
  icons.go              SVG 图标
  kit.go                通用组件：卡片、按钮、分区标题、横向货架、骨架屏
  images.go             远程图片加载与缓存
  shell.go              侧边栏、顶部工具条、页面容器
  page_*.go             各页面
  play.go               播放入口 a.play，把 internal/player 接进窗口
  settings.go           设置持久化
  shots.go              截图自检
```

多个代理并行工作时，各自只改自己负责的文件。需要别人文件里的改动时，在最终报告里说明，不要直接改。

## 3. 视觉语言

参考 macOS 26 的 TV、Music 两个应用，以及 `mygo-music` 的实现手法。

- **窗口**：1280×820，最小 960×620，`TitleBarHidden`，红绿灯位置 (20,20) 嵌在侧边栏里，`StateKey: "main"`。
- **侧边栏**：宽 232，`VibrancySidebar` 材质，根节点透明。顶部留出红绿灯高度并可拖动窗口。内容从上到下：搜索入口、首页、收藏、媒体库分区（每个库一项，图标按类型）、底部用户头像与名字，点击弹出菜单（设置、切换服务器、退出登录）。
- **内容区**：不透明背景，浅色 `#ffffff`，深色 `#141416`。页面在下方滚动，顶部浮一条玻璃工具条（`plugins/glass`），含后退、前进、页面标题，右侧是页面自己的操作。
- **主题**：默认跟随系统，设置里可选浅色、深色。强调色跟随系统强调色。
- **字体**：系统字体。页面大标题 28 Bold，分区标题 20 Semibold，卡片标题 13 Medium，辅助文字 12 `TextMuted`。数字用 `tnum`。
- **圆角**：海报 10，横幅卡 12，按钮 8 或胶囊，面板 16。macOS 上是连续曲率，框架自动处理。
- **卡片**：竖版海报 2:3，横版 16:9。悬停时放大到 1.03，加阴影，中间浮出圆形播放按钮。已看完显示对勾角标，未看集数显示数字角标，有进度显示底部进度条。图片未到时显示骨架色块，到达后淡入。
- **动效**：用框架的 transition 和 spring，时长 150–300ms，尊重 Reduce Motion。
- **留白**：内容区左右内边距 32，货架之间 28，卡片间距 16。

禁止：渐变紫色背景、emoji 当图标、过度发光、网页感的边框堆叠。

## 4. 功能清单

1. **登录**：服务器地址、用户名、密码。地址自动补全协议和去尾斜杠，连接前先探测 `/System/Info/Public`。错误信息要具体。会话存本地，启动自动恢复。支持保存多个服务器并切换。
2. **首页**：顶部主打区轮播最近添加条目的背景图，含标题、简介、播放按钮。下面是继续观看（横版卡带进度）、接下来观看、每个库的最近添加货架。
3. **媒体库页**：虚拟化海报网格（`ui.GridView`），分页加载。排序：名称、加入时间、年份、评分，可切升降序。筛选：全部、未看、收藏。支持电影、剧集、合集、文件夹、音乐视频等类型，未知类型按通用文件夹浏览。
4. **详情页**：全宽背景图向下渐隐。左侧海报，右侧标题、年份、时长、分级、评分、类型标签、简介。按钮：播放或继续播放、从头播放、收藏、标记已看。版本、音轨、字幕下拉选择。剧集显示季切换和分集列表（缩略图、标题、简介、时长、进度）。下方演职员横排、相似推荐货架。
5. **人物页**：头像、简介、参演作品网格。
6. **搜索**：⌘F 聚焦，输入防抖 300ms，结果按电影、剧集、分集、人物分组。
7. **收藏页**：收藏的电影、剧集、分集分组展示。
8. **播放器**：覆盖整个窗口的黑色层。底部玻璃控制条：播放暂停、快退快进 10 秒、进度条带缓冲与时间、音量、倍速、音轨、字幕、下一集、全屏。3 秒无操作隐藏控制条与光标。快捷键：空格、左右方向键、上下调音量、F 全屏、M 静音、Esc 退出。向服务器上报开始、进度、停止，支持断点续播，播完自动下一集。
9. **右键菜单**：卡片上提供播放、标记已看或未看、收藏或取消收藏。
10. **设置**：外观、最高码率、默认字幕语言、默认音轨语言、自动下一集开关、缓存清理、关于。
11. **应用菜单与快捷键**：⌘[ ⌘] 后退前进，⌘F 搜索，⌘, 设置，⌘R 刷新。

## 5. 播放方案

`ui/video` 在 macOS 上是 AVPlayer。可直接播放的是 MP4、M4V、MOV 容器里的 H.264、HEVC，音频 AAC、AC3、EAC3、MP3、ALAC、FLAC，以及 HLS。

- 调 `POST /Items/{id}/PlaybackInfo`，带设备能力描述（DeviceProfile）：直放上述组合，其余走 HLS 转码或转封装。
- 能直放就用静态流地址，否则用返回的 `TranscodingUrl`。令牌放在查询参数 `api_key` 里，因为播放器无法加请求头。
- 服务器不能转码时（主测的 Emby 就是这样），只要编码是 H.264/HEVC 加 AAC/AC3/EAC3，就交给 `internal/remux` 在本机转封装：纯 Go、进程内，按关键帧索引用 HTTP Range 随机读取，现读现封装成 fMP4 分片，不重新编码，不落盘。拖动就是 AVPlayer 请求另一个分片。HEVC 一律输出 hvc1 标签。
- 上述纯 Go 路径是默认快捷路径。服务器不能转码且实际选中的音轨是 DTS、TrueHD、Opus、FLAC 等不能写入 fMP4 的格式时，可按需调用本机 FFmpeg，仅把该音轨按分片转为 AAC-LC 48 kHz 双声道；视频仍 stream-copy，保留 HDR/DV 标记，分片经内存管道传递并随取消/关闭清理，不写整片或长时磁盘缓存。只有确实需要时才启动，缺失时明确提示，绝不静默丢音；详见 `docs/media-compatibility.md`。
- 每个视频帧都会让窗口重建一次视图，所以播放器打开时根视图只建播放器层，控制条隐藏时只剩画面和字幕，热路径不分配内存。
- `VideoPlayer` 没有音轨切换接口。切换非默认音轨时带 `AudioStreamIndex` 重新请求播放信息并强制走服务器流，重开播放器后跳回原位置。
- 字幕：外挂文本字幕通过服务器取 VTT；内嵌文本字幕由 `internal/remux` 在读分片时解出，陆续推给播放器；都由客户端在画面上叠加渲染。图形字幕（PGS、VobSub）在能转码的服务器上请求烧录，不能转码时暂不支持。
- macOS 电影播放通过 AVPlayerLayer 原生视图显示，保留系统 HDR/EDR 与 Dolby Vision 处理，不经过 MyGo 的 8-bit NV12/SDR shader；UI 控制条和字幕叠加在其上。其他平台仍走 `ui/video`。实现、依赖补丁和验收命令见 `docs/native-video.md`。
- 续播：打开后 `Seek` 到上次位置。

## 6. 工程约定

- 视图函数每帧都会跑，里面不做 IO，不创建位图。耗时操作放 goroutine，结果用 `win.Update` 回主线程。
- 过期结果要丢弃：页面切换后迟到的响应不能覆盖新页面的状态。
- 列表元素一律给稳定 `Key`。
- 图片统一走 `images.go` 的加载器：内存 LRU 加磁盘缓存，并发上限 8，向服务器按显示尺寸乘以屏幕缩放请求缩略图。
- 文案中文为主，集中放一处，便于以后加英文。
- 每个包写必要的测试。界面用 `ui.NewTester` 做无窗口测试。
- **截图自检**：环境变量 `EMBY_SHOTS=<目录>` 时，应用自动登录演示服务器，依次走过主要页面，用 `Window.CapturePage()` 存 PNG 后退出。改完界面必须跑一遍并亲眼看图，确认没有错位、溢出、空白。参考 `mygo-music` 的 `internal/app/debug_shots.go`。
