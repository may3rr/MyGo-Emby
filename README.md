# MyGo Emby

用 Go 和 [MyGo](https://github.com/egoist/mygo) 构建的 Emby / Jellyfin 桌面客户端，让媒体库融入 macOS 的日常体验。

![MyGo Emby 主界面宣传图](docs/assets/promo.png)

> **当前为 0.1.0 测试版。** 主要在 macOS 上开发与验证，播放兼容性、字幕和交互仍在完善；暂不承诺其他平台的完整体验。

## 功能

- **媒体库浏览**：首页推荐、继续观看（支持右键清除进度）、接下来观看、最近添加、海报网格、电影合集（BoxSet）/ 播放列表（Playlist）专属详情页与智能连播、媒体技术规格标签（分辨率 / HDR / 编码 / 声道 / 体积）、外部元数据直达（IMDb / TMDb 等）与按类型（Genre）浏览。
- **高效检索**：搜索支持拼音/关键词防抖、键盘上下选择、最近搜索历史记录与一键清空、列表按名称/加入时间/年份/评分/最近播放/片长排序。
- **多服务器登录**：保存多个账户并切换，支持 Emby 与 Jellyfin；当前主要围绕 Emby 验证。
- **播放器与字幕增强**：
  - **进度条缩略图预览与片头片尾跳过**：支持 Trickplay 雪碧图（GPU 子区域零拷贝裁切）与章节缩略图悬停预览；支持 `MediaSegments` 及章节智能推断的**跳过片头 / 片尾 / 回顾**浮层（快捷键 `S`）与进度条高亮区间标记。
  - 章节列表与章节直达起播、倍速、全屏、断点续播与下一集倒计时。
  - 原生支持 SRT / WebVTT 与 ASS / SSA 字幕解析，支持加载本地外挂字幕文件、实时字幕时间偏移调节与 4 档字幕字号调节；针对无服务端转码条件下的 PGS / VobSub 图形字幕提供一键外部播放器接管入口。
  - 按 `I` 查看实时播放诊断 HUD（播放链路、容器、视频规格、音频、字幕状态及脱敏后的流地址）。
  - 系统级媒体集成（macOS Now Playing 信息同步、系统媒体键控制、播放防休眠）。
- **本机转封装与外部播放器**：支持的 MKV、TS、hev1 MP4 组合可转成 fMP4 HLS（视频不重新编码）；遇到特殊格式或播放失败时，可一键拉起本机已安装的 **IINA / mpv / VLC** 并继承续播进度。
- **macOS 原生视频输出与原生 UI**：通过 `AVPlayerLayer` 播放（HDR / Dolby Vision 效果取决于片源、封装、系统及显示设备）；使用 MyGo 的 Go UI 渲染路径，无 WebView；浅色、深色与系统主题，可折叠侧边栏。

## 快捷键

| 场景 | 快捷键 | 功能 |
| --- | --- | --- |
| 全局浏览 | `Cmd/Ctrl + F` | 聚焦搜索栏（有历史时直达最近搜索） |
| 全局浏览 | `Cmd/Ctrl + R` | 刷新当前页面与图片缓存 |
| 全局浏览 | `Cmd/Ctrl + ,` | 打开设置 |
| 全局浏览 | `Cmd/Ctrl + [` / `]` | 后退 / 前进 |
| 全局浏览 | `Super + Ctrl + S` (macOS) / `Ctrl + Alt + S` | 显示 / 隐藏侧边栏 |
| 播放器 | `Space` / `K` | 播放 / 暂停 |
| 播放器 | `←` / `J` · `→` / `L` | 快退 10 秒 / 快进 10 秒 |
| 播放器 | `↑` / `↓` · `M` | 音量加减 / 静音切换 |
| 播放器 | `F` · `N` · `S` | 全屏切换 / 立即播放下一集 / 跳过当前片头或片尾 |
| 播放器 | `C` · `I` | 章节菜单 / 播放诊断 HUD |
| 播放器 | `[` / `]` / `\` | 字幕提前 0.5 秒 / 延后 0.5 秒 / 重置偏移 |
| 播放器 | `Esc` | 关闭菜单或诊断面板，或退出播放器 |

## 运行与构建

需要 macOS（跨平台构建支持 Linux / Windows，但视频硬件引擎当前围绕 macOS 验证）、Go **1.27.4 或更新版本**，以及你有权访问的媒体服务器。

```sh
git clone https://github.com/may3rr/MyGo-Emby.git
cd MyGo-Emby
go run .
```

```sh
go tool mygo dev                       # 开发模式
go tool mygo build                     # 构建应用与 DMG
CGO_ENABLED=0 go test -race ./internal/... # 运行完整测试套件
go tool mygo vet .                     # UI 生命周期检查
CGO_ENABLED=0 go vet ./...             # Go 静态检查
```

仓库包含带局部补丁的 `vendor/`，请保留并使用它；直接运行 `go mod vendor` 会覆盖原生视频相关修改。详见 [原生视频实现](docs/native-video.md) 与 [贡献指南](CONTRIBUTING.md)。

部分不兼容音轨需要本机 FFmpeg 转成 AAC；仅在需要时调用。未安装时会提示，视频仍保持原编码。可使用 `brew install ffmpeg` 安装。

## 工程与安全文档

- [贡献指南 (CONTRIBUTING.md)](CONTRIBUTING.md)：包依赖边界、MyGo UI 规范与本地验证命令。
- [安全策略 (SECURITY.md)](SECURITY.md)：凭据存储、URL 脱敏与漏洞报告流程。
- 更多说明：[媒体兼容性](docs/media-compatibility.md) · [侧边栏](docs/sidebar.md)。

## 测试版已知限制

- 兼容性受服务器 API、媒体源和音视频编码影响，不能保证所有文件都能播放；极端封装或光盘镜像建议配合外部播放器（IINA / mpv / VLC）使用。
- 图形字幕（如 PGS、VobSub）需要服务器烧录能力；没有该能力时存在限制。
- 尚未发布正式稳定版安装包；当前可从源码运行。

## 截图与宣传素材

宣传图中的界面来自应用实际运行时的截图，内容为公开 Jellyfin 演示库。背景由 NumPy 绘制，圆角、阴影与红绿灯由 Pillow 合成，**未使用生图模型**。系统窗口装饰不在内容截图内，因此在宣传排版中补绘；[原始截图](docs/assets/home.png)单独保留。

```sh
EMBY_SHOTS=build/release/shots EMBY_SHOTS_DARK=0 go run .
```

上述命令使用公开演示服务器；自有服务器可通过 `EMBY_SHOTS_SERVER`、`EMBY_SHOTS_USER`、`EMBY_SHOTS_PASS` 环境变量提供。截图发布前请检查账户、服务器和媒体信息。

重新生成宣传图（macOS 系统字体）：

```sh
python3 -m venv build/promo-venv
build/promo-venv/bin/pip install numpy pillow
build/promo-venv/bin/python scripts/make_promo.py
```

## 隐私与反馈

访问令牌默认通过系统钥匙串（`mygo.Secrets`）加密保存；**当系统钥匙串不可用或写入失败时，会降级保存至本地 `settings.json`**，并在「设置 -> 关于 -> 凭据存储」行明确标注当前存储状态（详见 [SECURITY.md](SECURITY.md)）。本地设置仍包含服务器地址、账户显示信息与最近搜索词等元数据。请勿提交真实配置、令牌、密码或带鉴权参数的 URL。反馈时提供操作步骤、服务器版本、媒体容器与编码，并先脱敏日志和截图。

## 致敬与感谢

- [MyGo / egoist](https://github.com/egoist/mygo)：本项目的 UI 与桌面框架基础，感谢它让 Go 原生桌面应用成为现实。
- [qEmby / AlanHJ](https://github.com/AlanHJ/qEmby)：感谢它在 Emby 客户端功能与观影体验上的探索，为本项目提供了参考和灵感。

MyGo Emby 是独立实验项目，与上述项目及 Emby、Jellyfin 无官方隶属关系。截图中媒体海报与剧照的权利归各自权利人，应用不提供媒体资源。

目前尚未为本项目代码指定开源许可证；第三方依赖保留各自的许可证与声明。
