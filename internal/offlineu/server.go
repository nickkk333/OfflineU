package offlineu

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// App wires the configuration, the course store and the progress tracker into an
// http.Handler that serves the Vue single page app, the JSON API and course
// files.
type App struct {
	Config   *Config
	Store    *CourseStore
	Progress *ProgressTracker

	web      fs.FS
	index    []byte
	hasIndex bool
}

// NewApp builds an application from a configuration.
func NewApp(cfg Config) *App {
	store := NewCourseStore(&cfg)
	return &App{Config: &cfg, Store: store, Progress: &ProgressTracker{}}
}

// Handler attaches the built frontend (embedded or on disk) and returns the
// ready to use HTTP handler.
func (a *App) Handler(web fs.FS) http.Handler {
	a.web = web
	if web != nil {
		if data, err := fs.ReadFile(web, "index.html"); err == nil {
			a.index = data
			a.hasIndex = true
		}
	}
	return a
}

// ServeHTTP dispatches every request. The routes are matched by hand (instead of
// using http.ServeMux) because the mux would clean "../" segments and answer with
// a redirect instead of the 403 that OfflineU promises for traversal attempts.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.Config.RefreshRoots()
	requestPath := r.URL.Path

	switch {
	case requestPath == "/health":
		a.handleHealth(w, r)
	case requestPath == "/browse" || requestPath == "/api/browse":
		a.handleBrowse(w, r)
	case requestPath == "/load_course" || requestPath == "/api/load_course":
		a.handleLoadCourse(w, r)
	case requestPath == "/api/state":
		a.handleState(w, r)
	case requestPath == "/api/lesson":
		a.handleLessonAPI(w, r)
	case requestPath == "/api/progress":
		a.handleProgress(w, r)
	case requestPath == "/api/reset_course":
		a.handleResetCourseAPI(w, r)
	case requestPath == "/api/forget_course":
		a.handleForgetCourseAPI(w, r)
	case requestPath == "/reset_course":
		a.handleResetCourse(w, r)
	case requestPath == "/forget_course":
		a.handleForgetCourse(w, r)
	case strings.HasPrefix(requestPath, "/files/"):
		a.handleFile(w, r)
	case strings.HasPrefix(requestPath, "/subtitles/"):
		a.handleSubtitle(w, r)
	default:
		a.handleStatic(w, r)
	}
}

// ActiveCourse returns the course in memory, restoring the remembered one when
// the process just started (or the home page was opened after a reload).
func (a *App) ActiveCourse() *Course {
	if course := a.Store.Get(); course != nil {
		return course
	}
	return a.Store.Restore()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

// relativeFromPrefix extracts the path that follows prefix from the *escaped*
// request path and decodes it exactly once.
func relativeFromPrefix(r *http.Request, prefix string) (string, bool) {
	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, prefix) {
		return "", false
	}
	decoded, err := url.PathUnescape(strings.TrimPrefix(escaped, prefix))
	if err != nil {
		return "", false
	}
	return decoded, true
}

func decodeJSON(r *http.Request, target any) error {
	defer io.Copy(io.Discard, r.Body)
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(target)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

type courseSummary struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	ProgressFile string `json:"progress_file"`
}

// handleState is the single call the SPA uses to render the home page: it
// returns the active course (if any), its tree, the stats and the recent list.
func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	roots := a.Config.Roots
	if roots == nil {
		roots = []string{}
	}
	course := a.ActiveCourse()
	if course == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"version":        Version,
			"roots":          roots,
			"course":         nil,
			"tree":           nil,
			"stats":          Stats{},
			"recent_courses": a.recentCoursesWithProgress(),
		})
		return
	}

	a.Progress.ApplyToTree(course)
	AttachNodeStats(course.Root)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version,
		"roots":   roots,
		"course": courseSummary{
			Name:         course.Name,
			Path:         course.Path,
			ProgressFile: course.ProgressFile,
		},
		"tree":           course.Root,
		"stats":          a.Progress.Stats(course),
		"recent_courses": []RecentCourse{},
	})
}

