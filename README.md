# OfflineU：自托管的本地课程加载器与进度追踪器

**OfflineU** 是一个简洁的自托管 Web 应用，能把任意一文件夹里的离线视频、音频、文档和测验资料，
变成一个可自由浏览的课程面板，并自动记录学习进度。无论是 Udemy 下载包、"开源"培训资料，还是你自己的笔记，
直接指过去就行 —— 不需要元数据，不需要数据库，也不需要云端。

后端是一个**单文件静态 Go 二进制**（仅用标准库），内置重新设计过的 **Vue 3** 前端，
所以部署就是"一个文件"：不需要 Python，不需要 Node，不需要虚拟环境，也没有任何运行时依赖。

---

## ✨ 功能特性

* 📁 **目录浏览与文件夹解析** —— 点一下就能选中一门课程，OfflineU 会把它的结构映射成可浏览的课程树。
* 🧷 **记住你的课程** —— 选中的课程会写入磁盘，所以回到首页、切换课程或重启 OfflineU 都不会丢。
  每个被记住的课程还会显示完成百分比，一眼就能看出该接着学哪一门。
* 🎥 **视频与音频播放器** —— 内置播放器支持断点续播、观看时长统计，播完自动标记完成。
* ▶️ **连续播放** —— 一节播完会自动接着播下一节可播放的课时（中间的文档会被跳过），
  你设置的倍速也会带到后面每一节。无需任何设置，浏览器播放和投屏都是如此。
* 📄 **文档内联显示** —— `.txt`/`.md` 以纯文本展示，`.html`/`.pdf` 内嵌显示，
  其他 Office 文件提供"在新标签页打开"的链接。
* 💬 **字幕** —— 与视频同名的 `.srt`/`.vtt` 会自动挂到该视频上，并即时转换为 WebVTT，
  这样浏览器才能真正显示出来。
* 📺 **投屏到 DLNA/UPnP 设备** —— 课时页面会列出局域网里的电视、音箱和播放器，
  把你选中的那台设备推送当前视频或音频（并从已保存的位置续播）。纯标准库实现：
  SSDP 发现 + `SetAVTransportURI`/`Play` 这两个 SOAP 动作。
* ✅ **课时进度追踪** —— 观看时长与完成状态自动保存，重看课时也不会丢失。
* ♻️ **接着上次继续** —— 面板上的"继续学习"卡片直接跳回上次看到的那一节。
* ⌨️ **键盘快捷键** —— 空格（播放/暂停）、← / →（快退/快进 10 秒）、↑ / ↓（音量）。
* 💾 **本地优先、隐私优先** —— 100% 离线，进度就是你自己拥有的一份纯 JSON 文件。
* 🧑‍💻 **适配任意课程格式** —— 不需要元数据，只需要有结构的文件夹。
* 🌐 **中英文双语** —— 右上角按钮可在 English 与中文之间切换整个界面。
  选择会记在浏览器里；服务端自己生成的少量文案（挂载提示）则跟随 SPA 发来的 `Accept-Language` 请求头。
* 🐳 **自托管 Docker 部署**，并带有可配置、加固过的文件访问白名单。

---


## 🛠️ 构建与运行

OfflineU 以**一个静态 Go 二进制**发布，Vue 前端已内置其中，因此运行时既不需要 Python 也不需要 Node。

### ⚡ 一条命令：Makefile

下面的每一步也都做成了 make 目标（GNU Make 4.x；Windows、macOS、Linux 均可）：

| 命令 | 产物 |
| ---- | ---- |
| `make` | 列出所有目标 |
| `make all` | **Docker 镜像 + 64 位镜像文件** —— 完整发布包 |
| `make image` | `offlineu:local` **和** `offlineu:<version>`（linux/amd64，64 位） |
| `make image-save` | `image-dist/offlineu-amd64-<version>.tar`，供离线 `docker load` |
| `make web` | 只构建前端（`web/dist`，开发自检） |
| `make check` | `go vet` + `go test` |
| `make clean` | 删除 `image-dist/` |

> **只以 Docker 形式分发**：镜像与导出的 tar 都是 `linux/amd64`（64 位）；
> CI 另外会发布多架构 manifest（`linux/amd64` + `linux/arm64`）。
> Windows exe 与 fnOS `.fpk` 打包已经移除。

版本号始终取自 git tag，与 `docker/run.ps1` 完全一致：
`HEAD` 打了 `v2.1.2` 就是 `2.1.2`，否则是 `latest`。所以一次带版本号的发布是：

```bash
git tag v2.1.2 && git push origin v2.1.2
make all
```

依赖：**Docker**（构建镜像和 tar）。Go 1.23+ 与 Node 22+ 只在开发自检时需要
（`make check`、`make web`）—— 分发的镜像是在 Dockerfile 内部自己构建前端和二进制的。

### 前置条件

* **Go 1.23+** —— `go version`（Dockerfile 与 CI 使用 Go 1.24；1.23/1.24 都可以）。
* **Node 22+** —— 只用于构建前端（`web/dist`）。打包结果在编译期通过 `//go:embed all:web/dist`
  嵌入，所以 `go build` 需要它（或者运行时用 `--web-dir` 指定）。

### 1. 构建前端

```bash
cd web
npm install
npm run build      # 输出到 web/dist
cd ..
```

> 仓库里始终存在 `web/dist`（由一个纳入版本控制的 `.gitkeep` 保住），
> 所以即使还没跑 `npm run build`，`go build` 也能通过 —— 只是你会看到"仅 API"的提示页。

### 2. 构建二进制

```bash
go build -o offlineu .
./offlineu --check-web     # 校验内置的前端包，然后退出
```

注入版本号（它会出现在 `--help` 和 API 里；CI 从 git tag 取得）：

```bash
go build -trimpath -ldflags="-s -w -X github.com/nickkk333/offlineu/internal/offlineu.Version=1.2.3" -o offlineu .
```

> **只以 Docker 形式分发** —— Dockerfile 自己交叉编译 Linux 二进制
> （`linux/amd64` / `linux/arm64`）；不再有 Windows exe，也不再有 fnOS `.fpk`。
> 本地 `go build` 仅供开发自检。

### 3. 运行

```bash
./offlineu "/path/to/My Course"
```

打开 <http://127.0.0.1:5000>。不带路径参数时，OfflineU 会恢复上次打开的课程
（如果没有记住任何课程，就显示文件夹选择器）。常用参数：`--host 0.0.0.0` 暴露到局域网，
`--port 5000` 换端口 —— 两者都有对应的环境变量 `OFFLINEU_HOST` / `OFFLINEU_PORT`。
完整列表见 **⚙️ 配置**。

热重载开发（Vite + 真正的 Go API）见下面的 **👩‍💻 开发**。
若想不重新编译就跑磁盘上的生产构建，加 `--web-dir web/dist`。

---

## 📚 课程目录结构

OfflineU 不需要元数据：它能识别的每个文件都会成为一个课时，文件夹结构就是课程树
（隐藏文件/文件夹会被忽略，扫描最多 10 层）。

```text
My Course/
├── Section 1 - Getting Started/
│   ├── 01 - Welcome.mp4            → 视频课时（续播 + 连播）
│   ├── 01 - Welcome.srt            → 作为字幕挂上去（转换为 WebVTT）
│   ├── 02 - Notes.md               → 文本课时
│   ├── 03 - Quiz.html              → 测验课时（名字含 "quiz"/"exam"/"test"）
│   └── 04 - Handout.docx           → 文档，提供"在新标签页打开"链接
└── Section 2 - Deep Dive/
    ├── 01 - Lecture.mp3            → 音频课时
    └── resources/
        └── cheat-sheet.pdf         → 内嵌 PDF 课时
```

