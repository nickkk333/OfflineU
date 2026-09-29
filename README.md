# OfflineU: Self-Hosted Local Course Loader & Progress Tracker

**OfflineU** is a sleek, self-hosted web app that turns any folder of offline video, audio,
document and quiz material into a fully navigable course dashboard with automatic progress
tracking. Point it at an Udemy download, an "open sourced" training archive or your own notes -
no metadata, no database, no cloud.

---

## ✨ Features

* 📁 **Directory browser & folder parsing** - pick a course with a click, OfflineU maps its
  structure into a browsable tree view.
* 🧷 **Remembers your courses** - the course you picked is written to disk, so coming back to
  the home page, switching courses or restarting OfflineU never loses it.
* 🎥 **Video & audio player** - integrated player with resume, watched-time tracking and
  automatic completion when the media ends.
* ▶️ **Continuous playback** - when a video finishes, the next playable lesson starts
  automatically (documents in between are skipped), and the speed you picked carries over to
  every following lesson. Both can be turned off in the player toolbar.
* 📄 **Documents inline** - `.txt`/`.md` are shown as text, `.html`/`.pdf` are embedded,
  other Office files get an "open in new tab" link.
* 💬 **Subtitles** - `.srt`/`.vtt` files next to a video are attached to it and converted to
  WebVTT on the fly, so the browser can actually display them.
* ✅ **Lesson progress tracking** - time spent and completion are stored automatically and are
  never lost by revisiting a lesson.
* ♻️ **Continue where you left off** - a resume card on the dashboard jumps straight back to
  the last lesson.
* ⌨️ **Keyboard shortcuts** - space (play/pause), ← / → (skip 10 s), ↑ / ↓ (volume).
* 💾 **Local-first & private** - 100% offline, progress is a plain JSON file you own.
* 🧑‍💻 **Works with any course format** - no metadata required, just structured folders.
* 🐳 **Self-hosted Docker deployment** with a hardened, configurable file-access allow-list.

---

## 🖼️ Screenshots

