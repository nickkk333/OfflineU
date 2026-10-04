package offlineu

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// stubTranscoder pretends ffmpeg is installed without needing one: probing
// fails (no ffprobe), which is exactly the case the container heuristic covers.
func stubTranscoder() *Transcoder {
	return &Transcoder{ffmpeg: "ffmpeg"}
}

func TestTranscoderIsUnavailableWithoutFFmpeg(t *testing.T) {
	if (&Transcoder{}).Available() {
		t.Error("an empty transcoder must report itself as unavailable")
	}
	if findBinary("OFFLINEU_TEST_BINARY", "definitely-not-an-installed-binary") != "" {
		t.Error("a missing binary was reported as found")
	}
	if info, err := (&Transcoder{ffmpeg: "ffmpeg"}).Probe(context.Background(), "x.mkv"); err == nil {
		t.Errorf("probing without ffprobe should fail, got %+v", info)
	}
}

func TestPlanMarksUnfriendlyContainersForConversion(t *testing.T) {
	transcoder := stubTranscoder()
	if plan := transcoder.Plan(context.Background(), "Section 1/01 - Intro.mkv"); !plan.NeedsTranscode || plan.Reason != "container" {
		t.Errorf("mkv plan = %+v", plan)
	}
	if plan := transcoder.Plan(context.Background(), "Section 1/01 - Intro.avi"); !plan.NeedsTranscode {
		t.Errorf("avi plan = %+v", plan)
	}
	if plan := transcoder.Plan(context.Background(), "Section 1/01 - Intro.mp4"); plan.NeedsTranscode {
		t.Errorf("mp4 plan = %+v", plan)
	}
	if plan := transcoder.Plan(context.Background(), "Section 1/01 - Intro.mp3"); plan.NeedsTranscode {
		t.Errorf("mp3 plan = %+v", plan)
	}
	if !transcoder.Plan(context.Background(), "x.mkv").TranscodeAvailable {
		t.Error("the plan should say that converting is possible")
	}
}

func TestRepackagingCopiesBothStreamsIntoMPEGTS(t *testing.T) {
	args, mime, err := stubTranscoder().Args("lesson.mkv", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, 0)
	if err != nil {
		t.Fatalf("args failed: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{"-c:v copy", "-c:a copy", "-f mpegts", "-sn", "-dn", "pipe:1"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("%q missing from %q", expected, joined)
		}
	}
	if strings.Contains(joined, "libx264") {
		t.Errorf("a supported file must not be re-encoded: %q", joined)
	}
	if mime != mpegTSMime {
		t.Errorf("mime = %q", mime)
	}
}

func TestUnsupportedCodecsAreReencoded(t *testing.T) {
	args, _, err := stubTranscoder().Args("lesson.mkv", MediaInfo{VideoCodec: "hevc", AudioCodec: "dts"}, 0)
	if err != nil {
		t.Fatalf("args failed: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v libx264") || !strings.Contains(joined, "-c:a aac") {
		t.Errorf("the file was not scheduled for re-encoding: %q", joined)
	}
	if strings.Contains(joined, "-c:a copy") {
		t.Errorf("dts must not be copied: %q", joined)
	}

	// A video the renderer cannot decode but audio it can: only the video is
	// re-encoded.
	args, _, _ = stubTranscoder().Args("lesson.mkv", MediaInfo{VideoCodec: "vp9", AudioCodec: "aac"}, 0)
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v libx264") || !strings.Contains(joined, "-c:a copy") {
		t.Errorf("unexpected mix: %q", joined)
	}
}

func TestAudioLessonsBecomeAAC(t *testing.T) {
	args, mime, err := stubTranscoder().Args("lesson.flac", MediaInfo{AudioCodec: "flac"}, 0)
	if err != nil {
		t.Fatalf("args failed: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:a aac") || !strings.Contains(joined, "-f adts") {
		t.Errorf("flac was not scheduled for conversion: %q", joined)
	}
	if mime != aacADTSMime {
		t.Errorf("mime = %q", mime)
	}

	// mp3 stays mp3: it is copied into the mp3 muxer, not into AAC's ADTS.
	args, mime, _ = stubTranscoder().Args("lesson.mp3", MediaInfo{AudioCodec: "mp3"}, 0)
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "-c:a copy") || !strings.Contains(joined, "-f mp3") {
		t.Errorf("mp3 should be copied: %q", joined)
	}
	if mime != "audio/mpeg" {
		t.Errorf("mime = %q", mime)
	}
}

