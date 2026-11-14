package player

// str is every text the player shows, in one place, so that another
// language can replace it later.
var str = struct {
	Back, Play, Pause, Rewind, Forward, Volume, Mute, Unmute                   string
	Speed, Audio, Subtitles, SubtitlesOff, NextEpisode, Chapters               string
	FullScreen, ExitFullScreen, Progress, Loading, Close, Retry                string
	UpNext, UpNextIn, PlayNow, Cancel, Remaining                               string
	ErrTitle, ErrNotPlayable, ErrDiscImage, ErrNoStream, ErrPlayer, ErrNetwork string
	ErrAudioTranscoder                                                         string
	ErrUnauthorized, ErrNotFound, ErrServer, BurnInUnavailable                 string
	EpisodeFmt, SeasonEpisodeFmt, AudioSwitchFailed, SubtitleFailed            string
	NormalSpeed                                                                string
	ProgressPreview                                                            string
	ChapterFmt, Diagnostics                                                    string
	PipeDirect, PipeLocalRemux, PipeLocalRemuxAudio, PipeServerTranscode       string
	LoadLocalSubtitle, SubtitleDelay, SubtitleEarlier, SubtitleLater           string
	SubtitleResetDelay, SubtitleSize                                           string
	SubtitleSizeSmall, SubtitleSizeNormal, SubtitleSizeLarge, SubtitleSizeXL   string
	LocalSubtitleTag, OpenExternalFmt, OpenExternalSubFmt                      string
	DiagMode, DiagContainer, DiagVideo, DiagAudio, DiagSub, DiagURL            string
	SubtitlesNone, SubtitlesBurned                                             string
	SkipIntro, SkipOutro, SkipRecap, SkipPreview, SkipAd, Dismiss              string
}{
	Back:            "返回",
	Play:            "播放",
	Pause:           "暂停",
	Rewind:          "快退 10 秒",
	Forward:         "快进 10 秒",
	Volume:          "音量",
	Mute:            "静音",
	Unmute:          "取消静音",
	Speed:           "播放速度",
	Audio:           "音轨",
	Subtitles:       "字幕",
	SubtitlesOff:    "关闭",
	NextEpisode:     "下一集",
	Chapters:        "章节",
	FullScreen:      "全屏",
	ExitFullScreen:  "退出全屏",
	Progress:        "播放进度",
	ProgressPreview: "跳转时间",
	Loading:         "正在载入",
	Close:           "关闭",
	Retry:           "重试",
	UpNext:          "即将播放",
	UpNextIn:        "秒后播放下一集",
	PlayNow:         "立即播放",
	Cancel:          "取消",
	Remaining:       "剩余",

	ErrTitle:           "无法播放",
	ErrNotPlayable:     "这个片源的格式无法在本机直接播放，服务器也不能为它转码。",
	ErrDiscImage:       "这是 Blu-ray 或 DVD 光盘镜像，本机播放器和内置转封装不能直接读取，服务器也没有可用转码。请选择同一影片的 MKV 或 MP4 版本。",
	ErrNoStream:        "服务器没有给出可以播放的片源。",
	ErrAudioTranscoder: "这个片源的音轨需要用本机 FFmpeg 转码，但应用找不到 FFmpeg。请安装 ffmpeg，或把 MYGO_FFMPEG 指向可执行文件后重启应用。",
	ErrPlayer:          "播放中断：",
	ErrNetwork:         "连不上服务器，请检查网络后重试。",
	ErrUnauthorized:    "登录已失效，请重新登录。",
	ErrNotFound:        "服务器上找不到这个条目。",
	ErrServer:          "服务器出错：",
	BurnInUnavailable:  "（服务器不能转码，无法显示）",

	EpisodeFmt:        "E%d",
	SeasonEpisodeFmt:  "S%dE%d",
	AudioSwitchFailed: "切换音轨失败",
	SubtitleFailed:    "字幕载入失败",
	NormalSpeed:       "正常",

	ChapterFmt:          "第 %d 章",
	Diagnostics:         "播放诊断",
	PipeDirect:          "AVPlayer 原生直放",
	PipeLocalRemux:      "本机纯 Go 转封装 (HLS)",
	PipeLocalRemuxAudio: "本机转封装 + FFmpeg 音频",
	PipeServerTranscode: "服务器 HLS 转码",
	LoadLocalSubtitle:   "加载本地字幕…",
	SubtitleDelay:       "字幕偏移",
	SubtitleEarlier:     "提前 0.5 秒 ([)",
	SubtitleLater:       "延后 0.5 秒 (])",
	SubtitleResetDelay:  "重置偏移 (\\)",
	SubtitleSize:        "字幕大小",
	SubtitleSizeSmall:   "小 (85%)",
	SubtitleSizeNormal:  "标准 (100%)",
	SubtitleSizeLarge:   "大 (120%)",
	SubtitleSizeXL:      "特大 (145%)",
	LocalSubtitleTag:    "本地",
	OpenExternalFmt:     "用 %s 打开",
	OpenExternalSubFmt:  "用 %s 打开以渲染图形字幕",
	DiagMode:            "播放模式",
	DiagContainer:       "容器与码率",
	DiagVideo:           "视频规格",
	DiagAudio:           "音频轨道",
	DiagSub:             "字幕状态",
	DiagURL:             "流媒体地址",
	SubtitlesNone:       "未开启",
	SubtitlesBurned:     "服务器烧录",
	SkipIntro:           "跳过片头",
	SkipOutro:           "跳过片尾",
	SkipRecap:           "跳过回顾",
	SkipPreview:         "跳过预告",
	SkipAd:              "跳过广告",
	Dismiss:             "忽略",
}