> ![image](https://github.com/WhiskeyCoder/OfflineU/blob/main/images/lesson-0-8-2025-08-04-04_58_17.png)

---

## 🛠️ Installation

### 🔁 Quick Start (Local)

1. Clone the repo:

   ```bash
   git clone https://github.com/WhiskeyCoder/OfflineU.git
   cd OfflineU
   ```

2. Create a virtual environment and install the dependencies.

   **Windows - CMD:**

   ```bat
   python -m venv .venv
   .venv\Scripts\activate.bat
   pip install -r requirements.txt
   ```

   **Windows - PowerShell:**

   ```powershell
   python -m venv .venv
   .\.venv\Scripts\Activate.ps1
   pip install -r requirements.txt
   ```

   > If PowerShell refuses to run the activate script ("running scripts is disabled on this
   > system"), allow it for the current session only:
   > `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass`

   **Linux / macOS / Git Bash (shells that have `source`):**

   ```bash
   python3 -m venv .venv
   source .venv/bin/activate
   pip install -r requirements.txt
   ```

   > Activating is convenient but optional - you can call the interpreter directly:
   > `.venv\Scripts\python -m pip install -r requirements.txt` and
   > `.venv\Scripts\python offlineu_core.py` (use `.venv/bin/python ...` on Linux/macOS).
   > Note that `source` does **not** exist in CMD/PowerShell; it is a bash builtin.

3. Run the app:

   ```bash
   python offlineu_core.py
   ```

4. Open your browser and pick your course folder:

   ```
   http://127.0.0.1:5000
   ```

   You can also load a course right away:

   ```bash
   python offlineu_core.py "D:\Courses\Python Tutorial"
   ```

By default OfflineU only listens on `127.0.0.1`. Pass `--host 0.0.0.0` (or set
`OFFLINEU_HOST`) if you really want to expose it to your network - see
[Security notes](#-security-notes) first.

---

## 📂 Folder Structure Example

```bash
MyCourse/
├── Section 1/
│   ├── 01 - Intro.mp4
│   ├── 01 - Intro.srt          ← attached to the video above
│   ├── 02 - Setup Guide.pdf
│   └── 03 - Quiz.html
├── Section 2/
│   ├── 04 - Advanced Tips.mp4
│   └── resources/
│       └── extras.md
└── .offlineu_progress.json     ← created automatically (unless relocated)
```

> 🌟 File types are detected automatically - videos, audio, quizzes and documents.
> Hidden files (starting with `.`) and unsupported extensions are ignored.

---

## 📁 Supported File Types

| Type      | Extensions                                                  | Rendering                       |
| --------- | ----------------------------------------------------------- | ------------------------------- |
| Videos    | `.mp4`, `.mkv`, `.webm`, `.mov`, `.avi`, `.m4v`, `.flv`, `.wmv` | HTML5 player with resume     |
| Audio     | `.mp3`, `.wav`, `.m4a`, `.aac`, `.ogg`, `.flac`             | HTML5 player with resume        |
| Docs      | `.txt`, `.md`                                               | Fetched and shown as plain text |
| Docs      | `.html`, `.htm`, `.pdf`                                     | Embedded in an iframe           |
| Docs      | `.docx`, `.doc`, `.rtf`                                     | Download / open in a new tab    |
| Subtitles | `.srt`, `.vtt`, `.ass`, `.sub`, `.sbv`                      | Converted to WebVTT, attached to the video |
| Quizzes   | any document whose name contains `quiz`, `exam`, `test`, …  | Badge + document view           |

> Browsers only play the codecs they know: `.mkv`/`.avi` containers may still need to be
> converted to `.mp4`/`.webm` before they play in the browser.

---

## ⚙️ CLI Options

| Option                             | Description                                                    |
| ---------------------------------- | -------------------------------------------------------------- |
| `--host`                           | Host to bind to (default: `127.0.0.1`, env `OFFLINEU_HOST`)     |
| `--port`                           | Port to bind to (default: `5000`, env `OFFLINEU_PORT`)          |
| `--debug`                          | Enable Flask debug mode                                        |
| `--create-templates` / `--check-templates` | Verify that the HTML templates are present (creates `templates/` if missing) |
| `<course_path>`                    | Load a course directly at startup                              |

---

## 🌍 Environment Variables

| Variable                | Description                                                                                            |
| ----------------------- | ------------------------------------------------------------------------------------------------------ |
| `OFFLINEU_HOST`         | Default value for `--host` (use `0.0.0.0` inside Docker)                                                |
| `OFFLINEU_PORT`         | Default value for `--port`                                                                             |
| `OFFLINEU_ROOTS`        | Allow-list of folders (`:`/`;` separated). When set, browsing, loading and file serving are limited to these folders. **Recommended whenever OfflineU is reachable from the network.** |
| `OFFLINEU_PROGRESS_DIR` | Store progress files **and the course list** (`offlineu_state.json`) in this folder instead of inside the course folder / next to the app. Needed when the course folder is read-only. |
| `AUTO_LOAD_COURSE`      | Load this course as soon as the server starts (same as passing the path)                                |
| `OFFLINEU_SECRET_KEY`   | Flask secret key. A random one is generated per start when unset (OfflineU keeps no sessions).          |

---

## 💾 How progress is stored

* Every course gets one JSON file. By default it lives **inside the course folder** as
  `.offlineu_progress.json`; with `OFFLINEU_PROGRESS_DIR` it is stored as
  `<md5-ish hash>_progress.json` in that folder instead.
* Keys are **course-relative paths** (e.g. `Section 1/01 - Intro.mp4`), so renaming the
  lesson display name no longer loses progress. Files written by older versions (which used
  `<path>/<Title_With_Underscores>`) are still read.
* Each entry stores `completed`, `progress_seconds` and `last_accessed`.
  Watched seconds never move backwards, and opening a lesson **never** resets its completion.
* Writes are locked and atomic (temp file + `os.replace`), so an interrupted save cannot
  truncate your progress file. If the location is not writable, the API returns an error and
  the lesson page shows a warning instead of failing silently.

### Remembered courses

* Next to the progress files OfflineU keeps one small bookkeeping file, `offlineu_state.json`
  (in `OFFLINEU_PROGRESS_DIR` when set, otherwise `./data/` next to the app). It stores the
  course you are on (`active_course`) and the courses you opened before (`recent_courses`,
  newest first, up to 20).
* Opening the home page reopens the last course automatically - so a page reload, a server
  restart or a container restart no longer looks like "my course was deleted".
* **Select Different Course** keeps the course in the picker's *Recent courses* card, so
  switching back is one click and never requires typing a path again. The `✕` button removes
  an entry from that list; it never touches your files or progress.
* Entries whose folder disappeared, or that fall outside `OFFLINEU_ROOTS`, are hidden
  automatically. Deleting the state file only clears the convenience list.

### Player behaviour

* **Autoplay next:** when a video/audio lesson reaches the end it is marked as completed and
  ~1 s later the page moves on to the next *playable* lesson. Documents sitting between two
  videos are skipped (they cannot be played); if nothing playable is left, the next lesson -
  document or not - is opened so the course still advances. The last lesson simply stops.
* The **"Autoplay next lesson"** checkbox in the player toolbar turns this off, and the choice
  is remembered in your browser.
* **Playback speed:** the toolbar offers 0.5× - 2× and stays in sync with the browser's own
  speed menu. The chosen rate is remembered per browser and re-applied to every following
  lesson, so a course watched at 1.5× keeps playing at 1.5× - including when autoplay moves on
  or the page is reloaded. Resume position and speed are set *before* autoplay starts.
* Browsers sometimes refuse to start playback with sound on a freshly loaded page; OfflineU
  then shows "Autoplay was blocked by the browser - press play to continue".
* These preferences live in your browser's localStorage (`offlineu.playbackRate`,
  `offlineu.autoplayNext`), never in the course folder.


---

## 🐳 Docker

```bash
docker compose up -d
# then open http://localhost:5000
```

`docker-compose.yml` mounts `./courses` (your library, read-only) and `./data` (progress) and
sets `OFFLINEU_ROOTS=/app/courses`, so the container can only ever see your course folder:

```yaml
environment:
  - OFFLINEU_ROOTS=/app/courses
  - OFFLINEU_PROGRESS_DIR=/app/data
  # - AUTO_LOAD_COURSE=/app/courses/My Course
volumes:
  - ./courses:/app/courses:ro
  - ./data:/app/data
```

The image (`ghcr.io/skippysteve/offlineu`) is built and pushed by GitHub Actions on every
push to `main` and every `v*` tag, after the test suite passes.

Because `OFFLINEU_PROGRESS_DIR` points at `/app/data`, the mounted `./data` folder also keeps
the course list: restart or recreate the container and OfflineU reopens the course you had
open (and still offers every other one it remembers).

### Building / moving the image

```bash
docker build -t offlineu:latest .
```

Export the built image as one file, so it can be copied to any machine (no registry needed):

```bash
docker save -o dist/offlineu-image.tar offlineu:latest   # -> ~210 MB tar
# on the target machine
docker load -i offlineu-image.tar                        # or: docker load < offlineu-image.tar
docker run -d --name offlineu -p 5000:5000 \
  -v /path/to/your/courses:/app/courses:ro \
  -v /path/to/offlineu-data:/app/data \
  offlineu:latest
```

The course library is mounted read-only: progress files *and* the course list are written to
`/app/data`, so the course folders are never modified.


---

## 🔌 HTTP API

| Method | Endpoint                          | Purpose                                                        |
| ------ | --------------------------------- | -------------------------------------------------------------- |
| GET    | `/`                               | Dashboard, or the course picker when nothing is loaded          |
| GET    | `/browse?path=<dir>`              | JSON listing of one directory level (used by the picker)        |
| POST   | `/load_course`                    | `{"course_path": "..."}` - parse a folder and make it active    |
| GET    | `/lesson/<lesson_path>`           | Lesson page (player, documents, prev/next)                      |
| POST   | `/api/progress`                   | Partial update: `{"lesson_path", "completed", "progress_seconds"}` (omitted fields are left untouched) |
| GET    | `/files/<path>`                   | Serve a file inside the active course                           |
| GET    | `/subtitles/<path>`               | Serve subtitles as WebVTT (converts SRT on the fly)             |
| GET    | `/health`                         | `{"status": "healthy"}` (used by Docker health checks)          |
| GET    | `/reset_course`                   | Back to the picker; the course stays in the recent list         |
| GET    | `/forget_course?path=<dir>`       | Remove one entry from the recent course list                    |

---

## 🧪 Tests

The suite uses only the standard library plus Flask's test client, so it runs offline:

```bash
python -m unittest discover -s tests -v
```

It covers folder parsing, subtitle attachment, document modes, progress persistence
(including the "revisiting a lesson must not reset it" regression), path-traversal
protection, the `OFFLINEU_ROOTS` allow-list, the JSON API and the CLI.

---

## 🛡️ Security notes

OfflineU is designed for a trusted LAN or a single machine:

* It **binds to `127.0.0.1` by default**. Only expose it (`--host 0.0.0.0`) if you have to.
* There is **no authentication**. Anyone who can reach the port can browse the folders that
  are in scope and download the files inside them.
* Set **`OFFLINEU_ROOTS`** to a dedicated course folder to limit what is browsable and
  servable. File requests are resolved with `os.path.commonpath` and symlinks are resolved, so
  `..` escapes and prefix tricks (e.g. `/courses-ab` for `/courses`) are rejected.

---

## 🧠 Roadmap

* [x] Base function and testing
* [x] Self hosted Docker Deployment
* [x] Directory browser, subtitle support, keyboard shortcuts
* [ ] Multi-user profile support
* [ ] Dark/light theme switcher
* [ ] Built-in quiz interactivity
* [ ] Import/export course metadata
* [ ] Mobile app wrapper

---

## 💬 Community

Join the development, suggest features, or ask questions via:

* GitHub Issues: [https://github.com/WhiskeyCoder/OfflineU/issues](https://github.com/WhiskeyCoder/OfflineU/issues)

---

## 🛡️ License

MIT License - Use freely, modify locally, share widely.

---

## ✨ Author

Built with ❤️ by [@WhiskeyCoder](https://github.com/WhiskeyCoder)
Inspired by the dream of **learning freely, offline, and without limits.**


