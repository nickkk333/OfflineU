package offlineu

import (
	"bytes"
	"context"
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
	"strconv"
	"strings"
	"time"
)

// App wires the configuration, the course store and the progress tracker into an
// http.Handler that serves the Vue single page app, the JSON API and course
// files.
type App struct {
	Config     *Config
	Store      *CourseStore
	Progress   *ProgressTracker
	DLNA       *DLNAHub
	Transcoder *Transcoder

	// cast is the lesson currently playing on a renderer, if any: the browser
	// polls it to show the position and the watchdog continues with the next
	// lesson when it ends.
	cast castLock

	web      fs.FS
	index    []byte
	hasIndex bool
}

// NewApp builds an application from a configuration.
func NewApp(cfg Config) *App {
	store := NewCourseStore(&cfg)
	return &App{
		Config:     &cfg,
		Store:      store,
		Progress:   &ProgressTracker{},
		DLNA:       NewDLNAHub(),
		Transcoder: NewTranscoder(cfg.CacheDir, cfg.CacheLimit, cfg.Transcode),
	}
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
	case requestPath == "/api/dlna/devices":
		a.handleDLNADevices(w, r)
	case requestPath == "/api/dlna/cast":
		a.handleDLNACast(w, r)
	case requestPath == "/api/dlna/stream":
		a.handleDLNAStream(w, r)
	case requestPath == "/api/dlna/session":
		a.handleDLNASession(w, r)
	case requestPath == "/api/dlna/control":
		a.handleDLNAControl(w, r)
	case requestPath == "/api/settings":
		a.handleSettings(w, r)
	case requestPath == "/api/media/status":
		a.handleMediaStatus(w, r)
	case requestPath == "/api/hls/playlist" || requestPath == "/api/hls/playlist.m3u8":
		a.handleHLSPlaylist(w, r)
	case requestPath == "/api/hls/segment":
		a.handleHLSSegment(w, r)
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

// requestLanguage reports which UI language the caller asked for. Only the few
// strings generated on the server (currently the mount hint) follow it.
func requestLanguage(r *http.Request) string {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "zh") {
		return "zh"
	}
	return "en"
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
	DisplayPath  string `json:"display_path,omitempty"`
	ProgressFile string `json:"progress_file"`
}

// summaryFor describes a course for the API, using the mapped-folder relative
// path for everything the UI prints.
func (a *App) summaryFor(course *Course) courseSummary {
	return courseSummary{
		Name:         course.Name,
		Path:         course.Path,
		DisplayPath:  a.Config.DisplayPath(course.Path),
		ProgressFile: course.ProgressFile,
	}
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
			"version":         Version,
			"roots":           roots,
			"course":          nil,
			"tree":            nil,
			"stats":           Stats{},
			"recent_courses":  a.recentCoursesWithProgress(),
			"roots_display":   a.Config.RootDisplays(),
			"needs_mount":     a.Config.NeedsMount(),
			"read_capability": readCapabilityValue(),
			"mount_issue":     string(a.Config.MountIssue()),
			"roots_detail":    a.Config.RootStatuses(),
			"mount_hint":      MountHintFor(requestLanguage(r)),
			"dlna_enabled":    a.Config.DLNAEnabled,
			"play_mode":       a.Store.PlayMode(),
			"cast_play_mode":  a.Store.CastPlayMode(),
		})
		return
	}

	a.Progress.ApplyToTree(course)
	AttachNodeStats(course.Root)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         Version,
		"roots":           roots,
		"course":          a.summaryFor(course),
		"tree":            course.Root,
		"stats":           a.Progress.Stats(course),
		"recent_courses":  []RecentCourse{},
		"roots_display":   a.Config.RootDisplays(),
		"needs_mount":     false,
		"read_capability": readCapabilityValue(),
		"mount_issue":     string(a.Config.MountIssue()),
		"roots_detail":    a.Config.RootStatuses(),
		"mount_hint":      MountHintFor(requestLanguage(r)),
		"dlna_enabled":    a.Config.DLNAEnabled,
		"play_mode":       a.Store.PlayMode(),
		"cast_play_mode":  a.Store.CastPlayMode(),
	})
}

