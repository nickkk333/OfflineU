package offlineu

import (
	"net/http"
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
	if !session.Autoplay {
		t.Error("autoplay should be on by default")
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

func TestWatchdogStopsAfterTheLastLesson(t *testing.T) {
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