| 类型 | 扩展名 | 行为 |
| ---- | ------ | ---- |
| 视频 | `.mp4` `.mkv` `.avi` `.mov` `.webm` `.m4v` `.flv` `.wmv` | 内联播放器、续播、播完标记完成 |
| 音频 | `.mp3` `.wav` `.m4a` `.aac` `.ogg` `.flac` | 内联播放器、续播、播完标记完成 |
| 文本 | `.txt` `.md` | 在课时页面以文本渲染 |
| 内嵌 | `.html` `.htm` `.pdf` | 在 iframe 中渲染 |
| 其他文档 | `.docx` `.doc` `.rtf` | 下载/打开链接 |
| 字幕 | `.srt` `.vtt` `.ass` `.sub` `.sbv` | 挂到同目录同名视频上，以 VTT 提供 |

---

## 💾 数据存放在哪里

* **进度** —— 默认 `offlineu_progress.json`（放在课程目录旁）。若设置了
  `OFFLINEU_PROGRESS_DIR`，则每门课程在该目录下得到一份 `<12 位 sha1>-progress.json`
  （Docker 镜像用它，这样课程挂载可以保持只读）。它是一个以课时相对路径为键的扁平 JSON：
  `{"Section 1/01 - Intro.mp4/Intro": {"completed": true, "progress_seconds": 412}}`。
  局部更新永远不会重置已存的值：只提交 `progress_seconds` 会保留完成标记，
  而且观看秒数永远不会倒退。
* **课程台账** —— `offlineu_state.json` 记录当前课程与最近课程列表（最多 20 条）。
  它位于 `OFFLINEU_PROGRESS_DIR`，若未设置则在工作目录旁的 `data/` 文件夹里。
  每条记录都缓存了课时总数（`total_lessons`），这样选择器无需重新扫描课程就能显示完成百分比；
  `completed_lessons` 则在每次 `/api/state` 调用时从进度文件统计。

---

## 🔌 HTTP API

Vue 前端调用的正是上一版暴露的那套 JSON API，所以每个接口都仍然可以用脚本驱动：

| 方法 | 接口 | 用途 |
| ---- | ---- | ---- |
| GET | `/` | SPA 外壳（由 Vue 应用决定显示面板还是选择器） |
| GET | `/api/state` | 当前课程、课程树、最近课程（含完成计数）、`roots_display`、`needs_mount`/`mount_issue`/`roots_detail`/`mount_hint` 与版本号 |
| GET | `/browse?path=<dir>` | 单级目录的 JSON 列表（选择器用） |
| POST | `/load_course` | `{"course_path": "..."}` —— 解析文件夹并设为当前课程 |
| GET | `/api/lesson?path=<lesson>&autoplay=1` | 课时数据：媒体/字幕 URL、文档、相邻课时、连播目标、存储告警 |
| GET | `/lesson/<lesson_path>` | 进入 SPA 课时页面的深链接 |
| POST | `/api/progress` | 局部更新：`{"lesson_path", "completed", "progress_seconds"}`（省略的字段保持原值） |
| GET | `/files/<path>` | 提供当前课程内的文件 |
| GET | `/api/media/status?path=<lesson>` | 浏览器该如何播放这一节：`{"mode":"direct"\|"hls"\|"remux"\|"raw", "ready", "preparing", "hls_url", "error", "duration"}` —— `hls=0` 表示不要提供 HLS |
| GET | `/api/hls/playlist.m3u8?lesson=<path>` | 分片播放的媒体播放列表 |
| GET | `/api/hls/segment?lesson=<path>&i=3` | 该流的一个分片（MPEG-TS），首次请求时转换并缓存 |
| GET | `/subtitles/<path>` | 以 WebVTT 提供字幕（即时转换 SRT） |
| GET | `/health` | `{"status": "healthy"}`（Docker 健康检查用） |
| POST | `/api/reset_course` | 回到选择器；课程仍保留在最近列表里 |
| POST | `/api/forget_course` | `{"path": "..."}` —— 从最近列表移除该课程并删除其进度 |
| GET | `/reset_course`, `/forget_course` | 上述两个接口的旧版重定向变体 |
| GET | `/api/dlna/devices` | 局域网上发现的投屏设备（`?refresh=1` 重新发起 SSDP 搜索） |
| POST | `/api/dlna/cast` | `{"device": "<udn>", "lesson_path": "...", "start_seconds": 30, "transcode": "auto"\|"on"\|"off"}` —— 把一节推到投屏设备；播完会自动接下一节；应答中的 `converted` 说明是否用了转码流 |
| GET | `/api/dlna/stream?lesson=<path>&start=30` | 设备播放时拉取的转换流（MPEG-TS / AAC） |
| GET | `/api/dlna/session` | 正在进行的投屏：设备、课时、位置/时长、状态、下一节 |
| POST | `/api/dlna/control` | `{"device": "<udn>", "action": "play"\|"pause"\|"stop"\|"next"\|"seek", "position": 90}` |

`/api/state` 与 `/api/lesson` 都带 `dlna_enabled`，因此在 `OFFLINEU_DLNA=off` 时界面会隐藏投屏按钮。
`OFFLINEU_DLNA=off` 还会让三个 DLNA 接口返回 `403`。

`/api/browse` 与 `/api/load_course` 作为上述两个选择器接口的别名被接受。

`GET /api/state` 会根据 `Accept-Language` 请求头本地化它的 `mount_hint`（`zh*` → 中文，其他 → 英文），
所以 SPA 切换语言无需重启服务端。

---

## 🎥 播放（以及大文件为什么不再卡顿）

一节课时有四种播放方式，由 `/api/media/status` 按文件决定：

| 模式 | 何时使用 | 代价 |
| ---- | -------- | ---- |
| direct | 文件确实是 MP4/M4V/MOV/WebM/Ogg（扩展名可能骗人） | 无 —— 带 Range 支持直接提供 |
| hls | 容器或编码不是浏览器原生支持，且时长已知 | **每次请求只转一个 6 秒分片** |
| remux | 音频课时、时长未知的文件，或没有 ffprobe | 文件在后台重新封装一次 |
| raw | 没有可用的 ffmpeg | 原样把字节交给浏览器 |

差别在性能较弱的机器上最明显（比如一台装了 fnOS/飞牛OS 的迷你主机 NAS）。
以前，浏览器放不了的文件要**先从头到尾重新封装完，第一个字节才到播放器** ——
播放器干等着，期间读写了几个 GB；遇到冷门编码还要整体重编码，弱 CPU 根本做不到实时。现在：

* **HLS** 把一节切成约 6 秒的分片，切点落在关键帧上，浏览器只请求它马上要播的那些分片。
  起播只花一个分片，跳到中间也只花一个分片；如果视频流本来就是 H.264，就直接**拷贝**
  （`-c copy`）—— 只解复用再复用，占用不到一个核。只有浏览器解不了的流
  （HEVC、VP9、DTS 等）才重编码，而且只编码那一条流：H.264 视频 + DTS 音频会保留视频原样。
* **每次探测都有缓存。** 读取容器意味着解复用整个文件，而浏览器在拖动时会反复请求同一节。
  探测结果会被记住，直到文件发生变化（大小 + 修改时间），因此从"每个请求一次 ffprobe"
  变成了"每个文件一次"。
* **没有任何环节被转换阻塞。** 整体 remux 在后台跑，页面显示 *准备中*，
  文件就绪后自动开始播放。