// readCapabilityValue renders ReadCapability() for /api/state: true/false inside
// a Linux container and null where the question cannot be answered at all (no
// /proc). The frontend only explains the capability when it gets a plain false,
// so a desktop build never accuses the container of having dropped it.
func readCapabilityValue() any {
	if readable, known := ReadCapability(); known {
		return readable
	}
	return nil
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
		entry.DisplayPath = a.Config.DisplayPath(entry.Path)
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
	DisplayPath       string `json:"display_path,omitempty"`
	MediaFiles        int    `json:"media_files"`
	IsCourseCandidate bool   `json:"is_course_candidate"`
}

type browsePayload struct {
	CurrentPath    string        `json:"current_path"`
	CurrentDisplay string        `json:"current_display_path,omitempty"`
	ParentPath     *string       `json:"parent_path"`
	ParentDisplay  string        `json:"parent_display_path,omitempty"`
	Directories    []browseEntry `json:"directories"`
	Roots          []string      `json:"roots"`
	RootsDisplay   []string      `json:"roots_display,omitempty"`
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
		display := a.Config.DisplayPath(full)
		media, err := countMediaFiles(full, MaxMediaScanEntries)
		if err != nil {
			directories = append(directories, browseEntry{
				Name:        entry.Name() + " (access denied)",
				Path:        full,
				DisplayPath: display,
			})
			continue
		}
		directories = append(directories, browseEntry{
			Name:              entry.Name(),
			Path:              full,
			DisplayPath:       display,
			MediaFiles:        media,
			IsCourseCandidate: media > 0,
		})
	}

	var parent *string
	parentDisplay := ""
	parentPath := filepath.Dir(current)
	if filepath.Clean(parentPath) != filepath.Clean(current) && a.Config.InsideRoots(parentPath) {
		value := parentPath
		parent = &value
		parentDisplay = a.Config.DisplayPath(parentPath)
	}

	writeJSON(w, http.StatusOK, browsePayload{
		CurrentPath:    current,
		CurrentDisplay: a.Config.DisplayPath(current),
		ParentPath:     parent,
		ParentDisplay:  parentDisplay,
		Directories:    directories,
		Roots:          roots,
		RootsDisplay:   a.Config.RootDisplays(),
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
		"course":      a.summaryFor(course),
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
	DLNAEnabled       bool           `json:"dlna_enabled"`
	CastPlan          CastPlan       `json:"cast_plan"`
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
		Course:            a.summaryFor(course),
		Lesson:            lesson,
		Resources:         BuildTextResources(lesson),
		RequestedAutoplay: r.URL.Query().Get("autoplay") == "1",
		Position:          position,
		Total:             len(lessons),
		DLNAEnabled:       a.Config.DLNAEnabled,
		CastPlan:          a.castPlanFor(r.Context(), course, lesson),
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
		a.forgetCourse(forgotten)
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
	a.forgetCourse(payload.Path)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// forgetCourse drops a course from the recent list *and* deletes its stored
// progress, so opening the folder again starts from scratch: the X button in
// the picker must really remove the course, not only hide it.
//
// The path is resolved exactly like ScanCourse does (abs + symlinks), so the
// derived progress file matches the one the course was written to. The file
// name itself is computed by the server and only courses inside OFFLINEU_ROOTS
// lose their data, so a crafted request cannot delete an arbitrary file.
func (a *App) forgetCourse(raw string) {
	coursePath := strings.TrimSpace(raw)
	if coursePath == "" {
		return
	}
	if absolute, err := filepath.Abs(ExpandHome(coursePath)); err == nil {
		coursePath = absolute
	}
	if resolved, err := filepath.EvalSymlinks(coursePath); err == nil {
		coursePath = resolved
	}
	coursePath = filepath.Clean(coursePath)

	// The recent list entry goes first: forgetting must work even when the
	// folder sits outside OFFLINEU_ROOTS (it simply stops being offered).
	a.Store.Forget(coursePath)
	if !a.Config.InsideRoots(coursePath) {
		return
	}
	if err := a.Progress.Delete(a.Config.ProgressFileFor(coursePath)); err != nil {
		logf("warning: cannot delete the progress of %s: %v", coursePath, err)
	}
}

// handleDLNADevices lists the media renderers (TVs, speakers, …) that answered
// the SSDP search. ?refresh=1 repeats the search instead of reading the cache.
func (a *App) handleDLNADevices(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if !a.Config.DLNAEnabled {
		writeError(w, http.StatusForbidden, "dlna is disabled")
		return
	}
	devices, err := a.DLNA.Devices(r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"devices": devices,
	})
}

// handleDLNACast hands the media file of a lesson to one renderer and starts
// playback: the SetAVTransportURI and Play SOAP actions of its AVTransport.
func (a *App) handleDLNACast(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !a.Config.DLNAEnabled {
		writeError(w, http.StatusForbidden, "dlna is disabled")
		return
	}
	course := a.ActiveCourse()
	if course == nil {
		writeError(w, http.StatusBadRequest, "no course loaded")
		return
	}
	var payload struct {
		Device       string  `json:"device"`
		LessonPath   string  `json:"lesson_path"`
		StartSeconds *int    `json:"start_seconds"`
		Transcode    string  `json:"transcode"` // "auto" (default), "on" or "off"
		PlayMode     string  `json:"play_mode"` // "once" (default), "loop" or "next"
		Autoplay     *bool   `json:"autoplay"`  // legacy: true = "next", false = "once"
		Duration     float64 `json:"duration"`  // length the browser worked out (optional)
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "device and lesson_path are required")
		return
	}
	if strings.TrimSpace(payload.Device) == "" {
		writeError(w, http.StatusBadRequest, "device and lesson_path are required")
		return
	}
	lesson := FindLessonInTree(course.Root, strings.TrimSpace(payload.LessonPath))
	if lesson == nil {
		writeError(w, http.StatusNotFound, "lesson not found")
		return
	}
	startSeconds := 0
	if payload.StartSeconds != nil && *payload.StartSeconds > 0 {
		startSeconds = *payload.StartSeconds
	}
	baseURL := a.absoluteURL(r, "")
	target, hasMedia, err := a.castTarget(baseURL, course, lesson, payload.Transcode, startSeconds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !hasMedia {
		writeError(w, http.StatusBadRequest, "this lesson has no media to cast")
		return
	}
	renderer, ok := a.DLNA.Lookup(payload.Device)
	if !ok {
		writeError(w, http.StatusNotFound, "the cast device was not found on the network")
		return
	}
	// A converted stream starts at the right position already: ffmpeg was given
	// -ss, and a renderer cannot seek inside a live stream.
	seekTo := startSeconds
	if target.Converted {
		seekTo = 0
	}
	if err := a.DLNA.Cast(renderer, target.Item, seekTo); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Exactly what opening a lesson in the browser does: the resume card and
	// the "continue where you left off" entry follow the cast too.
	if _, err := a.Progress.RecordAccess(course, lesson.RelPath); err != nil {
		logf("dlna: cannot record the cast: %v", err)
	}

	// What happens when the lesson ends: the new play_mode wins, the legacy
	// autoplay boolean still maps onto it, and nothing at all means "once"
	// (stop when the lesson is over).
	playMode := PlayModeOnce
	switch {
	case strings.TrimSpace(payload.PlayMode) != "":
		playMode = normalizePlayMode(payload.PlayMode)
	case payload.Autoplay != nil && *payload.Autoplay:
		playMode = PlayModeNext
	}

	// Remember what is playing: the browser shows the position and the
	// watchdog applies the play mode when this lesson ends.
	// A cast remembers its own mode: what was picked here is the cast setting,
	// not the one the browser player uses.
	a.Store.SetCastPlayMode(playMode)

	session := &CastSession{
		UDN:          renderer.UDN,
		Device:       renderer.DisplayName(),
		CoursePath:   course.Path,
		LessonPath:   lesson.RelPath,
		LessonURL:    lesson.URL,
		LessonTitle:  lesson.Title,
		BaseURL:      baseURL,
		Converted:    target.Converted,
		PlayMode:     playMode,
		MediaFile:    a.mediaFileFor(course, lesson),
		StartSeconds: float64(startSeconds),
		Duration:     a.castDuration(course, lesson, payload.Duration),
		StartedAt:    time.Now(),
		State:        CastStatePlaying,
	}
	a.beginCast(session)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"device":    renderer.DisplayName(),
		"title":     target.Item.Title,
		"url":       target.Item.URL,
		"converted": target.Converted,
		"session":   a.castSnapshot(),
	})
}