// recentCoursesWithProgress lists the remembered courses and adds how far each
// of them was watched, so the picker can show a progress percentage next to the
// "Open" button.
//
// The lesson total is cached in the state file when a course is opened; it is
// only re-scanned for entries that do not have it yet (state files written by an
// older version), and the result is written back straight away. The number of
// completed lessons is read from the course progress file on every call, which
// keeps the picker correct after a lesson was finished.
func (a *App) recentCoursesWithProgress() []RecentCourse {
	courses := a.Store.RecentCourses()
	for index := range courses {
		entry := &courses[index]
		entry.CompletedLessons = CountCompleted(a.Config.ProgressFileFor(entry.Path))
		if entry.TotalLessons == 0 {
			if parsed, err := a.Config.ScanCourse(entry.Path); err == nil {
				entry.TotalLessons = len(AllLessons(parsed.Root))
			}
		}
		if entry.TotalLessons > 0 && entry.CompletedLessons > entry.TotalLessons {
			entry.CompletedLessons = entry.TotalLessons
		}
	}
	a.Store.SetRecentTotals(courses)
	return courses
}

type browseEntry struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	MediaFiles        int    `json:"media_files"`
	IsCourseCandidate bool   `json:"is_course_candidate"`
}

type browsePayload struct {
	CurrentPath string        `json:"current_path"`
	ParentPath  *string       `json:"parent_path"`
	Directories []browseEntry `json:"directories"`
	Roots       []string      `json:"roots"`
}

// handleBrowse is the JSON directory browser used by the course picker.
func (a *App) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	roots := a.Config.Roots
	requested := strings.TrimSpace(r.URL.Query().Get("path"))

	if requested == "" {
		if len(roots) > 0 {
			a.writeBrowse(w, roots[0], roots)
			return
		}
		if runtime.GOOS == "windows" {
			writeJSON(w, http.StatusOK, browsePayload{
				CurrentPath: "Select a Drive",
				Directories: windowsDrives(),
				Roots:       []string{},
			})
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot determine the home directory")
			return
		}
		a.writeBrowse(w, home, roots)
		return
	}

	current := filepath.Clean(ExpandHome(requested))
	if !a.Config.InsideRoots(current) {
		writeError(w, http.StatusForbidden, "this path is outside the configured OFFLINEU_ROOTS")
		return
	}
	info, err := os.Stat(current)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, "not a directory: "+current)
		return
	}
	a.writeBrowse(w, current, roots)
}

func (a *App) writeBrowse(w http.ResponseWriter, current string, roots []string) {
	entries, err := sortedEntries(current)
	if err != nil {
		writeError(w, http.StatusForbidden, "access denied to "+current+": "+err.Error())
		return
	}
	if roots == nil {
		roots = []string{}
	}

	directories := []browseEntry{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
			continue
		}
		full := filepath.Join(current, entry.Name())
		media, err := countMediaFiles(full, MaxMediaScanEntries)
		if err != nil {
			directories = append(directories, browseEntry{
				Name: entry.Name() + " (access denied)",
				Path: full,
			})
			continue
		}
		directories = append(directories, browseEntry{
			Name:              entry.Name(),
			Path:              full,
			MediaFiles:        media,
			IsCourseCandidate: media > 0,
		})
	}

	var parent *string
	parentPath := filepath.Dir(current)
	if filepath.Clean(parentPath) != filepath.Clean(current) && a.Config.InsideRoots(parentPath) {
		value := parentPath
		parent = &value
	}

	writeJSON(w, http.StatusOK, browsePayload{
		CurrentPath: current,
		ParentPath:  parent,
		Directories: directories,
		Roots:       roots,
	})
}

// windowsDrives lists the mounted drive letters for the picker.
func windowsDrives() []browseEntry {
	drives := []browseEntry{}
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + ":\\"
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			drives = append(drives, browseEntry{Name: "Drive " + string(letter) + ":", Path: root})
		}
	}
	return drives
}

