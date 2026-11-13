# 交接说明

日期：2026-11-11。目标：用 MyGo 原生 UI 写一个 Emby 桌面客户端，界面美观，功能齐全，macOS 原生观感。设计规范见 `DESIGN.md`，以它为准。

## 分工约定（2026-11-11 晚起改为单代理）

- 三个 Opus 子代理并行约一小时就耗尽 5 小时额度，之后**改为单个代理按顺序做**，不再开子代理。
- 界面把关由 Jackie 本人来做。代理负责把应用跑起来、跑截图自检，不替 Jackie 下界面结论。
- 每完成一项就更新对应的 `.agents/reports/*.md`，防止中途断掉丢进度。
- 原来按代理划分的文件归属（app / player / remux）已不再需要，但三个包的依赖方向保持：`remux`、`player` 不依赖 `app`。

## 环境现状

- Go 1.27.4 已用 Homebrew 安装。`GOPROXY` 已全局设为 `https://goproxy.cn,direct`，因为直连 GitHub 不稳。
- MyGo 固定在 `v0.8.4-0.20261111053122-9171337c021b`。已发布的 v0.8.3 没有 `ui/video`，不能用。
- 框架文档在模块缓存：`$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)/docs/ui/`。
- 参考仓库克隆在会话临时目录，可能已被清理，需要时重新克隆 `egoist/mygo-music`、`egoist/mygo`、`AlanHJ/qEmby`。
- 已 `git init`，第一批代码已提交（2026-11-11，初始提交）。

## 测试服务器

以 Emby 为准，Jellyfin 只兜底。

- Emby 4.9.3：测试服务器地址与用户名只通过环境变量 `EMBY_TEST_SERVER` / `EMBY_TEST_USER` / `EMBY_TEST_PASS` 提供，不写入仓库或对话记录。这是别人的真实服务器，尽量只读，改了收藏或已看标记要恢复，不要触发长时间转码。
- Jellyfin 演示站：`https://demo.jellyfin.org/stable`，用户 `demo`，密码为空。

两台都已验证可以登录。

## 进度（2026-11-11 15:05 核对）

第一批两个代理并非"只读了文档"：14:04–14:58 的 54 分钟里它们把两个包都写完了，只是回传报告时撞上 5 小时限额。

1. `internal/emby/`：19 个文件、5,088 行（13 个源文件 3,352 行 + 6 个测试 1,736 行）。登录、浏览、搜索、收藏、播放信息、进度上报、字幕解析都在。
2. `internal/app/` + `main.go`：15 个文件、4,078 行（11 个源文件 3,402 行 + 3 个测试 662 行 + `main.go` 14 行）。窗口与侧边栏、路由、主题、图标、图片缓存、组件库、截图自检、设置都在。

已核对：

- `go build ./...`、`go vet ./...`、`go test ./...` 全绿；47 个测试通过；`gofmt -l .` 无输出。
- 窗口能开：子代理用 `EMBY_SHOTS` 跑过真窗口，浅色、深色各一轮，共 35 张截图。
- 两半还没接上：`internal/app` 没有 import `internal/emby`；首页仍是组件库（gallery），其余页面是占位；没有登录页，也没有播放器。

### A. Build Emby API client（`internal/emby/`）

时间线：

- 14:04–14:22 读 `DESIGN.md`，用 curl 在 scratchpad 里把两台服务器摸了一遍：登录、Views、Resume、Latest、Search、详情、字幕、PlaybackInfo。
- 14:18–14:38 写包：`doc.go`、`errors.go`、`client.go`、`auth.go`、`models.go`、`item.go`、`images.go`、`items.go`、`profile.go`、`playback.go`、`reporting.go`、`subtitles.go`。
- 14:34–14:45 写单元测试（`client/item/subtitles/playback_test.go`）和在线测试（`live_test.go`），对 Jellyfin 演示站和 Emby 服务器实跑。
- 14:40 发现"开始播放"上报会把 Emby 条目的 PlayCount 从 0 加到 1，当场用"标记未看"还原成 0。
- 14:41–14:52 撞到一串服务器怪癖并逐个处理（见下）。
- 14:54–14:56 `go build`、`go vet`、gofmt、全仓库凭据扫描，全清；`go.mod` 未动，无第三方依赖。
- 14:56–14:58 两台在线测试复跑全绿，撤销早期手工 curl 留下的令牌，清理 scratchpad，写报告。

