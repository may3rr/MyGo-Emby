package app

// uiStrings holds every user-facing string of the app, so that another
// language can be added by filling a second value of this type. Pages read
// them through str. Formats take fmt verbs.
type uiStrings struct {
	AppName string

	// Sidebar and navigation.
	Search          string
	Home            string
	Favorites       string
	Libraries       string
	ExpandSidebar   string
	CollapseSidebar string
	Settings        string
	SwitchServer    string
	SignOut         string
	Back            string
	Forward         string
	AccountMenu     string

	// Menus.
	MenuAbout      string
	MenuSettings   string
	MenuServices   string
	MenuHide       string
	MenuHideOthers string
	MenuShowAll    string
	MenuQuit       string
	MenuEdit       string
	MenuUndo       string
	MenuRedo       string
	MenuCut        string
	MenuCopy       string
	MenuPaste      string
	MenuSelectAll  string
	MenuView       string
	MenuRefresh    string
	MenuFind       string
	MenuSidebar    string
	MenuFullScreen string
	MenuGo         string
	MenuWindow     string
	MenuMinimize   string
	MenuZoom       string
	MenuClose      string
	MenuFront      string

	// Common actions and states.
	Play           string
	PlayFromStart  string
	Resume         string
	SeeAll         string
	Retry          string
	Loading        string
	More           string
	ScrollLeft     string
	ScrollRight    string
	MarkPlayed     string
	MarkUnplayed   string
	AddFavorite    string
	RemoveFavorite string
	Played         string
	UnplayedFmt    string // number of unplayed episodes
	FavoriteMark   string
	ComingLater    string
	ComingLaterFmt string // page name
	NotFound       string
	NotFoundBody   string
	ErrorTitle     string
	EmptyTitle     string

	// Durations and metadata.
	HoursMinutesFmt string
	MinutesFmt      string
	EpisodeFmt      string // season, episode

	// Developer gallery.
	Gallery        string
	GalleryEyebrow string
	Developer      string

	// Signing in, and the servers kept.
	LoginTitle        string
	LoginSubtitle     string
	ServerAddress     string
	ServerPlaceholder string
	UserName          string
	Password          string
	PasswordHint      string
	SignIn            string
	SigningIn         string
	Probing           string
	Cancel            string
	SavedServers      string
	AddServer         string
	ChooseServer      string
	ChooseSubtitle    string
	CurrentServer     string
	SignedOutServer   string
	RemoveServer      string
	SwitchServerMenu  string
	KindEmby          string
	KindJellyfin      string

	// What went wrong, as the user is told.
	ErrInvalidURL     string
	ErrEnterUser      string
	ErrUnreachable    string
	ErrTimeout        string
	ErrNotServer      string
	ErrBadCredentials string
	ErrForbidden      string
	ErrServerFmt      string // HTTP status
	ErrNotFound       string
	ErrUnknown        string
	SessionExpired    string
	OfflineTitle      string
	OfflineFmt        string // server name
	LibrariesFailed   string
	LoadMoreFailed    string

	// Home.
	ContinueWatching string
	NextUp           string
	LatestFmt        string // library name
	RecentlyAdded    string
	MoreInfo         string
	SlideFmt         string // item, of items
	Previous         string
	Next             string
	HomeEmpty        string
	HomeEmptyBody    string
	NoLibraries      string
	RemainingFmt     string // run time left

	// Libraries.
	SortBy          string
	SortName        string
	SortAdded       string
	SortYear        string
	SortRating      string
	SortPlayed      string
	SortRuntime     string
	Ascending       string
	Descending      string
	FilterAll       string
	FilterUnplayed  string
	FilterFavorites string
	ItemsFmt        string // count
	EmptyUnplayed   string
	EmptyFavorites  string
	EmptyFilterBody string
	EmptyFolder     string
	BoxSet          string
	Genre           string
	GenreEmpty      string

	// The player & items' pages.
	Cast              string
	Similar           string
	Chapters          string
	ChapterNumFmt     string // 1-based chapter index
	Version           string
	Audio             string
	Subtitles         string
	SubtitlesOff      string
	Favorited         string
	PlayedDone        string
	ResetProgress     string
	OpenExternal      string
	OpenWithFmt       string // external player name
	OpenedExternalFmt string // external player name
	ExternalFailedFmt string // error
	Directors         string
	Studios           string
	ResumeLeftFmt     string // time left
	PlayEpisodeFmt    string // episode
	ResumeEpisodeFmt  string // episode
	EpisodeNumFmt     string // number, name
	SeriesLink        string
	CollectionItems   string
	ExternalLinks     string
	PlayCollection    string

	// People.
	Person           string
	BornFmt          string // date
	Filmography      string
	WorksUnsupported string
	WorksNone        string

	// Search.
	SearchMovies   string
	SearchSeries   string
	SearchEpisodes string
	SearchPeople   string
	SearchBoxSets  string
	SearchNoneFmt  string // query
	SearchNoneBody string
	SearchHint     string
	SearchRecent   string
	ClearHistory   string

	// Favorites.
	FavoritesEmpty     string
	FavoritesEmptyBody string

	// Settings.
	Appearance          string
	FollowSystem        string
	Light               string
	Dark                string
	Playback            string
	MaxBitrate          string
	Auto                string
	SubtitleLang        string
	SubtitleSize        string
	SubtitleSizeSmall   string
	SubtitleSizeNormal  string
	SubtitleSizeLarge   string
	SubtitleSizeXL      string
	AudioLang           string
	LangOff             string
	AutoNext            string
	AutoNextBody        string
	Cache               string
	CacheSize           string
	Measuring           string
	ClearCache          string
	CacheCleared        string
	About               string
	AppVersion          string
	ServerLabel         string
	SignedInAs          string
	DeviceLabel         string
	CredentialStore     string
	CredentialKeychain  string
	CredentialPlaintext string
}