func TestResumePositionBecomesAnInputSeek(t *testing.T) {
	args, _, err := stubTranscoder().Args("lesson.mkv", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, 90)
	if err != nil {
		t.Fatalf("args failed: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-ss 00:01:30") {
		t.Errorf("the seek is missing: %q", joined)
	}
	// -ss has to come before -i, otherwise ffmpeg decodes everything first.
	if strings.Index(joined, "-ss") > strings.Index(joined, "-i ") {
		t.Errorf("-ss must precede the input: %q", joined)
	}
	if got := formatClockDuration(3725); got != "01:02:05" {
		t.Errorf("duration = %q", got)
	}
}

func TestStreamAnnouncesTheConvertedFormat(t *testing.T) {
	// The test binary stands in for ffmpeg: it is not one, so the conversion
	// fails - but the response headers are already written by then, which is
	// what this test checks.
	self, err := os.Executable()
	if err != nil {
		t.Skip("cannot locate the test binary")
	}
	// The stand-in prints its own usage to stderr; keep that out of the test log.
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	recorder := httptest.NewRecorder()
	_ = (&Transcoder{ffmpeg: self}).Stream(context.Background(), recorder, "lesson.mkv",
		MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, 0)

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != mpegTSMime {
		t.Errorf("content type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache control = %q", got)
	}
}

func TestStreamWithoutFFmpegIsReportedAsNotImplemented(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	env.app.Transcoder = &Transcoder{}

	response := env.request(http.MethodGet, "/api/dlna/stream?lesson=Section%201%2F01%20-%20Intro.mp4", nil)
	if response.Code != http.StatusNotImplemented {
		t.Errorf("status = %d %s", response.Code, response.Body.String())
	}
}

func TestStreamRejectsUnknownLessons(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	env.app.Transcoder = stubTranscoder()

	if response := env.request(http.MethodGet, "/api/dlna/stream?lesson=nope.mp4", nil); response.Code != http.StatusNotFound {
		t.Errorf("unknown lesson: status = %d", response.Code)
	}
	// A document lesson has nothing to stream.
	if response := env.request(http.MethodGet, "/api/dlna/stream?lesson=Section%201%2F02%20-%20Notes.txt", nil); response.Code != http.StatusNotFound {
		t.Errorf("document lesson: status = %d", response.Code)
	}
}

func TestCastConvertsWhatTheRendererWouldRefuse(t *testing.T) {
	env := newTestEnv(t)
	// The very case this feature exists for: a lesson in a container most TVs
	// refuse, although they decode the streams inside it.
	env.write("Section 1/07 - Bonus.mkv", bytes.Repeat([]byte{0}, 64))
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))
	env.app.Transcoder = stubTranscoder()

	response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":        "uuid:fake-renderer",
		"lesson_path":   "Section 1/07 - Bonus.mkv",
		"start_seconds": 45,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	payload := struct {
		Converted bool   `json:"converted"`
		URL       string `json:"url"`
	}{}
	env.decode(response, &payload)
	if !payload.Converted {
		t.Fatalf("expected a converted stream, got %+v", payload)
	}
	if !strings.HasPrefix(payload.URL, "http://example.com/api/dlna/stream?lesson=Section+1%2F07+-+Bonus.mkv") {
		t.Errorf("url = %q", payload.URL)
	}
	if !strings.Contains(payload.URL, "start=45") {
		t.Errorf("the resume position is missing: %q", payload.URL)
	}
	if body := fake.bodyOf(0); !strings.Contains(body, "video/mp2t") {
		t.Errorf("the renderer was not told about the stream format: %s", body)
	}
	// A converted stream cannot be seeked by the renderer: ffmpeg already did.
	if actions := fake.recorded(); len(actions) != 2 {
		t.Errorf("actions = %v", actions)
	}
}

func TestCastCanBeForcedEitherWay(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	fake := newFakeRenderer(t)
	seedRenderers(env.app.DLNA, fake.renderer(t))
	env.app.Transcoder = stubTranscoder()

	// "off": hand over the file, whatever it is.
	response := env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"transcode":   "off",
	})
	payload := struct {
		Converted bool   `json:"converted"`
		URL       string `json:"url"`
	}{}
	env.decode(response, &payload)
	if payload.Converted || !strings.Contains(payload.URL, "/files/") {
		t.Errorf("transcode=off: %+v", payload)
	}

	// Without ffmpeg, forcing conversion has to fail loudly instead of
	// silently playing a file the TV will refuse.
	env.app.Transcoder = &Transcoder{}
	response = env.request(http.MethodPost, "/api/dlna/cast", map[string]any{
		"device":      "uuid:fake-renderer",
		"lesson_path": "Section 1/01 - Intro.mp4",
		"transcode":   "on",
	})
	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", response.Code)
	}
}

func TestLessonPayloadCarriesTheCastPlan(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 1/07 - Bonus.mkv", bytes.Repeat([]byte{0}, 64))
	env.loadCourse()
	env.app.Transcoder = stubTranscoder()

	payload := struct {
		DLNAEnabled bool     `json:"dlna_enabled"`
		CastPlan    CastPlan `json:"cast_plan"`
	}{}
	env.decode(env.lessonRequest("Section 1/07 - Bonus.mkv", false), &payload)
	if !payload.DLNAEnabled {
		t.Error("dlna should be enabled by default")
	}
	if !payload.CastPlan.TranscodeAvailable || !payload.CastPlan.NeedsTranscode {
		t.Errorf("mkv cast plan = %+v", payload.CastPlan)
	}

	// A file every renderer takes needs no conversion at all.
	env.decode(env.lessonRequest("Section 1/01 - Intro.mp4", false), &payload)
	if payload.CastPlan.NeedsTranscode {
		t.Errorf("mp4 cast plan = %+v", payload.CastPlan)
	}
}

func TestLimitedBufferKeepsTheFirstBytes(t *testing.T) {
	buffer := &limitedBuffer{limit: 8}
	if _, err := buffer.Write([]byte("diagnostics that go on and on")); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "diagnost" {
		t.Errorf("buffer = %q", got)
	}
}
