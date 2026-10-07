package offlineu

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseClockSeconds(t *testing.T) {
	cases := map[string]float64{
		"00:01:30":        90,
		"1:02:05":         3725,
		"02:05":           125,
		"00:00:00":        0,
		"NOT_IMPLEMENTED": 0,
		"":                0,
		"unknown":         0,
	}
	for raw, want := range cases {
		if got := parseClockSeconds(raw); got != want {
			t.Errorf("%q = %v, want %v", raw, got, want)
		}
	}
}

func TestPositionFallsBackToTheClock(t *testing.T) {
	session := &CastSession{
		StartSeconds: 30,
		Duration:     600,
		StartedAt:    time.Now().Add(-10 * time.Second),
		State:        CastStatePlaying,
	}
	position, reported := session.position(time.Now())
	if reported {
		t.Error("without a device report the position is only estimated")
	}
	if position < 39 || position > 41 {
		t.Errorf("position = %v, want ~40", position)
	}
	if session.finished(time.Now()) {
		t.Error("a 600 s lesson is not finished after 40 s")
	}
}

func TestDeviceReportWinsOverTheClock(t *testing.T) {
	now := time.Now()
	session := &CastSession{
		Duration:     600,
		StartedAt:    now.Add(-10 * time.Second),
		State:        CastStatePlaying,
		RealPosition: 120,
		RealAt:       now.Add(-2 * time.Second),
		RealState:    "PLAYING",
	}
	position, reported := session.position(now)
	if !reported {
		t.Error("the device reported, so it should be trusted")
	}
	if position < 121 || position > 123 {
		t.Errorf("position = %v, want ~122", position)
	}

	// A paused device does not move on.
	session.RealState = "PAUSED_PLAYBACK"
	if position, _ := session.position(now); position != 120 {
		t.Errorf("a paused device should stay at 120, got %v", position)
	}

	// A report that stopped coming is ignored.
	session.RealAt = now.Add(-time.Hour)
	if _, reported := session.position(now); reported {
		t.Error("a stale report must not be used")
	}
}

func TestFinishedAtTheEndOfTheMedia(t *testing.T) {
	session := &CastSession{
		Duration:  300,
		StartedAt: time.Now().Add(-299 * time.Second),
		State:     CastStatePlaying,
	}
	if !session.finished(time.Now()) {
		t.Error("a lesson 1 s before its end counts as finished")
	}
	session.Duration = 0
	if session.finished(time.Now()) {
		t.Error("without a duration nothing can be finished")
	}
}

func TestPauseFreezesTheClock(t *testing.T) {
	base := time.Now()
	session := &CastSession{Duration: 600, StartedAt: base, State: CastStatePlaying}
	// 10 s playing, then paused for 10 s, then playing again.
	session.pause(base.Add(10 * time.Second))
	session.resume(base.Add(20 * time.Second))
	if elapsed := session.elapsed(base.Add(25 * time.Second)); elapsed > 16 || elapsed < 14 {
		t.Errorf("elapsed = %v, want ~15 (10 s before the pause + 5 s after it)", elapsed)
	}
	// Pausing twice must not count the pause twice.
	session.pause(base.Add(30 * time.Second))
	session.pause(base.Add(31 * time.Second))
	session.resume(base.Add(35 * time.Second))
	if elapsed := session.elapsed(base.Add(40 * time.Second)); elapsed > 26 || elapsed < 24 {
		t.Errorf("elapsed = %v, want ~25 (15 s + 10 s, pause counted once)", elapsed)
	}
}

func TestSessionEndpointIsQuietWithoutACast(t *testing.T) {
	env := newTestEnv(t)
	payload := struct {
		Active bool `json:"active"`
	}{}
	env.decode(env.request(http.MethodGet, "/api/dlna/session", nil), &payload)
	if payload.Active {
		t.Error("no cast is running, so the session must be inactive")
	}
}

