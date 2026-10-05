# OfflineU: Self-Hosted Local Course Loader & Progress Tracker

**OfflineU** is a sleek, self-hosted web app that turns any folder of offline video, audio,
document and quiz material into a fully navigable course dashboard with automatic progress
tracking. Point it at an Udemy download, an "open sourced" training archive or your own notes —
no metadata, no database, no cloud.

The backend is a **single static Go binary** (standard library only) that embeds a redesigned
**Vue 3** frontend, so deployment is one file: no Python, no Node, no virtualenv, no runtime
dependencies.

---

## ✨ Features

* 📁 **Directory browser & folder parsing** — pick a course with a click, OfflineU maps its
  structure into a browsable tree view.
* 🧷 **Remembers your courses** — the course you picked is written to disk, so coming back to
  the home page, switching courses or restarting OfflineU never loses it. Each remembered
  course also shows a completion percentage, so you can see what to continue at a glance.
* 🎥 **Video & audio player** — integrated player with resume, watched-time tracking and
  automatic completion when the media ends.
* ▶️ **Continuous playback** — when a video finishes, the next playable lesson starts
  automatically (documents in between are skipped), and the speed you picked carries over to
  every following lesson. Both can be turned off in the player toolbar.
* 📄 **Documents inline** — `.txt`/`.md` are shown as text, `.html`/`.pdf` are embedded, other
  Office files get an "open in new tab" link.
* 💬 **Subtitles** — `.srt`/`.vtt` files next to a video are attached to it and converted to
  WebVTT on the fly, so the browser can actually display them.
* 📺 **Cast to DLNA/UPnP devices** — the lesson view lists the TVs, speakers and players on your
  LAN and pushes the current video or audio to the one you pick (resuming at your saved position).
  Pure standard library: SSDP discovery plus the `SetAVTransportURI`/`Play` SOAP actions.
* ✅ **Lesson progress tracking** — time spent and completion are stored automatically and are
  never lost by revisiting a lesson.
* ♻️ **Continue where you left off** — a resume card on the dashboard jumps straight back to
  the last lesson.
* ⌨️ **Keyboard shortcuts** — space (play/pause), ← / → (skip 10 s), ↑ / ↓ (volume).
* 💾 **Local-first & private** — 100% offline, progress is a plain JSON file you own.
* 🧑‍💻 **Works with any course format** — no metadata required, just structured folders.
* 🌐 **English & 中文** — the button in the header flips the whole interface between English and
  Chinese. The choice is remembered in the browser, and the few strings the server generates
  itself (the mount hint) follow the `Accept-Language` header the SPA sends.
* 🐳 **Self-hosted Docker deployment** with a hardened, configurable file-access allow-list.

---

## 🖼️ Screenshots

> ![OfflineU lesson view](images/lesson-0-8-2025-08-04-04_58_17.png)

---

## 🛠️ Installation

### 📦 Build from source (one binary)