// zhCN is the app's Chinese text.
var zhCN = uiStrings{
	AppName: "MyGo Emby",

	Search:          "搜索",
	Home:            "首页",
	Favorites:       "收藏",
	Libraries:       "媒体库",
	ExpandSidebar:   "展开侧栏",
	CollapseSidebar: "收起侧栏",
	Settings:        "设置",
	SwitchServer:    "切换服务器",
	SignOut:         "退出登录",
	Back:            "后退",
	Forward:         "前进",
	AccountMenu:     "账户",

	MenuAbout:      "关于 MyGo Emby",
	MenuSettings:   "设置…",
	MenuServices:   "服务",
	MenuHide:       "隐藏 MyGo Emby",
	MenuHideOthers: "隐藏其他",
	MenuShowAll:    "全部显示",
	MenuQuit:       "退出 MyGo Emby",
	MenuEdit:       "编辑",
	MenuUndo:       "撤销",
	MenuRedo:       "重做",
	MenuCut:        "剪切",
	MenuCopy:       "拷贝",
	MenuPaste:      "粘贴",
	MenuSelectAll:  "全选",
	MenuView:       "显示",
	MenuRefresh:    "刷新",
	MenuFind:       "搜索",
	MenuSidebar:    "显示/隐藏侧栏",
	MenuFullScreen: "进入全屏幕",
	MenuGo:         "前往",
	MenuWindow:     "窗口",
	MenuMinimize:   "最小化",
	MenuZoom:       "缩放",
	MenuClose:      "关闭",
	MenuFront:      "前置全部窗口",

	Play:           "播放",
	PlayFromStart:  "从头播放",
	Resume:         "继续播放",
	SeeAll:         "查看全部",
	Retry:          "重试",
	Loading:        "正在载入",
	More:           "更多",
	ScrollLeft:     "向左翻页",
	ScrollRight:    "向右翻页",
	MarkPlayed:     "标记为已看",
	MarkUnplayed:   "标记为未看",
	AddFavorite:    "加入收藏",
	RemoveFavorite: "取消收藏",
	Played:         "已看完",
	UnplayedFmt:    "%d 集未看",
	FavoriteMark:   "已收藏",
	ComingLater:    "这一页还在路上",
	ComingLaterFmt: "%s将在后续版本中提供。",
	NotFound:       "找不到这一页",
	NotFoundBody:   "链接可能已失效。",
	ErrorTitle:     "载入失败",
	EmptyTitle:     "这里还没有内容",

	HoursMinutesFmt: "%d 小时 %d 分钟",
	MinutesFmt:      "%d 分钟",
	EpisodeFmt:      "第 %d 季第 %d 集",

	Gallery:        "组件库",
	GalleryEyebrow: "开发预览",
	Developer:      "开发",

	LoginTitle:        "连接到媒体服务器",
	LoginSubtitle:     "登录你的 Emby 或 Jellyfin 服务器",
	ServerAddress:     "服务器地址",
	ServerPlaceholder: "https://media.example.com",
	UserName:          "用户名",
	Password:          "密码",
	PasswordHint:      "没有密码可以留空",
	SignIn:            "登录",
	SigningIn:         "正在登录…",
	Probing:           "正在连接服务器…",
	Cancel:            "取消",
	SavedServers:      "已保存的服务器",
	AddServer:         "添加服务器…",
	ChooseServer:      "选择服务器",
	ChooseSubtitle:    "选择要使用的服务器和账户",
	CurrentServer:     "当前",
	SignedOutServer:   "已退出",
	RemoveServer:      "移除",
	SwitchServerMenu:  "切换服务器…",
	KindEmby:          "Emby",
	KindJellyfin:      "Jellyfin",

	ErrInvalidURL:     "服务器地址无效，请检查拼写，例如 https://media.example.com",
	ErrEnterUser:      "请输入用户名",
	ErrUnreachable:    "无法连接到服务器，请检查地址和网络",
	ErrTimeout:        "连接服务器超时，请检查地址和网络",
	ErrNotServer:      "这个地址上没有找到 Emby 或 Jellyfin 服务器",
	ErrBadCredentials: "用户名或密码错误",
	ErrForbidden:      "这个账户已被停用，或不允许登录",
	ErrServerFmt:      "服务器出错（HTTP %d），请稍后再试",
	ErrNotFound:       "找不到这个条目，它可能已被删除",
	ErrUnknown:        "出了点问题，请稍后再试",
	SessionExpired:    "登录已过期，请重新登录",
	OfflineTitle:      "无法连接到服务器",
	OfflineFmt:        "暂时连不上 %s，请检查网络后重试。",
	LibrariesFailed:   "媒体库载入失败",
	LoadMoreFailed:    "载入更多内容失败",

	ContinueWatching: "继续观看",
	NextUp:           "接下来观看",
	LatestFmt:        "最近添加 · %s",
	RecentlyAdded:    "最近添加",
	MoreInfo:         "详细信息",
	SlideFmt:         "第 %d 项，共 %d 项",
	Previous:         "上一项",
	Next:             "下一项",
	HomeEmpty:        "这里还没有内容",
	HomeEmptyBody:    "媒体库添加影片后会显示在这里。",
	NoLibraries:      "这个账户没有可用的媒体库。",
	RemainingFmt:     "剩余 %s",

	SortBy:          "排序",
	SortName:        "名称",
	SortAdded:       "加入时间",
	SortYear:        "年份",
	SortRating:      "评分",
	SortPlayed:      "最近播放",
	SortRuntime:     "片长",
	Ascending:       "升序",
	Descending:      "降序",
	FilterAll:       "全部",
	FilterUnplayed:  "未看",
	FilterFavorites: "收藏",
	ItemsFmt:        "%d 项",
	EmptyUnplayed:   "没有未看的内容",
	EmptyFavorites:  "还没有收藏的内容",
	EmptyFilterBody: "换个筛选条件看看。",
	EmptyFolder:     "这个文件夹是空的",
	BoxSet:          "合集",
	Genre:           "类型",
	GenreEmpty:      "这个类型下还没有影片。",

	Cast:              "演职人员",
	Similar:           "相似推荐",
	Chapters:          "章节",
	ChapterNumFmt:     "章节 %d",
	Version:           "版本",
	Audio:             "音轨",
	Subtitles:         "字幕",
	SubtitlesOff:      "关闭",
	Favorited:         "已收藏",
	PlayedDone:        "已看完",
	ResetProgress:     "清除播放进度",
	OpenExternal:      "外部播放器",
	OpenWithFmt:       "用 %s 打开",
	OpenedExternalFmt: "已在 %s 中打开",
	ExternalFailedFmt: "启动外部播放器失败：%v",
	Directors:         "导演",
	Studios:           "出品",
	ResumeLeftFmt:     "继续播放 · 剩余 %s",
	PlayEpisodeFmt:    "播放 %s",
	ResumeEpisodeFmt:  "继续播放 %s",
	EpisodeNumFmt:     "%d. %s",
	SeriesLink:        "前往剧集",
	CollectionItems:   "收录内容",
	ExternalLinks:     "外部链接",
	PlayCollection:    "播放合集",

	Person:           "人物",
	BornFmt:          "生于 %s",
	Filmography:      "参演作品",
	WorksUnsupported: "这台服务器暂不支持按人物列出作品。",
	WorksNone:        "媒体库里没有这位的作品。",

	SearchMovies:   "电影",
	SearchSeries:   "剧集",
	SearchEpisodes: "分集",
	SearchPeople:   "人物",
	SearchBoxSets:  "合集",
	SearchNoneFmt:  "没有找到“%s”",
	SearchNoneBody: "换个关键词试试，片名、人名都可以。",
	SearchHint:     "输入片名或人名开始搜索",
	SearchRecent:   "最近搜索",
	ClearHistory:   "清空",

	FavoritesEmpty:     "还没有收藏",
	FavoritesEmptyBody: "在卡片上右键选择“收藏”，喜欢的影片会出现在这里。",

	Appearance:          "外观",
	FollowSystem:        "跟随系统",
	Light:               "浅色",
	Dark:                "深色",
	Playback:            "播放",
	MaxBitrate:          "最高码率",
	Auto:                "自动",
	SubtitleLang:        "默认字幕语言",
	SubtitleSize:        "字幕字号",
	SubtitleSizeSmall:   "小 (85%)",
	SubtitleSizeNormal:  "标准 (100%)",
	SubtitleSizeLarge:   "大 (120%)",
	SubtitleSizeXL:      "特大 (145%)",
	AudioLang:           "默认音轨语言",
	LangOff:             "不显示字幕",
	AutoNext:            "自动播放下一集",
	AutoNextBody:        "一集播完后接着播放下一集。",
	Cache:               "缓存",
	CacheSize:           "图片缓存",
	Measuring:           "正在计算…",
	ClearCache:          "清理缓存",
	CacheCleared:        "图片缓存已清理",
	About:               "关于",
	AppVersion:          "版本",
	ServerLabel:         "服务器",
	SignedInAs:          "账户",
	DeviceLabel:         "设备",
	CredentialStore:     "凭据存储",
	CredentialKeychain:  "系统钥匙串（已加密）",
	CredentialPlaintext: "本地配置文件（钥匙串不可用，令牌明文保存）",
}

// str is the language the app shows.
var str = zhCN