// castDuration is how long the lesson plays. Without it the cast can never tell
// that a lesson ended (and would neither mark it completed nor continue), so
// three sources are tried in order: ffprobe/ffmpeg, the length the browser
// measured while loading the file, and - later, while playing - whatever the
// device itself reports (see CastSession.RealPosition).
func (a *App) castDuration(course *Course, lesson *Lesson, fromBrowser float64) float64 {
	if fromBrowser > 0 {
		return fromBrowser
	}
	if file := a.mediaFileFor(course, lesson); file != "" {
		if duration := a.mediaDuration(file); duration > 0 {
			return duration
		}
	}
	return 0
}

// mediaDuration asks ffprobe (or ffmpeg) how long a file plays; 0 means
// "unknown".
func (a *App) mediaDuration(file string) float64 {
	if file == "" || !a.Transcoder.Available() {
		return 0
	}
	ctx, cancel := probeContext()
	defer cancel()
	info, err := a.Transcoder.Probe(ctx, file)
	if err != nil {
		return 0
	}
	return info.Duration
}

// handleDLNASession reports the running cast: where the device is, how long the
// lesson is and which lesson comes next.
func (a *App) handleDLNASession(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if !a.Config.DLNAEnabled {
		writeError(w, http.StatusForbidden, "dlna is disabled")
		return
	}
	writeJSON(w, http.StatusOK, a.castSnapshot())
}