func TestCastRegistersASession(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	if session == nil {
		t.Fatal("the cast was not remembered")
	}
	t.Cleanup(func() { env.app.endCast(session) })

	if session.LessonPath != "Section 1/01 - Intro.mp4" {
		t.Errorf("lesson = %q", session.LessonPath)
	}
	if session.PlayMode != PlayModeOnce {
		t.Errorf("play mode = %q, want the default %q", session.PlayMode, PlayModeOnce)
	}

	payload := struct {
		Session CastSessionView `json:"session"`
	}{}
	env.decode(response, &payload)
	if !payload.Session.Active || payload.Session.LessonPath != "Section 1/01 - Intro.mp4" {
		t.Errorf("session = %+v", payload.Session)
	}
	// The course has a second video after the documents: that is what plays next.
	if payload.Session.NextLessonPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("next lesson = %q", payload.Session.NextLessonPath)
	}
}

func TestWatchdogContinuesWithTheNextLesson(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"play_mode":   PlayModeNext,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	// Pretend the lesson ran to its end.
	session.Duration = 60
	session.StartedAt = time.Now().Add(-61 * time.Second)

	if !env.app.castTick(session) {
		t.Fatal("the watchdog should keep watching after handing over")
	}
	if session.LessonPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("the session did not move on: %q", session.LessonPath)
	}
	if session.StartSeconds != 0 || session.State != CastStatePlaying {
		t.Errorf("session = %+v", session)
	}
	// Intro is finished, and the stored progress says so.
	stored := struct {
		Completed       bool `json:"completed"`
		ProgressSeconds int  `json:"progress_seconds"`
	}{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &stored)
	if !stored.Completed {
		t.Errorf("the finished lesson was not marked completed: %+v", stored)
	}
	if stored.ProgressSeconds != 60 {
		t.Errorf("progress seconds = %d, want 60", stored.ProgressSeconds)
	}

	actions := fake.recorded()
	setURI := 0
	for _, action := range actions {
		if action == "SetAVTransportURI" {
			setURI++
		}
	}
	if setURI != 2 {
		t.Errorf("expected two SetAVTransportURI calls (the lesson and the next one), got %v", actions)
	}
}

func TestSessionFollowsTheLessonTheWatchdogStarted(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"duration":    60,
		"play_mode":   PlayModeNext,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	before := struct {
		LessonPath    string `json:"lesson_path"`
		LessonURL     string `json:"lesson_url"`
		NextLessonURL string `json:"next_lesson_path"`
	}{}
	env.decode(env.request(http.MethodGet, "/api/dlna/session", nil), &before)
	if before.LessonPath != "Section 1/01 - Intro.mp4" {
		t.Fatalf("lesson = %q", before.LessonPath)
	}
	if before.LessonURL != "Section 1/01 - Intro.mp4/Intro" {
		t.Fatalf("the browser needs the lesson URL to follow: %q", before.LessonURL)
	}

	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	session.Duration = 60
	session.StartedAt = time.Now().Add(-61 * time.Second)
	if !env.app.castTick(session) {
		t.Fatal("the watchdog should keep watching after handing over")
	}

	// What the browser polls next: it has to name the lesson the TV got now.
	after := struct {
		Active        bool   `json:"active"`
		LessonPath    string `json:"lesson_path"`
		LessonURL     string `json:"lesson_url"`
		LessonTitle   string `json:"lesson_title"`
		CoursePath    string `json:"course_path"`
		NextLessonURL string `json:"next_lesson_path"`
	}{}
	env.decode(env.request(http.MethodGet, "/api/dlna/session", nil), &after)
	if !after.Active {
		t.Fatalf("the session went inactive: %+v", after)
	}
	if after.LessonPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("lesson = %q, want the next one", after.LessonPath)
	}
	if after.LessonURL != "Section 1/06 - Wrap Up.mp4/Wrap_Up" {
		t.Errorf("lesson url = %q - the browser cannot follow without it", after.LessonURL)
	}
	if after.LessonTitle != "Wrap Up" {
		t.Errorf("lesson title = %q", after.LessonTitle)
	}
	if after.CoursePath != env.courseDir {
		t.Errorf("course = %q", after.CoursePath)
	}
}

func TestWatchdogStopsAfterTheLastLesson(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/06 - Wrap Up.mp4",
		"play_mode":   PlayModeNext,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	session.Duration = 60
	session.StartedAt = time.Now().Add(-61 * time.Second)

	if env.app.castTick(session) {
		t.Error("the last lesson has no successor, so the watchdog should stop")
	}
	if session.State != CastStateEnded {
		t.Errorf("state = %q", session.State)
	}
}

