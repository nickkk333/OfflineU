package offlineu

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// simulateRestart throws away the in-memory state, exactly like a fresh process.
func (e *testEnv) simulateRestart() {
	e.t.Helper()
	e.app = NewApp(e.cfg)
}

func (e *testEnv) state() struct {
	Course *courseSummary `json:"course"`
	Recent []RecentCourse `json:"recent_courses"`
} {
	e.t.Helper()
	payload := struct {
		Course *courseSummary `json:"course"`
		Recent []RecentCourse `json:"recent_courses"`
	}{}
	e.decode(e.request(http.MethodGet, "/api/state", nil), &payload)
	return payload
}

func (e *testEnv) stateJSON() stateData {
	e.t.Helper()
	raw, err := os.ReadFile(e.app.Store.StateFile())
	if err != nil {
		e.t.Fatalf("cannot read state file: %v", err)
	}
	decoded := stateData{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		e.t.Fatalf("cannot decode state file: %v", err)
	}
	return decoded
}

func TestLoadingACourseIsRememberedOnDisk(t *testing.T) {
	env := newTestEnv(t)
	course := env.loadCourse()

	stateFile := env.app.Store.StateFile()
	if !filepath.IsAbs(stateFile) || !strings.HasPrefix(stateFile, env.progressDir) {
		t.Fatalf("state file %q is not inside %q", stateFile, env.progressDir)
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file was not written: %v", err)
	}

	state := env.stateJSON()
	if state.ActiveCourse != course.Path {
		t.Errorf("active course = %q", state.ActiveCourse)
	}
	if len(state.RecentCourses) != 1 || state.RecentCourses[0].Name != "Python Tutorial" {
		t.Errorf("recent courses = %+v", state.RecentCourses)
	}
}

func TestHomePageRestoresTheCourseAfterARestart(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	env.simulateRestart()
	if env.app.Store.Get() != nil {
		t.Fatal("the new store should start empty")
	}

	payload := env.state()
	if payload.Course == nil {
		t.Fatal("the course was not restored")
	}
	if env.app.Store.Get() == nil || env.app.Store.Get().Path != env.courseDir {
		t.Errorf("store was not repopulated: %+v", env.app.Store.Get())
	}
}

func TestGoingBackToTheHomePageKeepsTheCourse(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	if response := env.lessonRequest("Section 1/01 - Intro.mp4/Intro", false); response.Code != http.StatusOK {
		t.Fatalf("lesson request failed: %d", response.Code)
	}
	if payload := env.state(); payload.Course == nil || payload.Course.Name != "Python Tutorial" {
		t.Errorf("course was lost: %+v", payload.Course)
	}
}

func TestResetKeepsTheCourseInTheRecentList(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	env.request(http.MethodGet, "/reset_course", nil)

	payload := env.state()
	if payload.Course != nil {
		t.Error("reset should clear the active course")
	}
	if len(payload.Recent) != 1 || payload.Recent[0].Name != "Python Tutorial" {
		t.Fatalf("recent courses = %+v", payload.Recent)
	}
	if state := env.stateJSON(); state.ActiveCourse != "" {
		t.Errorf("active_course should be gone, got %q", state.ActiveCourse)
	}

	// one click is enough to get it back: no path typing required
	response := env.request(http.MethodPost, "/load_course", map[string]string{"course_path": env.courseDir})
	if response.Code != http.StatusOK {
		t.Fatalf("reloading failed: %d", response.Code)
	}
	if env.state().Course == nil {
		t.Error("the course was not loaded again")
	}
}

func TestCoursesWhoseFolderDisappearedAreNotOffered(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	if err := os.RemoveAll(env.courseDir); err != nil {
		t.Fatal(err)
	}

	env.simulateRestart()
	if payload := env.state(); payload.Course != nil || len(payload.Recent) != 0 {
		t.Errorf("stale course was offered: %+v", payload)
	}
}

func TestRestoreAndRecentListRespectTheAllowedRoots(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	elsewhere := filepath.Join(env.root, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvRoots, elsewhere)

	env.simulateRestart()
	payload := env.state()
	if payload.Course != nil {
		t.Error("a course outside OFFLINEU_ROOTS must not be restored")
	}
	if len(payload.Recent) != 0 {
		t.Errorf("recent courses = %+v", payload.Recent)
	}
}

func TestRecentCourseListIsCappedAndNewestFirst(t *testing.T) {
	env := newTestEnv(t)
	first := env.loadCourse()
	if len(env.app.Store.RecentCourses()) != 1 {
		t.Fatal("first course was not remembered")
	}

	second := filepath.Join(env.root, "Second Course")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	env.write("Section 1/01 - Intro.mp4", []byte("x"))
	response := env.request(http.MethodPost, "/load_course", map[string]string{"course_path": second})
	if response.Code != http.StatusOK {
		t.Fatalf("loading the second course failed: %d", response.Code)
	}

	recent := env.app.Store.RecentCourses()
	if len(recent) != 2 {
		t.Fatalf("recent courses = %+v", recent)
	}
	if recent[0].Path != second || recent[1].Path != first.Path {
		t.Errorf("recent order = %+v", recent)
	}
}
