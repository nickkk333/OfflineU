package offlineu

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexWithoutCourseServesThePicker(t *testing.T) {
	env := newTestEnv(t)

	page := env.request(http.MethodGet, "/", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("status = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "OfflineU") {
		t.Error("fallback page does not mention OfflineU")
	}

	state := map[string]any{}
	env.decode(env.request(http.MethodGet, "/api/state", nil), &state)
	if state["course"] != nil {
		t.Errorf("expected no active course, got %v", state["course"])
	}
}

func TestDashboardStateRendersTreeAndContinueCard(t *testing.T) {
	env := newTestEnv(t)
	course := env.loadCourse()
	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": "Section 1/01 - Intro.mp4/Intro", "completed": true, "progress_seconds": 90,
	})

	state := struct {
		Course struct {
			Name string `json:"name"`
		} `json:"course"`
		Stats Stats          `json:"stats"`
		Tree  *DirectoryNode `json:"tree"`
	}{}
	env.decode(env.request(http.MethodGet, "/api/state", nil), &state)

	if state.Course.Name != course.Name {
		t.Errorf("course name = %q", state.Course.Name)
	}
	if state.Tree == nil || state.Tree.Name != "Course Root" {
		t.Fatalf("tree missing: %+v", state.Tree)
	}
	if state.Stats.TotalLessons != 7 || state.Stats.CompletedLessons != 1 {
		t.Errorf("stats = %+v", state.Stats)
	}
	if state.Stats.CompletionPercentage != 14.3 {
		t.Errorf("percentage = %v", state.Stats.CompletionPercentage)
	}
	if state.Stats.LastAccessedURL != "Section 1/01 - Intro.mp4/Intro" {
		t.Errorf("last accessed url = %q", state.Stats.LastAccessedURL)
	}
	if state.Tree.Stats.TotalLessons != 7 {
		t.Errorf("root stats = %+v", state.Tree.Stats)
	}
	for _, child := range state.Tree.Children {
		if child.Name == "Section 1" && child.Stats.CompletedLessons != 1 {
			t.Errorf("section stats = %+v", child.Stats)
		}
	}
}

func TestLessonAPIIncludesPlayerSubtitlesAndNeighbours(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	payload := struct {
		Lesson  *Lesson `json:"lesson"`
		NextURL string  `json:"next_url"`
		PrevURL string  `json:"prev_url"`
	}{}
	env.decode(env.lessonRequest("Section 1/01 - Intro.mp4/Intro", false), &payload)

	if payload.Lesson == nil || payload.Lesson.VideoSrc() != "/files/Section%201/01%20-%20Intro.mp4" {
		t.Fatalf("video source missing: %+v", payload.Lesson)
	}
	if payload.Lesson.VideoMime != "video/mp4" {
		t.Errorf("video mime = %q", payload.Lesson.VideoMime)
	}
	if payload.Lesson.SubtitleSrc() != "/subtitles/Section%201/01%20-%20Intro.srt" {
		t.Errorf("subtitle src = %q", payload.Lesson.SubtitleSrc())
	}
	if payload.NextURL != "Section 1/02 - Notes.txt/Notes" {
		t.Errorf("next url = %q", payload.NextURL)
	}
	if payload.PrevURL != "" {
		t.Errorf("first lesson should not have a previous lesson, got %q", payload.PrevURL)
	}

	notes := struct {
		PrevURL   string         `json:"prev_url"`
		NextURL   string         `json:"next_url"`
		Resources []TextResource `json:"resources"`
	}{}
	env.decode(env.lessonRequest("Section 1/02 - Notes.txt/Notes", false), &notes)
	if notes.PrevURL != "Section 1/01 - Intro.mp4/Intro" {
		t.Errorf("notes prev url = %q", notes.PrevURL)
	}
	if notes.NextURL != "Section 1/03 - Quiz.html/Quiz" {
		t.Errorf("notes next url = %q", notes.NextURL)
	}
	if len(notes.Resources) != 1 || notes.Resources[0].Mode != "text" {
		t.Errorf("notes resources = %+v", notes.Resources)
	}
}