* **分片缓存在磁盘上**并复用，所以重看（或往回跳）是零成本的。

在小 NAS 上有两个设置很关键：

```yaml
environment:
  # 转换后的分片放哪里。默认是系统临时目录，而在 NAS 上那往往是容量很小的系统盘 —— 改成大分区。
  - OFFLINEU_CACHE_DIR=/app/cache
  # 按最旧文件优先清理，所以永远不会把磁盘塞满（默认 20 GiB）。
  - OFFLINEU_CACHE_LIMIT_GB=20
volumes:
  - ./cache:/app/cache
```

### 真的必须重编码时

拷贝几乎不花代价；重编码才是弱机顶不住的地方，所以有两件事把它压下来：

* **720p 上限** —— 需要重编码的视频会被缩到 `OFFLINEU_TRANSCODE_HEIGHT`（默认 720）。
  这个上限只会缩小，绝不会放大：1080p 变 720p，720p 保持 720p，480p 保持 480p。
  `OFFLINEU_TRANSCODE_HEIGHT=0` 保持原始尺寸，
  `OFFLINEU_TRANSCODE_PRESET=ultrafast` 在很慢的 CPU 上用画质换帧率。
* **硬件编码** —— `OFFLINEU_HWACCEL=auto`（默认）时，OfflineU 会问一次 ffmpeg 支持哪些硬件编码器，
  确认设备确实存在，然后用第一个既能解码又能编码的方案：

  | 后端 | 编码器 | 解码器 | 平台 | 需要 |
  | ---- | ------ | ------ | ---- | ---- |
  | VideoToolbox | `h264_videotoolbox` | `videotoolbox` | macOS | — |
  | NVENC | `h264_nvenc` | `cuda` | Linux、Windows | `/dev/nvidia*` |
  | Quick Sync | `h264_qsv` | `qsv` | Linux、Windows | `/dev/dri` |
  | VAAPI | `h264_vaapi` | `vaapi` | Linux | `/dev/dri/renderD*` |
  | AMF | `h264_amf` | `d3d11va` | Windows、Linux | Linux 上为 `/dev/kfd` |
  | V4L2 M2M | `h264_v4l2m2m` | `v4l2m2m` | Linux（ARM、树莓派） | `/dev/video*` |
  | RKMPP | `h264_rkmpp` | `rkmpp` | Linux（Rockchip） | `/dev/dri` 或 `/dev/mpp_service` |

  解码器是加分项，不是必需项：只有编码器没有解码器的构建，仍然能把开销大的那一半交给硬件。
  被拷贝的流完全不参与以上流程。如果硬件路径失败 —— 驱动无法分配内存、编码器拒绝该像素格式 ——
  就会关掉 GPU，用 libx264 重试同一次转换，所以任何驱动都不会让一节课时变得无法播放。
  `OFFLINEU_HWACCEL=off` 关闭探测；写上面的任意一个名字
  （或 `cuda`、`nvidia`、`vt`、`v4l2`、`rockchip`）则强制使用某一种。

在 Docker 里，只有把设备节点交给容器，GPU 才可见：

```yaml
    devices:
      - /dev/dri:/dev/dri      # Intel / AMD / Rockchip 核显（VAAPI、QSV、RKMPP）
      # - /dev/nvidia0:/dev/nvidia0   # NVIDIA（NVENC）—— 还要 nvidiactl、nvidia-uvm
      # - /dev/video0:/dev/video0     # ARM V4L2 内存到内存
```

如果你的素材大多是 HEVC/4K，连一个分片都转得太慢，最省事的办法仍然是在一台正经电脑上
把片库整体转一次（`ffmpeg -i in.mkv -c:v libx264 -crf 23 -preset fast -c:a aac -movflags
+faststart out.mp4`），或者把这一节投屏到电视/播放器，让**它**去解码 ——
此时服务端只做重新封装，永不重编码。

---

## 📺 投屏（DLNA / UPnP）

课时页面上的 **📺 投屏** 按钮会列出局域网里的媒体渲染设备，并把这一节的视频或音频投到你选中的那台上。
电视上不需要装 App，不需要账号，也不需要云服务：

1. OfflineU 广播 **SSDP `M-SEARCH`**（`MediaRenderer:1` 和 `ssdp:all`），并监听约 2 秒。
2. 每个应答的设备都会被拉取**设备描述**；其中暴露 `AVTransport` 服务的成为投屏目标。
3. 选中一台后发送 **`SetAVTransportURI`**（绝对地址 `http://<你的主机>:5000/files/…` 加 DIDL-Lite 元数据）
   紧接着 **`Play`** —— 如果这一节有保存的进度，还会发一次 `Seek`，让电视从浏览器停下的位置继续。
4. 设备播放期间，**暂停 / 播放 / 停止** 在同一个菜单里始终可用。

它需要什么：

* **局域网可达的地址。** 渲染设备会自己下载文件，所以 OfflineU 用浏览器访问时所用的主机来拼 URL
  （支持 `X-Forwarded-Host`/`Proto`）。在运行它的机器上用 `http://localhost:5000` 打开没问题；
  但电视访问不到这个主机名，所以要打开局域网地址（例如 `http://192.168.1.5:5000`），
  或者设置 `--host 0.0.0.0`。
* **同一网络内的组播。** 在 Docker 里，只有使用 host 网络（或 macvlan）时 SSDP 搜索才能到达局域网：
  给服务加 `network_mode: host`，或者直接在主机上运行 OfflineU。否则投屏菜单会一直是空的。
* **设备能理解的格式** —— 见下文，OfflineU 会转换它必须转换的部分。

#### "我的电视放不了这个文件" —— 兼容模式

大多数渲染设备会拒绝**容器**（`.mkv`、`.avi`、`.flv`、`.wmv`、`.webm`），
哪怕里面的流它其实能解。因此 OfflineU 会逐节判断设备是否可能拒收，
如果可能，就给设备一个**流而不是文件** —— `/api/dlna/stream`，由 ffmpeg 在设备已经开始播放时产出：

| 情况 | OfflineU 的做法 | 代价 |
| ---- | --------------- | ---- |
| `.mp4`/`.m4v`/`.mov` + H.264 + AAC/MP3/AC3 | 原样把文件交给设备 | 无 |
| `.mkv`/`.avi`/… 但编码没问题 | **重新封装**为 MPEG-TS（`-c copy`） | 一个核的几个百分点 |
| HEVC/VP9、DTS、WMA 等（设备解不了的编码） | **重编码**为 H.264 + AAC | 真吃 CPU，但能播 |
| 音频课时（`.flac`、`.wav`、`.ogg` 等） | 重编码为 AAC（mp3 保持 mp3） | 很小 |

因为转换是在播放过程中进行的，所以续播位置是作为 `-ss` 传给 ffmpeg 的
（设备无法在直播流里跳转），字幕和附件会被丢掉，而设备停止或断开时进程立即结束。

**浏览器播放**和**投屏**都靠 ffmpeg 干重活：它把浏览器或渲染设备本来会拒收的一节重新封装 ——
名不副实的 `.mp4`（其实是 MPEG-TS）或 `.mkv` 会变成浏览器原生的 MP4；
投给电视的 `.mkv` 会变成 MPEG-TS。没有它，OfflineU 只能把原始文件交给客户端，然后播放失败。

* Docker：镜像**始终**自带 ffmpeg（无需构建开关）—— 镜像大约大 90 MB，但每一节都能播，
  浏览器和电视都可以。