func TestWatchdogUsesWhatTheDeviceReports(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))
	// The device is 5 s into a 10 minute lesson, which the file itself cannot
	// tell us (no ffmpeg in the test).
	fake.setPosition(5, 600, "PLAYING")

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	session.Duration = 0 // only the device knows how long the lesson is
	session.StartedAt = time.Now().Add(-2 * time.Second)

	if !env.app.castTick(session) {
		t.Fatal("the watchdog should keep watching")
	}
	if session.RealPosition != 5 {
		t.Errorf("the reported position was ignored: %v", session.RealPosition)
	}
	if session.Duration != 600 {
		t.Errorf("the reported duration should fill in the missing one: %v", session.Duration)
	}
	view := env.app.castSnapshot()
	if !view.Reported || view.Position < 5 || view.Position > 7 {
		t.Errorf("view = %+v", view)
	}
}

func TestCastKeepsACompletionItDidNotEarn(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	// The lesson was finished in the browser before.
	env.request(http.MethodPost, "/api/progress", map[string]any{
		"lesson_path":      "Section 1/01 - Intro.mp4",
		"completed":        true,
		"progress_seconds": 500,
	})
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	// Watching the finished lesson again from the start: 20 s in, the cast
	// saves its position - but it must not "un-complete" the lesson.
	session.Duration = 600
	session.StartedAt = time.Now().Add(-20 * time.Second)
	if !env.app.castTick(session) {
		t.Fatal("the cast should still be running")
	}

	stored := struct {
		Completed       bool `json:"completed"`
		ProgressSeconds int  `json:"progress_seconds"`
	}{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &stored)
	if !stored.Completed {
		t.Errorf("the completion flag was cleared: %+v", stored)
	}
	if stored.ProgressSeconds != 500 {
		t.Errorf("progress seconds = %d, want 500 (never backwards)", stored.ProgressSeconds)
	}
}

func TestCastUsesTheDurationTheBrowserMeasured(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	// No ffmpeg here, so the only length available is the one from the browser.
	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"duration":    754,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	if session.Duration != 754 {
		t.Errorf("duration = %v, want 754", session.Duration)
	}
}

func TestSkippingAheadMarksTheLessonCompleted(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "next",
	})

	stored := struct {
		Completed bool `json:"completed"`
	}{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &stored)
	if !stored.Completed {
		t.Error("the lesson that was skipped past is not marked completed")
	}
	if session.LessonPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("session = %q", session.LessonPath)
	}
}

func TestCastIsRecordedLikeOpeningALesson(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/06 - Wrap Up.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	// The resume card of the dashboard follows the cast.
	course := env.app.Store.Get()
	env.app.Progress.ApplyToTree(course)
	if course.LastAccessedPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("last accessed = %q", course.LastAccessedPath)
	}
}

func TestSeekRepositionsAPlainFile(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"duration":    600,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	if err := env.app.castSeek(session, 125); err != nil {
		t.Fatalf("seek failed: %v", err)
	}
	if session.StartSeconds != 125 {
		t.Errorf("start seconds = %v, want 125", session.StartSeconds)
	}
	if session.LastSaved != 125 {
		t.Errorf("the saved position should follow the seek: %v", session.LastSaved)
	}
	// The device was told, in the format AVTransport expects.
	body := ""
	actions := fake.recorded()
	for index, action := range actions {
		if action == "Seek" {
			body = fake.bodyOf(index)
			break
		}
	}
	if body == "" {
		t.Fatalf("no Seek was sent to the device: %v", actions)
	}
	if !strings.Contains(body, "<Target>00:02:05</Target>") || !strings.Contains(body, "REL_TIME") {
		t.Errorf("seek request = %s", body)
	}

	// Seeking past the end lands just before it, so the lesson can still end.
	if err := env.app.castSeek(session, 99999); err != nil {
		t.Fatalf("seek failed: %v", err)
	}
	if session.StartSeconds != 599 {
		t.Errorf("start seconds = %v, want 599", session.StartSeconds)
	}
}

