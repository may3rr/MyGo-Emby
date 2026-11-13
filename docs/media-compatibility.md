# 媒体兼容与有声音回退

播放器优先使用 AVPlayer 直放。初次 `PlaybackInfo` 已返回服务器 HLS 时直接
使用；没有可用服务器流时先尝试本机 fMP4 转封装。只有本机 remux 失败后，
才在服务器声明支持转码时再强制请求一次服务器 HLS。本机 remux 需要音频兜底
时才按需使用 FFmpeg；视频中已有音轨时不得以 `NoAudio` 静默降级。

## 默认转封装路径

Matroska、MPEG-TS 和需要修正标签的 MP4/MOV 由 `internal/remux` 生成
fMP4 HLS。H.264/HEVC 视频始终 stream-copy，HEVC 输出为 `hvc1`，Dolby
Vision 的 `dvh1` 与色彩/HDR 标记由现有 demux/mux 路径保留。AAC、AC-3 和
E-AC-3 音频同样直接复制。该快捷路径保持纯 Go、进程内、内存分片，不落盘，
也不启动外部程序。

## 按需本机音频转换

只有本机 remux 已选中且实际文件中的音轨不能写入 fMP4 时，才启用 FFmpeg
音频兜底。当前覆盖 FFmpeg 能解码的常见本地格式，已用真实 DTS-HD MA 样片
验证；TrueHD、Opus、FLAC 等走同一路径，但仍应以实际文件 probe 为准。默认
无可用复制音轨时，`remux.Decide` 选择实际默认音轨并设置 `AudioTranscode`，
不会把 `AudioIndex` 改成 `-1`。

FFmpeg 仅处理必要的一条音轨：

- 按 demux 后的真实容器 track order 映射 `0:a:<order>`，不假设 Emby
  `MediaStream.Index` 等于实际容器 index。
- 输出 AAC-LC、48 kHz、双声道，每片约 192 kbps。
- 视频不进入 FFmpeg，保持原 remux 路径的 stream-copy 与 HDR/DV 元数据。
- 每个 HLS 分片通过 stdout 管道读入内存，不创建整片或长时磁盘缓存。
- AAC 分片按 1024 样本帧网格对齐，移除 encoder priming；下一片从前一跨界帧
  的结束位置开始，避免静音缝、重复音和 seek 后的延迟起音。
- 先把容器 packet 的绝对 PTS 统一归一到 media origin。FFmpeg 的输入 seek 按
  相对容器起点计算，生成的 AAC 再映射回内容相对时间；不能直接把
  `seg.start + i*frameDuration` 当作源时间。
- 取消 HTTP 分片读取或关闭 session 会终止 FFmpeg 进程组，等待清理完成。

## FFmpeg 查找与提示

应用按以下顺序查找 FFmpeg：

1. `MYGO_FFMPEG` 指向的可执行文件；
2. `/opt/homebrew/bin/ffmpeg`；
3. `/usr/local/bin/ffmpeg`；
4. GUI 进程继承的 `PATH`。

因此 Finder 或 LaunchServices 启动、没有 Homebrew PATH 的 `.app` 仍能找到
Apple Silicon 的常见安装位置。部署时也可以显式设置 `MYGO_FFMPEG`。找不到
时播放器显示明确提示，要求安装 FFmpeg 或设置 `MYGO_FFMPEG` 后重启，不会
播放无声视频。

## 失败顺序与边界

1. AVPlayer 可直接播放时直接播放。
2. 初次播放信息已经返回服务器 HLS 时，直接使用服务器流。
3. 没有可用的服务器 HLS 时先尝试本地转封装，优先选择可复制的音轨。
4. 无复制候选时，使用本机 FFmpeg 转换实际选中的音轨。
5. 本机 fallback 失败后，如果服务器声明支持转码，再请求一次强制服务器转码；
   两边都失败时保留本地错误并附上服务器转码错误。
6. 视频本身不支持时，先尝试服务器转码；仍不支持则明确报错，不用音频兜底
   强行输出错误画面。

已知限制：每次 seek 或重开音轨会启动按片 FFmpeg，首次起播增加少量延迟；
当前本地兜底将多声道音轨下混为双声道；FFmpeg 缺失时这类片源必须安装依赖
或使用支持服务器转码的服务器。图形字幕仍遵循服务器烧录能力，本机音频兜底
不会改变 PGS/VobSub 的支持范围。

## 同步回归

媒体回归使用 H.264 B 帧、长 GOP、视觉闪帧、DTS/Opus 音频脉冲和
Matroska 非零/负时间原点；分别检查起播、连续多个分片、30 秒与 60 秒
seek、最后分片和非零起点字幕。测试同时用 ffprobe 读取真实的
presentation/frame time，用 FFmpeg 解码画面亮度和 PCM 脉冲，比较源与
输出的事件时间；只比较时长或有声不够。Matroska 的 `Duration` 在 FFmpeg
生成非零起点文件时是绝对时间轴终点，回归还会确认 `Session.Duration`、全部
`EXTINF` 之和和最后分片都已减去同一个 media origin。MP4 和 MPEG-TS 的
reader 已产生内容相对时长，不做这一步相减。

实际《善地》S01E01 文件的 `format.start_time` 是 `89.089s`，视频从
`89.089s` 开始，DTS-HD MA 从 `89.099s` 开始。修复前本机音频被重造为从
`0s` 开始，事件因此约提前 `1.234s`（合成非零起点样片）或约 `89.09s`
（该真实文件）并持续到 seek 后。归一后，真实首片视频 stream start 为
`42ms`、AAC 为 `21.333ms`；源的内容相对起点是视频 `0ms`、音频 `10ms`。
因此源 A/V delta 是 `0 - 10ms = -10ms`，输出 delta 是
`42 - 21.333ms = +20.667ms`，差值的实际变化是 `30.667ms`，不能写成
`10.7ms`。

合成样片中测到输出视觉事件相对解码源帧约晚 `84ms`。该数值来自 FFmpeg
解码实际画面并配合 ffprobe 的帧 presentation time，不是把 demuxer 的
`dts_shift` 或 `start_time` 直接当作音画误差。零起点样片也有相同量级，音频
事件通常相差小于 `1ms`，因此这是仍需原生时钟验收的 B 帧离散显示偏移，
不能据此宣称已经完全同步；可以确认的是约 `89.09s` 的文件原点错位已经移除，
起播和多个 seek 后不再出现该量级漂移。

原生验收使用 `build/avsync-verification/avsync-h264-dts-offset.mkv`：72 秒
H.264 内容带 B 帧和长 GOP，Matroska 时间原点为 `1.234s`（视频首帧
`1.250s`），音轨为 DTS，另含非零起点字幕。`TestNativeWindowPlayback` 中
本机 remux 得到 content duration `1m12.016s`；AVPlayer 首次 ready 时测得
position `0s`、duration `1m12.015s`，随后 8 秒 seek、恢复播放及两轮全屏
进入/返回均通过，native view 正常关闭。该测试只证明原生时间轴从内容零点和
正常 seek 开始，不捕获系统音频，也不把离线测得的 `84ms` 残余当作零。