* 本地运行（任意系统/架构，包括 fnOS/飞牛OS 这样的 ARM NAS）：机器上找不到 ffmpeg 时，
  OfflineU 会在首次播放时自动下载一份静态 ffmpeg —— 覆盖 Windows、Linux amd64/arm64 和 macOS，
  所以镜像不含 ffmpeg 的设备也能播放；也可以把 `OFFLINEU_FFMPEG` / `OFFLINEU_FFPROBE`
  指向已有安装，或者设 `OFFLINEU_NO_AUTOFFMPEG=1` 关掉下载。
  （自动下载的版本会同时取 ffmpeg 和 ffprobe；如果 ffprobe 取不到，
  OfflineU 仍能用 `ffmpeg -i` 识别容器与编码。）

投屏菜单里的**兼容模式**会按服务端为该节算出的结论预置
（`/api/lesson` 返回 `cast_plan: {needs_transcode, transcode_available, reason}`），
也可以手动切换：关掉就推送未改动的文件（完全不占 CPU），
打开则在设备比 OfflineU 预想的更挑剔时强制转换。

### 在浏览器里跟随投屏

画面在电视上，但页面并不会"失明"：`/api/dlna/session` 会报告设备播到哪儿了，
课时页面把它画成进度条（暂停/播放中、位置、时长、下一节是什么），
同时那些按钮可以从浏览器暂停、跳过和停止设备。

* 位置来自设备本身（`GetPositionInfo`），只要它应答；这也能捕捉到用电视遥控器做的暂停或跳集；
  拒绝该调用的设备则回退到时钟推算，界面会显示"估算"。
* **投屏写入的进度和播放器完全一样。** 位置与完成状态写进同一份 `.offlineu_progress.json`、
  同一个键，所以在电视上看完的一节会同样计入课程总进度、课程树和"接着上次继续"卡片 ——
  规则也相同：秒数永不倒退，完成标记只在课时真的播到结尾时才写入
  （重看已完成的一节会保持完成）。播放中每约 15 秒写一次，结束时写一次，被停止时写一次。
* **课时长度决定了到底能不能看到"结束"**，所以它取自任何能找到的地方：
  ffprobe（没有 ffprobe 时用 `ffmpeg -i`）、浏览器加载文件时测出的长度，
  以及播放期间设备上报的 `TrackDuration`。如果三者都拿不到，
  投屏条仍会显示位置，但无法判断课时已经播完；此时进度条会说明情况，由你手动标记完成。
* **点击进度条任意位置即可跳转。** 直接投原文件时在设备上跳转（`Seek` 带 `REL_TIME`）；
  转换流没有可跳转的时间轴，所以用 `-ss` 重启 ffmpeg 并把新 URL 给设备。
  跳过结尾时会停在结尾前 1 秒，这样课时仍能正常播完并续播。
* **播放总是继续**：一节播完，下一节可播放的课时会自动开始 —— 浏览器播放和投屏都一样
  （文档会被跳过），浏览器会跟着跳到新的课时。没有任何模式需要选择。
  投屏时下一节由服务端里的看门狗**推送给同一台设备**，而不是靠页面，所以关掉浏览器也照样继续。
* 投屏本身存在于服务端，所以关闭浏览器（或换一个浏览器）不会打断播放 ——
  新窗口只要从 `GET /api/dlna/session` 接管正在进行的投屏并显示投屏条即可。
* 投屏条不限于课时页面：**面板上也会显示**（带一个跳到正在播放课时的链接），
  所以你浏览课程树时也能看进度、暂停或跳过。设备一换课时，课时页面就跟着走 ——
  比较是基于路由做的，因此即使某一节还在加载也能生效。

投屏可以整体关闭：`OFFLINEU_DLNA=off` 会隐藏按钮，并让 `/api/dlna/*` 返回 `403`。
发现结果缓存 30 秒；**⟳ 重新搜索** 会立即再搜一次。

---

## 🗂️ 项目结构

```text
OfflineU/
├── main.go                     # 命令行、嵌入前端、HTTP 服务启动
├── internal/offlineu/
│   ├── config.go               # 参数/环境变量、OFFLINEU_ROOTS 白名单、路径工具
│   ├── model.go                # Course / Lesson / 文件类型表、JSON 序列化
│   ├── parser.go               # 文件夹 → 课程树、测验识别、文本资源
│   ├── paths.go                # URL 转义、递归扫描工具
│   ├── progress.go             # 进度文件读写、观看时长规则
│   ├── store.go                # 当前课程 + 最近课程（offlineu_state.json）
│   ├── subtitles.go            # SRT → WebVTT 转换（含 CP1252 回退）
│   ├── dlna.go                 # SSDP 发现、设备描述、SOAP 投屏
│   ├── transcode.go            # 可选的 ffmpeg：探测、重新封装/重编码、直播流
│   ├── castsession.go          # 正在进行的投屏：位置、看门狗、自动续播下一节
│   ├── server.go               # 路由、JSON API、静态文件与 SPA 回退
│   └── *_test.go               # Go 测试套件
└── web/                        # Vue 3 + Vite 前端（构建到 web/dist 并被嵌入）
    ├── src/{api.js,store.js,router.js,i18n.js,views,components,composables,styles}
    │                           # i18n.js：en/zh 词典、`t()` 助手、语言切换
    └── vite.config.js          # 开发服务器把 API 代理到 :5000
```

---

## 👩‍💻 开发

开两个终端即可在前端热重载的同时，由真正的 Go 后端提供数据：

```bash
# 终端 1 —— API 跑在 http://127.0.0.1:5000
go run . "/path/to/My Course"

# 终端 2 —— Vite 开发服务器跑在 http://127.0.0.1:5173（代理 /api、/files 等）
cd web
npm install
npm run dev
```

要生产构建：先 `cd web && npm run build`，再 `go build -o offlineu .` ——
编译后的包会被嵌入二进制。`--web-dir web/dist` 则改为从磁盘提供该包
（调试 `npm run build` 时很方便）。

### 行尾（CRLF / LF）

本仓库**只用 LF**，由 `.gitattributes` 强制：

```gitattributes
* text=auto eol=lf                 # 以 LF 存储，在所有系统上都以 LF 检出
*.bat / *.cmd                      # 唯一的 CRLF 例外（cmd.exe 需要）
*.png / *.tar / *.exe / …  # 二进制文件，绝不改动
```

这些属性优先于 `core.autocrlf`，所以 Windows 检出不再把工作区重写成 CRLF
（以前这会让 76 个文件永久显示为"已修改"，还可能弄坏在 Linux 上运行的 shell 脚本）。
改完 `.gitattributes` 后，做一次重新规范化：

```bash
git add --renormalize .
git status        # 应当恢复干净
```

---

## 🧪 测试

Go 测试套件只使用标准库，因此完全离线运行：

```bash
go test ./... -count=1
```

它覆盖文件夹解析、字幕挂载、文档模式、连播目标选择、进度持久化
（包括"重看课时不得重置进度"这个回归用例）、路径穿越防护、`OFFLINEU_ROOTS` 白名单、
JSON API、课程台账、命令行，以及 DLNA 层（设备描述解析、一次投屏产生的 SOAP 请求、UPnP 错误上报）。

一些顺手的辅助命令：

```bash
gofmt -l .          # 格式检查
go vet ./...        # 静态检查
go run . --check-web # 前端包是否已嵌入？
```

### 端到端场景（`e2e/`）

`e2e/` 以浏览器或电视的方式驱动一个**正在运行的**服务端，
这样被验证的是一种部署形态（原生二进制、容器、Linux 主机），而不是单一代码路径：

