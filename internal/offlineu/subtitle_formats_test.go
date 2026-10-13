package offlineu

import (
	"net/http"
	"strings"
	"testing"
)

// TestCommonSubtitleFormatsAreSupported pins down the five subtitle formats the
// product promises: each one must be recognised as a subtitle (attached to the
// video, never a lesson of its own) and served as WebVTT on the fly -
// regardless of filename case.
func TestCommonSubtitleFormatsAreSupported(t *testing.T) {
	// Minimal payload per format; each carries the cue "Cue works".
	payloads := map[string]string{
		".srt": "1\n00:00:01,000 --> 00:00:02,000\nCue works\n",
		".vtt": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nCue works\n",
		".ass": "[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
			"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Cue works\n",
		".sub": "{0}{25}Cue works\n",
		".sbv": "0:00:01.000,0:00:02.000\nCue works\n",
	}
	promised := []string{".srt", ".vtt", ".ass", ".sub", ".sbv"}
	for _, extension := range promised {
		if !extIn(extension, subtitleExtensions) {
			t.Errorf("%s is not in subtitleExtensions", extension)
		}
	}

	// End to end: one video per format in its own folder plus an UPPER-CASE
	// name, each subtitle attached beside it; /subtitles/ must answer
	// text/vtt with a WEBVTT body.
	env := newTestEnv(t)
	cases := map[string]string{}
	folders := map[string]string{
		".srt": "Srt",
		".vtt": "Vtt",
		".ass": "Ass",
		".sub": "Sub",
		".sbv": "Sbv",
	}
	for _, extension := range promised {
		folder := "Section 5/" + folders[extension]
		video := folder + "/01 - Clip.mp4"
		subtitle := folder + "/01 - Clip" + extension
		if extension == ".sbv" {
			video = folder + "/02 - TALK.MP4"
			subtitle = folder + "/02 - TALK.SBV"
		}
		env.write(video, []byte("fake video"))
		env.write(subtitle, []byte(payloads[extension]))
		cases[subtitle] = video
	}
	env.loadCourse()

	lessons := env.lessonsByPath()
	for subtitle, video := range cases {
		lesson := lessons[video]
		if lesson == nil {
			t.Fatalf("%s: video lesson missing", video)
		}
		if lesson.SubtitleFile != subtitle {
			t.Errorf("%s: subtitle not attached: %q", video, lesson.SubtitleFile)
		}
		if !strings.HasPrefix(lesson.SubtitleSrc(), "/subtitles/") {
			t.Errorf("%s: subtitle src = %q", video, lesson.SubtitleSrc())
		}
		if _, exists := lessons[subtitle]; exists {
			t.Errorf("%s: subtitle became a lesson of its own", subtitle)
		}
		response := env.request(http.MethodGet, "/subtitles/"+EscapeURLPath(subtitle), nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s: status = %d", subtitle, response.Code)
			continue
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/vtt") {
			t.Errorf("%s: content type = %q", subtitle, got)
		}
		body := response.Body.String()
		if !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, "Cue works") {
			t.Errorf("%s: unexpected body: %s", subtitle, body)
		}
	}
}
