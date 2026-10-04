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
* ✅ **Lesson progress tracking** — time spent and completion are stored automatically and are
  never lost by revisiting a lesson.
* ♻️ **Continue where you left off** — a resume card on the dashboard jumps straight back to
  the last lesson.
* ⌨️ **Keyboard shortcuts** — space (play/pause), ← / → (skip 10 s), ↑ / ↓ (volume).
* 💾 **Local-first & private** — 100% offline, progress is a plain JSON file you own.
* 🧑‍💻 **Works with any course format** — no metadata required, just structured folders.
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
| GET    | `/api/state`                            | Active course, its lesson tree, recent courses (with completion counters) and version |
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

`/api/browse` and `/api/load_course` are accepted as aliases of the two picker endpoints.

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
│   ├── server.go               # routes, JSON API, static + SPA fallback
│   └── *_test.go               # Go test suite
└── web/                        # Vue 3 + Vite frontend (built into web/dist and embedded)
    ├── src/{api.js,store.js,router.js,views,components,composables,styles}
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
protection, the `OFFLINEU_ROOTS` allow-list, the JSON API, the course store and the CLI.

Useful companions:

```bash
gofmt -l .          # formatting
go vet ./...        # static checks
go run . --check-web # is the frontend bundle embedded?
```

---

## 🐳 Docker

Build and run with Docker Compose (mount your courses read-only, keep progress in `./data`):

```yaml
services:
  offlineu:
    image: ghcr.io/nickkk333/offlineu:main
    ports: ["5000:5000"]
    environment:
      - OFFLINEU_ROOTS=/app/courses
      - OFFLINEU_PROGRESS_DIR=/app/data
    volumes:
      - ./courses:/app/courses:ro
      - ./data:/app/data
```

```bash
docker compose up -d          # or: docker build -t offlineu . && docker run ...
```

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
| `OFFLINEU_PROGRESS_DIR` | Store progress files *and* the course list here instead of next to the course |
| `AUTO_LOAD_COURSE`      | Load this course at startup when no path argument is given                  |

Any positional argument is a course folder to load on startup:
`offlineu "/mnt/media/Courses/Go in Depth"`.