| 文件 | 覆盖内容 |
| ---- | -------- |
| `e2e/api.mjs` | JSON API：课程加载、进度、课时的连播目标、`/health`、DLNA 接口 |
| `e2e/browser.mjs` | 真实的 Edge：播放器工具栏、倍速偏好、连播跳到下一节、`?autoplay=1` |
| `e2e/cast.mjs` | 投屏：SSDP 发现、SOAP 请求、投屏条、看门狗走完整个课程 |

```bash
cd e2e
npm install                                # 只需 playwright-core
node api.mjs     http://127.0.0.1:5100     # 第 3 个参数可选：首次加载用的课程路径
node browser.mjs http://127.0.0.1:5100     # 需要已安装 Edge（channel: 'msedge'）
node cast.mjs    http://127.0.0.1:5100     # 自己启动 e2e/fakerenderer.exe
```

它们要求服务端根目录下有一门名为 `E2E Course` 的课程，内含 6 秒的短片
（`Section 1/01 - Alpha.mp4`、`Section 1/02 - Notes.txt`、`Section 1/03 - Beta.mp4`、
`Section 2/04 - Gamma.mp4`）。

`cast.mjs` 会跑一个假的 DLNA 渲染器（`cd e2e/fakerenderer && go build`），
它应答真实的 SSDP 与 SOAP 流量，所以服务端必须能通过组播访问到它。
这在原生环境和带 `--network host` 的容器里可行，但**不行**于 Docker Desktop 的桥接/NAT：
请在真实的 Linux 主机（fnOS、NAS）上验证投屏，或用 WSL 部署（Windows 侧的渲染器通过 WSL 网络接口被发现）。
API 与浏览器套件与形态无关，已分别在原生二进制、Docker Desktop（桥接）和 WSL2 上验证过。

---

## 🏷️ 打标签与发布

### 什么时候打标签

向 `main`/`master` 推送一个匹配 `v*` 的标签（例如 `v1.2.3`）即完成一次发布。
CI 工作流（`.github/workflows/docker-build.yml`）随后会自动构建、测试并发布 GitHub Release ——
无需手动上传。请遵循**语义化版本号**：

* `vMAJOR.0.0` —— 破坏性变更 / 重大里程碑
* `vMAJOR.MINOR.0` —— 新功能，向后兼容
* `vMAJOR.MINOR.PATCH` —— 仅缺陷修复 / 小改动

```bash
git tag v1.2.3
git push origin v1.2.3
```

> **不带**标签推送 `main`/`master` 同样会构建并发布一切，只是版本号用 **`latest`** 而不是数字：
> 镜像以 `ghcr.io/<repo>:latest` 推送（外加 `:main` / `:sha-…`），
> 本次运行的产物是 `offlineu-amd64-latest.tar`（64 位镜像文件）。此时不会创建 GitHub Release；
> 产物保留 14 天，且会被下一次推送覆盖。

### 发布包含什么

每次推送一个 `v*` tag，Release 会自动附带以下资源：

| 资源 | 说明 |
| ---- | ---- |
| `offlineu-amd64-<version>.tar` | Docker 镜像文件 `linux/amd64`（64 位），离线 `docker load -i`，无需镜像仓库 |
| `checksums.txt` | SHA-256 校验和 |

### 版本流转

版本号取自 git tag（`v2.0.0` → `2.0.0`），并在构建时注入到所有地方，
所以一次发布的每个产物都共用同一个号：

* **Docker 镜像** —— 推送到 GitHub Container Registry，即 `ghcr.io/nickkk333/offlineu:2.0.0`，
  外加滚动标签 `:2.0` 和 `:2`。`:latest` 标签保留给**分支推送**
  （推送到 `main` → `ghcr.io/<repo>:latest` 自动重建）。
* **Docker 镜像文件** —— `offlineu-amd64-2.0.0.tar`，同一镜像用 `docker save` 导出
  （64 位 `linux/amd64`），供没有镜像仓库的机器使用。
* **`checksums.txt`** —— 所有附件的 SHA-256，用于校验下载。

> 任何不是带标签提交的东西（分支 / PR / 本地构建）在所有地方都用 **`latest`** ——
> `--build-arg VERSION`、镜像标签和 tar 文件名 —— 所以开发构建永远不会与真正的发布号冲突，
> 版本号也永远不用手打（`docker/run.ps1` 和 Makefile 都从 git 推导）。

### 用 Docker 跑

```bash
# 固定版本
docker pull ghcr.io/nickkk333/offlineu:2.0.0
# 或跟随最新：每次推送 main 都会自动重建并更新 :latest
docker pull ghcr.io/nickkk333/offlineu:latest
docker run -d --name offlineu -p 5000:5000 \
  -v "/path/to/your/courses:/courses:ro" \
  -v offlineu-data:/app/data \
  ghcr.io/nickkk333/offlineu:2.0.0
```

---

## 🐳 Docker

你的课程文件夹没有任何内容被打进镜像：`/courses` 只是它期待的挂载点。
把你自己的文件夹映射上去，进度保存在 `/app/data`，这样课程挂载就能保持只读。

### 本地构建镜像

镜像是三阶段构建（Node → Go → Alpine），内置前端**以及** ffmpeg，
因此完全自包含（几十 MB，运行时不需要 Python/Node）。直接在仓库根目录构建：

```bash
docker build -t offlineu .
```

按 CI 的方式注入版本号（它会成为二进制里的 `Version`，并显示在界面/API 中）：

```bash
docker build --build-arg VERSION=1.2.3 -t offlineu:1.2.3 .
```

用 Buildx 做多架构（amd64 + arm64）—— 推送到仓库，或只加载回本地：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=1.2.3 -t offlineu:1.2.3 --load .
```

运行本地构建的镜像（下面例子里的 `:main` 标签可以换成 `offlineu:1.2.3`）：

```bash
docker run -d --name offlineu -p 5000:5000 \
  -v "/path/to/your/courses:/courses:ro" \
  -v offlineu-data:/app/data \
  offlineu:1.2.3
```

```bash
docker run -d --name offlineu -p 5000:5000 \
  -v "/path/to/your/courses:/courses:ro" \
  -v offlineu-data:/app/data \
  ghcr.io/nickkk333/offlineu:main
```

等价的 Compose 写法：

```yaml
services:
  offlineu:
    image: ghcr.io/nickkk333/offlineu:main
    ports: ["5000:5000"]
    environment:
      - OFFLINEU_ROOTS=/courses
      # 可选：界面上显示的别名，替代挂载点名称
      # - OFFLINEU_ROOTS_LABEL=My courses
      - OFFLINEU_PROGRESS_DIR=/app/data
    volumes:
      - ./courses:/courses:ro
      - ./data:/app/data
```

```bash
docker compose up -d          # 或者：docker build -t offlineu . && docker run ...
```

投屏需要局域网的 SSDP 组播，而 Docker 默认的桥接网络不会转发：
给服务加 `network_mode: host`（并去掉 `ports` 映射，容器会直接使用主机网络），
📺 投屏按钮才能找到你的电视。

### 本地开发循环 —— 用主机网络构建并运行（Windows / PowerShell）

```powershell
.\docker\run.ps1
```

一条命令做两件事：从本目录构建镜像，并以 `--network host` 启动容器 `offlineu-dev`
（重复执行会替换旧容器）。版本号从 git 推导，所以永远不用手打：

| HEAD | 镜像 | `VERSION` 构建参数（二进制 / `/api/state`） |
| ---- | ---- | ---------------------------------------- |
| 打了 `v1.2.3` 标签 | `offlineu:1.2.3` | `1.2.3` |
| 其他（开发） | `offlineu:latest` | `latest` |

| 参数 | 含义 |
| ---- | ---- |
| `-Port 8080` | 监听端口（默认 `5000`） |
| `-Courses <dir>` / `-Data <dir>` | 覆盖默认的 `./courses` 与 `./data` 挂载 |
| `-Foreground` | 前台运行，Ctrl-C 停止 |
| `-Bridge` | 用 `-p <port>:<port>` 代替主机网络 |
| `-NoBuild` | 复用已有镜像，跳过 `docker build` |
| `-Logs` | 启动后跟踪 `docker logs -f` |

`-Bridge` 是 Windows/macOS Docker Desktop 的兜底方案，因为那里的 `--network host`
指的是 Docker Desktop 背后的 Linux 虚拟机，而不是你的桌面（所以桌面上的
`http://localhost:5000` 访问不到它）；在 WSL 里运行该脚本则保留真正的主机网络。
多架构发布镜像仍然只由 `.github/workflows/docker-build.yml` 构建。

