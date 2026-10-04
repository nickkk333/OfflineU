package offlineu

import "testing"

func TestAutoplayTargetsTheNextMediaLessonAndSkipsDocuments(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	payload := struct {
		AutoplayURL   string `json:"autoplay_url"`
		AutoplayTitle string `json:"autoplay_title"`
		AutoplayHref  string `json:"autoplay_href"`
	}{}
	env.decode(env.lessonRequest("Section 1/01 - Intro.mp4/Intro", false), &payload)
	// 02..05 are documents, so the next *playable* lesson is the wrap-up video
	if payload.AutoplayURL != "Section 1/06 - Wrap Up.mp4/Wrap_Up" {
		t.Errorf("autoplay url = %q", payload.AutoplayURL)
	}
	if payload.AutoplayTitle != "Wrap Up" {
		t.Errorf("autoplay title = %q", payload.AutoplayTitle)
	}
	if payload.AutoplayHref != "/lesson/Section%201/06%20-%20Wrap%20Up.mp4/Wrap_Up?autoplay=1" {
		t.Errorf("autoplay href = %q", payload.AutoplayHref)
	}
	if payload.AutoplayURL == "Section 1/02 - Notes.txt/Notes" {
		t.Error("documents must be skipped when a playable lesson follows")
	}
}

func TestAutoplayWorksForAudioLessonsToo(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 2/resources/01 - Recap.mp3", []byte("fake audio"))
	env.loadCourse()
	payload := struct {
		AutoplayURL string `json:"autoplay_url"`
	}{}
	env.decode(env.lessonRequest("Section 1/06 - Wrap Up.mp4/Wrap_Up", false), &payload)
	if payload.AutoplayURL != "Section 2/resources/01 - Recap.mp3/Recap" {
		t.Errorf("autoplay url = %q", payload.AutoplayURL)
	}
}

func TestAutoplayFallsBackToTheNextLessonAfterTheLastMedia(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	payload := struct {
		AutoplayURL string `json:"autoplay_url"`
	}{}
	env.decode(env.lessonRequest("Section 1/06 - Wrap Up.mp4/Wrap_Up", false), &payload)
	if payload.AutoplayURL != "Section 2/resources/extras.md/Extras" {
		t.Errorf("autoplay url = %q", payload.AutoplayURL)
	}
}

func TestLastLessonOfTheCourseHasNoAutoplayTarget(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	payload := struct {
		AutoplayURL   string `json:"autoplay_url"`
		AutoplayTitle string `json:"autoplay_title"`
		AutoplayHref  string `json:"autoplay_href"`
	}{}
	env.decode(env.lessonRequest("Section 2/resources/extras.md/Extras", false), &payload)
	if payload.AutoplayURL != "" || payload.AutoplayTitle != "" || payload.AutoplayHref != "" {
		t.Errorf("the last lesson must not autoplay anything: %+v", payload)
	}
}

func TestAutoplayRequestIsForwardedToThePlayer(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	payload := struct {
		RequestedAutoplay bool `json:"requested_autoplay"`
	}{}
	env.decode(env.lessonRequest("Section 1/06 - Wrap Up.mp4/Wrap_Up", false), &payload)
	if payload.RequestedAutoplay {
		t.Error("autoplay was not requested but is flagged as such")
	}
	env.decode(env.lessonRequest("Section 1/06 - Wrap Up.mp4/Wrap_Up", true), &payload)
	if !payload.RequestedAutoplay {
		t.Error("?autoplay=1 was not forwarded")
	}
}

func TestMediaLessonsExposeEverythingThePlayerNeeds(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()

	media := struct {
		Lesson        *Lesson `json:"lesson"`
		AutoplayTitle string  `json:"autoplay_title"`
		Total         int     `json:"total"`
		Position      int     `json:"position"`
	}{}
	env.decode(env.lessonRequest("Section 1/01 - Intro.mp4/Intro", false), &media)
	if !media.Lesson.HasMedia() || media.Lesson.VideoFile == "" {
		t.Fatalf("media lesson not recognised: %+v", media.Lesson)
	}
	if media.AutoplayTitle == "" {
		t.Error("up-next title is missing")
	}
	if media.Total != 7 || media.Position != 0 {
		t.Errorf("position/total = %d/%d", media.Position, media.Total)
	}

	document := struct {
		Lesson *Lesson `json:"lesson"`
	}{}
	env.decode(env.lessonRequest("Section 1/02 - Notes.txt/Notes", false), &document)
	if document.Lesson.HasMedia() {
		t.Error("a document must not be treated as playable")
	}
}