// castTarget is what a cast actually pushes: either the lesson file itself or,
// when the renderer would refuse it, a stream ffmpeg converts on the fly.
type castTarget struct {
	Item      MediaItem
	Converted bool
}

// castTarget builds the media of one lesson. mode is "auto" (convert when the
// container or the codecs need it), "on" (always convert) or "off" (never
// convert - hand over the file, which is what OfflineU always did).
func (a *App) castTarget(baseURL string, course *Course, lesson *Lesson, mode string, startSeconds int) (castTarget, bool, error) {
	relative, mime, class := lesson.VideoFile, lesson.VideoMime, "object.item.videoItem"
	if relative == "" {
		relative, mime, class = lesson.AudioFile, lesson.AudioMime, "object.item.audioItem.musicTrack"
	}
	if relative == "" {
		return castTarget{}, false, nil
	}
	if mime == "" {
		mime = GuessMime(relative, "video/mp4")
	}
	title := strings.TrimSpace(lesson.Title)
	if title == "" {
		title = BaseName(relative)
	}
	file := a.mediaFileFor(course, lesson)
	// Subtitles travel as a separate URL the renderer fetches on its own, so they
	// are attached no matter whether the video itself is transcoded or handed over
	// as a plain file. The ?raw=1 makes the server return the subtitle verbatim
	// (the TV decodes SRT/VTT itself, it does not want our WebVTT transcription).
	subURL, subType, subMime := "", "", ""
	if lesson.SubtitleFile != "" {
		subURL = baseURL + fileURL("/subtitles/", lesson.SubtitleFile) + "?raw=1"
		subType = captionType(lesson.SubtitleFile)
		subMime = subtitleProtocolMime(subType)
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on":
		if !a.Transcoder.Available() {
			return castTarget{}, false, errors.New("ffmpeg is not installed, so this lesson cannot be converted")
		}
	case "off":
		file = "" // fall through to the plain file
	default:
		needs := false
		if file != "" && a.Transcoder.Available() {
			ctx, cancel := probeContext()
			needs = a.Transcoder.Plan(ctx, file).NeedsTranscode
			cancel()
		}
		if !needs {
			file = ""
		}
	}
	if file == "" {
		return castTarget{Item: MediaItem{
			Title:        title,
			URL:          baseURL + fileURL("/files/", relative),
			Mime:         mime,
			Class:        class,
			SubtitleURL:  subURL,
			SubtitleType: subType,
			SubtitleMime: subMime,
		}}, true, nil
	}

	// The converted stream: MPEG-TS for video, AAC for a lesson that is audio.
	streamMime, streamClass := mpegTSMime, "object.item.videoItem"
	if lesson.VideoFile == "" {
		streamMime, streamClass = aacADTSMime, "object.item.audioItem.musicTrack"
	}
	query := url.Values{}
	query.Set("lesson", lesson.RelPath)
	if startSeconds > 0 {
		query.Set("start", strconv.Itoa(startSeconds))
	}
	return castTarget{
		Item: MediaItem{
			Title:        title,
			URL:          baseURL + "/api/dlna/stream?" + query.Encode(),
			Mime:         streamMime,
			Class:        streamClass,
			SubtitleURL:  subURL,
			SubtitleType: subType,
			SubtitleMime: subMime,
		},
		Converted: true,
	}, true, nil
}