### 离线安装 —— 导出一个可 `docker load` 的 tar

镜像是自包含的，所以一台没有镜像仓库（甚至没有网络）的机器只需要这个 tar：
在有网的地方构建一次，把文件带过去，在那边加载。

```bash
# 1. 在联网机器上构建。`--provenance=false --sbom=false` 是可选的：它只是把两份证明 manifest
#    排除在归档之外（16 → 12 个条目，省几 KB）。
docker build --provenance=false --sbom=false -t offlineu:1.0.0 .
# （或：在 docker-compose.yml 里取消 `build: .` 的注释，然后 docker compose build）

# 2. 打上 docker-compose.yml 期望的标签，这样 tar 在另一端可以直接用
docker tag offlineu:1.0.0 ghcr.io/nickkk333/offlineu:main

# 3. 把两个标签导出到一个文件（约 12 MB）。不要压缩 —— 见下面说明。
docker save -o offlineu-1.0.0.tar offlineu:1.0.0 ghcr.io/nickkk333/offlineu:main
```

把 `offlineu-1.0.0.tar` 拷到目标机器，然后：

```bash
docker load -i offlineu-1.0.0.tar     # -> Loaded image: offlineu:1.0.0（+ ghcr.io/...:main）
docker compose up -d                  # compose 文件已经指向该标签
```

…或者不用 Compose 直接跑：

```bash
docker run -d --name offlineu -p 5000:5000 \
  -v "/path/to/your/courses:/courses:ro" \
  -v offlineu-data:/app/data \
  ghcr.io/nickkk333/offlineu:main
```

说明：

* 这个 tar 就是普通的 docker 归档：`manifest.json`（外加 OCI 元数据）就在里面，
  所以 `docker load -i` 一次还原**两个**标签，`docker compose up -d` 立刻就能找到
  `ghcr.io/nickkk333/offlineu:main`，既不需要镜像仓库也不需要重新构建。
  tar 大约 9 MB，加载后的镜像约 32 MB。
* **不要给 tar 做 gzip**：`docker load -i offlineu.tar.gz`（以及 `docker load < offlineu.tar.gz`）
  会报 `unrecognized image format`。原样传输，或先在目标机上解包 ——
  `gunzip -c offlineu-1.0.0.tar.gz > offlineu-1.0.0.tar && docker load -i offlineu-1.0.0.tar`
  （导出侧用 `gzip -c`/`tar -czf` 没问题）。
* **目标 CPU 与构建机不同？** 一步为它构建并写出 tar：
  `docker buildx build --provenance=false --sbom=false --platform linux/amd64 --output type=docker,dest=offlineu-1.0.0.tar -t offlineu:1.0.0 .`
  （树莓派 / ARM NAS 换成 `linux/arm64`；若还想要镜像进入本地存储，把 `--output ...` 换成 `--load`）。
* 加载后自检：`docker image inspect ghcr.io/nickkk333/offlineu:main --format '{{.Id}} {{.Os}}/{{.Architecture}} {{.Size}}'`，
  跑起来后再 `curl http://127.0.0.1:5000/health` → `{"status":"healthy"}`。
* 在 Windows PowerShell 上同样的命令可以直接用，只是文件写成 `.\offlineu-1.0.0.tar`
  （`image-dist/` 里的那个 tar 就是这样构建并做过加载测试的）。
* 想在离线环境 `docker build`？那就把三个基础镜像也带上：
  `docker save -o bases.tar node:22-alpine golang:1.24-alpine alpine:3.21`。

### NAS 的 Docker 图形界面（飞牛OS / fnOS、群晖、Unraid 等）

镜像声明了两个挂载点（`VOLUME ["/courses", "/app/data"]`），
所以 NAS Docker 应用的*创建容器*向导已经列出了它们 —— 你只需填两个主机文件夹。
没有这个声明，Docker 会悄悄挂上临时的匿名卷，你的进度就会随容器一起消失。在飞牛OS 上：

1. **导入镜像** —— 镜像 → 导入，选择上一节产出的 `offlineu-1.0.0.tar`
   （或 `image-dist/` 里那个）；导入不需要联网，也不需要登录镜像仓库。
2. **创建容器** —— 容器 → 新增，选择 `ghcr.io/nickkk333/offlineu:main`，
   并按向导显示映射这两个文件夹：

   | 容器路径 | 飞牛OS 上的主机文件夹（示例） | 权限 | 存放内容 |
   | -------- | ---------------------------- | ---- | -------- |
   | `/courses` | `/vol1/1000/courses` | 只读 | 你的课程库 |
   | `/app/data` | `/vol1/1000/offlineu-data` | 读写 | 进度 + 课程列表 |

   `vol1` 是飞牛OS 的第一个存储空间，`1000` 是第一个用户 —— 两行都指向你也能通过 SMB 访问的文件夹，
   如果你的布局不同就换成自己的路径。给 `/app/data` 一个单独的文件夹：
   OfflineU 会在那里写 `offlineu_progress*.json` 和 `offlineu_state.json`，
   正因为如此 `/courses` 才能保持只读，并能与其他工具共享。
3. **端口映射** —— 5000 → 5000（任何空闲的主机端口都行，例如 15000 → 5000）。
4. **环境变量保持原样** —— 镜像已经设置了 `OFFLINEU_ROOTS=/courses`、
   `OFFLINEU_PROGRESS_DIR=/app/data`、`OFFLINEU_HOST=0.0.0.0` 和 `OFFLINEU_PORT=5000`，
   不需要手动加任何东西。
5. 打开 `http://<nas-ip>:5000`。如果 `/courses` 仍然是空的，选择器会说明情况并重复这两个映射要求；
   右上角的开关可在 English 与中文之间切换整个界面。

以后重建容器（换了镜像版本、改了文件夹），只要把 `/app/data` 映射回同一个位置，
一切都会保留 —— 最近课程、续播位置和已完成课时都在那里。

#### "还没有映射课程文件夹" —— 选择器到底在说什么

选择器不再靠猜：`/api/state` 返回 `mount_issue`，外加每个已映射文件夹的 `roots_detail`
（路径、可读、条目数、错误），卡片会点出真实情况，并显示一份带对应解决办法的
**📌 映射状态 / 📌 Mapping status** 列表。`docker logs <container>` 在启动时也打印同样的列表。