Requirements: [Go 1.23+](https://go.dev/dl/) and [Node 20+](https://nodejs.org/) — Node is only
needed to build the frontend.

```bash
git clone https://github.com/nickkk333/OfflineU.git
cd OfflineU
```

1. Build the frontend into `web/dist` (it is embedded into the binary):

   ```bash
   cd web
   npm install
   npm run build
   cd ..
   ```

2. Build (and verify) the binary:

   ```bash
   go build -o offlineu .
   ./offlineu --check-web        # prints "Frontend assets OK" and exits
   ```

3. Run it, pointing OfflineU at your course folder:

   ```bash
   ./offlineu "/path/to/My Course"      # Windows: .\offlineu.exe "D:\Courses\My Course"
   ```

4. Open <http://127.0.0.1:5000>. Without a path argument OfflineU restores the last course it
   opened; with no remembered course you get the folder picker.

> `web/dist` always exists in the repository (kept by a tracked `.gitkeep`), so `go build`

---

## 📚 Course layout

OfflineU needs no metadata: every file it understands becomes one lesson, and the folder
structure becomes the course tree (hidden files/folders are ignored, scans stop after 10 levels).

```text
My Course/
├── Section 1 - Getting Started/
│   ├── 01 - Welcome.mp4            → video lesson (resume + autoplay)
│   ├── 01 - Welcome.srt            → attached as subtitles (converted to WebVTT)
│   ├── 02 - Notes.md               → text lesson
│   ├── 03 - Quiz.html              → quiz lesson (name contains "quiz"/"exam"/"test")
│   └── 04 - Handout.docx           → document with an "open in a new tab" link
└── Section 2 - Deep Dive/
    ├── 01 - Lecture.mp3            → audio lesson
    └── resources/
        └── cheat-sheet.pdf         → embedded PDF lesson
```

| Type      | Extensions                                          | Behaviour                                |
| --------- | --------------------------------------------------- | ---------------------------------------- |
| Video     | `.mp4` `.mkv` `.avi` `.mov` `.webm` `.m4v` `.flv` `.wmv` | Inline player, resume, completion on end |
| Audio     | `.mp3` `.wav` `.m4a` `.aac` `.ogg` `.flac`           | Inline player, resume, completion on end |
| Text      | `.txt` `.md`                                         | Rendered as text in the lesson view      |
| Embed     | `.html` `.htm` `.pdf`                                | Rendered in an iframe                    |
| Other doc | `.docx` `.doc` `.rtf`                                | Download/open link                       |
| Subtitles | `.srt` `.vtt` `.ass` `.sub` `.sbv`                   | Attached to the sibling video, served as VTT |

---

## 💾 Where your data lives

* **Progress** — `.offlineu_progress.json`, stored next to the course by default. With
  `OFFLINEU_PROGRESS_DIR` set, each course gets `<12-char-sha1>-progress.json` inside that
  folder instead (used by the Docker image so course mounts can stay read-only). It is a flat
  JSON map keyed by the lesson's relative path:
  `{"Section 1/01 - Intro.mp4/Intro": {"completed": true, "progress_seconds": 412}}`.
  Partial updates never reset stored values: sending `progress_seconds` alone keeps the
  completion flag, and watched seconds never move backwards.
* **Course bookkeeping** — `offlineu_state.json` remembers the active course and the recent
  course list (up to 20 entries). It lives in `OFFLINEU_PROGRESS_DIR`, or in a `data/` folder
  next to the working directory otherwise. Every entry caches the number of lessons
  (`total_lessons`) so the picker can show a completion percentage without re-scanning the
  course; `completed_lessons` is counted from the progress file on each `/api/state` call.

---

## 🔌 HTTP API

The Vue frontend talks to the same JSON API the previous version exposed, so every endpoint
stays scriptable:

| Method | Endpoint                                | Purpose                                                        |
| ------ | --------------------------------------- | -------------------------------------------------------------- |
| GET    | `/`                                     | SPA shell (the Vue app decides dashboard vs. picker)            |
| GET    | `/api/state`                            | Active course, its lesson tree, recent courses (with completion counters), `roots_display`, `needs_mount`/`mount_issue`/`roots_detail`/`mount_hint` and version |
| GET    | `/browse?path=<dir>`                    | JSON listing of one directory level (used by the picker)        |
| POST   | `/load_course`                          | `{"course_path": "..."}` — parse a folder and make it active    |
| GET    | `/api/lesson?path=<lesson>&autoplay=1`  | Lesson payload: media/subtitle URLs, documents, neighbours, autoplay target, storage warning |
| GET    | `/lesson/<lesson_path>`                 | Deep link into the SPA lesson view                              |
| POST   | `/api/progress`                         | Partial update: `{"lesson_path", "completed", "progress_seconds"}` (omitted fields are left untouched) |
| GET    | `/files/<path>`                         | Serve a file inside the active course                           |
| GET    | `/subtitles/<path>`                     | Serve subtitles as WebVTT (converts SRT on the fly)             |
| GET    | `/health`                               | `{"status": "healthy"}` (used by Docker health checks)          |
| POST   | `/api/reset_course`                     | Back to the picker; the course stays in the recent list         |
| POST   | `/api/forget_course`                    | `{"path": "..."}` — remove one entry from the recent list       |
| GET    | `/reset_course`, `/forget_course`       | Legacy redirect variants of the two endpoints above             |
| GET    | `/api/dlna/devices`                     | Renderers found on the LAN (`?refresh=1` repeats the SSDP search) |
| POST   | `/api/dlna/cast`                        | `{"device": "<udn>", "lesson_path": "...", "start_seconds": 30, "transcode": "auto"\|"on"\|"off"}` — push a lesson to a renderer; `converted` in the answer says whether a stream was used |
| GET    | `/api/dlna/stream?lesson=<path>&start=30` | The converted stream (MPEG-TS / AAC) a renderer pulls while playing |
| GET    | `/api/dlna/session`                     | The running cast: device, lesson, position/duration, state, next lesson |
| POST   | `/api/dlna/control`                     | `{"device": "<udn>", "action": "play"\|"pause"\|"stop"\|"next"\|"seek", "position": 90}` |

`/api/state` and `/api/lesson` carry `dlna_enabled`, so the UI hides the cast button when
`OFFLINEU_DLNA=off`. `OFFLINEU_DLNA=off` makes the three DLNA endpoints answer `403`.

`/api/browse` and `/api/load_course` are accepted as aliases of the two picker endpoints.

`GET /api/state` localises its `mount_hint` from the `Accept-Language` header (`zh*` → Chinese,
anything else → English), so the SPA can switch languages without a server restart.

---

## 📺 Casting (DLNA / UPnP)

The **📺 Cast** button of a lesson lists the media renderers on your LAN and plays the lesson's
video or audio on the one you pick. No app on the TV, no account, no cloud service:

1. OfflineU broadcasts an **SSDP `M-SEARCH`** (`MediaRenderer:1` and `ssdp:all`) and listens ~2 s.
2. Every device that answers is asked for its **device description**; the ones exposing an
   `AVTransport` service become cast targets.
3. Choosing one sends **`SetAVTransportURI`** (the absolute `http://<your-host>:5000/files/…` URL
   plus DIDL-Lite metadata) followed by **`Play`** — and a `Seek` when the lesson has a saved
   position, so the TV continues where the browser stopped.
4. **Pause / Play / Stop** stay available in the same menu while the device is playing.

What it needs:

* **A LAN-reachable address.** The renderer downloads the file itself, so OfflineU builds the URL
  from the host the browser used (`X-Forwarded-Host`/`Proto` are honoured). Opening OfflineU on
  `http://localhost:5000` from the machine it runs on is fine; a TV cannot reach that host name,
  so open the LAN address (e.g. `http://192.168.1.5:5000`) or set `--host 0.0.0.0`.
* **Multicast on the same network.** In Docker the SSDP search only reaches the LAN with host
  networking (or macvlan); add `network_mode: host` to the service, or run OfflineU directly on
  the machine. Without it the cast menu simply stays empty.
* **A format the device understands** — see below, OfflineU converts what it has to.

#### “My TV refuses this file” — compatibility mode

Most renderers reject a *container* (`.mkv`, `.avi`, `.flv`, `.wmv`, `.webm`) even though they
decode the streams inside it. OfflineU therefore decides per lesson whether the device is likely
to refuse it and, if so, hands the device a **stream instead of the file** — `/api/dlna/stream`,
produced by ffmpeg while the device is already playing:

| Situation | What OfflineU does | Cost |
| --------- | ------------------ | ---- |
| `.mp4`/`.m4v`/`.mov` with H.264 + AAC/MP3/AC3 | hands over the file unchanged | none |
| `.mkv`/`.avi`/… whose codecs are fine | **repackages** into MPEG-TS (`-c copy`) | a few % of one core |
| HEVC/VP9, DTS, WMA, … (codec the device cannot decode) | **re-encodes** to H.264 + AAC | real CPU, but it plays |
| audio lesson (`.flac`, `.wav`, `.ogg`, …) | re-encodes to AAC (mp3 stays mp3) | small |

Because the conversion happens while streaming, the resume position is given to ffmpeg as `-ss`
(the device cannot seek in a live stream), subtitles and attachments are dropped, and the process
dies the moment the device stops or disconnects.

ffmpeg does the heavy lifting for both **browser playback** and **casting**: it repackages a lesson a
browser or a renderer would otherwise refuse — a mislabeled `.mp4` that is really MPEG-TS, or an `.mkv`,
becomes a browser-native MP4; an `.mkv` bound for a TV becomes MPEG-TS. Without it OfflineU can only hand
the original file to the client, which then fails to play it.

* Docker: the image **always** bundles ffmpeg (no build flag needed) — a ~90 MB bigger image, but every
  lesson plays, both in the browser and on the TV.
* local run (any OS/arch, incl. an ARM NAS like fnOS/飞牛OS): OfflineU auto-downloads a static ffmpeg
  on first play when none is found on the machine - this covers Windows, Linux amd64/arm64 and macOS, so
  a box whose image does not ship ffmpeg still plays lessons; alternatively point `OFFLINEU_FFMPEG` /
  `OFFLINEU_FFPROBE` at an existing install, or set `OFFLINEU_NO_AUTOFFMPEG=1` to disable the download.
  (The auto-downloaded build fetches both ffmpeg and ffprobe; if ffprobe cannot be fetched, OfflineU still
  detects containers and codecs with `ffmpeg -i`.)

**Compatibility mode** in the cast menu is pre-set to what the server worked out for the lesson
(`/api/lesson` returns `cast_plan: {needs_transcode, transcode_available, reason}`) and can be
toggled by hand: switch it off to push the untouched file (no CPU at all), switch it on to force a
conversion when a device is pickier than OfflineU assumed.

### Following the cast from the browser

The picture stays on the TV, but the page does not go blind: `/api/dlna/session` reports where the
device is and the lesson view shows it as a progress bar (paused/playing, position, length, what
plays next), while the buttons pause, skip and stop the device from the browser.

* The position comes from the device itself (`GetPositionInfo`) whenever it answers, which also
  catches a pause or a skip done with the TV's own remote; devices that refuse the call fall back
  to clock arithmetic and the UI says "estimated".
* **The cast writes the same progress as the player.** Position and completion go into the very
  same `.offlineu_progress.json`, under the same key, so a lesson watched on the TV counts towards
  the course total, the tree and the "continue where you left off" card exactly like one watched in
  the browser - and with the same rules: seconds never move backwards, and the completion flag is
  only written when the lesson really reached its end (rewatching a finished lesson keeps it
  completed). Every ~15 s while playing, once when it ends, and once when it is stopped.
* **The length of the lesson decides whether an end can be seen at all**, so it is taken from
  wherever it can be found: ffprobe (or `ffmpeg -i` when there is no ffprobe), the length the
  browser measured while loading the file, and - while playing - the `TrackDuration` the device
  reports. If none of them answers, the cast still shows its position but cannot tell that the
  lesson ended; the progress bar then says so, and you mark the lesson completed by hand.
* **Click anywhere on the progress bar to jump there.** A cast of the plain file is seeked on the
  device (`Seek` with `REL_TIME`); a converted stream has no timeline to jump in, so ffmpeg is
  restarted at that position (`-ss`) and the device gets the new URL. Jumping past the end stops a
  second before it, so the lesson can still finish and continue.
* When the lesson ends, OfflineU **pushes the next playable lesson to the same device** by itself
  (documents are skipped) and the browser follows to the new lesson. This is done by a watchdog in
  the server, not by the page, so it also works with the browser closed. Turn it off by passing
  `autoplay: false` to `/api/dlna/cast` — or by switching off *Autoplay next lesson* in the player
  toolbar before casting.
* The cast bar is not tied to the lesson page: the **dashboard shows it too** (with a link to the
  lesson that is playing), so you can watch the progress, pause or skip while browsing the course
  tree. The lesson page follows the device as soon as it moves on - the comparison is made against
  the route, so it also works when a lesson is still loading.

Casting can be switched off entirely: `OFFLINEU_DLNA=off` hides the button and makes
`/api/dlna/*` answer `403`. The discovery result is cached for 30 s; **⟳ Search again** repeats
the search immediately.

---

## 🗂️ Project structure

```text
OfflineU/
├── main.go                     # CLI, embedded frontend, HTTP server bootstrap
├── internal/offlineu/
│   ├── config.go               # flags/env, OFFLINEU_ROOTS allow-list, path helpers
│   ├── model.go                # Course / Lesson / file-type tables, JSON marshalling
│   ├── parser.go               # folder → course tree, quiz detection, text resources
│   ├── paths.go                # URL escaping, recursive scanning helpers
│   ├── progress.go             # progress file loading/saving, watched-time rules
│   ├── store.go                # active course + recent courses (offlineu_state.json)
│   ├── subtitles.go            # SRT → WebVTT conversion (CP1252 fallback)
│   ├── dlna.go                 # SSDP discovery, device description, SOAP casting
│   ├── transcode.go            # optional ffmpeg: probe, repackage/re-encode, live stream
│   ├── castsession.go          # running cast: position, watchdog, autoplay of the next lesson
│   ├── server.go               # routes, JSON API, static + SPA fallback
│   └── *_test.go               # Go test suite
└── web/                        # Vue 3 + Vite frontend (built into web/dist and embedded)
    ├── src/{api.js,store.js,router.js,i18n.js,views,components,composables,styles}
    │                           # i18n.js: en/zh dictionaries, `t()` helper, language switch
    └── vite.config.js          # dev server proxies the API to :5000
```

---

## 👩‍💻 Development

Two terminals give hot reload in the frontend while the real Go backend serves the data:

```bash
# terminal 1 — API on http://127.0.0.1:5000
go run . "/path/to/My Course"

# terminal 2 — Vite dev server on http://127.0.0.1:5173 (proxies /api, /files, ...)
cd web
npm install
npm run dev
```

For a production build, run `cd web && npm run build` and then `go build -o offlineu .` — the
compiled bundle is embedded into the binary. `--web-dir web/dist` lets you serve the bundle
from disk instead (handy when experimenting with `npm run build`).

---

## 🧪 Tests

The Go suite uses only the standard library, so it runs completely offline:

```bash
go test ./... -count=1
```

It covers folder parsing, subtitle attachment, document modes, autoplay selection, progress
persistence (including the "revisiting a lesson must not reset it" regression), path-traversal
protection, the `OFFLINEU_ROOTS` allow-list, the JSON API, the course store, the CLI and the DLNA
layer (device description parsing, the SOAP requests a cast produces, UPnP error reporting).

Useful companions:

```bash
gofmt -l .          # formatting
go vet ./...        # static checks
go run . --check-web # is the frontend bundle embedded?
```

---

## 📦 Releases

每次推送一个 `v*` tag（例如 `v2.0.0`），CI 会自动构建并发布一个 GitHub Release，附带以下资源：

| 资源 | 平台 / 架构 | 说明 |
| ---- | ----------- | ---- |
| `offlineu-linux-amd64` | Linux x86_64 | 原生二进制，前端已内嵌 |
| `offlineu-linux-arm64` | Linux ARM64（树莓派 / ARM NAS） | 同上 |
| `offlineu-windows-amd64.exe` | Windows x86_64 | 同上 |
| `offlineu_<version>_x86.fpk` | 飞牛OS / fnOS（amd64） | 应用包，内置镜像，离线安装 |
| `checksums.txt` | — | SHA-256 校验和 |

版本号统一跟随 git tag（`v2.0.0` → `2.0.0`）：二进制内的 `Version`、fpk 文件名与镜像 tag 全部同步。
Docker 镜像同时推送到 GitHub Container Registry（`ghcr.io/nickkk333/offlineu`），tag 与 Release 版本一致，例如 `ghcr.io/nickkk333/offlineu:2.0.0`。

### 飞牛OS / fnOS

应用中心 → 手动安装 → 上传 `offlineu_<version>_x86.fpk`。镜像已打包进应用包，安装全程离线，无需镜像仓库。仅支持 amd64。

### 原生二进制（Linux / Windows）

直接运行对应可执行文件（前端已编译进二进制，无需 Node）：

```bash
./offlineu-linux-amd64 --host 0.0.0.0 --port 5000
./offlineu-linux-arm64 --host 0.0.0.0 --port 5000
.\offlineu-windows-amd64.exe --port 5000
```

> 实时转封装（MKV / MPEG-TS → 浏览器原生 MP4）依赖 ffmpeg：原生二进制首次播放会自动下载
> 静态 ffmpeg，或自行安装并放入 PATH / 设置 `OFFLINEU_FFMPEG`。

### Docker

```bash
docker pull ghcr.io/nickkk333/offlineu:2.0.0
docker run -d --name offlineu -p 5000:5000 \
  -v "/path/to/your/courses:/courses:ro" \
  -v offlineu-data:/app/data \
  ghcr.io/nickkk333/offlineu:2.0.0
```

---

## 🐳 Docker

Nothing about your course folder is baked into the image: `/courses` is just the mount point it
expects. Map your own folder onto it and progress is kept in `/app/data` so the course mount can
stay read-only.

```bash
docker run -d --name offlineu -p 5000:5000 \
  -v "/path/to/your/courses:/courses:ro" \
  -v offlineu-data:/app/data \
  ghcr.io/nickkk333/offlineu:main
```

The Compose equivalent:

```yaml
services:
  offlineu:
    image: ghcr.io/nickkk333/offlineu:main
    ports: ["5000:5000"]
    environment:
      - OFFLINEU_ROOTS=/courses
      # Optional: name shown in the UI instead of the mount point name
      # - OFFLINEU_ROOTS_LABEL=My courses
      - OFFLINEU_PROGRESS_DIR=/app/data
    volumes:
      - ./courses:/courses:ro
      - ./data:/app/data
```

```bash
docker compose up -d          # or: docker build -t offlineu . && docker run ...
```

Casting needs the SSDP multicast of your LAN, which Docker's default bridge network does not
forward: add `network_mode: host` to the service (and drop the `ports` mapping, the container then
uses the host network directly) if you want the 📺 Cast button to find your TV.

### Offline install — export a `docker load`-able tar

The image is self-contained, so a machine without a registry (or without internet at all) only
needs the tar: build it once where the network is, carry the file over, load it there.

```bash
# 1. build on the connected machine. `--provenance=false --sbom=false` is optional: it just
#    keeps the two attestation manifests out of the archive (16 → 12 entries, a few KB).
docker build --provenance=false --sbom=false -t offlineu:1.0.0 .
# (or: uncomment `build: .` in docker-compose.yml and run `docker compose build`)

# 2. add the tag docker-compose.yml expects, so the tar works as-is on the other side
docker tag offlineu:1.0.0 ghcr.io/nickkk333/offlineu:main

# 3. export both tags into one file (~12 MB). Leave it uncompressed — see the note below.
docker save -o offlineu-1.0.0.tar offlineu:1.0.0 ghcr.io/nickkk333/offlineu:main
```

Copy `offlineu-1.0.0.tar` to the target machine and:

```bash
docker load -i offlineu-1.0.0.tar     # -> Loaded image: offlineu:1.0.0 (+ ghcr.io/...:main)
docker compose up -d                  # the compose file already points at that tag
```

…or run it without Compose:

```bash
docker run -d --name offlineu -p 5000:5000 \
  -v "/path/to/your/courses:/courses:ro" \
  -v offlineu-data:/app/data \
  ghcr.io/nickkk333/offlineu:main
```

Notes:

* The tar is a plain docker archive: `manifest.json` (plus OCI metadata) is inside, so
  `docker load -i` restores **both** tags in one go, `docker compose up -d` finds
  `ghcr.io/nickkk333/offlineu:main` immediately, and no registry and no rebuild are needed. Expect
  roughly 9 MB for the tar and 32 MB for the loaded image.
* **Do not gzip the tar**: `docker load -i offlineu.tar.gz` (and `docker load < offlineu.tar.gz`)
  answers `unrecognized image format`. Transfer it as-is, or unpack on the target first —
  `gunzip -c offlineu-1.0.0.tar.gz > offlineu-1.0.0.tar && docker load -i offlineu-1.0.0.tar`
  (`gzip -c`/`tar -czf` on the export side is fine).
* **Target CPU differs from the build machine?** Build for it and write the tar in one step:
  `docker buildx build --provenance=false --sbom=false --platform linux/amd64 --output type=docker,dest=offlineu-1.0.0.tar -t offlineu:1.0.0 .`
  (swap in `linux/arm64` for a Raspberry Pi / ARM NAS; add `--load` instead of `--output ...` when
  you want the image in the local store as well).
* Sanity check after loading: `docker image inspect ghcr.io/nickkk333/offlineu:main --format '{{.Id}} {{.Os}}/{{.Architecture}} {{.Size}}'`
  and, once it runs, `curl http://127.0.0.1:5000/health` → `{"status":"healthy"}`.
* On Windows PowerShell the same commands work unchanged; just spell the file as `.\offlineu-1.0.0.tar`
  (that is how the tar in `image-dist/` was built and load-tested here).
* Want to `docker build` offline too? Also carry the three base images:
  `docker save -o bases.tar node:22-alpine golang:1.24-alpine alpine:3.21`.

### NAS Docker GUI (飞牛OS / fnOS, Synology, Unraid, …)

The image declares both mount points (`VOLUME ["/courses", "/app/data"]`), so the *create
container* wizard of a NAS Docker app already lists them — you only fill in two host folders.
Without that declaration Docker would quietly attach throwaway anonymous volumes instead, and your
progress would vanish with the container. On 飞牛OS:

1. **Import the image** — 镜像 → 导入, pick the `offlineu-1.0.0.tar` produced by the section above
   (or the one in `image-dist/`); importing needs no network and no registry login.
2. **Create the container** — 容器 → 新增, choose `ghcr.io/nickkk333/offlineu:main` and map the two
   folders the wizard shows:

   | Container path | Host folder on fnOS (example) | Access     | What it holds          |
   | -------------- | ----------------------------- | ---------- | ---------------------- |
   | `/courses`     | `/vol1/1000/courses`          | read-only  | your course library    |
   | `/app/data`    | `/vol1/1000/offlineu-data`    | read-write | progress + course list |

   `vol1` is the first storage space and `1000` the first user in fnOS — point both rows at folders
   you can also reach over SMB, and swap in your own paths if your layout differs. Give `/app/data`
   a folder of its own: OfflineU writes `offlineu_progress*.json` and `offlineu_state.json` there,
   which is why `/courses` stays read-only and can be shared with other tools.
3. **Port mapping** — 5000 → 5000 (any free host port works, e.g. 15000 → 5000).
4. **Leave the environment variables as they are** — the image already sets
   `OFFLINEU_ROOTS=/courses`, `OFFLINEU_PROGRESS_DIR=/app/data`, `OFFLINEU_HOST=0.0.0.0` and
   `OFFLINEU_PORT=5000`, so nothing has to be added by hand.
5. Open `http://<nas-ip>:5000`. If `/courses` is still empty, the picker says so and repeats the
   two mappings, and the switch in the header flips the whole UI between English and 中文.

Recreating the container later (new image version, changed folder) keeps everything as long as the
`/app/data` folder is mapped back to the same place — recent courses, resume position and completed
lessons all live there.

#### “还没有映射课程文件夹” — what the picker really means

The picker does not guess any more: `/api/state` reports `mount_issue` plus a `roots_detail` entry per
mapped folder (path, readable, entries, error), the card names the actual case and shows a
**📌 映射状态 / 📌 Mapping status** list with the matching fix. `docker logs <container>` prints the
same list at startup.

| Card title | Meaning | Fix |
| ---------- | ------- | --- |
| `📂 /courses is mapped, but still empty` | The mapping works — a host folder is bound, there is simply no non-hidden file inside, not even at a deeper level. | Copy your courses into the host folder and press **⟳ Check again**. No rebuild, no restart. |
| `🔒 /courses is mapped, but the container cannot read it` | The container runs as uid 10001 and the host folder lets only root (or another NAS user) read it. | `chmod -R a+rX /vol1/1000/courses` (fnOS: grant read access to everyone), make sure the container did not drop the built-in read capability (see *Permissions* below), run the container as the folder's owner (`user: "1000:1000"`) or, as a last resort, as root (`--user 0:0`) — see *Permissions: which user reads your folders* below. |
| `🔌 Docker put a throwaway volume on /courses instead of your folder` | No host folder is bound: Docker attached an anonymous volume (or a named one). Your courses are invisible here and anything written there is lost when the container is recreated. | Map a host folder onto `/courses` (and one onto `/app/data`) and **recreate** the container — the usual “I picked a folder but saved it into the wrong column” case. |
| `🔌 Nothing is mounted on /courses inside the container` | No mount at all sits on the path and the folder is empty. | Same as above: the mapping never reached the container. |
| `📍 The mapping did not arrive: /courses is missing in the container` | The path itself does not exist (rare — it was removed while the container ran). | Recreate the container. |
| `🔌 No course folder is mapped yet` | `OFFLINEU_ROOTS` resolved to nothing, so no folder is checked at all. | Add the two mappings from the table above. |

Folders are read live: files you add to an already mapped folder show up after **⟳ Check again**
without any restart. Only a changed/added *mapping* needs the container to be recreated.

#### Permissions: which user reads your folders

The image runs as the unprivileged user `offlineu` (**uid/gid 10001**) and hands that uid a *read
capability*: `/app/offlineu-cap` is the same binary plus `cap_dac_override=ep`, and `/entrypoint.sh`
starts that copy whenever Docker allows the exec. A mapped folder may therefore belong to `root` or to
another NAS account and is still readable - **without ticking 「使用高权限执行容器」 and without a
`chmod` anywhere on the NAS** (details and the measured numbers are in the subsection below).

Only when the capability is unavailable - the container dropped it, or a network share checks
the permissions itself - is a folder that uid 10001 may not open reported as
`🔒 mapped, but the container cannot read it` even though the mapping is correct. Pick one of these,
in that order:

| Option | How | Notes |
| ------ | --- | ----- |
| 1. Make the folder readable | `chmod -R a+rX /vol1/1000/courses` — in fnOS you can also grant read access to *everyone* in the folder's 权限 dialog | Changes nothing about Docker; the read-only mapping stays read-only. |
| 2. Run as the folder's owner | fnOS's first user is normally `1000:1000` (check with `id <user>` over SSH). Look for a **用户/User** (运行身份) field in the NAS container dialog — compose: `user: "1000:1000"`, CLI: `docker run --user 1000:1000` | Usually the best NAS answer: the same uid normally owns `/vol1/1000/offlineu-data` as well, so progress keeps working. That is why `/app/data` should stay mapped too. |
| 3. Run as root | NAS dialog: user = `root`; compose: `user: "0:0"`; CLI: `docker run --user 0:0` | Widest privileges. Progress files are then created as root, so run `chown -R 10001:10001 <data folder>` if you later switch back to uid 10001. |

**Root has to be decided when the container is created.** A process that starts as uid 10001 cannot
promote itself, so `--user 0:0`, compose `user: "0:0"` and the NAS dialog's user field are the only
switches, and they cannot be changed afterwards — `docker update` does not touch the user. To change
it, recreate the container with the same two folder mappings:

```bash
# Run as the owner of /vol1/1000/courses (recommended on fnOS)
docker run -d --name offlineu -p 5000:5000 \
  --user 1000:1000 \
  -v "/vol1/1000/courses:/courses:ro" \
  -v "/vol1/1000/offlineu-data:/app/data" \
  ghcr.io/nickkk333/offlineu:main

# Last resort: run as root (only if the permissions cannot be changed)
docker run -d --name offlineu -p 5000:5000 \
  --user 0:0 \
  -v "/vol1/1000/courses:/courses:ro" \
  -v "/vol1/1000/offlineu-data:/app/data" \
  ghcr.io/nickkk333/offlineu:main
```

In the NAS GUI both variants are the same dialog, only the user field differs; with Compose (fnOS
"Compose 项目", Portainer, …) it is the `user:` line of the service. When the picker is open you can
also press **⟳ Check again** after `chmod` on the host — no restart is needed for a permission change.


##### The built-in read capability (and what 「使用高权限执行容器」 does not do)

`/api/state` carries `read_capability` and the startup log prints one `Read access:` line, so the state
is never guesswork:

| Container started with | `read_capability` | Folder that only root may read |
| ---------------------- | ----------------- | ------------------------------ |
| nothing special (fnOS: no checkbox ticked) | `true` | readable - `/app/offlineu-cap` runs with `CAP_DAC_OVERRIDE` |
| 「使用高权限执行容器」 (`--privileged`) | `true` | readable - but the checkbox is not what does it |
| `--cap-add DAC_OVERRIDE` | `true` | readable - the capability stays in the bounding set |
| `--cap-drop DAC_OVERRIDE`, `--cap-drop ALL`, `no-new-privileges` | `false` | unreadable - the entrypoint falls back to `/app/offlineu`, the picker lists the fixes above |

Two facts worth knowing, both measured on a real container by reading `/proc/1/status`:

* **A high-privilege container alone cannot grant read access.** `--privileged` widens the *capability
  bounding set* of the container (`CapBnd: 000001ffffffffff`) but it does not change the user the
  container runs as, and Linux clears the capability sets of a non-root process whenever it executes a
  file that carries no file capabilities (`CapEff: 0000000000000000`). The folder stays unreadable even
  though Docker shows the container as privileged. That is why the capability sits on the file itself:
  it belongs to the binary, so it is in effect for uid 10001 without any checkbox.
* **There is no password to type.** Handing the container a NAS password would not help: the password
  lives in the NAS's `/etc/shadow`, which is not inside the container, and a container that starts as
  uid 10001 cannot become root at all - the user is fixed when the container is created and
  `docker update` cannot change it afterwards. `CAP_DAC_OVERRIDE` is the switch that actually works, and
  the image sets it for you.

Check it inside a running container (also after `docker load` of the offline image tar):

```bash
docker exec offlineu-app getcap /app/offlineu-cap              # -> cap_dac_override=ep
docker exec offlineu-app /app/offlineu-cap --cap-check; echo $?  # -> 0 while the capability is active
```

The capability reaches only what is mounted into the container, and a read-only mapping stays
read-only. If you prefer to switch it off, drop it explicitly (`cap_drop: [DAC_OVERRIDE]` /
`--cap-drop DAC_OVERRIDE`): OfflineU then starts the plain binary, keeps working and reports a
root-owned folder as unreadable again, exactly as the table above describes. `cap_drop: [ALL]` is safe
in the same way - the entrypoint notices the exec failure and falls back instead of refusing to boot.

You never have to rebuild the image to add courses, and the startup log names the real state of every
mapped folder (`docker logs offlineu-app`, or 日志 in the fnOS container view):

```text
Course folders:
  /courses -> mapped, but empty (no file at any depth)

Warning: the folder(s) above are mapped but still empty.
Copy your course files into the host folder and reload the page - no restart is needed.
```

When nothing is mapped at all, the log and the picker card instead print the exact `-v` line to copy:

```text
No course material was found in the configured folder(s).

Mount your own folders when you start the container - nothing has to be rebuilt:
  docker run -d --name offlineu -p 5000:5000 \
    -v "/path/to/your/courses:/courses:ro" ...
```

After you mount a folder, the whole UI refers to locations by the **mapped folder's name**
(`My Course/Section 1`) instead of container paths such as `/courses` — the JSON API still
returns the absolute `path` next to the friendly `display_path`.

Mounting several folders is fine too: map them under their own names
(`-v "/home/me/Piano Lessons:/courses/piano:ro"`), list the parents in `OFFLINEU_ROOTS`, and set
`OFFLINEU_ROOTS_LABEL` when you want a friendlier label than the folder name.

The image is a three-stage build (Node builds the frontend, Go compiles the static binary,
Alpine runs it) and comes out at a few tens of megabytes — no Python and no Node in the final
layer.

---

## 🛡️ Security notes

OfflineU is designed for a trusted LAN or a single machine:

* It **binds to `127.0.0.1` by default**. Only expose it (`--host 0.0.0.0`) if you have to.
* There is **no authentication**. Anyone who can reach the port can browse the folders that
  are in scope and download the files inside them.
* Set **`OFFLINEU_ROOTS`** to a dedicated course folder to limit what is browsable and
  servable. Requests are resolved with `filepath.Rel` *and* symlink evaluation, so `..`
  escapes and prefix tricks (e.g. `/courses-ab` for `/courses`) are rejected with `403`.
* URL paths are unescaped exactly once, so double-encoded traversal attempts stay inert.
* Request bodies are capped (1 MiB) and only JSON `POST` routes mutate state.

---

## 🧠 Roadmap

* [x] Base function and testing
* [x] Self hosted Docker deployment
* [x] Directory browser, subtitle support, keyboard shortcuts
* [x] Go rewrite with an embedded Vue 3 frontend
* [x] Cast lessons to DLNA/UPnP devices on the LAN (incl. on-the-fly conversion)
* [ ] Multi-user profile support
* [ ] Dark/light theme switcher
* [ ] Built-in quiz interactivity
* [ ] Import/export course metadata
* [ ] Mobile app wrapper

---

## 💬 Community

Join the development, suggest features, or ask questions via:

* GitHub Issues: [https://github.com/nickkk333/OfflineU/issues](https://github.com/nickkk333/OfflineU/issues)

---

## 🛡️ License

MIT License — use freely, modify locally, share widely.

---

## ✨ Credits

Originally built with ❤️ by [@WhiskeyCoder](https://github.com/WhiskeyCoder) as a Python/Flask
application; this version is a Go + Vue 3 rewrite that keeps the same features and API.
Inspired by the dream of **learning freely, offline, and without limits.**


> works even before you run `npm run build` — you would just see the API-only notice.

---

## ⚙️ Configuration

Command line:

| Flag              | Default     | Purpose                                                       |
| ----------------- | ----------- | ------------------------------------------------------------- |
| `--host`          | `127.0.0.1` | Interface to bind to (`0.0.0.0` to expose it)                 |
| `--port`          | `5000`      | Port to listen on                                              |
| `--debug`         | `false`     | Verbose request logging                                        |
| `--web-dir <dir>` | (embedded)  | Serve the frontend from a directory instead of the binary      |
| `--check-web`     | `false`     | Verify the bundled frontend exists and exit (alias: `--check-templates`) |

Environment variables:

| Variable                | Purpose                                                                   |
| ----------------------- | ------------------------------------------------------------------------- |
| `OFFLINEU_HOST`         | Default for `--host`                                                       |
| `OFFLINEU_PORT`         | Default for `--port`                                                       |
| `OFFLINEU_ROOTS`        | Path-list (`;` on Windows, `:` elsewhere) of folders OfflineU may browse and serve. **Strongly recommended.** |
| `OFFLINEU_ROOTS_LABEL`  | Name shown in the UI for the first top-level folder instead of its real name (e.g. `Movie Night`) |
| `OFFLINEU_PROGRESS_DIR` | Store progress files *and* the course list here instead of next to the course |
| `OFFLINEU_DLNA`         | `off` (or `0`/`false`) hides casting and refuses the `/api/dlna/*` endpoints; anything else leaves it on |
| `OFFLINEU_FFMPEG`       | Path of ffmpeg — lets casting convert a file a device would refuse (`.mkv`, HEVC, …). Falls back to `ffmpeg` on `PATH` |
| `OFFLINEU_FFPROBE`      | Path of ffprobe; defaults to the binary next to `OFFLINEU_FFMPEG` |
| `AUTO_LOAD_COURSE`      | Load this course at startup when no path argument is given                  |

Locations inside a configured root are shown **relative to the folder you mapped in**
(`My Course/Section 1`), so container paths such as `/courses` or `/app/courses` never appear in
the browser, the picker or the dashboard. The JSON API keeps returning the absolute `path`
alongside the `display_path` used by the UI.

Any positional argument is a course folder to load on startup:
`offlineu "/mnt/media/Courses/Go in Depth"`.
