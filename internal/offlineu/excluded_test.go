package offlineu

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestExcludingALessonHidesItFromTheTree(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	lesson := "Section 1/02 - Notes.txt"
	response := env.request(http.MethodPost, "/api/exclude", map[string]any{"path": lesson})
	if response.Code != http.StatusOK {
		t.Fatalf("exclude failed: %d %s", response.Code, response.Body.String())
	}
	payload := struct {
		Excluded []string `json:"excluded"`
	}{}
	env.decode(response, &payload)
	if len(payload.Excluded) != 1 || payload.Excluded[0] != lesson {
		t.Fatalf("excluded = %v", payload.Excluded)
	}

	if _, exists := env.lessonsByPath()[lesson]; exists {
		t.Errorf("excluded lesson still in the tree")
	}
	if stored := (&ExclusionStore{}).Load(env.app.Store.Get()); len(stored) != 1 || stored[0] != lesson {
		t.Errorf("exclusion was not persisted: %v", stored)
	}

	// Reloading the course keeps the exclusion: it is pruned on every scan.
	env.loadCourse()
	if _, exists := env.lessonsByPath()[lesson]; exists {
		t.Errorf("exclusion did not survive a reload")
	}

	// Restoring brings the lesson back.
	restore := env.request(http.MethodPost, "/api/restore_excluded", map[string]any{"path": lesson})
	if restore.Code != http.StatusOK {
		t.Fatalf("restore failed: %d %s", restore.Code, restore.Body.String())
	}
	if _, exists := env.lessonsByPath()[lesson]; !exists {
		t.Errorf("restored lesson did not come back")
	}
}

func TestExcludingAFolderHidesItsWholeSubtree(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 9/nested.md", []byte("# nested"))
	env.loadCourse()

	response := env.request(http.MethodPost, "/api/exclude", map[string]any{"path": "Section 9"})
	if response.Code != http.StatusOK {
		t.Fatalf("exclude failed: %d %s", response.Code, response.Body.String())
	}
	if _, exists := env.lessonsByPath()["Section 9/nested.md"]; exists {
		t.Errorf("lesson inside the excluded folder still in the tree")
	}

	state := map[string]any{}
	env.decode(env.request(http.MethodGet, "/api/state", nil), &state)
	excluded, _ := state["excluded"].([]any)
	if len(excluded) != 1 || excluded[0] != "Section 9" {
		t.Errorf("/api/state excluded = %v", state["excluded"])
	}
}

func TestExcludingAnUnknownPathIsRejected(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	response := env.request(http.MethodPost, "/api/exclude", map[string]any{"path": "Section 1/missing.mp4"})
	if response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.Code)
	}
}

func TestExcludedLessonsDoNotCountTowardCompletion(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path": "Section 1/02 - Notes.txt/Notes", "completed": true,
	})
	course := env.app.Store.Get()
	if got := CountCompleted(course.ProgressFile, nil); got != 1 {
		t.Fatalf("completed = %d, want 1", got)
	}
	if got := CountCompleted(course.ProgressFile, []string{"Section 1/02 - Notes.txt"}); got != 0 {
		t.Errorf("excluded completion still counted: %d", got)
	}
}

func TestExclusionFileRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	course := env.app.Store.Get()
	store := &ExclusionStore{}
	if err := store.Save(course, []string{"b", "a", "a", ""}); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(course); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("loaded = %v", got)
	}
	raw, err := os.ReadFile(course.ExcludedFile)
	if err != nil {
		t.Fatal(err)
	}
	parsed := map[string]any{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["excluded"]; !ok {
		t.Errorf("unexpected file format: %s", raw)
	}
}
