package player

// str is every text the player shows, in one place, so that another
// language can replace it later.
var str = struct {
	Back, Play, Pause, Rewind, Forward, Volume, Mute, Unmute        string
	Speed, Audio, Subtitles, SubtitlesOff, NextEpisode              string
	FullScreen, ExitFullScreen, Progress, Loading, Close, Retry     string
	UpNext, UpNextIn, PlayNow, Cancel, Remaining                    string
	ErrTitle, ErrNotPlayable, ErrNoStream, ErrPlayer, ErrNetwork    string
	ErrAudioTranscoder                                              string
	ErrUnauthorized, ErrNotFound, ErrServer, BurnInUnavailable      string
	EpisodeFmt, SeasonEpisodeFmt, AudioSwitchFailed, SubtitleFailed string
	NormalSpeed                                                     string
	ProgressPreview                                                 string
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
}