它最重要的发现——**这台 "Emby 4.9.3" 不是标准 Emby，是一个模拟 Emby 接口的网盘代理**：

- 完全不能转码（所有片源 `SupportsTranscoding:false`）。`master.m3u8` 只是指向原始 MKV 的 CDN 链接，不是真 HLS。最近添加的 60 部里 59 部是 MKV 或 AVPlayer 不支持的格式，**在 AVPlayer 上基本放不了**。客户端会把 `Playback.NativelyPlayable` 设为 false，界面应提示而不是放黑屏。
- 内嵌字幕一律返回空 VTT（只有 `WEBVTT` 头）；PGS 被错标成文本字幕，客户端改为按编码判断。
- 演员的 `Type` 是 `"Other"`，所以 `Cast()` 改成"除已知幕后职位外都算演员"。
- 按人物筛选（`PersonIds`）返回 HTTP 500，按名字筛选被静默忽略；"相似推荐"为空。
- `PublicInfo` 里没有 `ProductName`，缺省一律当 Emby。
- 视频流会 302 跳到对象存储，`Content-Type` 是 `application/octet-stream`。

两类服务器的差异及处理：

- **鉴权正好相反。** Jellyfin 12.4 关掉了旧式鉴权：只认 `Authorization: MediaBrowser …` 和查询参数 `ApiKey`。Emby 认 `api_key`，不认 `ApiKey`。处理：请求同时带三个头，播放和字幕地址同时带两个查询参数，两边都实测可用。
- Jellyfin 不返回 `DirectStreamUrl`，客户端自己拼 `/Videos/{id}/stream.{容器}?Static=true…`；Emby 直接用服务器返回的地址并换上当前令牌。
- 两边返回的地址都不带路径前缀（`/stable`、`/emby`），客户端统一补 base 且不重复拼。
- Jellyfin 把 mp4 报成 `mov`；旧的 `/Users/{uid}/…` 路由在 12.4 上仍可用，两边共用一套代码。

导出 API（摘自它的报告，完整签名看源码）：