// mediaFileFor resolves the playable file of a lesson inside the active course
// and returns "" when it is missing or not allowed.
func (a *App) mediaFileFor(course *Course, lesson *Lesson) string {
	if course == nil || lesson == nil {
		return ""
	}
	relative := lesson.VideoFile
	if relative == "" {
		relative = lesson.AudioFile
	}
	if relative == "" {
		return ""
	}
	full, ok := ResolveInside(course.Path, relative)
	if !ok || !a.Config.InsideRoots(full) {
		return ""
	}
	if info, err := os.Stat(full); err != nil || info.IsDir() {
		return ""
	}
	return full
}

// castPlanFor tells the UI whether a cast of this lesson will be converted (and
// whether it could be at all), so it can say so before the user picks a device.
func (a *App) castPlanFor(ctx context.Context, course *Course, lesson *Lesson) CastPlan {
	if !a.Config.DLNAEnabled || !a.Transcoder.Available() {
		return CastPlan{}
	}
	file := a.mediaFileFor(course, lesson)
	if file == "" {
		return CastPlan{}
	}
	return a.Transcoder.Plan(ctx, file)
}

// handleDLNAStream feeds a converted stream to a renderer. It is the endpoint
// /api/dlna/cast hands out when the original file would not play: ffmpeg
// repackages (or re-encodes) the lesson while the device is already playing.
func (a *App) handleDLNAStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.Config.DLNAEnabled {
		writeError(w, http.StatusForbidden, "dlna is disabled")
		return
	}
	course := a.ActiveCourse()
	if course == nil {
		writeError(w, http.StatusNotFound, "no course loaded")
		return
	}
	lesson := FindLessonInTree(course.Root, strings.TrimSpace(r.URL.Query().Get("lesson")))
	if lesson == nil {
		writeError(w, http.StatusNotFound, "lesson not found")
		return
	}
	file := a.mediaFileFor(course, lesson)
	if file == "" {
		writeError(w, http.StatusNotFound, "this lesson has no media to stream")
		return
	}
	if !a.Transcoder.Available() {
		writeError(w, http.StatusNotImplemented, "ffmpeg is not installed, so this file cannot be converted")
		return
	}
	startSeconds, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("start")))
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", mpegTSMime)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}

	info, err := a.Transcoder.Probe(r.Context(), file)
	if err != nil {
		// Without ffprobe we still know whether this is a video or an audio
		// lesson, which is enough to convert it (slower, but it plays).
		info = MediaInfo{}
		if extIn(strings.ToLower(filepath.Ext(file)), audioExtensions) {
			info.AudioCodec = "unknown"
		} else {
			info.VideoCodec = "unknown"
		}
		logf("dlna: %v - converting without probing", err)
	}
	if err := a.Transcoder.Stream(r.Context(), w, file, info, startSeconds); err != nil {
		logf("dlna: streaming %s failed: %v", filepath.Base(file), err)
	}
}