func TestSeekRestartsAConvertedStream(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))
	// "ffmpeg" is present, so the cast hands over a converted stream that the
	// device cannot seek in.
	env.app.Transcoder = &Transcoder{ffmpeg: "ffmpeg"}

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"transcode":   "on",
		"duration":    600,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	if !session.Converted {
		t.Fatal("the cast should be using a converted stream")
	}

	if err := env.app.castSeek(session, 300); err != nil {
		t.Fatalf("seek failed: %v", err)
	}
	if session.StartSeconds != 300 {
		t.Errorf("start seconds = %v, want 300", session.StartSeconds)
	}
	// A converted stream is restarted instead of seeked, and ffmpeg gets -ss.
	if actions := fake.recorded(); countOf(actions, "SetAVTransportURI") != 2 {
		t.Errorf("the stream was not handed over again: %v", actions)
	}
	last := ""
	for index, action := range fake.recorded() {
		if action == "SetAVTransportURI" {
			last = fake.bodyOf(index)
		}
	}
	if !strings.Contains(last, "start=300") {
		t.Errorf("the restarted stream does not start at 300 s: %s", last)
	}
}

func TestSeekThroughTheControlEndpoint(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"duration":    600,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	if response := env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "seek",
	}); response.Code != http.StatusBadRequest {
		t.Errorf("without a position: status = %d, want 400", response.Code)
	}
	response := env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device":   "uuid:fake-renderer",
		"action":   "seek",
		"position": 90,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	if session.StartSeconds != 90 {
		t.Errorf("start seconds = %v, want 90", session.StartSeconds)
	}
	if view := env.app.castSnapshot(); view.Position < 89 {
		t.Errorf("the session does not report the new position: %+v", view)
	}
}

func countOf(actions []string, wanted string) int {
	found := 0
	for _, action := range actions {
		if action == wanted {
			found++
		}
	}
	return found
}

func TestWatchdogWaitsWhileAnotherCourseIsOpen(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	courseA := env.app.Store.Get()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	// The user opens a different course while the TV keeps playing.
	other := filepath.Join(env.root, "Other Course")
	if err := os.MkdirAll(filepath.Join(other, "Part 1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "Part 1", "01 - Other.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if response := env.request(http.MethodPost, "/load_course", map[string]string{"course_path": other}); response.Code != http.StatusOK {
		t.Fatalf("loading the other course failed: %d %s", response.Code, response.Body.String())
	}
	if env.app.Store.Get().Path == courseA.Path {
		t.Fatal("the other course was not loaded")
	}

	// The cast survives, but nothing is written into the course that is open.
	if !env.app.castTick(session) {
		t.Error("the watchdog must keep the cast alive while another course is open")
	}
	// (the other course has no progress file yet, so read it directly)
	if data, err := os.ReadFile(env.app.Store.Get().ProgressFile); err == nil {
		if strings.Contains(string(data), "Section 1/01") {
			t.Errorf("the cast wrote into the other course's progress: %s", data)
		}
	}

	// Coming back picks the cast up again.
	if response := env.request(http.MethodPost, "/load_course", map[string]string{"course_path": env.courseDir}); response.Code != http.StatusOK {
		t.Fatalf("loading the first course failed: %d %s", response.Code, response.Body.String())
	}
	session.Duration = 600
	session.StartedAt = time.Now().Add(-20 * time.Second)
	if !env.app.castTick(session) {
		t.Fatal("the cast should continue after coming back")
	}
	stored := struct {
		ProgressSeconds int `json:"progress_seconds"`
	}{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &stored)
	if stored.ProgressSeconds < 15 {
		t.Errorf("progress was not written after coming back: %+v", stored)
	}
}

func TestControlPauseResumeAndStop(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()

	env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "pause",
	})
	if session.PausedAt.IsZero() {
		t.Error("pausing the cast should freeze its clock")
	}
	if view := env.app.castSnapshot(); view.State != string(CastStatePaused) {
		t.Errorf("state = %q", view.State)
	}

	env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "play",
	})
	if !session.PausedAt.IsZero() {
		t.Error("resuming should restart the clock")
	}

	// "next" jumps to the following lesson without waiting for the end.
	env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "next",
	})
	if session.LessonPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("next did not advance: %q", session.LessonPath)
	}

	env.request(http.MethodPost, "/api/dlna/control", map[string]any{
		"device": "uuid:fake-renderer",
		"action": "stop",
	})
	if env.app.currentCast() != nil {
		t.Error("stopping should forget the cast")
	}
	if view := env.app.castSnapshot(); view.Active {
		t.Error("the session should be inactive after stop")
	}
}

