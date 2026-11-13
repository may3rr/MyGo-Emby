# 《善地》字幕菜单缺少真实轨的诊断与修复

日期：2026-11-12

范围：右下角字幕菜单看不到真实 MKV 字幕轨、无法选择其他语言。只处理服务器缺少字幕 `MediaStreams` 时，本地 remux 已枚举真实轨却没有接进播放器菜单的问题。没有改变音画同步链路，没有触发播放上报，没有请求长时间转码。

## 真实媒体结论

现有服务器记录的续播条目是《善地》`S1:E2 · 飞翔`（`itm_f236a609def697a7e343cc06`）。

- 默认媒体版本 `ms_itm_f236a609def697a7e343cc06` 报告两条字幕：
  - index 2：英文 SubRip，`language=eng`，`title=English`。
  - index 3：英文 PGS/SDH，`language=eng`，`title=SDH`。
- ffprobe 和 remux 对该实际 MKV 的枚举同样是英文 SubRip、英文 PGS。
- 实际 MKV 没有中文或其他第三语言字幕；没有服务器外置字幕记录。
- 同一集版本 `ms_itm_6a243aa35f757f403948ca52` 返回 `MediaStreams=0`，但 remux 仍能按容器顺序识别英文 SubRip 和英文 PGS。这个版本复现了“真实轨存在、服务器字幕列表为空”的接线缺口。
- 2026-11-12 的只读复测仍得到相同结论。该版本的直取 Range 返回 `403 Forbidden`，但 remux 头解析仍列出上述两条英文字幕；这属于服务器代理/媒体版本的独立问题，不用于推断存在新语言。

因此，本次修复只能让真实存在的英文轨重新进入菜单。不能、也不会合成中文等不存在的语言。

## 已实现行为

修复采用显式的本地字幕描述，不把任意负数当作服务器轨：

1. `RemuxFallback` 按 `sess.Tracks` 的字幕顺序生成稳定的播放器内部 ID，从 `-2` 开始；`-1` 仍只表示关闭。
2. 每个本地轨同时保存它在 remux cue feed 中的索引。服务器缺元数据时，该 feed 索引按字幕顺序为 `-1-n`；选择本地轨道时用这个索引读取 `Embedded` feed。
3. `subtitleChoices` 只在 PlaybackInfo 没有任何字幕轨时启用本地后备。服务器已有字幕列表时继续以服务器列表为准，因此完整元数据不会重复，外置字幕的有效 server index、URL 和类型不会被替换。
4. `pickSubtitle`、`trackByIndex`、初选、预取、手动切换和关闭都识别已注册的本地 ID；不存在的负数仍解析不到轨道。
5. 本地文本轨用 remux feed 显示文字。本地 PGS 保留在菜单中，但始终禁用并显示不可呈现原因，不会请求服务器 burn-in。
6. Playback 上报只在选择服务器轨或关闭时发送索引。选择本地合成轨时省略 `SubtitleStreamIndex`；关闭仍上报 `-1`。
7. 服务器没有 `MediaStreams` 时，`RemuxFallback` 允许 session 在探测到不可复制音轨后启动可选 FFmpeg 音频解码。该许可只打开后备开关；AAC、AC3、E-AC3 等可复制音轨仍不会启动 FFmpeg。这保证《善地》的 DTS 版本能越过音轨创建阶段并进入字幕菜单。

相关位置：

- `internal/player/source.go:86`：本地字幕描述与有效轨选择。
- `internal/player/remux.go:34`：缺服务器流信息时允许探测后的本地 DTS 音频转码。
- `internal/player/remux.go:98`：从 `sess.Tracks` 生成稳定 ID 和 feed 索引。
- `internal/player/player.go:357`：初选、预取和 resolved 轨道列表接线。
- `internal/player/player.go:631`：防止合成索引进入服务器上报。
- `internal/player/player.go:773`：本地字幕切换。
- `internal/player/tracks.go:109`：按语言初选本地文本轨。
- `internal/player/tracks.go:149`：仅 `-1` 固定为关闭。
- `internal/player/view.go:633`：菜单列出并禁用本地 PGS。