// handleDLNAControl sends Play, Pause or Stop to a renderer that is already
// playing something OfflineU pushed to it.
func (a *App) handleDLNAControl(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !a.Config.DLNAEnabled {
		writeError(w, http.StatusForbidden, "dlna is disabled")
		return
	}
	var payload struct {
		Device   string   `json:"device"`
		Action   string   `json:"action"`
		Position *float64 `json:"position"` // seconds, for "seek"
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "device and action are required")
		return
	}
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if action != "play" && action != "pause" && action != "stop" && action != "next" && action != "seek" {
		writeError(w, http.StatusBadRequest, "action must be play, pause, stop, next or seek")
		return
	}
	renderer, ok := a.DLNA.Lookup(payload.Device)
	if !ok {
		writeError(w, http.StatusNotFound, "the cast device was not found on the network")
		return
	}
	session := a.currentCast()
	if session != nil && session.UDN != renderer.UDN {
		session = nil // the request is about another device than the one casting
	}
	switch action {
	case "next":
		if session == nil {
			writeError(w, http.StatusBadRequest, "no cast is running on this device")
			return
		}
		if err := a.castNext(session); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "seek":
		if session == nil {
			writeError(w, http.StatusBadRequest, "no cast is running on this device")
			return
		}
		if payload.Position == nil {
			writeError(w, http.StatusBadRequest, "position is required")
			return
		}
		if err := a.castSeek(session, *payload.Position); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "pause", "play":
		if err := a.DLNA.Control(renderer, action); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		a.cast.mutex.Lock()
		if session != nil && !session.stopped {
			if action == "pause" {
				session.pause(time.Now())
			} else {
				session.resume(time.Now())
			}
		}
		a.cast.mutex.Unlock()
	case "stop":
		if err := a.DLNA.Control(renderer, "stop"); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		// Write the last position down before forgetting the cast.
		a.cast.mutex.Lock()
		if session != nil && !session.stopped {
			position := session.estimate(time.Now())
			if course := a.Store.Get(); course != nil && course.Path == session.CoursePath {
				seconds := int(position)
				if _, err := a.Progress.Update(course, session.LessonPath, nil, &seconds); err != nil {
					logf("dlna: cannot save the cast progress: %v", err)
				}
			}
		}
		a.cast.mutex.Unlock()
		a.endCast(session)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"device":  renderer.DisplayName(),
		"action":  action,
		"session": a.castSnapshot(),
	})
}

// handleSettings stores a client setting on the server, which is what makes it
// global: the choice is kept next to the course bookkeeping (state file) and
// handed to every browser through /api/state, so a new browser, another machine
// or a cleared cache still shows what was picked last.
//
// play_mode also drives the cast that is running right now: it is NOT frozen at
// the moment the cast starts, so switching 单播循环 / 单播不循环 / 连播 while the TV is
// playing takes effect - and a cast that already stopped in "once" mode is woken
// up again when the new mode wants more. Unlike /api/dlna/control this does not
// look the device up on the network first: the watchdog applies the mode, so a
// TV that is slow to answer must not make the switch fail.
func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var payload struct {
		Device       string `json:"device"`         // optional: only to be sure which device is meant
		PlayMode     string `json:"play_mode"`      // browser player: "once", "loop" or "next"
		CastPlayMode string `json:"cast_play_mode"` // cast on a renderer
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "play_mode or cast_play_mode is required")
		return
	}
	wantsLocal := strings.TrimSpace(payload.PlayMode) != ""
	wantsCast := strings.TrimSpace(payload.CastPlayMode) != ""
	if !wantsLocal && !wantsCast {
		writeError(w, http.StatusBadRequest, "play_mode or cast_play_mode is required")
		return
	}

	// The browser player and the cast keep their own choice: setting one never
	// touches the other.
	if wantsLocal {
		a.Store.SetPlayMode(payload.PlayMode)
	}
	castMode := ""
	if wantsCast {
		castMode = a.Store.SetCastPlayMode(payload.CastPlayMode)
		// A running cast picks the new mode up at once - no need to start again.
		if session := a.currentCast(); session != nil {
			if device := strings.TrimSpace(payload.Device); device == "" || device == session.UDN {
				a.setPlayMode(session, castMode)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"play_mode":      a.Store.PlayMode(),
		"cast_play_mode": a.Store.CastPlayMode(),
		"session":        a.castSnapshot(),
	})
}

// absoluteURL turns a server path into the URL a cast device can fetch. The
// renderer is another machine on the LAN, so it needs the same host the browser
// used (and not "localhost", which would point at the TV itself).
func (a *App) absoluteURL(r *http.Request, serverPath string) string {
	scheme := "http"
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.ToLower(strings.Split(forwarded, ",")[0])
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host == "" {
		return serverPath
	}
	return scheme + "://" + host + serverPath
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
	// Media files whose real container does not match a browser-friendly one
	// (for example an .mp4 that is actually an MPEG-TS stream) are repackaged to
	// a playable MP4 on the fly, or served with the correct MIME when the
	// extension merely lied about an otherwise native container. Without ffmpeg
	// this step is skipped and the bytes are served exactly as before.
	extension := strings.ToLower(filepath.Ext(full))
	if extIn(extension, videoExtensions) || extIn(extension, audioExtensions) {
		if a.Transcoder != nil && a.Transcoder.ServeBrowser(w, r, full) {
			return
		}
	}
	serveCourseFile(w, r, full, GuessMime(full, "application/octet-stream"))
}

