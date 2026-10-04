package offlineu

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpeningALessonDoesNotResetCompletion(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	lessonURL := "Section 1/01 - Intro.mp4/Intro"

	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": lessonURL, "completed": true, "progress_seconds": 120,
	})
	// opening the lesson (RecordAccess) must keep the stored values
	if response := env.lessonRequest(lessonURL, false); response.Code != http.StatusOK {
		t.Fatalf("lesson request failed: %d", response.Code)
	}
	// a timeupdate save without the completed flag and with a smaller position
	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": lessonURL, "progress_seconds": 30,
	})

	entry := map[string]any{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &entry)
	if completed, _ := entry["completed"].(bool); !completed {
		t.Errorf("completion flag was lost: %v", entry)
	}
	if seconds, _ := entry["progress_seconds"].(float64); seconds != 120 {
		t.Errorf("watched seconds moved backwards: %v", entry)
	}
}

func TestPeriodicSaveDoesNotCreateACompletedKey(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": "Section 1/01 - Intro.mp4/Intro", "progress_seconds": 15,
	})
	entry := map[string]any{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &entry)
	if _, exists := entry["completed"]; exists {
		t.Errorf("periodic save created a completed key: %v", entry)
	}
}

func TestProgressAcceptsURLsAndRelativePaths(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	for _, lessonPath := range []string{"Section 1/01 - Intro.mp4/Intro", "Section 1/01 - Intro.mp4"} {
		response := env.request(http.MethodPost, "/api/progress", map[string]any{"lesson_path": lessonPath})
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d", lessonPath, response.Code)
		}
		payload := map[string]any{}
		env.decode(response, &payload)
		if payload["lesson_key"] != "Section 1/01 - Intro.mp4" {
			t.Errorf("%s: lesson_key = %v", lessonPath, payload["lesson_key"])
		}
	}
	if _, ok := env.progressJSON()["Section 1/01 - Intro.mp4"]; !ok {
		t.Error("progress entry was not written")
	}
}

func TestSavedProgressIsAppliedToTheTree(t *testing.T) {
	env := newTestEnv(t)
	course := env.loadCourse()

	legacy := map[string]any{
		"Section 1/01 - Intro.mp4/Intro": map[string]any{"completed": true, "progress_seconds": 180},
		"last_accessed_path":             "Section 1/01 - Intro.mp4/Intro",
	}
	raw, _ := json.Marshal(legacy)
	if err := os.MkdirAll(filepath.Dir(course.ProgressFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(course.ProgressFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	env.app.Progress.ApplyToTree(course)
	lesson := env.lessonsByPath()["Section 1/01 - Intro.mp4"]
	if !lesson.Completed {
		t.Error("completion was not applied")
	}
	if lesson.ProgressSeconds != 180 {
		t.Errorf("progress seconds = %d", lesson.ProgressSeconds)
	}
	if course.LastAccessedURL != "Section 1/01 - Intro.mp4/Intro" {
		t.Errorf("last accessed url = %q", course.LastAccessedURL)
	}
	if course.LastAccessedTitle != "Intro" {
		t.Errorf("last accessed title = %q", course.LastAccessedTitle)
	}
}

func TestProgressIsStoredOutsideTheCourseFolderWhenConfigured(t *testing.T) {
	env := newTestEnv(t)
	course := env.loadCourse()
	if !strings.HasPrefix(course.ProgressFile, env.progressDir) {
		t.Fatalf("progress file %q is not inside %q", course.ProgressFile, env.progressDir)
	}
	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": "Section 1/02 - Notes.txt/Notes", "progress_seconds": 5,
	})
	if _, err := os.Stat(course.ProgressFile); err != nil {
		t.Fatalf("progress file was not created: %v", err)
	}
}

func TestUnwritableProgressLocationIsReported(t *testing.T) {
	env := newTestEnv(t)
	course := env.loadCourse()

	blocker := filepath.Join(env.root, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	course.ProgressFile = filepath.Join(blocker, "progress.json")

	response := env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": "Section 1/01 - Intro.mp4/Intro", "progress_seconds": 10,
	})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	payload := map[string]any{}
	env.decode(response, &payload)
	message, _ := payload["error"].(string)
	if !strings.Contains(message, "cannot write progress") {
		t.Errorf("unexpected error message: %q", message)
	}

	lessonResponse := env.lessonRequest("Section 1/01 - Intro.mp4/Intro", false)
	if lessonResponse.Code != http.StatusOK {
		t.Fatalf("lesson request failed: %d", lessonResponse.Code)
	}
	lessonPayload := map[string]any{}
	env.decode(lessonResponse, &lessonPayload)
	warning, _ := lessonPayload["storage_warning"].(string)
	if !strings.Contains(warning, "cannot write progress") {
		t.Errorf("lesson payload did not report the storage problem: %q", warning)
	}
}