## 回归测试

新增独立测试文件 `internal/player/subtitle_fallback_test.go`，使用 `ui.NewTester` 驱动真实菜单。覆盖：

- `MediaStreams` 为空时，真实 MKV ASS 轨进入菜单，选择后显示实际 feed 文字。
- 临时封装两条真实 SRT 的中文/英文 MKV，依次选择两种语言，再关闭；旧语言 cue 不残留。
- 临时封装真实 HEVC+DTS+SRT 的缺失元数据 MKV，验证没有自动关闭音轨、没有以 `ErrUnsupportedAudio` 提前失败，并抓取第一个短片段确认输出 AAC。
- 服务器元数据完整时不追加本地轨；外置字幕 server ID 和 URL 保留。
- 本地 PGS 在菜单中列出但禁用，直接选择也不触发服务器 burn-in 或重新打开流。
- 合成 ID 稳定、唯一；关闭固定为 `-1`；上报中不出现任何小于 `-1` 的索引。

定向运行：

```sh
CGO_ENABLED=0 go test ./internal/player \
  -run 'Test(SubtitleFallback|RemuxFallbackTwoRealSubtitles|RemuxFallbackMissingMetadataDTS|LocalSubtitleMenu|CompleteServerSubtitles|LocalPGS|PickSubtitleUses)' \
  -count=1 -v
```

完整播放器包：

```sh
CGO_ENABLED=0 go test ./internal/player -skip 'Native' -count=1
```

修复前同一核心用例的红色证据是：

```text
subtitle tracks: []
FAIL TestSubtitleFallbackFromRemuxAppearsInMenu
```

DTS 接线用例在修复前也先在菜单前失败：

```text
missing metadata plus DTS failed before the subtitle menu
FAIL TestRemuxFallbackMissingMetadataDTSReachesSubtitles
```

修复后定向测试和排除原生播放器测试的 `./internal/player` 完整包全部通过。

## 《善地》只读验证

使用本机现有设置和 macOS Keychain 会话：

```sh
MYGO_GOODPLACE_LIVE=1 go test ./internal/app \
  -run TestGoodPlaceSubtitleMetadataDiagnostic -count=1 -v -timeout=90s
```

该测试只请求搜索、PlaybackInfo、文件头 Range、ffprobe 和本地 remux 头解析；日志不输出 token、密码或鉴权 URL。2026-11-12 复测通过，并再次确认实际可恢复的字幕只有英文 SubRip 和英文 PGS。

真实 DTS 接线也提供了独立的只读短段验收。它打开缺 `MediaStreams` 的《善地》代理版本，经 `RemuxFallback` 进入 `ui.NewTester` 字幕菜单，选择英文轨，并只抓取第一个 remux 片段检查 AAC：

```sh
MYGO_GOODPLACE_LIVE_DTS=1 CGO_ENABLED=0 go test ./internal/player \
  -run TestGoodPlaceMissingMetadataDTSMenuLive -count=1 -v -timeout=120s
```

2026-11-12 复测通过：

- 默认《善地》媒体版本包含 DTS 音轨。
- 代理版本仍返回 `MediaStreams=0`。
- 修复前该路径在字幕菜单出现前进入 `ErrUnsupportedAudio`；修复后菜单出现真实 `English`、`SDH`，English 可选择。
- 第一个短片段包含 AAC 输出音轨。
- 仍然没有中文或其他第三语言轨。

## 边界

- 本次真实故障只证实了服务器字幕列表为空、remux 能找到真实轨的版本。
- 若服务器只上报部分字幕轨、同时漏掉另一条，本次最小修复继续把非空服务器列表视为权威，不会尝试按不完整序号猜测和合并。该“部分漏报”场景尚未证实，也没有纳入本次改动。
- 用户实际播放的是哪一个媒体版本仍没有用户侧日志锁定；但 `MediaStreams=0` 的后备接线缺陷已经由真实服务器响应和确定性测试证实。