// serveCourseFile streams a course file to the browser with the given MIME and
// full Range support (the standard path used for everything that is served as
// is, including the bytes ServeBrowser falls back to).
func serveCourseFile(w http.ResponseWriter, r *http.Request, full, mime string) {
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
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, filepath.Base(full), info.ModTime(), handle)
}

// mediaStatus is how the browser is told to play one lesson:
//
//	direct  the file already sits in a container the browser plays; /files/ is
//	        all that is needed
//	hls     the lesson is streamed in short pieces (see hls.go) - one piece is
//	        converted per request, so playback starts right away
//	remux   the whole file is being repackaged in the background; ready turns
//	        true when /files/ can serve the finished copy
//	raw     nothing can be done for it - the bytes are served as they are
type mediaStatus struct {
	Mode      string `json:"mode"`
	Ready     bool   `json:"ready"`
	Preparing bool   `json:"preparing"`
	HLSURL    string `json:"hls_url,omitempty"`
	// HLSReady says whether the segment plan is finished. For a long lesson on a
	// slow machine that takes minutes, so the browser waits for this while
	// telling the reader what is happening instead of failing to play.
	HLSReady bool    `json:"hls_ready"`
	Error    string  `json:"error,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	// VideoCodec and NeedsReencode tell the UI when a lesson cannot simply be
	// repackaged: every piece then has to be re-encoded, which is the one thing
	// a low powered NAS cannot do smoothly - and the moment to suggest casting
	// the lesson to a TV instead, because the device decodes it itself.
	VideoCodec    string `json:"video_codec,omitempty"`
	NeedsReencode bool   `json:"needs_reencode"`
}

// handleMediaStatus decides how a lesson has to be played and starts whatever
// has to be prepared for it. It only ever answers from the cached probe - the
// expensive work (reading the keyframes, repackaging the file) runs in the
// background so the request comes back immediately.
func (a *App) handleMediaStatus(w http.ResponseWriter, r *http.Request) {
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
	file := a.mediaFileFor(course, lesson)
	if file == "" {
		writeError(w, http.StatusNotFound, "this lesson has no media")
		return
	}

	status := mediaStatus{Mode: "raw", Ready: true}
	if a.Transcoder != nil {
		a.Transcoder.ensureFFmpeg()
		facts, err := a.Transcoder.Inspect(r.Context(), file)
		if err == nil {
			status.VideoCodec = facts.Info.VideoCodec
			// True when the video stream cannot be copied into the stream as it
			// is (re-encoding was forced, or the codec is not one HLS copies):
			// every piece then has to be re-encoded, which is exactly what a
			// weak machine cannot keep up with.
			status.NeedsReencode = !a.Transcoder.canCopyVideo(facts.Info, hlsSafeVideoCodecs)
		}
		switch {
		case err != nil || facts.Container == "":
			status.Mode = "raw"
		case browserPlayable(facts.Container, facts.Info) != "":
			status.Mode = "direct"
		case !a.Transcoder.Available():
			status.Mode = "raw"
		case r.URL.Query().Get("hls") != "0" && a.Transcoder.CanStreamHLS(r.Context(), file):
			status.Mode = "hls"
			status.HLSURL = "/api/hls/playlist.m3u8?lesson=" + url.QueryEscape(requested)
			status.Duration = facts.Info.Duration
			// Build the segment plan now, so the first request for the
			// playlist does not have to wait for it.
			a.Transcoder.EnsureHLSIndex(file)
			status.HLSReady = a.Transcoder.HLSIndexReady(file)
		default:
			// Audio lessons, files of unknown length and files whose video
			// would have to be re-encoded are repackaged as one MP4 instead.
			status.Mode = "remux"
			status.Duration = facts.Info.Duration
			if _, ready := a.Transcoder.BrowserCacheReady(file); ready {
				status.Ready = true
			} else {
				a.Transcoder.StartBrowserCache(file, facts.Info)
				status.Preparing = true
				if failure := a.Transcoder.BrowserCacheFailure(file); failure != nil {
					status.Preparing = false
					status.Error = failure.Error()
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, status)
}

// handleHLSPlaylist serves the media playlist of a lesson: the list of pieces
// the browser may ask for.
func (a *App) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	course := a.ActiveCourse()
	if course == nil {
		writeError(w, http.StatusNotFound, "no course loaded")
		return
	}
	requested := strings.TrimSpace(r.URL.Query().Get("lesson"))
	lesson := FindLessonInTree(course.Root, requested)
	if lesson == nil {
		writeError(w, http.StatusNotFound, "lesson not found")
		return
	}
	file := a.mediaFileFor(course, lesson)
	if file == "" {
		writeError(w, http.StatusNotFound, "this lesson has no media")
		return
	}
	if a.Transcoder == nil || !a.Transcoder.Available() {
		writeError(w, http.StatusNotImplemented, "ffmpeg is not installed, so this file cannot be converted")
		return
	}
	lessonParam := url.QueryEscape(requested)
	body, err := a.Transcoder.HLSPlaylist(r.Context(), file, func(index int) string {
		return "/api/hls/segment?lesson=" + lessonParam + "&i=" + strconv.Itoa(index)
	})
	if err != nil {
		if errors.Is(err, ErrHLSNotReady) {
			// The plan is still being built; the browser retries in a moment.
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

// handleHLSSegment serves one piece of a lesson, converting it when it is not
// cached yet.
func (a *App) handleHLSSegment(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	course := a.ActiveCourse()
	if course == nil {
		writeError(w, http.StatusNotFound, "no course loaded")
		return
	}
	requested := strings.TrimSpace(r.URL.Query().Get("lesson"))
	lesson := FindLessonInTree(course.Root, requested)
	if lesson == nil {
		writeError(w, http.StatusNotFound, "lesson not found")
		return
	}
	file := a.mediaFileFor(course, lesson)
	if file == "" {
		writeError(w, http.StatusNotFound, "this lesson has no media")
		return
	}
	position, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("i")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "the segment number is missing")
		return
	}
	if a.Transcoder == nil || !a.Transcoder.Available() {
		writeError(w, http.StatusNotImplemented, "ffmpeg is not installed, so this file cannot be converted")
		return
	}
	segment, err := a.Transcoder.HLSSegment(r.Context(), file, position)
	if err != nil {
		if errors.Is(err, ErrHLSNotReady) {
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Keep the piece: it is what makes a rewatch or a jump back free.
	a.Transcoder.trimmer().Touch(segment)
	serveCourseFile(w, r, segment, mpegTSMime)
}

// handleSubtitle serves subtitles. By default it converts SRT (and friends) to
// WebVTT on the fly because browsers cannot display them otherwise. A ?raw=1
// query returns the subtitle verbatim (still re-decoded to UTF-8): that is what
// DLNA renderers want - the TV decodes SRT/VTT itself and would be confused by a
// WebVTT transcription served under an .srt name.
func (a *App) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	full, ok := a.resolveCourseFile(w, r, "/subtitles/")
	if !ok {
		return
	}
	raw := r.URL.Query().Get("raw") == "1"
	var body string
	if raw {
		text, err := ReadSubtitleText(full)
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		body = text
	} else if strings.EqualFold(filepath.Ext(full), ".vtt") {
		rawBytes, err := os.ReadFile(full)
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		body = string(rawBytes)
	} else {
		text, err := ReadSubtitleText(full)
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		body = ConvertSRTToVTT(text)
	}
	contentType := "text/vtt; charset=utf-8"
	if raw {
		// Renderers key off the Content-Type when deciding how to decode the
		// external track, so tell the truth for each format.
		switch strings.ToLower(filepath.Ext(full)) {
		case ".vtt":
			contentType = "text/vtt; charset=utf-8"
		case ".srt", ".sbv":
			contentType = "application/x-subrip; charset=utf-8"
		default:
			contentType = "text/plain; charset=utf-8"
		}
	}
	w.Header().Set("Content-Type", contentType)
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