// handleLoadCourse parses a directory and makes it the active course.
func (a *App) handleLoadCourse(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var payload struct {
		CoursePath string `json:"course_path"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid course path")
		return
	}
	coursePath := strings.TrimSpace(payload.CoursePath)
	if coursePath == "" {
		writeError(w, http.StatusBadRequest, "invalid course path")
		return
	}

	candidate := filepath.Clean(ExpandHome(coursePath))
	info, err := os.Stat(candidate)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "directory not found: "+coursePath)
		return
	}
	if !a.Config.InsideRoots(candidate) {
		writeError(w, http.StatusForbidden, "this path is outside the configured OFFLINEU_ROOTS")
		return
	}

	course, err := a.Config.ScanCourse(candidate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.Store.Set(course)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"course_name": course.Name,
		"course": courseSummary{
			Name:         course.Name,
			Path:         course.Path,
			ProgressFile: course.ProgressFile,
		},
	})
}

type lessonPayload struct {
	Course            courseSummary  `json:"course"`
	Lesson            *Lesson        `json:"lesson"`
	Resources         []TextResource `json:"resources"`
	PrevURL           string         `json:"prev_url,omitempty"`
	NextURL           string         `json:"next_url,omitempty"`
	AutoplayURL       string         `json:"autoplay_url,omitempty"`
	AutoplayTitle     string         `json:"autoplay_title,omitempty"`
	AutoplayHref      string         `json:"autoplay_href,omitempty"`
	RequestedAutoplay bool           `json:"requested_autoplay"`
	Position          int            `json:"position"`
	Total             int            `json:"total"`
	StorageWarning    string         `json:"storage_warning,omitempty"`
}

// handleLessonAPI returns everything the lesson view needs: the lesson itself,
// its documents, the neighbours and the next playable lesson for autoplay.
func (a *App) handleLessonAPI(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	course := a.ActiveCourse()
	if course == nil {
		writeError(w, http.StatusNotFound, "no course loaded")
		return
	}
	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	lesson := FindLessonInTree(course.Root, requested)
	if lesson == nil {
		writeError(w, http.StatusNotFound, "lesson not found")
		return
	}

	// Refresh first so deep links (and reloads) show the stored completion state
	// and resume position instead of the values parsed a while ago.
	a.Progress.ApplyToTree(course)

	lessons := AllLessons(course.Root)
	position := -1
	for index, candidate := range lessons {
		if candidate == lesson {
			position = index
			break
		}
	}

	payload := lessonPayload{
		Course: courseSummary{
			Name:         course.Name,
			Path:         course.Path,
			ProgressFile: course.ProgressFile,
		},
		Lesson:            lesson,
		Resources:         BuildTextResources(lesson),
		RequestedAutoplay: r.URL.Query().Get("autoplay") == "1",
		Position:          position,
		Total:             len(lessons),
	}
	if position > 0 {
		payload.PrevURL = lessons[position-1].URL
	}
	if position >= 0 && position < len(lessons)-1 {
		payload.NextURL = lessons[position+1].URL
	}

	// What plays after this lesson ends: the next playable lesson (documents are
	// skipped because they cannot be played), falling back to the next lesson so
	// a course whose tail is made of documents still advances.
	var later []*Lesson
	if position >= 0 {
		later = lessons[position+1:]
	}
	var autoplayLesson *Lesson
	for _, candidate := range later {
		if candidate.HasMedia() {
			autoplayLesson = candidate
			break
		}
	}
	if autoplayLesson == nil && len(later) > 0 {
		autoplayLesson = later[0]
	}
	if autoplayLesson != nil {
		payload.AutoplayURL = autoplayLesson.URL
		payload.AutoplayTitle = autoplayLesson.Title
		payload.AutoplayHref = "/lesson/" + EscapeURLPath(autoplayLesson.URL) + "?autoplay=1"
	}

	if _, err := a.Progress.RecordAccess(course, lesson.RelPath); err != nil {
		payload.StorageWarning = err.Error()
		logf("warning: %v", err)
	}
	writeJSON(w, http.StatusOK, payload)
}

// handleProgress stores progress for one lesson; omitted fields stay untouched.
func (a *App) handleProgress(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	course := a.Store.Get()
	if course == nil {
		writeError(w, http.StatusBadRequest, "no course loaded")
		return
	}
	var payload map[string]any
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "lesson_path is required")
		return
	}
	rawPath, _ := payload["lesson_path"].(string)
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "lesson_path is required")
		return
	}

	lesson := FindLessonInTree(course.Root, rawPath)
	lessonKey := NormalizeRel(rawPath)
	if lesson != nil {
		lessonKey = lesson.RelPath
	}

	var completed *bool
	if value, ok := payload["completed"]; ok && value != nil {
		if boolean, ok := value.(bool); ok {
			completed = &boolean
		}
	}
	var seconds *int
	if value, ok := payload["progress_seconds"]; ok && value != nil {
		if number, ok := value.(float64); ok {
			integer := int(number)
			seconds = &integer
		}
	}

	entry, err := a.Progress.Update(course, lessonKey, completed, seconds)
	if err != nil {
		var storage *ProgressStorageError
		if errors.As(err, &storage) {
			writeError(w, http.StatusInternalServerError, storage.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"lesson_key": lessonKey,
		"progress":   entry,
	})
}

// handleResetCourse clears the active course but keeps it in the recent list.
func (a *App) handleResetCourse(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	a.Store.Clear()
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleResetCourseAPI is the SPA friendly variant of /reset_course.
func (a *App) handleResetCourseAPI(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	a.Store.Clear()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleForgetCourse removes one entry from the recent course list.
func (a *App) handleForgetCourse(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if forgotten := r.URL.Query().Get("path"); forgotten != "" {
		a.Store.Forget(forgotten)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleForgetCourseAPI is the SPA friendly variant of /forget_course.
func (a *App) handleForgetCourseAPI(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if strings.TrimSpace(payload.Path) == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	a.Store.Forget(payload.Path)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// resolveCourseFile validates that a requested file really lives inside the
// active course (and inside OFFLINEU_ROOTS when it is configured).
func (a *App) resolveCourseFile(w http.ResponseWriter, r *http.Request, prefix string) (string, bool) {
	course := a.Store.Get()
	if course == nil {
		http.Error(w, "No course loaded", http.StatusNotFound)
		return "", false
	}
	relative, ok := relativeFromPrefix(r, prefix)
	if !ok {
		http.Error(w, "Access denied", http.StatusForbidden)
		return "", false
	}
	full, ok := ResolveInside(course.Path, relative)
	if !ok {
		http.Error(w, "Access denied", http.StatusForbidden)
		return "", false
	}
	if !a.Config.InsideRoots(full) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return "", false
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.Error(w, "File not found", http.StatusNotFound)
		return "", false
	}
	return full, true
}

// handleFile serves any file that lives inside the active course directory.
func (a *App) handleFile(w http.ResponseWriter, r *http.Request) {
	full, ok := a.resolveCourseFile(w, r, "/files/")
	if !ok {
		return
	}
	handle, err := os.Open(full)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", GuessMime(full, "application/octet-stream"))
	http.ServeContent(w, r, filepath.Base(full), info.ModTime(), handle)
}

// handleSubtitle serves subtitles as WebVTT, converting SRT (and friends) on
// the fly because browsers cannot display them otherwise.
func (a *App) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	full, ok := a.resolveCourseFile(w, r, "/subtitles/")
	if !ok {
		return
	}
	var body string
	if strings.EqualFold(filepath.Ext(full), ".vtt") {
		raw, err := os.ReadFile(full)
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		body = string(raw)
	} else {
		text, err := ReadSubtitleText(full)
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		body = ConvertSRTToVTT(text)
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
}

// handleStatic serves the built Vue assets and falls back to index.html so the
// SPA router can handle deep links such as /lesson/<path>.
func (a *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.web != nil {
		rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if rel != "" {
			name := path.Clean(rel)
			if !strings.HasPrefix(name, "..") && !strings.Contains(name, "/../") {
				if data, err := fs.ReadFile(a.web, name); err == nil {
					w.Header().Set("Content-Type", staticContentType(name))
					if strings.HasPrefix(name, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					http.ServeContent(w, r, filepath.Base(name), time.Time{}, bytes.NewReader(data))
					return
				}
			}
		}
	}
	a.serveIndex(w, r)
}

func staticContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".map":
		return "application/json; charset=utf-8"
	default:
		return GuessMime(name, "application/octet-stream")
	}
}

func (a *App) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if a.hasIndex {
		_, _ = w.Write(a.index)
		return
	}
	_, _ = io.WriteString(w, fallbackIndexPage)
}

// fallbackIndexPage is shown when the frontend has not been built yet, so the
// API remains usable and the operator knows exactly what to run.
const fallbackIndexPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OfflineU - frontend not built</title>
<style>
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         background: radial-gradient(circle at 20% 20%, #1e293b, #0b1020 60%);
         color: #e2e8f0; font-family: system-ui, -apple-system, Segoe UI, sans-serif; }
  .card { max-width: 620px; padding: 40px; border-radius: 20px;
          background: rgba(255,255,255,.05); border: 1px solid rgba(255,255,255,.1);
          box-shadow: 0 30px 60px rgba(0,0,0,.45); }
  h1 { margin: 0 0 12px; font-size: 1.6rem; }
  code { background: rgba(99,102,241,.2); padding: 2px 8px; border-radius: 6px;
         color: #c7d2fe; font-size: .95em; }
  pre { background: #0f172a; padding: 16px; border-radius: 12px; overflow: auto; }
  p { line-height: 1.7; color: #cbd5e1; }
</style>
</head>
<body>
  <div class="card">
    <h1>OfflineU is running, but the frontend bundle is missing</h1>
    <p>The API is available (<code>/health</code>, <code>/api/state</code>), only the
       Vue assets have not been built yet. Build them once and reload:</p>
    <pre>cd web
npm install
npm run build
cd ..
go run .</pre>
    <p>For development you can instead run <code>npm run dev</code> inside
       <code>web/</code>, which proxies the API to this server.</p>
  </div>
</body>
</html>
`