```go
// 会话
NewDeviceID() string; NormalizeServerURL(raw) (string, error)
NewClient(url, DeviceInfo{Client,Device,DeviceID,Version}, ...Option) (*Client, error)
RestoreClient(Session, DeviceInfo, ...Option) (*Client, error); WithHTTPClient(*http.Client) Option
Session{ServerURL,ServerID,ServerName,ServerVersion,ServerKind,UserID,UserName,UserImageTag,Token} // JSON 标签，可直接持久化
c.PublicInfo(ctx) (*PublicInfo, error)          // .Kind() 返回 "emby"/"jellyfin"
c.Login(ctx,u,p) (Session, error); c.Logout(ctx) error; c.CurrentUser(ctx) (*User, error)
c.Session()/SetSession/LoggedIn/UserID/Token/IsJellyfin/BaseURL
// 错误：*Error{Kind,Op,StatusCode,Message,Err}; KindOf(err); StatusCode(err)
// errors.Is: ErrUnreachable ErrUnauthorized ErrForbidden ErrNotFound ErrHTTP ErrBadResponse
//             ErrNotLoggedIn ErrInvalidURL ErrNotPlayable；ctx 取消原样返回 ctx.Err()
// 浏览
c.Views(ctx); c.ResumeItems(ctx,limit); c.NextUp(ctx,seriesID,limit)
c.Latest(ctx,parentID,limit); c.Items(ctx, ItemQuery) (*ItemsResult, error); c.Item(ctx,id)
c.Seasons(ctx,seriesID); c.Episodes(ctx,seriesID,seasonID); c.NextEpisode(ctx,*Item)
c.Similar(ctx,id,limit); c.PersonItems(ctx,personID,start,limit); c.Favorites(ctx,types...)
c.Search(ctx,term,limitPerGroup) (*SearchResults{Movies,Series,Episodes,People,BoxSets}, error)
// 用户数据
c.SetFavorite(ctx,id,bool); c.SetPlayed(ctx,id,bool) (*UserData, error)
// 图片（只拼 URL，不联网、不带令牌）
c.ImageURL(id, ImagePrimary|Backdrop|Thumb|Logo|Banner…, ImageOptions{…})
c.UserImageURL(uid,tag,opts); c.PersonImageURL(Person,opts)
it.Image(RolePoster|RoleLandscape|RoleBackdrop|RoleLogo|RoleBanner|RolePrimary) (ImageRef, bool)
// 播放
c.PlaybackInfo(ctx,id, PlaybackOptions{MediaSourceID,AudioStreamIndex,SubtitleStreamIndex,
    StartTimeTicks,MaxStreamingBitrate,ForceTranscode,Profile}) (*Playback, error)
Index(i) *int; AVPlayerProfile(maxBitrate) DeviceProfile; c.SubtitleURL(id,msID,idx)
Playback{…, Method(DirectPlay|DirectStream|Transcode), StreamURL, IsHLS,
    NativelyPlayable, TranscodeUnavailable, AudioTracks, SubtitleTracks, Source, MediaSources}
    .AudioTrack(i) .SubtitleTrack(i) .Report(pos,paused)
Track{Index,Type,Codec,Language,Title,DisplayTitle,IsDefault,IsForced,IsExternal,IsText,
    NeedsBurnIn,Channels,DeliveryMethod,URL}.Label()
// 上报与字幕
c.ReportPlaybackStart/Progress/Stopped(ctx, PlaybackReport); c.StopTranscoding(ctx, playSessionID)
ParseSubtitles([]byte) (*Subtitles, error); NewSubtitles([]Cue)
s.At(t) []Cue; s.TextAt(t); s.Len(); c.FetchSubtitles(ctx, url)
// 条目工具
TicksToDuration; DurationToTicks; FormatRuntime; FormatClock
it.Runtime/RuntimeText/ResumePosition/CanResume/PlayedPercentage/Progress/Remaining
it.IsPlayed/IsFavorite/UnplayedCount/IsFolderLike/IsPlayable/EpisodeLabel/DisplayTitle
it.Year/YearText/RatingText/Cast/Crew/Directors/StudioNames/Tagline
```

验证情况：

- 两台都在线验证：地址探测、错密码映射 401、登录、恢复会话、登出，媒体库、继续观看、接下来观看、最近添加、分页加筛选、电影详情，海报/背景/人物/剧集图片返回 200 且类型正确，季、集、下一集（最后一集返回 nil），搜索（含人物），收藏切换后立即还原，三种播放上报，停止转码。直放地址用不带请求头的客户端取前几字节，嗅探出 mp4 / mkv。
- 只在 Jellyfin 上在线验证：HLS 转码（主播放列表、变体、首个 TS 分片字节 0x47）、强制转码、已看标记、人物作品列表。
- 只有单元测试：标准 Emby 的 `TranscodingUrl` / 字幕 `DeliveryUrl`（线上没见到真实样本）、真实内容字幕解析（两台都没有可用字幕流）、`NextEpisode` 的 `AdjacentTo` 兜底、403/500/HTML 错误映射、标准 Emby 是否接受我们的设备能力描述。

已知缺口：HLS 只用 ts + H.264，HEVC 的 MKV 会在服务器整片转 H.264，开销大；ASS 样式和 VTT 定位信息被丢掉，只留文字；`FormatRuntime` 输出 `"1h 49m"`，中文文案要界面自己拼。

在线测试跑法（密码走环境变量，别写进仓库）：

```sh
EMBY_LIVE=1 EMBY_TEST_SERVER=… EMBY_TEST_USER=… EMBY_TEST_PASS=… \
  go test ./internal/emby -run TestLive -v
# 加 EMBY_LIVE_ONLY=jellyfin-demo 或 emby 只跑一台；每次跑完都会登出
```

### B. Build UI foundation shell（`internal/app/` + `main.go`）

时间线：