| 卡片标题 | 含义 | 解决办法 |
| -------- | ---- | -------- |
| `📂 /courses is mapped, but still empty` | 映射是成功的 —— 确实绑定了主机文件夹，只是里面没有任何非隐藏文件，连更深层级也没有。 | 把课程拷进该主机文件夹，然后按 **⟳ Check again**。无需重新构建，无需重启。 |
| `🔒 /courses is mapped, but the container cannot read it` | 容器以 uid 10001 运行，而该主机文件夹只允许 root（或另一个 NAS 用户）读取。 | `chmod -R a+rX /vol1/1000/courses`（飞牛OS：给所有人授予读取权限），确认容器没有丢掉内置的读取 capability（见下面「权限：到底由哪个用户读取你的文件夹」一节），或者以文件夹属主身份运行容器（`user: "1000:1000"`），万不得已时以 root 运行（`--user 0:0`）。 |
| `🔌 Docker put a throwaway volume on /courses instead of your folder` | 没有绑定任何主机文件夹：Docker 挂了个匿名卷（或命名卷）。你的课程在这里不可见，写进去的任何东西都会在重建容器时丢失。 | 把一个主机文件夹映射到 `/courses`（再映射一个到 `/app/data`）并**重建**容器 —— 就是常见的"我选了文件夹，却填错了列"。 |
| `🔌 Nothing is mounted on /courses inside the container` | 该路径上没有任何挂载，且文件夹是空的。 | 同上：映射根本没进到容器里。 |
| `📍 The mapping did not arrive: /courses is missing in the container` | 路径本身不存在（很少见 —— 容器运行期间被删掉了）。 | 重建容器。 |
| `🔌 No course folder is mapped yet` | `OFFLINEU_ROOTS` 解析为空，所以一个文件夹都没检查。 | 按上面的表格补上两个映射。 |

文件夹是实时读取的：往已映射的文件夹里加文件，按 **⟳ Check again** 就会出现，无需任何重启。
只有**映射**被改动/新增时才需要重建容器。

#### 权限：到底由哪个用户读取你的文件夹

镜像以非特权用户 `offlineu`（**uid/gid 10001**）运行，并给这个 uid 一份*读取 capability*：
`/app/offlineu-cap` 是同一个二进制加上 `cap_dac_override=ep`，
只要 Docker 允许 exec，`/entrypoint.sh` 就启动这份副本。
因此映射进来的文件夹可以属于 `root` 或另一个 NAS 账号，仍然可读 ——
**不用勾选「使用高权限执行容器」，也不用在 NAS 上做任何 `chmod`**
（细节与实测数据见下面的小节）。

只有当 capability 不可用时 —— 容器把它丢掉了，或者网络共享自己校验权限 ——
一个 uid 10001 打不开的文件夹才会被报成 `🔒 mapped, but the container cannot read it`，
即便映射是正确的。请按以下顺序选一种：

| 选项 | 做法 | 说明 |
| ---- | ---- | ---- |
| 1. 让文件夹可读 | `chmod -R a+rX /vol1/1000/courses` —— 在飞牛OS 上也可以在文件夹的权限对话框里给*所有人*授予读取权限 | 对 Docker 没有任何改动；只读映射仍然是只读。 |
| 2. 以文件夹属主身份运行 | 飞牛OS 的第一个用户通常是 `1000:1000`（可通过 SSH 用 `id <user>` 确认）。在 NAS 容器对话框里找 **用户/User**（运行身份）字段 —— compose：`user: "1000:1000"`，CLI：`docker run --user 1000:1000` | 通常是 NAS 上最好的答案：同一个 uid 一般也拥有 `/vol1/1000/offlineu-data`，所以进度照常可用。这也是 `/app/data` 也要保持映射的原因。 |
| 3. 以 root 运行 | NAS 对话框：用户填 `root`；compose：`user: "0:0"`；CLI：`docker run --user 0:0` | 权限最大。此时进度文件以 root 创建，如果以后换回 uid 10001，需执行 `chown -R 10001:10001 <data folder>`。 |

**是否以 root 运行必须在创建容器时决定。** 以 uid 10001 启动的进程无法自我提权，
所以 `--user 0:0`、compose 的 `user: "0:0"` 和 NAS 对话框里的用户字段是仅有的开关，
而且之后不能改 —— `docker update` 不会动用户。要改就用相同的两个文件夹映射重建容器：

```bash
# 以 /vol1/1000/courses 的属主运行（飞牛OS 上推荐）
docker run -d --name offlineu -p 5000:5000 \
  --user 1000:1000 \
  -v "/vol1/1000/courses:/courses:ro" \
  -v "/vol1/1000/offlineu-data:/app/data" \
  ghcr.io/nickkk333/offlineu:main

# 最后手段：以 root 运行（仅在权限无法改动时）
docker run -d --name offlineu -p 5000:5000 \
  --user 0:0 \
  -v "/vol1/1000/courses:/courses:ro" \
  -v "/vol1/1000/offlineu-data:/app/data" \
  ghcr.io/nickkk333/offlineu:main
```

在 NAS 图形界面里，两种写法是同一个对话框，只有用户字段不同；用 Compose
（飞牛OS 的"Compose 项目"、Portainer 等）则是服务的 `user:` 一行。
选择器打开时，你也可以在主机上 `chmod` 之后按 **⟳ Check again** —— 权限变更不需要重启。

##### 内置的读取 capability（以及「使用高权限执行容器」做不到什么）

`/api/state` 带有 `read_capability`，启动日志也会打印一行 `Read access:`，所以状态永远不用猜：

| 容器启动方式 | `read_capability` | 只有 root 能读的文件夹 |
| ------------ | ----------------- | ---------------------- |
| 无特殊设置（飞牛OS：不勾选任何框） | `true` | 可读 —— `/app/offlineu-cap` 以 `CAP_DAC_OVERRIDE` 运行 |
| 「使用高权限执行容器」（`--privileged`） | `true` | 可读 —— 但起作用的并不是那个勾选框 |
| `--cap-add DAC_OVERRIDE` | `true` | 可读 —— capability 仍在 bounding set 中 |
| `--cap-drop DAC_OVERRIDE`、`--cap-drop ALL`、`no-new-privileges` | `false` | 不可读 —— entrypoint 回退到 `/app/offlineu`，选择器列出上面的解决办法 |

有两个事实值得知道，都是在真实容器里读 `/proc/1/status` 实测得出的：

* **仅靠高权限容器并不能给予读取权限。** `--privileged` 放宽的是容器的 *capability bounding set*
  （`CapBnd: 000001ffffffffff`），但它不改变容器以哪个用户运行；
  而 Linux 会在非 root 进程执行一个不带文件 capability 的文件时清空其 capability 集合
  （`CapEff: 0000000000000000`）。所以即使 Docker 显示容器是特权容器，文件夹依然不可读。
  这就是为什么 capability 要放在文件本身上：它属于这个二进制，因此对 uid 10001 生效，无需任何勾选。
* **没有密码可输。** 把 NAS 密码交给容器也帮不上忙：密码在 NAS 的 `/etc/shadow` 里，
  不在容器里；而以 uid 10001 启动的容器根本无法变成 root —— 用户在创建容器时就固定了，
  之后 `docker update` 也改不了。真正管用的开关是 `CAP_DAC_OVERRIDE`，而镜像已经替你设好了。

在运行中的容器里检查（也可以在离线镜像 tar 被 `docker load` 之后检查）：

```bash
docker exec offlineu-app getcap /app/offlineu-cap              # -> cap_dac_override=ep
docker exec offlineu-app /app/offlineu-cap --cap-check; echo $?  # -> capability 生效时为 0
```

该 capability 只覆盖挂载进容器的东西，只读映射依然是只读。
如果你想关掉它，就显式丢弃（`cap_drop: [DAC_OVERRIDE]` / `--cap-drop DAC_OVERRIDE`）：
OfflineU 会改为启动普通二进制，继续正常工作，并把 root 属主的文件夹重新报为不可读，
正如上面的表格所述。`cap_drop: [ALL]` 同样安全 —— entrypoint 会察觉 exec 失败并回退，而不是拒绝启动。

