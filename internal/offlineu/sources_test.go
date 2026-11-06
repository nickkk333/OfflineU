package offlineu

// Tests for course sources (WebDAV, SMB and local folders configured from the
// UI). A tiny in-process WebDAV server stands in for a NAS so the whole path -
// PROPFIND listing, course scan, range streaming and the JSON API (including
// the login/connect and edit endpoints) - is exercised without any network
// setup. SMB needs a real share and is not covered by these offline tests.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fake WebDAV server ---------------------------------------------------

type webDAVFixture struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

func newWebDAVFixture() *webDAVFixture {
	return &webDAVFixture{files: map[string][]byte{}, dirs: map[string]bool{"/": true}}
}

func (f *webDAVFixture) addFile(name string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cleaned := path.Clean("/" + strings.TrimPrefix(name, "/"))
	f.files[cleaned] = data
	for dir := path.Dir(cleaned); dir != "/" && dir != "."; dir = path.Dir(dir) {
		f.dirs[dir] = true
	}
}

func (f *webDAVFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleaned := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))

	f.mu.Lock()
	data, isFile := f.files[cleaned]
	isDir := f.dirs[cleaned]
	f.mu.Unlock()

	if !isFile && !isDir {
		http.NotFound(w, r)
		return
	}
	if isFile && r.Method == http.MethodGet {
		http.ServeContent(w, r, path.Base(cleaned), time.Unix(1700000000, 0), bytes.NewReader(data))
		return
	}
	if r.Method != "PROPFIND" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type resource struct {
		href string
		name string
		dir  bool
		size int
	}
	resources := []resource{{href: cleaned, name: path.Base(cleaned), dir: isDir, size: len(data)}}
	if isDir && r.Header.Get("Depth") != "0" {
		f.mu.Lock()
		for name := range f.dirs {
			if path.Dir(name) == cleaned {
				resources = append(resources, resource{href: name, name: path.Base(name), dir: true})
			}
		}
		for name, content := range f.files {
			if path.Dir(name) == cleaned {
				resources = append(resources, resource{href: name, name: path.Base(name), size: len(content)})
			}
		}
		f.mu.Unlock()
	}

	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)
	for _, resource := range resources {
		body.WriteString(`<D:response><D:href>`)
		body.WriteString((&url.URL{Path: resource.href}).EscapedPath())
		body.WriteString(`</D:href><D:propstat><D:prop><D:resourcetype>`)
		if resource.dir {
			body.WriteString(`<D:collection/>`)
		}
		body.WriteString(`</D:resourcetype><D:getcontentlength>`)
		body.WriteString(strconv.Itoa(resource.size))
		body.WriteString(`</D:getcontentlength><D:getlastmodified>Wed, 15 Nov 2023 12:00:00 GMT</D:getlastmodified>`)
		body.WriteString(`<D:displayname>`)
		body.WriteString(resource.name)
		body.WriteString(`</D:displayname></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`)
	}
	body.WriteString(`</D:multistatus>`)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write(body.Bytes())
}

// newWebDAVFixtureCourse fills the fixture with the course every test uses.
func newWebDAVFixtureCourse() *webDAVFixture {
	fixture := newWebDAVFixture()
	fixture.addFile("/dav/Course/Section 1/01 - Intro.mp4", []byte("0123456789abcdefghijklmnopqrstuvwxyz"))
	fixture.addFile("/dav/Course/Section 1/01 - Intro.srt", []byte(srtSample))
	fixture.addFile("/dav/Course/Section 1/02 - Notes.txt", []byte("Hello from WebDAV"))
	fixture.addFile("/dav/Course/Section 2/03 - Wrap Up.mp4", []byte("wrap up video bytes"))
	return fixture
}

// --- registry -------------------------------------------------------------