- 14:04–14:10 读 `DESIGN.md`、`SKILL.md`、MyGo 的 20 余篇 UI 文档（views/layout/styling/windows/navigation/image/scroll/grid-view/list/sidebar/text/text-input/search-field/input/transitions/motion/popover/context-menu/menu-button/toast/testing/custom-widgets）、`plugins/glass.md`、`menus.md`。
- 14:10–14:16 读 mygo-music 参考实现（`app.go`、`ui_main.go`、`ui_kit.go`、`artwork.go`、`debug_shots.go`、`menu.go`、`settings.go`），并用 `go doc` 逐个核对 `Router`、`GridView`、`ScrollWith`、`Theme`、`WindowOptions` 等签名。
- 14:16–14:18 向 Jellyfin 演示站取真实样例数据与图片 URL（`auth.json`、`items.json`、`eps.json`），供组件库使用。
- 14:19–14:28 先写了一个一次性的 `zz_exp_test.go` 做路由 / 网格 / 侧边栏实验，确定方案后删掉——这解释了为什么第一个正式文件到 14:29 才出现。
- 14:29–14:43 依次写：`strings.go`、`theme.go`、`icons.go`、`images.go`、`settings.go`、`app.go`、`routes.go`、`shell.go`、`kit.go`、`page_gallery.go`（含截图步骤）。
- 14:43–14:47 构建二进制，浅色 `EMBY_SHOTS` 跑第一轮，逐张看 11 张截图。
- 14:48–14:51 修完问题跑第二轮（深色 + 浅色），又看 9 张。
- 14:52–14:56 写 `app_test.go`、`images_test.go`、`settings_test.go`；修未用 import、flaky 等待、gofmt；全量验证通过。
- 14:57–14:58 额外验证 focus ring 渲染，删掉临时渲染测试，最后一次 `go build ./... && go vet ./internal/app/` 返回 ok（14:58:24）。
- 14:58:45 429，报告没写。

它交付的东西：

- **路由**用 `ui.Router`，不是自建栈：返回时页面保留滚动位置和焦点，页面之间淡入/滑入，`⌘[`、`⌘]`、鼠标侧键、双指滑动都免费得到。所有页面在 `routes.go` 的 `pages` 一个 switch 里注册；加页面 = 加一个 `page_<name>.go`，实现 `func (a *app) namePage(c *ui.Context, r *ui.Route)`，再在 switch 加一个 case（`page_home.go` 的注释已经写明）。
- **窗口**：1280×820、最小 960×620、隐藏标题栏、红绿灯内缩到 (20,20)、侧边栏 vibrancy、`stateKey="main"`。
- **组件库**（`kit.go`，全部是 `*app` 的方法）：`posterCard` / `landscapeCard`（2:3 与 16:9，悬停放大 1.03 + 阴影 + 居中播放键，进度条、已看角标、未看计数、收藏标、骨架屏与淡入、单独回调的点击与播放点击）、`shelf`（横向滚动 + 悬停时左右翻页箭头）、`sectionTitle`、`pillButton`（primary/secondary）、`iconButton`、`chip`、`filterChip`、`metaRow`、`emptyState`、`errorState`、`loadingState`、`skeletonShelf`、`skeletonGrid`、`posterGrid`（基于 `ui.GridView`，按宽度算列数，滚到末尾回调 `OnNearEnd`）。
- **图片缓存**（`images.go`）：`a.img.Get(url) *ui.Bitmap`，加载中返回 nil 并在到达时触发重绘；内存 LRU 按字节计、磁盘缓存按 URL 哈希放在用户 cache 目录、最多 8 个并发、失败记住不重试、带加载时间戳供淡入；`Prefetch`、`ForgetFailures`、`DiskUsage`、`TrimDisk`、`ClearDisk`。它只认 `*http.Client`，不知道 Emby。
- **截图自检**（`shots.go`）：`EMBY_SHOTS=<dir>` 开窗、按序执行步骤、`Window.CapturePage()` 存 PNG、退出；`EMBY_SHOTS_DARK=1/0` 强制深/浅色，文件名带后缀；页面用 `addShots(order, steps...)` 追加（gallery 用 10–19）。
- **设置**（`settings.go`）：`Settings` 已预留 `Servers` 和 `Session` 两个 `json.RawMessage` 槽位，主题、码率、字幕/音频语言、自动连播、音量都在；后台 goroutine 原子写入，不阻塞 UI。
- **组件库页**：`/dev/gallery`，同时暂代首页；用 Jellyfin 演示站的真实海报，展示所有组件与状态。`/library/{id}` 有一个带网格的占位页。

