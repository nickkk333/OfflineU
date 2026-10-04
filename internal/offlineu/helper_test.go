package offlineu

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const srtSample = "1\n00:00:01,000 --> 00:00:04,000\nHello OfflineU\n\n" +
	"2\n00:00:05,500 --> 00:00:07,000\nSecond cue\n"

// testEnv builds a temporary course, an isolated progress folder and the app.
type testEnv struct {
	t           *testing.T
	root        string
	courseDir   string
	progressDir string
	cfg         Config
	app         *App
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{t: t}
	env.root = t.TempDir()
	env.courseDir = filepath.Join(env.root, "Python Tutorial")
	env.progressDir = filepath.Join(env.root, "progress")

	env.write("Section 1/01 - Intro.mp4", bytes.Repeat([]byte{0}, 64))
	env.write("Section 1/01 - Intro.srt", []byte(srtSample))
	env.write("Section 1/02 - Notes.txt", []byte("Hello OfflineU"))
	env.write("Section 1/03 - Quiz.html", []byte("<html><body><h1>Q1</h1></body></html>"))
	env.write("Section 1/04 - Slides.pdf", []byte("%PDF-1.4 fake"))
	env.write("Section 1/05 - Handout.docx", []byte("PK\x03\x04 fake"))
	// a second video, placed *after* the documents, to test autoplay skipping them
	env.write("Section 1/06 - Wrap Up.mp4", bytes.Repeat([]byte{0}, 64))
	env.write("Section 2/resources/extras.md", []byte("# Extras"))
	env.write("Section 2/.hidden.md", []byte("should not show up"))
	env.write("ignore.zip", []byte("zip"))
	if err := os.WriteFile(filepath.Join(env.root, "secret.txt"), []byte("outside the course"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvProgressDir, env.progressDir)
	t.Setenv(EnvRoots, "")
	env.cfg = ConfigFromEnv()
	env.app = NewApp(env.cfg)
	return env
}

func (e *testEnv) write(relative string, content []byte) string {
	e.t.Helper()
	path := filepath.Join(e.courseDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func (e *testEnv) request(method, target string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	e.app.ServeHTTP(recorder, request)
	return recorder
}

func (e *testEnv) lessonRequest(path string, autoplay bool) *httptest.ResponseRecorder {
	e.t.Helper()
	query := url.Values{}
	query.Set("path", path)
	if autoplay {
		query.Set("autoplay", "1")
	}
	return e.request(http.MethodGet, "/api/lesson?"+query.Encode(), nil)
}

func (e *testEnv) loadCourse() *Course {
	e.t.Helper()
	response := e.request(http.MethodPost, "/load_course", map[string]string{"course_path": e.courseDir})
	if response.Code != http.StatusOK {
		e.t.Fatalf("load course failed: %d %s", response.Code, response.Body.String())
	}
	return e.app.Store.Get()
}

func (e *testEnv) decode(response *httptest.ResponseRecorder, target any) {
	e.t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		e.t.Fatalf("cannot decode response %q: %v", response.Body.String(), err)
	}
}

// decodeRaw unmarshals a raw JSON fragment (used to inspect single entries).
func (e *testEnv) decodeRaw(raw json.RawMessage, target any) {
	e.t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		e.t.Fatalf("cannot decode fragment %q: %v", string(raw), err)
	}
}

func (e *testEnv) lessons() []*Lesson {
	e.t.Helper()
	return AllLessons(e.app.Store.Get().Root)
}

func (e *testEnv) lessonsByPath() map[string]*Lesson {
	e.t.Helper()
	result := map[string]*Lesson{}
	for _, lesson := range e.lessons() {
		result[lesson.RelPath] = lesson
	}
	return result
}

func (e *testEnv) progressJSON() map[string]json.RawMessage {
	e.t.Helper()
	course := e.app.Store.Get()
	raw, err := os.ReadFile(course.ProgressFile)
	if err != nil {
		e.t.Fatalf("cannot read progress file: %v", err)
	}
	decoded := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		e.t.Fatalf("cannot decode progress file: %v", err)
	}
	return decoded
}

func TestBuildsTreeAndDetectsLessonTypes(t *testing.T) {
	env := newTestEnv(t)
	course := env.loadCourse()

	names := []string{}
	for _, child := range course.Root.Children {
		names = append(names, child.Name)
	}
	if strings.Join(names, ",") != "Section 1,Section 2" {
		t.Fatalf("unexpected sections: %v", names)
	}

	types := map[string]string{}
	for _, lesson := range course.Root.Children[0].Lessons {
		types[lesson.Title] = lesson.LessonType
	}
	expected := map[string]string{
		"Intro": "video", "Notes": "text", "Quiz": "quiz", "Slides": "text", "Handout": "text",
	}
	for title, lessonType := range expected {
		if types[title] != lessonType {
			t.Errorf("lesson %q: got type %q, want %q", title, types[title], lessonType)
		}
	}
	if course.Root.Name != "Course Root" {
		t.Errorf("root node name = %q", course.Root.Name)
	}
}