func TestSourceRegistryRoundTrip(t *testing.T) {
	root := t.TempDir()
	courseDir := filepath.Join(root, "Course")
	if err := os.MkdirAll(courseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := NewSourceRegistry(filepath.Join(root, "state", "sources.json"))

	info, err := registry.Add(Source{
		Name:     "My Course",
		Kind:     SourceKindLocal,
		Path:     courseDir,
		Password: "secret", // irrelevant for a local source, but must persist
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.ID == "" || info.Root != SourceRoot(info.ID) {
		t.Fatalf("unexpected source info: %+v", info)
	}
	if !info.HasPassword {
		t.Fatalf("has_password should be true, got %+v", info)
	}
	if _, err := registry.Add(Source{Kind: SourceKindLocal, Path: courseDir, ID: info.ID}); err == nil {
		t.Fatal("duplicate source ids must be rejected")
	}
	if _, err := registry.Add(Source{Kind: SourceKindLocal, Path: filepath.Join(root, "missing")}); err == nil {
		t.Fatal("missing local folders must be rejected")
	}

	raw, err := os.ReadFile(registry.File())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("secret")) {
		t.Fatal("the password should be persisted in sources.json")
	}
	if !bytes.Contains(raw, []byte(`"version": 1`)) {
		t.Fatalf("unexpected source file: %s", raw)
	}
	if infos := registry.List(); len(infos) != 1 || !infos[0].HasPassword {
		t.Fatalf("unexpected list: %+v", infos)
	}

	reloaded := NewSourceRegistry(registry.File())
	stored, ok := reloaded.Find(info.ID)
	if !ok || stored.Password != "secret" || stored.Name != "My Course" {
		t.Fatalf("reloaded source = %+v, ok = %v", stored, ok)
	}
	if infos := reloaded.List(); len(infos) != 1 {
		t.Fatalf("reloaded list = %+v", infos)
	}

	if err := reloaded.Remove(info.ID); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 0 {
		t.Fatal("removed source is still listed")
	}
}

func TestSourceVirtualPaths(t *testing.T) {
	registry := NewSourceRegistry(filepath.Join(t.TempDir(), "sources.json"))
	info, err := registry.Add(Source{Kind: SourceKindLocal, Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	root := SourceRoot(info.ID)

	if !IsSourcePath(root) {
		t.Fatalf("%q should be a source path", root)
	}
	if !IsSourcePath(filepath.Join(root, "Section 1", "01.mp4")) {
		t.Fatalf("nested source path was not recognised")
	}
	if IsSourcePath(t.TempDir()) {
		t.Fatal("a normal folder must not be a source path")
	}

	source, relative, ok := registry.Resolve(filepath.Join(root, "Section 1", "01.mp4"))
	if !ok || source.ID != info.ID || relative != "Section 1/01.mp4" {
		t.Fatalf("resolve = %+v %q %v", source, relative, ok)
	}
	if _, _, ok := registry.Resolve(SourceRoot("unknown")); ok {
		t.Fatal("unknown source ids must not resolve")
	}
	if _, _, ok := registry.Resolve(filepath.Join(t.TempDir(), "Section 1", "01.mp4")); ok {
		t.Fatal("a normal folder must not resolve as a source")
	}
}

// --- WebDAV course --------------------------------------------------------

func newWebDAVSource(t *testing.T) (*webDAVFixture, *httptest.Server, *Config, SourceInfo) {
	t.Helper()
	fixture := newWebDAVFixtureCourse()
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)

	cfg := Config{
		StateDir:    filepath.Join(t.TempDir(), "state"),
		ProgressDir: filepath.Join(t.TempDir(), "progress"),
	}
	cfg.ensureSources()

	info, err := cfg.Sources.Add(Source{
		Name:     "NAS Course",
		Kind:     SourceKindWebDAV,
		URL:      server.URL + "/dav/Course",
		Username: "student",
		Password: "hunter2",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, server, &cfg, info
}

func TestWebDAVSourceScanAndFiles(t *testing.T) {
	_, _, cfg, info := newWebDAVSource(t)
	root := info.Root

	stat, err := cfg.StatPath(root)
	if err != nil || !stat.IsDir() {
		t.Fatalf("stat source root = %v, %v", stat, err)
	}
	entries, err := cfg.ReadDirPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "Section 1" || entries[1].Name() != "Section 2" {
		t.Fatalf("unexpected source listing: %+v", dirEntryNames(entries))
	}

	course, err := cfg.ScanCourse(root)
	if err != nil {
		t.Fatal(err)
	}
	if course.Name != "NAS Course" {
		t.Fatalf("course name = %q", course.Name)
	}
	lessons := AllLessons(course.Root)
	if len(lessons) != 3 {
		t.Fatalf("lessons = %+v", lessonPaths(lessons))
	}
	byPath := map[string]*Lesson{}
	for _, lesson := range lessons {
		byPath[lesson.RelPath] = lesson
	}
	intro := byPath["Section 1/01 - Intro.mp4"]
	if intro == nil {
		t.Fatalf("intro lesson missing: %+v", lessonPaths(lessons))
	}
	if intro.SubtitleFile != "Section 1/01 - Intro.srt" {
		t.Fatalf("subtitle = %q", intro.SubtitleFile)
	}
	if intro.VideoMime != "video/mp4" {
		t.Fatalf("mime = %q", intro.VideoMime)
	}
	if notes := byPath["Section 1/02 - Notes.txt"]; notes == nil || notes.LessonType != "text" {
		t.Fatalf("notes lesson = %+v", notes)
	}

	if !strings.HasPrefix(course.ProgressFile, cfg.ProgressDir) {
		t.Fatalf("progress file %q should live in %q", course.ProgressFile, cfg.ProgressDir)
	}
	if !strings.HasPrefix(course.ExcludedFile, cfg.ProgressDir) {
		t.Fatalf("exclusion file %q should live in %q", course.ExcludedFile, cfg.ProgressDir)
	}
	if _, err := os.Stat(course.ProgressFile); err == nil {
		t.Fatal("scanning must not create the progress file yet")
	}

	data, err := cfg.ReadFilePath(filepath.Join(root, "Section 1", "02 - Notes.txt"), 0)
	if err != nil || string(data) != "Hello from WebDAV" {
		t.Fatalf("read file = %q, %v", data, err)
	}
	subtitle, err := cfg.ReadSubtitleText(filepath.Join(root, "Section 1", "01 - Intro.srt"))
	if err != nil || !strings.Contains(subtitle, "Hello OfflineU") {
		t.Fatalf("subtitle = %q, %v", subtitle, err)
	}

	if _, ok := cfg.ResolveCoursePath(root, "../other"); ok {
		t.Fatal("course-relative traversal must be rejected")
	}
	if _, ok := cfg.ResolveCoursePath(root, "Section 1/01 - Intro.mp4"); !ok {
		t.Fatal("a valid course-relative path was rejected")
	}
	if display, ok := cfg.SourceDisplayPath(root); !ok || display != "NAS Course" {
		t.Fatalf("display = %q, %v", display, ok)
	}
	if display := cfg.DisplayPath(filepath.Join(root, "Section 1", "01 - Intro.mp4")); display != "NAS Course/Section 1/01 - Intro.mp4" {
		t.Fatalf("nested display = %q", display)
	}
	if parent, ok := cfg.ParentPath(root); ok {
		t.Fatalf("source root should not have a parent, got %q", parent)
	}
}

func TestWebDAVSourceSeek(t *testing.T) {
	_, _, cfg, info := newWebDAVSource(t)
	file, err := cfg.OpenPath(filepath.Join(info.Root, "Section 1", "01 - Intro.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	position, err := file.Seek(10, io.SeekStart)
	if err != nil || position != 10 {
		t.Fatalf("seek = %d, %v", position, err)
	}
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(file, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != "abcde" {
		t.Fatalf("read after seek = %q", buffer)
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	buffer = make([]byte, 4)
	if _, err := io.ReadFull(file, buffer); err != io.EOF {
		t.Fatalf("reading at EOF = %v", err)
	}
}

// dirEntryNames is a tiny helper for readable test failures.
func dirEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		suffix := ""
		if entry.IsDir() {
			suffix = "/"
		}
		names = append(names, entry.Name()+suffix)
	}
	return names
}

func lessonPaths(lessons []*Lesson) []string {
	paths := make([]string, 0, len(lessons))
	for _, lesson := range lessons {
		paths = append(paths, lesson.RelPath)
	}
	return paths
}

// --- HTTP API -------------------------------------------------------------

func newWebDAVApp(t *testing.T) (*webDAVFixture, *httptest.Server, *testEnv) {
	t.Helper()
	fixture := newWebDAVFixtureCourse()
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)

	env := &testEnv{t: t}
	env.root = t.TempDir()
	env.progressDir = filepath.Join(env.root, "progress")
	t.Setenv(EnvProgressDir, env.progressDir)
	t.Setenv(EnvRoots, "")
	env.cfg = ConfigFromEnv()
	env.app = NewApp(env.cfg)
	return fixture, server, env
}

func TestSourceHTTPAPI(t *testing.T) {
	_, server, env := newWebDAVApp(t)

	response := env.request(http.MethodPost, "/api/sources", map[string]any{
		"name":     "NAS Course",
		"kind":     "webdav",
		"url":      server.URL + "/dav/Course",
		"username": "student",
		"password": "hunter2",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("add source: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "hunter2") {
		t.Fatal("the password leaked into the API response")
	}
	var created struct {
		Source SourceInfo `json:"source"`
	}
	env.decode(response, &created)
	if created.Source.ID == "" || !created.Source.HasPassword {
		t.Fatalf("created source = %+v", created.Source)
	}

	response = env.request(http.MethodGet, "/api/sources", nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "hunter2") {
		t.Fatalf("get sources: %d %s", response.Code, response.Body.String())
	}

	// Testing a stored source with only its id must reuse the saved URL and
	// credentials.
	response = env.request(http.MethodPost, "/api/sources/test", map[string]any{"id": created.Source.ID})
	if response.Code != http.StatusOK {
		t.Fatalf("test source: %d %s", response.Code, response.Body.String())
	}

	response = env.request(http.MethodGet, "/browse?path="+url.QueryEscape(created.Source.Root), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("browse source: %d %s", response.Code, response.Body.String())
	}
	var browse browsePayload
	env.decode(response, &browse)
	if len(browse.Directories) != 2 || browse.Directories[0].Name != "Section 1" {
		t.Fatalf("browse directories = %+v", browse.Directories)
	}
	if len(browse.Sources) != 1 {
		t.Fatalf("browse sources = %+v", browse.Sources)
	}

	response = env.request(http.MethodPost, "/api/load_course", map[string]string{"course_path": created.Source.Root})
	if response.Code != http.StatusOK {
		t.Fatalf("load course: %d %s", response.Code, response.Body.String())
	}
	course := env.app.Store.Get()
	if course == nil || course.Name != "NAS Course" {
		t.Fatalf("course = %+v", course)
	}

	response = env.request(http.MethodGet, "/api/state", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("state: %d %s", response.Code, response.Body.String())
	}
	var state struct {
		Course  *courseSummary `json:"course"`
		Sources []SourceInfo   `json:"sources"`
		Roots   []string       `json:"roots"`
	}
	env.decode(response, &state)
	if state.Course == nil || state.Course.DisplayPath != "NAS Course" {
		t.Fatalf("state course = %+v", state.Course)
	}
	if len(state.Sources) != 1 || state.Sources[0].ID != created.Source.ID {
		t.Fatalf("state sources = %+v", state.Sources)
	}
	if strings.Contains(response.Body.String(), "hunter2") {
		t.Fatal("the password leaked through /api/state")
	}

	response = env.request(http.MethodGet, "/api/media/status?path="+url.QueryEscape("Section 1/01 - Intro.mp4"), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("media status: %d %s", response.Code, response.Body.String())
	}
	var status mediaStatus
	env.decode(response, &status)
	if status.Mode != "raw" || !status.Ready {
		t.Fatalf("remote media status = %+v", status)
	}

	response = env.request(http.MethodGet, "/api/hls/playlist?lesson="+url.QueryEscape("Section 1/01 - Intro.mp4"), nil)
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("remote HLS playlist = %d %s", response.Code, response.Body.String())
	}
	response = env.request(http.MethodGet, "/api/dlna/stream?lesson="+url.QueryEscape("Section 1/01 - Intro.mp4"), nil)
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("remote DLNA stream = %d %s", response.Code, response.Body.String())
	}

	// Range streaming of the original bytes.
	fileURL := "/files/" + strings.ReplaceAll(url.PathEscape("Section 1/01 - Intro.mp4"), "%2F", "/")
	request := httptest.NewRequest(http.MethodGet, fileURL, nil)
	request.Header.Set("Range", "bytes=10-14")
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("range request = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "abcde" {
		t.Fatalf("range body = %q", recorder.Body.String())
	}

	// Progress is written to the bookkeeping folder, never to the source.
	response = env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path":      "Section 1/01 - Intro.mp4",
		"progress_seconds": 12,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("progress: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(course.ProgressFile); err != nil {
		t.Fatalf("progress file was not written: %v", err)
	}
	if !strings.HasPrefix(course.ProgressFile, env.progressDir) {
		t.Fatalf("progress file %q is outside the progress folder", course.ProgressFile)
	}

	response = env.request(http.MethodPost, "/api/sources/remove", map[string]string{"id": created.Source.ID})
	if response.Code != http.StatusOK {
		t.Fatalf("remove source: %d %s", response.Code, response.Body.String())
	}
	response = env.request(http.MethodGet, "/api/sources", nil)
	var list struct {
		Sources []SourceInfo `json:"sources"`
	}
	env.decode(response, &list)
	if len(list.Sources) != 0 {
		t.Fatalf("sources after removal = %+v", list.Sources)
	}
}

// The login flow (Android app + /login page) posts the source details to
// /api/sources/connect. Logging in twice must reuse the same virtual root,
// editing must keep the saved password, and `load` must open the course.
func TestSourceConnectAndUpdateAPI(t *testing.T) {
	_, server, env := newWebDAVApp(t)
	target := server.URL + "/dav/Course"

	var connect struct {
		Source     SourceInfo `json:"source"`
		Action     string     `json:"action"`
		CourseName string     `json:"course_name"`
	}
	response := env.request(http.MethodPost, "/api/sources/connect", map[string]any{
		"name": "NAS Course", "kind": "webdav", "url": target,
		"username": "student", "password": "hunter2",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "hunter2") {
		t.Fatal("the login response leaked the password")
	}
	env.decode(response, &connect)
	if connect.Action != "created" || connect.Source.ID == "" || !connect.Source.HasPassword {
		t.Fatalf("connect = %+v", connect)
	}
	id := connect.Source.ID

	// A phone that only knows the URL reconnects to the very same source.
	response = env.request(http.MethodPost, "/api/sources/connect", map[string]any{
		"kind": "webdav", "url": target,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("reconnect: %d %s", response.Code, response.Body.String())
	}
	env.decode(response, &connect)
	if connect.Action != "reused" || connect.Source.ID != id || connect.Source.Name != "NAS Course" {
		t.Fatalf("reconnect = %+v", connect)
	}

	// Editing the name in place keeps both the id and the stored password.
	response = env.request(http.MethodPut, "/api/sources", map[string]any{
		"id": id, "name": "Renamed", "kind": "webdav", "url": target,
		"username": "student",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("update: %d %s", response.Code, response.Body.String())
	}
	var updated struct {
		Source SourceInfo `json:"source"`
	}
	env.decode(response, &updated)
	if updated.Source.ID != id || updated.Source.Name != "Renamed" || !updated.Source.HasPassword {
		t.Fatalf("updated source = %+v", updated.Source)
	}
	response = env.request(http.MethodPost, "/api/sources/test", map[string]any{"id": id})
	if response.Code != http.StatusOK {
		t.Fatalf("test after update: %d %s", response.Code, response.Body.String())
	}

	// Login can open the source root as the active course in one step.
	response = env.request(http.MethodPost, "/api/sources/connect", map[string]any{
		"kind": "webdav", "url": target, "load": true,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("connect and load: %d %s", response.Code, response.Body.String())
	}
	env.decode(response, &connect)
	if connect.CourseName != "Renamed" || connect.Source.Root == "" {
		t.Fatalf("connect and load = %+v", connect)
	}
	if course := env.app.Store.Get(); course == nil || course.Name != "Renamed" {
		t.Fatalf("active course = %+v", course)
	}

	// Invalid configuration is a 400, not a 502 "server unreachable".
	response = env.request(http.MethodPost, "/api/sources/connect", map[string]any{
		"kind": "smb", "host": "", "share": "",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid connect = %d %s", response.Code, response.Body.String())
	}

	response = env.request(http.MethodGet, "/api/sources", nil)
	var list struct {
		Sources []SourceInfo `json:"sources"`
	}
	env.decode(response, &list)
	if len(list.Sources) != 1 {
		t.Fatalf("logging in duplicated the source: %+v", list.Sources)
	}
}

func TestLocalSourceThroughAPI(t *testing.T) {
	env := newTestEnv(t)
	info, err := env.app.Config.Sources.Add(Source{Name: "Extra", Kind: "local", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if info.Root == "" {
		t.Fatal("local source has no virtual root")
	}
	response := env.request(http.MethodGet, "/browse?path="+url.QueryEscape(info.Root), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("browse local source: %d %s", response.Code, response.Body.String())
	}
	var browse browsePayload
	env.decode(response, &browse)
	if len(browse.Sources) != 1 {
		t.Fatalf("sources = %+v", browse.Sources)
	}
}

// A malformed state file must not keep OfflineU from starting.
func TestSourceRegistryToleratesBrokenFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sources.json")
	if err := os.WriteFile(file, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewSourceRegistry(file)
	if len(registry.List()) != 0 {
		t.Fatal("broken source file should be treated as empty")
	}
	raw, _ := json.Marshal(registry.List())
	if string(raw) != "[]" {
		t.Fatalf("empty list should marshal to [], got %s", raw)
	}
}