验证：`go build ./... && go vet ./... && go tool mygo vet . && go test ./...` 通过，`gofmt` 干净；`ui.NewTester` 覆盖导航 push/back/forward、侧边栏跟随路由与搜索、滚动位置保留、栏标题淡入、控件命名、深色与最小尺寸、网格列数与加载更多；图片缓存用 `httptest` 覆盖去重、磁盘、失败记忆、瞬时失败重试、8 并发上限、LRU 淘汰；设置覆盖往返、损坏文件、无路径。

截图（浅色 r1、深色+浅色 r2，共 35 张）：

```
the session-local scratchpad directory
```

r2 有：`gallery-top`、`gallery-scrolled`、`gallery-middle`、`gallery-states`、`gallery-bottom`、`card-hover`、`library-grid`、`library-grid-scrolled`、`min-size`、`min-size-scrolled`、`wide`，各带 `-light` / `-dark`。要留档就先拷出来，scratchpad 会被系统清理。

### C. 清理与注意

- `go.mod` 被 UI 代理的实验改过：`github.com/egoist/mygo` 从 indirect 提升为直接依赖，加入 `go-text/typesetting`、`golang.org/x/image`，掉了 `golang.org/x/net`。`go mod tidy` 会把 `x/net` 加回来（当前 `go mod tidy -diff` 只差这一项）。接手时跑一次 tidy 即可，不影响构建。
- 参考仓库、截图、二进制、样例数据都在同一个 scratchpad：
  The session-local scratchpad directory
  （`refs/` 是 mygo-music 等参考仓库，`mygo-emby-bin` 是构建产物，`demo/auth.json` 有 Jellyfin 演示站的 AccessToken，`run1.log`/`run2.log` 是空的。）
- API 代理最后已登出两台服务器并撤销早期手工 curl 的令牌；仓库里凭据扫描为空。
- **转录 jsonl 里有 Emby 测试账号的密码明文**（在它跑在线测试的命令行里）。jsonl 在仓库外，但不要提交、不要外传、不要贴进 issue。
- 两个代理都按约定没碰对方的目录；`internal/app` 至今没有 import `internal/emby`。

## 下一步（第一批写的，已过时，以「单代理接手顺序」为准）

1. 把 `internal/emby` 接进 `internal/app`（`Settings.Session` 槽位已经留好），做登录页与会话恢复，把 gallery 换成真首页；先让 `go run .` 打开就能登录、浏览、看到真海报。
2. 第二批并行两个代理：一个做登录、首页、媒体库、详情、人物、搜索、收藏、设置各页面，一个做播放器。功能清单见 `DESIGN.md` 第 4 节，播放方案见第 5 节。
3. 每批完成后启动应用，由 Jackie 提修改意见，再转给代理。

## 第二批（2026-11-11 下午，额度将尽时紧急收尾）

方针变化：只用 Emby（主测服务器凭据走环境变量，不进仓库），Jellyfin 只兜底。抽样 100 部：MKV 56%、TS 7%、部分 MP4 是 hev1，AVPlayer 都放不了，服务器又不能转码；但编码全是 H.264/HEVC + AAC/AC3/EAC3，所以决定**纯 Go 本机转封装**（`internal/remux`，127.0.0.1 上的 fMP4 VOD HLS，按关键帧索引 Range 随机读，不转码不落盘）。播放器改为独立包 `internal/player`。性能约束：MyGo 每个视频帧都会整窗重建视图，播放时根视图只建播放器层。这些都已写进 `DESIGN.md`。

三个 Opus 子代理并行，各自报告在 `.agents/reports/`（已 gitignore，**接手先读这三份**）：