你永远不需要为了添加课程而重新构建镜像，启动日志会写出每个已映射文件夹的真实状态
（`docker logs offlineu-app`，或飞牛OS 容器视图里的日志）：

```text
Course folders:
  /courses -> mapped, but empty (no file at any depth)

Warning: the folder(s) above are mapped but still empty.
Copy your course files into the host folder and reload the page - no restart is needed.
```

当完全没有任何映射时，日志和选择器卡片会改为打印可直接复制的 `-v` 那一行：

```text
No course material was found in the configured folder(s).

Mount your own folders when you start the container - nothing has to be rebuilt:
  docker run -d --name offlineu -p 5000:5000 \
    -v "/path/to/your/courses:/courses:ro" ...
```

挂载好文件夹后，整个界面都用**被映射文件夹的名字**来指位置（`My Course/Section 1`），
而不会出现 `/courses` 这类容器路径 —— JSON API 仍会同时返回绝对路径 `path`
和供界面使用的 `display_path`。

挂载多个文件夹也没问题：按各自的名字挂进去
（`-v "/home/me/Piano Lessons:/courses/piano:ro"`），
在 `OFFLINEU_ROOTS` 里列出父目录，想用比文件夹名更友好的标签就设 `OFFLINEU_ROOTS_LABEL`。

镜像是三阶段构建（Node 构建前端，Go 编译静态二进制，Alpine 运行它），
最终体积几十 MB —— 最后一层里既没有 Python 也没有 Node。

---

## 🛡️ 安全说明

OfflineU 面向可信的局域网或单机使用：

* 它**默认只绑定 `127.0.0.1`**。只有在确有需要时才暴露出去（`--host 0.0.0.0`）。
* **没有鉴权。** 任何能访问该端口的人都能浏览范围内的文件夹并下载其中的文件。
* 请设置 **`OFFLINEU_ROOTS`** 指向专门的课程文件夹，以限制可浏览和可提供的范围。
  请求路径会用 `filepath.Rel` **以及**符号链接求值来解析，因此 `..` 穿越和前缀花招
  （例如用 `/courses-ab` 冒充 `/courses`）都会被 `403` 拒绝。
* URL 路径只会被反转义一次，所以双重编码的穿越尝试始终无效。
* 请求体有大小上限（1 MiB），且只有 JSON `POST` 路由会修改状态。

---

## 🧠 路线图

* [x] 基础功能与测试
* [x] 自托管 Docker 部署
* [x] 目录浏览、字幕支持、键盘快捷键
* [x] Go 重写并内置 Vue 3 前端
* [x] 把课时投屏到局域网的 DLNA/UPnP 设备（含实时转换）
* [ ] 多用户档案支持
* [ ] 深色/浅色主题切换
* [ ] 内置测验交互
* [ ] 课程元数据导入/导出
* [ ] 移动端外壳

---

## 💬 社区

参与开发、提功能建议或提问：

* GitHub Issues：[https://github.com/nickkk333/OfflineU/issues](https://github.com/nickkk333/OfflineU/issues)

---

## 🛡️ 许可证

MIT License —— 自由使用，随意本地修改，欢迎分享。

---

## ✨ 致谢

最初由 [@WhiskeyCoder](https://github.com/WhiskeyCoder) 用 ❤️ 以 Python/Flask 应用的形式构建；
本版本是 Go + Vue 3 的重写，保留了同样的功能与 API。
灵感来自那个**自由、离线、无界限地学习**的愿望。

---

## ⚙️ 配置

命令行：

| 参数 | 默认值 | 用途 |
| ---- | ------ | ---- |
| `--host` | `127.0.0.1` | 绑定的网卡（`0.0.0.0` 表示对外暴露） |
| `--port` | `5000` | 监听端口 |
| `--debug` | `false` | 详细的请求日志 |
| `--web-dir <dir>` | （内置） | 从目录提供前端，而不是用二进制里内置的 |
| `--check-web` | `false` | 校验内置前端是否存在并退出（别名：`--check-templates`） |

环境变量：

| 变量 | 用途 |
| ---- | ---- |
| `OFFLINEU_HOST` | `--host` 的默认值 |
| `OFFLINEU_PORT` | `--port` 的默认值 |
| `OFFLINEU_ROOTS` | 路径列表（Windows 用 `;`，其他系统用 `:`），OfflineU 只能浏览和提供其中的文件夹。**强烈建议设置。** |
| `OFFLINEU_ROOTS_LABEL` | 界面上为第一层文件夹显示的名字，替代其真实名称（例如 `Movie Night`） |
| `OFFLINEU_PROGRESS_DIR` | 把进度文件*和*课程列表存到这里，而不是课程目录旁 |
| `OFFLINEU_DLNA` | `off`（或 `0`/`false`）隐藏投屏并拒绝 `/api/dlna/*` 接口；其他值表示开启 |
| `OFFLINEU_FFMPEG` | ffmpeg 路径 —— 让投屏能转换设备会拒收的文件（`.mkv`、HEVC 等）。找不到时回退到 `PATH` 里的 `ffmpeg` |
| `OFFLINEU_FFPROBE` | ffprobe 路径；默认取 `OFFLINEU_FFMPEG` 旁边的那个二进制 |
| `OFFLINEU_CACHE_DIR` | 转换后媒体的写入位置（重新封装的 MP4、HLS 分片）。默认是系统临时目录 —— **在 NAS 上请指向大分区**，见 🎥 播放 一节 |
| `OFFLINEU_CACHE_LIMIT_GB` | 该目录最多可用多少磁盘，按最旧文件优先清理（默认 `20`；负数表示不限） |
| `OFFLINEU_TRANSCODE_HEIGHT` | 重编码视频被**缩**到的高度（默认 `720`）。绝不放大，也不会低于 720p —— 480p 的源仍是 480p。`0` 表示保持原始尺寸 |
| `OFFLINEU_TRANSCODE_PRESET` | libx264 的速度档（默认 `veryfast`；CPU 很弱时用 `ultrafast`） |
| `OFFLINEU_TRANSCODE_CRF` | 质量因子，同时用作硬件编码器的量化参数（默认 `23`） |
| `OFFLINEU_HWACCEL` | `auto`（默认）在 ffmpeg 支持时使用 GPU —— NVENC、Quick Sync、VAAPI、VideoToolbox、AMF、V4L2 或 RKMPP；`off` 从不使用；或写某个名字强制指定（`nvenc`、`qsv`、`vaapi`、`videotoolbox`、`amf`、`v4l2m2m`、`rkmpp`） |
| `OFFLINEU_FORCE_REENCODE` | `1` 表示对每一节都重编码，而不是拷贝其流 —— 针对某些平台提供的、带有故意损坏数据包的传输流（`.ts`）：拷贝会保留损坏，浏览器拒绝播放，重编码则会丢弃它们 |
| `AUTO_LOAD_COURSE` | 未给路径参数时，启动时加载这门课程 |

已配置根目录内的位置都按**你映射进来的文件夹**相对显示（`My Course/Section 1`），
所以 `/courses`、`/app/courses` 这类容器路径永远不会出现在浏览器、选择器或面板里。
JSON API 仍会同时返回绝对路径 `path` 与界面使用的 `display_path`。

任何位置参数都被当作启动时加载的课程文件夹：
`offlineu "/mnt/media/Courses/Go in Depth"`。
