package offlineu

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileAndSubtitleTraversalIsBlocked(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	for _, target := range []string{
		"/files/../secret.txt",
		"/files/%2e%2e%2fsecret.txt",
		"/subtitles/../secret.txt",
	} {
		if response := env.request(http.MethodGet, target, nil); response.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", target, response.Code)
		}
	}
}

func TestRootsConfineBrowsingAndLoading(t *testing.T) {
	env := newTestEnv(t)
	t.Setenv(EnvRoots, env.courseDir)

	query := url.Values{}
	query.Set("path", env.root)
	if response := env.request(http.MethodGet, "/browse?"+query.Encode(), nil); response.Code != http.StatusForbidden {
		t.Errorf("browse outside roots: status = %d, want 403", response.Code)
	}
	if response := env.request(http.MethodPost, "/load_course",
		map[string]string{"course_path": env.root}); response.Code != http.StatusForbidden {
		t.Errorf("load outside roots: status = %d, want 403", response.Code)
	}

	payload := struct {
		CurrentPath string  `json:"current_path"`
		ParentPath  *string `json:"parent_path"`
	}{}
	env.decode(env.request(http.MethodGet, "/browse", nil), &payload)
	if filepath.Clean(payload.CurrentPath) != filepath.Clean(env.courseDir) {
		t.Errorf("default browse path = %q", payload.CurrentPath)
	}
	if payload.ParentPath != nil {
		t.Errorf("the picker must not climb above the root: %v", *payload.ParentPath)
	}

	if response := env.request(http.MethodPost, "/load_course",
		map[string]string{"course_path": env.courseDir}); response.Code != http.StatusOK {
		t.Errorf("loading inside the root failed: %d", response.Code)
	}
}

func TestSiblingPrefixesAreNotInsideTheRoot(t *testing.T) {
	env := newTestEnv(t)
	sibling := env.courseDir + "-ab"
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if IsWithin(sibling, env.courseDir) {
		t.Error("a sibling folder with a shared prefix must not be considered inside the root")
	}
}

func TestLoadingAnInvalidPathReturns400(t *testing.T) {
	env := newTestEnv(t)
	if response := env.request(http.MethodPost, "/load_course", map[string]string{}); response.Code != http.StatusBadRequest {
		t.Errorf("empty path: status = %d, want 400", response.Code)
	}
	missing := filepath.Join(env.root, "nope")
	if response := env.request(http.MethodPost, "/load_course",
		map[string]string{"course_path": missing}); response.Code != http.StatusBadRequest {
		t.Errorf("missing path: status = %d, want 400", response.Code)
	}
}

func TestProgressWithoutACourseReturns400(t *testing.T) {
	env := newTestEnv(t)
	if response := env.request(http.MethodPost, "/api/progress",
		map[string]any{"lesson_path": "anything"}); response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.Code)
	}
	if response := env.request(http.MethodPost, "/api/progress", map[string]any{}); response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.Code)
	}
}

func TestFilesWithoutACourseReturns404(t *testing.T) {
	env := newTestEnv(t)
	if response := env.request(http.MethodGet, "/files/whatever.txt", nil); response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.Code)
	}
}

func TestLegacyRedirectsStillWork(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	forgetQuery := url.Values{}
	forgetQuery.Set("path", env.courseDir)
	forget := env.request(http.MethodGet, "/forget_course?"+forgetQuery.Encode(), nil)
	if forget.Code != http.StatusFound {
		t.Errorf("forget status = %d", forget.Code)
	}
	if len(env.app.Store.RecentCourses()) != 0 {
		t.Error("course was not forgotten")
	}
	// the course that is currently open is not affected
	if env.app.Store.Get() == nil {
		t.Error("the active course should survive forget_course")
	}
}

func TestForgettingAPathOutsideRootsKeepsItsProgressFile(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	// a plausible progress file belonging to a course outside OFFLINEU_ROOTS
	elsewhere := filepath.Join(env.root, "elsewhere", "Secret Course")
	progressFile := env.app.Config.ProgressFileFor(elsewhere)
	if err := os.MkdirAll(filepath.Dir(progressFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(progressFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvRoots, env.courseDir)
	response := env.request(http.MethodPost, "/api/forget_course", map[string]string{"path": elsewhere})
	if response.Code != http.StatusOK {
		t.Fatalf("forget status = %d", response.Code)
	}
	if _, err := os.Stat(progressFile); err != nil {
		t.Errorf("progress outside OFFLINEU_ROOTS was touched: %v", err)
	}
}

func TestWrongMethodsAreRejected(t *testing.T) {
	env := newTestEnv(t)
	if response := env.request(http.MethodPost, "/api/state", nil); response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", response.Code)
	}
	if response := env.request(http.MethodGet, "/api/progress", nil); response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", response.Code)
	}
}

func TestStaticAssetsAreServedFromTheGivenFileSystem(t *testing.T) {
	env := newTestEnv(t)
	assets := filepath.Join(env.root, "webassets")
	if err := os.MkdirAll(filepath.Join(assets, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := []byte("<!DOCTYPE html><title>OfflineU SPA</title>")
	if err := os.WriteFile(filepath.Join(assets, "index.html"), index, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.app.Handler(os.DirFS(assets))

	page := env.request(http.MethodGet, "/", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "OfflineU SPA") {
		t.Errorf("index page was not served: %d %q", page.Code, page.Body.String())
	}

	// a deep link must fall back to index.html so the SPA router can take over
	deep := env.request(http.MethodGet, "/lesson/whatever", nil)
	if deep.Code != http.StatusOK || !strings.Contains(deep.Body.String(), "OfflineU SPA") {
		t.Errorf("deep link did not fall back to index.html: %d", deep.Code)
	}

	asset := env.request(http.MethodGet, "/assets/app.js", nil)
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "console.log") {
		t.Errorf("asset was not served: %d %q", asset.Code, asset.Body.String())
	}
	if !strings.HasPrefix(asset.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("asset content type = %q", asset.Header().Get("Content-Type"))
	}
}