func TestLessonAPIExposesStoredCompletionAndResume(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": "Section 1/01 - Intro.mp4/Intro", "completed": true, "progress_seconds": 125,
	})

	payload := struct {
		Lesson *Lesson `json:"lesson"`
	}{}
	env.decode(env.lessonRequest("Section 1/01 - Intro.mp4/Intro", false), &payload)
	if payload.Lesson == nil || !payload.Lesson.Completed {
		t.Fatalf("completion was not reflected: %+v", payload.Lesson)
	}
	if payload.Lesson.ProgressSeconds != 125 {
		t.Errorf("progress seconds = %d", payload.Lesson.ProgressSeconds)
	}
}

func TestLessonAPIMarksDocumentsThatCannotBePreviewed(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	payload := struct {
		Resources []TextResource `json:"resources"`
	}{}
	env.decode(env.lessonRequest("Section 1/05 - Handout.docx/Handout", false), &payload)
	if len(payload.Resources) != 1 || payload.Resources[0].Mode != "download" {
		t.Errorf("handout resources = %+v", payload.Resources)
	}
}

func TestUnknownLessonReturns404(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	if response := env.lessonRequest("does/not/exist", false); response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.Code)
	}
}

func TestBrowseListsCourseCandidates(t *testing.T) {
	env := newTestEnv(t)
	query := url.Values{}
	query.Set("path", env.root)
	payload := struct {
		CurrentPath string        `json:"current_path"`
		ParentPath  *string       `json:"parent_path"`
		Directories []browseEntry `json:"directories"`
	}{}
	env.decode(env.request(http.MethodGet, "/browse?"+query.Encode(), nil), &payload)

	var candidate *browseEntry
	for index := range payload.Directories {
		if payload.Directories[index].Name == "Python Tutorial" {
			candidate = &payload.Directories[index]
		}
	}
	if candidate == nil {
		t.Fatalf("course folder missing from the listing: %+v", payload.Directories)
	}
	if !candidate.IsCourseCandidate || candidate.MediaFiles < 1 {
		t.Errorf("candidate = %+v", candidate)
	}
	expectedParent := filepath.Dir(env.root)
	if payload.ParentPath == nil || filepath.Clean(*payload.ParentPath) != filepath.Clean(expectedParent) {
		t.Errorf("parent path = %v, want %q", payload.ParentPath, expectedParent)
	}
}

func TestFilesAreServedWithTheRightMimeType(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	video := env.request(http.MethodGet, "/files/Section%201/01%20-%20Intro.mp4", nil)
	if video.Code != http.StatusOK {
		t.Fatalf("video status = %d", video.Code)
	}
	if !strings.HasPrefix(video.Header().Get("Content-Type"), "video/mp4") {
		t.Errorf("video content type = %q", video.Header().Get("Content-Type"))
	}

	notes := env.request(http.MethodGet, "/files/Section%201/02%20-%20Notes.txt", nil)
	if notes.Body.String() != "Hello OfflineU" {
		t.Errorf("notes body = %q", notes.Body.String())
	}
}

func TestSubtitleEndpointConvertsSRTToVTT(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	response := env.request(http.MethodGet, "/subtitles/Section%201/01%20-%20Intro.srt", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/vtt") {
		t.Errorf("content type = %q", response.Header().Get("Content-Type"))
	}
	body := response.Body.String()
	if !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, "00:00:01.000 --> 00:00:04.000") {
		t.Errorf("unexpected vtt body: %q", body)
	}
}

func TestHealthAndResetCourse(t *testing.T) {
	env := newTestEnv(t)
	health := map[string]string{}
	env.decode(env.request(http.MethodGet, "/health", nil), &health)
	if health["status"] != "healthy" {
		t.Errorf("health payload = %v", health)
	}

	env.loadCourse()
	if reset := env.request(http.MethodGet, "/reset_course", nil); reset.Code != http.StatusFound {
		t.Errorf("reset status = %d", reset.Code)
	}
	if env.app.Store.Get() != nil {
		t.Error("course was not cleared")
	}
	state := struct {
		Recent []RecentCourse `json:"recent_courses"`
	}{}
	env.decode(env.request(http.MethodGet, "/api/state", nil), &state)
	if len(state.Recent) != 1 || state.Recent[0].Name != "Python Tutorial" {
		t.Errorf("recent courses = %+v", state.Recent)
	}
}