func TestNextPlayableLessonSkipsDocuments(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	course := env.app.Store.Get()

	next := nextPlayableLesson(course, "Section 1/01 - Intro.mp4")
	if next == nil || next.RelPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("next = %+v", next)
	}
	if later := nextPlayableLesson(course, "Section 1/06 - Wrap Up.mp4"); later != nil {
		t.Errorf("after the last video there is nothing playable, got %q", later.RelPath)
	}
	if missing := nextPlayableLesson(course, "nope.mp4"); missing != nil {
		t.Error("an unknown lesson has no successor")
	}
}

// The product default is "once": nothing is sent, so the cast stops when the
// lesson is over (but the finished run still counts as completed).
func TestCastStopsWhenTheLessonEndsByDefault(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	if session.PlayMode != PlayModeOnce {
		t.Fatalf("play mode = %q, want the default %q", session.PlayMode, PlayModeOnce)
	}
	session.Duration = 60
	session.StartedAt = time.Now().Add(-61 * time.Second)

	if env.app.castTick(session) {
		t.Error("the default mode must stop when the lesson ends")
	}
	if session.State != CastStateEnded {
		t.Errorf("state = %q, want %q", session.State, CastStateEnded)
	}
	stored := struct {
		Completed       bool `json:"completed"`
		ProgressSeconds int  `json:"progress_seconds"`
	}{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &stored)
	if !stored.Completed || stored.ProgressSeconds != 60 {
		t.Errorf("the finished run was not stored: %+v", stored)
	}
}

// "loop" hands the very same lesson back to the device from the start and the
// watchdog keeps watching it.
func TestCastPlayModeLoopRestartsTheSameLesson(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"play_mode":   PlayModeLoop,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	if session.PlayMode != PlayModeLoop {
		t.Fatalf("play mode = %q, want %q", session.PlayMode, PlayModeLoop)
	}
	session.Duration = 60
	session.StartedAt = time.Now().Add(-61 * time.Second)

	if !env.app.castTick(session) {
		t.Fatal("looping must keep the watchdog running")
	}
	if session.LessonPath != "Section 1/01 - Intro.mp4" {
		t.Errorf("loop must stay on the lesson, got %q", session.LessonPath)
	}
	if session.State != CastStatePlaying || session.StartSeconds != 0 {
		t.Errorf("session = %+v", session)
	}
	// The run that just finished counts as completed before the repeat starts.
	stored := struct {
		Completed       bool `json:"completed"`
		ProgressSeconds int  `json:"progress_seconds"`
	}{}
	env.decodeRaw(env.progressJSON()["Section 1/01 - Intro.mp4"], &stored)
	if !stored.Completed || stored.ProgressSeconds != 60 {
		t.Errorf("progress of the finished run = %+v", stored)
	}
	// Initial push + the restart.
	setURI := 0
	for _, action := range fake.recorded() {
		if action == "SetAVTransportURI" {
			setURI++
		}
	}
	if setURI != 2 {
		t.Errorf("expected two SetAVTransportURI calls (the lesson and its repeat), got %v", fake.recorded())
	}
}