1. 界面接线（`app-integration.md`）：**第一步完成**。登录（探测、8 类错误）、会话恢复与多服务器切换、侧边栏真数据、真首页（轮播、继续观看、接下来观看、各库最近添加）、媒体库网格（排序筛选分页、文件夹下钻）、详情占位、`a.play` 占位。`go test -race` 全绿。截图 `build/shots/integration/`。第二步 11 项（修首页偶发卡在骨架屏/一次 5 分钟无响应、缺图卡片回退、令牌改存钥匙串、库排序筛选持久化、完整详情页、人物页、搜索、收藏页、设置页、右键菜单、快捷键）**未开始**，清单原样在报告里。
2. 播放器（`player.md`）：`internal/player` 按契约写完（Config/Result/New/Play/Active/View/Close，另有 Flush/Status/Debug），玻璃控制条、快捷键、3 秒自动隐藏、错误面板、下一集倒计时、方向键 400ms 合并、后台上报都有；流来源分直放/转封装/无法播放三类，remux 经 `ServerSource.Fallback` 接入；字幕支持 `CueFeed` 增量 cue。emby 档案：文本字幕全 External，hev1 不再算可直放。真窗口验证：Emby「2014」MP4 直放约 12s 就绪并前进；UserData 五次均还原并登出。绕过两处 MyGo 问题：首次建 Video 在主线程同步等 AVFoundation 读轨道（冻 21s，已改为拿到时长后再建）；就绪前 Seek 有声无画（改为静音遮挡从 0 起、首帧后 Seek，**未测**）。**未做**：player 单元测试与 NewTester 测试、截图、`MYGO_FRAME_STATS` 实测、Router 状态保留实验、切音轨测试。`build/logs/` 含令牌，勿外传。
3. 本机转封装（`remux.md`）：16:23 撞 429 中断。已写约 5,800 行底层（EBML/MKV、MP4、TS 解封装，H.264/H.265 SPS 与 avcC/hvcC，AAC/AC3/EAC3 参数盒，fMP4 封装与分片器，Range 读取含 302 缓存与签名过期重解析，无索引二分查找），能编译，未测试。未做：本机 HTTP 服务与播放列表、字幕、直放/转封装判断、测试、`cmd/remuxdemo`、真机验证。发现：Emby 元数据不可靠（标 mkv 的可能是 MP4，标 mpeg 的是 ISO），必须按魔数嗅探；对象存储签名 15 分钟过期。

19:30 额度恢复后三个代理已按各自报告继续：remux 补完服务与验证，player 补测试与性能数据并对接 remux，app 开始第二步。

收尾时未提交 git。下一步：读三份报告 → 修首页卡死 → 把 remux 接进 player、player 接进 `a.play` → 第二步页面。

## 单代理接手顺序（第二批收尾时写）

先读 `DESIGN.md` 和 `.agents/reports/` 下三份报告（`app-integration.md`、`player.md`、`remux.md`），报告里各有按优先级排好的未完成清单。建议顺序：

1. ~~修首页卡在骨架屏~~：已修（窗口被遮挡时不出帧，而数据只在建视图时才开始加载；改为跳转、登录、⌘R 时发起），有复现测试。
2. `internal/remux` 已基本完成并真机验证：MKV H.264、MKV HEVC 4K Dolby Vision 5 + EAC3 + SRT 字幕、hev1 MOV、无索引 TS 四类在 Emby 上都能播，首帧 1.4–8s（多为 Emby 302 冷启动），Seek 恢复 0.7–1.7s，remux 自身 CPU 约 0.7% 单核。未打包二进制 AVPlayer 能放 127.0.0.1。**剩下**（见 `remux.md` 末尾）：TS 冗余读取（上游 1.8 倍）、HDR10 与 ASS 字幕片未跑、稳态流量未测、打包 .app 的 ATS 未验证、4K 时 Go 堆偏大。
3. 播放器收尾：续播方案已验证（静音加海报遮挡从 0 起，首帧后 Seek，在 Emby「2014」上 30:00 准确揭开）；帧统计 1080p 约 47fps 时 build+layout 中位 0.4–0.5ms，进程 CPU 15–24%，无 GC，暂停不出帧；测试含 `-race` 通过。**剩下**：接 remux、跑 `PLAYER_SHOTS` 截图（从没跑过）、减少原生调用后的帧统计复测。
4. 按 `player.md` 的接入步骤把播放器接进 `internal/app/play.go`，播放时根视图只建播放器层。**注意 Router 实验结论**：Router 哪怕一帧不建，页面滚动位置就丢；隐藏着建又每帧整树重建。正确做法是各页面（含货架横向滚动）的 `ui.ScrollState` 放在 app 数据里并用 `TrackScroll` 绑定，实测 300 帧、10 分钟后位置仍在。
5. ~~第二步其余页面~~：代码与测试已完成（缺图回退、令牌进钥匙串、排序筛选持久化、完整详情页、人物、搜索、收藏、设置、右键菜单、快捷键），**截图没看过**：`build/shots/pages/emby-light/` 只拍到 17 张，有 3 次等待超时未查，深色未跑。`go run .` 重编译后 macOS 可能弹窗问钥匙串权限。
6. 每批完成后启动应用给 Jackie 看。改动一直没提交 git，接手后可先提交一次作为检查点（先问 Jackie）。