func TestURLsAndRelativePathsAreStable(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	lessons := env.lessonsByPath()

	intro, ok := lessons["Section 1/01 - Intro.mp4"]
	if !ok {
		t.Fatal("intro lesson missing")
	}
	if intro.URL != "Section 1/01 - Intro.mp4/Intro" {
		t.Errorf("url = %q", intro.URL)
	}
	if intro.Title != "Intro" {
		t.Errorf("title = %q", intro.Title)
	}
	extras := lessons["Section 2/resources/extras.md"]
	if extras == nil || extras.URL != "Section 2/resources/extras.md/Extras" {
		t.Errorf("extras url mismatch: %+v", extras)
	}
}

func TestHiddenAndUnsupportedFilesAreIgnored(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	for _, lesson := range env.lessons() {
		if strings.Contains(lesson.RelPath, ".hidden") {
			t.Errorf("hidden file became a lesson: %s", lesson.RelPath)
		}
		if strings.HasSuffix(lesson.RelPath, ".zip") {
			t.Errorf("unsupported file became a lesson: %s", lesson.RelPath)
		}
		if strings.HasSuffix(strings.ToLower(lesson.RelPath), ".srt") {
			t.Errorf("subtitle became a lesson: %s", lesson.RelPath)
		}
	}
}

func TestSubtitleIsAttachedToTheVideo(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	intro := env.lessonsByPath()["Section 1/01 - Intro.mp4"]
	if intro.SubtitleFile != "Section 1/01 - Intro.srt" {
		t.Errorf("subtitle file = %q", intro.SubtitleFile)
	}
	if intro.SubtitleSrc() != "/subtitles/Section%201/01%20-%20Intro.srt" {
		t.Errorf("subtitle src = %q", intro.SubtitleSrc())
	}
}

func TestMimeTypesAndEncodedSources(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	intro := env.lessonsByPath()["Section 1/01 - Intro.mp4"]
	if intro.VideoMime != "video/mp4" {
		t.Errorf("video mime = %q", intro.VideoMime)
	}
	if intro.VideoSrc() != "/files/Section%201/01%20-%20Intro.mp4" {
		t.Errorf("video src = %q", intro.VideoSrc())
	}
}

func TestDocumentModes(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	lessons := env.lessonsByPath()

	cases := []struct {
		path string
		want string
	}{
		{"Section 1/04 - Slides.pdf", "iframe"},
		{"Section 1/05 - Handout.docx", "download"},
		{"Section 1/02 - Notes.txt", "text"},
	}
	for _, testCase := range cases {
		resources := BuildTextResources(lessons[testCase.path])
		if len(resources) != 1 {
			t.Fatalf("%s: expected one resource, got %d", testCase.path, len(resources))
		}
		if resources[0].Mode != testCase.want {
			t.Errorf("%s: mode = %q, want %q", testCase.path, resources[0].Mode, testCase.want)
		}
	}
}

func TestSRTIsConvertedToWebVTT(t *testing.T) {
	vtt := ConvertSRTToVTT(srtSample)
	if !strings.HasPrefix(vtt, "WEBVTT") {
		t.Fatalf("missing WEBVTT header: %q", vtt)
	}
	if !strings.Contains(vtt, "00:00:01.000 --> 00:00:04.000") {
		t.Errorf("timestamps were not converted: %q", vtt)
	}
	if !strings.Contains(vtt, "Hello OfflineU") {
		t.Errorf("cue text lost: %q", vtt)
	}
	if strings.Contains(vtt, "\n1\n") {
		t.Errorf("index lines were not removed: %q", vtt)
	}
}

func TestResolveInsideBlocksDirectoryEscapes(t *testing.T) {
	env := newTestEnv(t)
	if _, ok := ResolveInside(env.courseDir, "../secret.txt"); ok {
		t.Error("parent traversal was allowed")
	}
	if _, ok := ResolveInside(env.courseDir, `..\secret.txt`); ok {
		t.Error("windows style traversal was allowed")
	}
	inside, ok := ResolveInside(env.courseDir, "Section 1/02 - Notes.txt")
	if !ok {
		t.Fatal("legitimate path was rejected")
	}
	if inside != filepath.Join(env.courseDir, "Section 1", "02 - Notes.txt") {
		t.Errorf("resolved to %q", inside)
	}
}