// The mode is a live setting: switching it while the TV is playing has to reach
// the watchdog, which is the part that actually applies it.
func TestPlayModeCanBeChangedWhileTheCastRuns(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	if session.PlayMode != PlayModeOnce {
		t.Fatalf("play mode = %q, want %q", session.PlayMode, PlayModeOnce)
	}

	response := env.request(http.MethodPost, "/api/settings", map[string]any{
		"device":    "uuid:fake-renderer",
		"play_mode": PlayModeNext,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	if session.PlayMode != PlayModeNext {
		t.Errorf("the running cast did not pick the mode up: %q", session.PlayMode)
	}
	view := env.app.castSnapshot()
	if view.PlayMode != PlayModeNext {
		t.Errorf("the browser does not see the new mode: %q", view.PlayMode)
	}

	// And it is the new mode that decides what happens at the end.
	session.Duration = 60
	session.StartedAt = time.Now().Add(-61 * time.Second)
	if !env.app.castTick(session) {
		t.Fatal("the watchdog should keep watching after handing over")
	}
	if session.LessonPath != "Section 1/06 - Wrap Up.mp4" {
		t.Errorf("the cast did not continue: %q", session.LessonPath)
	}
}

// A cast that already stopped ("once" ended it) has to wake up again when the
// user switches to 循环/连播 - before, the watchdog was gone for good.
func TestPlayModeWakesAnEndedCastUpAgain(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	session.Duration = 60
	session.StartedAt = time.Now().Add(-61 * time.Second)
	if env.app.castTick(session) {
		t.Fatal("the default mode should stop when the lesson ends")
	}
	if session.State != CastStateEnded {
		t.Fatalf("state = %q", session.State)
	}
	setURI := countOf(fake.recorded(), "SetAVTransportURI")

	if response := env.request(http.MethodPost, "/api/settings", map[string]any{
		"play_mode": PlayModeLoop,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	if session.State == CastStateEnded {
		t.Error("switching to loop must wake the cast up, not leave it ended")
	}
	// The next watchdog round repeats the lesson instead of staying dead.
	if !env.app.castTick(session) {
		t.Fatal("the woken up cast should keep watching")
	}
	if session.LessonPath != "Section 1/01 - Intro.mp4" {
		t.Errorf("loop must stay on the lesson, got %q", session.LessonPath)
	}
	if countOf(fake.recorded(), "SetAVTransportURI") != setURI+1 {
		t.Errorf("the lesson was not handed to the device again: %v", fake.recorded())
	}
}

// The setting is global: it is stored on the server, so another browser (or one
// with a cleared cache) picks it up again through /api/state.
func TestPlayModeIsRememberedOnTheServer(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	if response := env.request(http.MethodPost, "/api/settings", map[string]any{
		"play_mode": PlayModeLoop,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	state := struct {
		PlayMode string `json:"play_mode"`
	}{}
	env.decode(env.request(http.MethodGet, "/api/state", nil), &state)
	if state.PlayMode != PlayModeLoop {
		t.Errorf("/api/state play_mode = %q, want %q", state.PlayMode, PlayModeLoop)
	}

	// A "new browser" (a fresh store pointed at the same bookkeeping file) sees
	// the same choice - that is what survives a cleared cache.
	reopened := NewCourseStoreWithStateFile(&env.cfg, env.app.Store.StateFile())
	if reopened.PlayMode() != PlayModeLoop {
		t.Errorf("after reopening: play mode = %q, want %q", reopened.PlayMode(), PlayModeLoop)
	}
}

func TestPlayModeEndpointRejectsNonsense(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))
	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })

	if response := env.request(http.MethodPost, "/api/settings", map[string]any{}); response.Code != http.StatusBadRequest {
		t.Errorf("without a play_mode: status = %d, want 400", response.Code)
	}
	// A request about another device must not touch the running cast.
	if response := env.request(http.MethodPost, "/api/settings", map[string]any{
		"device":    "uuid:another-renderer",
		"play_mode": PlayModeLoop,
	}); response.Code != http.StatusOK {
		t.Errorf("another device: status = %d %s", response.Code, response.Body.String())
	}
	if session.PlayMode != PlayModeOnce {
		t.Errorf("the cast was switched although the device differs: %q", session.PlayMode)
	}
	// An unknown value falls back to the product default instead of sticking.
	env.request(http.MethodPost, "/api/settings", map[string]any{"play_mode": "nonsense"})
	if session.PlayMode != PlayModeOnce {
		t.Errorf("play mode = %q, want %q", session.PlayMode, PlayModeOnce)
	}
}

// The legacy autoplay boolean of /api/dlna/cast still maps onto the modes.
func TestCastLegacyAutoplayBooleanMapsToPlayMode(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))

	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"autoplay":    true,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if mode := env.app.currentCast().PlayMode; mode != PlayModeNext {
		t.Errorf("autoplay:true -> play mode %q, want %q", mode, PlayModeNext)
	}

	// beginCast replaces the running session, so a second request is enough.
	if response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"autoplay":    false,
	}); response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	session := env.app.currentCast()
	t.Cleanup(func() { env.app.endCast(session) })
	if session.PlayMode != PlayModeOnce {
		t.Errorf("autoplay:false -> play mode %q, want %q", session.PlayMode, PlayModeOnce)
	}
}