## 主会话直接接手（2026-11-11 19:45 起，单代理，不再开子代理）

已完成并通过 `go build/vet/test ./...`：

- **remux 接进 player**：`internal/player/remux.go` 的 `RemuxFallback(srv)`，按 `remux.Decide` 分直放/转封装/不支持；内嵌字幕按轨道建 `CueFeed`；`Stream.Playlist` 标记 HLS。
- **两个首帧 bug**（真窗口实测确认）：
  1. MyGo 的视频引擎只在状态变化时唤醒窗口，而状态要等建帧时取到第一张画面才变，没人请求帧就永远取不到首帧。播放器在首帧前每 16ms 请求一帧（`firstPicturePoll`）。之前「CDN 先出声十几秒才出画面」很可能就是它。
  2. HLS 流跳过「等 AVPlayer 报时长再建 Video」的闸门（HLS 读轨道不会卡主线程，且时长要建了 Video 才出）。
  修复后 4K 杜比视界 MKV（`itm_73a4e9a58a4ce6f65ef17528`）经 playerdemo 在打开流后约 2 秒出画面，位置与墙钟同步。
- **player 接进 app**：`play.go` 懒建 player 与 remux 服务，`PlayWith` 透传版本/音轨/字幕选择；根视图在播放器 Active 时只建播放器；`OnExit` 存音量并 `refresh()`；换账号和退出时关播放器并 `Flush` 停止上报。
- **页面状态移到 app**：`pagemem.go` 按路径存滚动、网格、货架位置（Router 一帧不建就丢元素状态）。测试 `TestPagesKeepScrollWhileNotBuilt` 已确认去掉修复会失败。
- **截图自检**：`EMBY_SHOTS_FROM=<order>` 只跑登录和该序号之后的组；`EMBY_SHOTS_PLAY=<条目>` 在 app 内播放该条目并截图，前后记录并还原 UserData。截图和 playerdemo 窗口改为置顶。
- **remux 头部缓存**：打开时读过的文件开头（≤8 MB）留在内存供首批分片用，TS 上游放大 1.57 → 1.36 倍，详见 `remux.md` 末尾，含下一步「读者停放」的设计。
- **打包**：`mygo.json` 加了 `NSAllowsLocalNetworking`，`go tool mygo build` 后 Info.plist 里已有该键。

**卡在显示器上**：20:01 起屏幕锁定、显示器关闭，MyGo 不出帧，app 内播放截图（`EMBY_SHOTS_PLAY`）停在加载转圈，item 页也会停在半截过渡。这不是代码问题。Jackie 回来后先跑：

```sh
EMBY_SHOTS=build/shots/play EMBY_SHOTS_FROM=90 EMBY_SHOTS_PLAY=itm_73a4e9a58a4ce6f65ef17528 \
EMBY_SHOTS_SERVER=… EMBY_SHOTS_USER=… EMBY_SHOTS_PASS=… ./build/mygo-emby-app
```

确认 app 内能出画面，再打开打包的 `build/darwin-arm64/MyGo Emby.app` 手动点播一部 MKV，确认 ATS 不拦。之后按上面清单继续：截图过一遍界面，remux 的 TS 冗余读取与内存。

## 已知限制

- 播放用系统 AVPlayer，只能直放 MP4、M4V、MOV，其余格式靠服务器转 HLS。
- `VideoPlayer` 没有音轨切换接口，切音轨要让服务器重新出流。
