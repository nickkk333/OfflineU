package offlineu

import (
	"net/http"
	"strings"
	"testing"
)

func TestSBVIsConvertedToWebVTT(t *testing.T) {
	sbv := "0:00:01.500,0:00:04.250\r\nFirst line\r\nSecond line\r\n\r\n" +
		"0:00:05.000,0:00:07.900\r\nNext cue\r\n"
	vtt := ConvertSubtitleToVTT("talk.sbv", sbv)
	if !strings.HasPrefix(vtt, "WEBVTT") {
		t.Fatalf("missing WEBVTT header: %q", vtt)
	}
	for _, want := range []string{
		"00:00:01.500 --> 00:00:04.250",
		"First line\nSecond line",
		"00:00:05.000 --> 00:00:07.900",
		"Next cue",
	} {
		if !strings.Contains(vtt, want) {
			t.Errorf("vtt misses %q: %s", want, vtt)
		}
	}
}

func TestASSIsConvertedToWebVTT(t *testing.T) {
	ass := `[Script Info]
Title: Sample
ScriptType: v4.00+

[V4+ Styles]
Format: Name, Fontname, Fontsize
Style: Default,Arial,20

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.50,Default,,0,0,0,,{\i1}Hello{\i0} world
Dialogue: 0,0:00:04.00,0:00:06.00,Default,,0,0,0,,Line one\NLine two
Comment: 0,0:00:07.00,0:00:08.00,Default,,0,0,0,,ignored
`
	vtt := ConvertSubtitleToVTT("talk.ass", ass)
	if !strings.HasPrefix(vtt, "WEBVTT") {
		t.Fatalf("missing WEBVTT header: %q", vtt)
	}
	for _, want := range []string{
		"00:00:01.000 --> 00:00:03.500\nHello world",
		"00:00:04.000 --> 00:00:06.000\nLine one\nLine two",
	} {
		if !strings.Contains(vtt, want) {
			t.Errorf("vtt misses %q: %s", want, vtt)
		}
	}
	for _, unwanted := range []string{"\\i1", "Script Info", "ignored", "Dialogue:"} {
		if strings.Contains(vtt, unwanted) {
			t.Errorf("vtt still contains %q: %s", unwanted, vtt)
		}
	}
}

func TestSubViewerSubIsConvertedToWebVTT(t *testing.T) {
	// SubViewer flavour of .sub: "[SUBTITLE]" blocks with "start,end" lines
	// that use a comma as the decimal separator.
	sub := "[INFORMATION]\r\nTitle: Sample\r\n[END INFORMATION]\r\n" +
		"[SUBTITLE]\r\n00:00:01,00,00:00:03,50\r\nHello there\r\n\r\n" +
		"[SUBTITLE]\r\n00:00:04,00,00:00:06,00\r\nBye\r\n"
	vtt := ConvertSubtitleToVTT("talk.sub", sub)
	for _, want := range []string{
		"00:00:01.000 --> 00:00:03.500\nHello there",
		"00:00:04.000 --> 00:00:06.000\nBye",
	} {
		if !strings.Contains(vtt, want) {
			t.Errorf("vtt misses %q: %s", want, vtt)
		}
	}
	if strings.Contains(vtt, "[SUBTITLE]") || strings.Contains(vtt, "[INFORMATION]") {
		t.Errorf("section markers leaked into the vtt: %s", vtt)
	}
}

func TestMicroDVDSubIsConvertedToWebVTT(t *testing.T) {
	// MicroDVD stores frames, not timestamps; the first line may declare
	// the frame rate ("{1}{1}25.000" = 25 fps here: frame 50 = 2s).
	sub := "{1}{1}25.000\r\n{0}{50}First cue\r\n{75}{100}{y:i}Second cue\r\n"
	vtt := ConvertSubtitleToVTT("talk.sub", sub)
	for _, want := range []string{
		"00:00:00.000 --> 00:00:02.000\nFirst cue",
		"00:00:03.000 --> 00:00:04.000\nSecond cue",
	} {
		if !strings.Contains(vtt, want) {
			t.Errorf("vtt misses %q: %s", want, vtt)
		}
	}
	if strings.Contains(vtt, "{y:i}") {
		t.Errorf("formatting code was not stripped: %s", vtt)
	}
}

func TestMicroDVDSubFallsBackTo25FPS(t *testing.T) {
	// No frame rate header: the documented default is 25 fps, so frame 100
	// lands at 4 seconds.
	vtt := ConvertSubtitleToVTT("plain.sub", "{25}{100}Late cue\n")
	if !strings.Contains(vtt, "00:00:01.000 --> 00:00:04.000\nLate cue") {
		t.Errorf("unexpected vtt: %s", vtt)
	}
}

func TestVTTKeepsItsHeaderAndSRTStaysTheFallback(t *testing.T) {
	// A .vtt file is passed through with its header intact (no double header).
	vtt := ConvertSubtitleToVTT("as-is.vtt", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n")
	if strings.Count(vtt, "WEBVTT") != 1 {
		t.Errorf("header mangled: %q", vtt)
	}
	// Unknown extensions keep the historical SubRip conversion.
	fallback := ConvertSubtitleToVTT("legacy.xyz", srtSample)
	if !strings.HasPrefix(fallback, "WEBVTT") || !strings.Contains(fallback, "00:00:01.000 --> 00:00:04.000") {
		t.Errorf("unexpected fallback: %q", fallback)
	}
}

func TestSubtitleEndpointConvertsEverySupportedFormat(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 1/07 - Talk.ass", []byte(
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"+
			"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,ASS works\n"))
	env.write("Section 1/08 - Talk.sub", []byte("{0}{25}SUB works\n"))
	env.write("Section 1/09 - Talk.sbv", []byte("0:00:01.000,0:00:02.000\nSBV works\n"))
	env.loadCourse()

	cases := map[string]string{
		"Section 1/07 - Talk.ass": "ASS works",
		"Section 1/08 - Talk.sub": "SUB works",
		"Section 1/09 - Talk.sbv": "SBV works",
	}
	for path, cue := range cases {
		response := env.request(http.MethodGet, "/subtitles/"+EscapeURLPath(path), nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, response.Code)
			continue
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/vtt") {
			t.Errorf("%s: content type = %q", path, got)
		}
		body := response.Body.String()
		if !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, cue) {
			t.Errorf("%s: unexpected body: %s", path, body)
		}
	}
}

func TestSubtitlesAttachForEverySupportedExtension(t *testing.T) {
	// One video per folder with its subtitle beside it: all six extensions
	// must be recognised as subtitles (attached, never a lesson of their own).
	for _, extension := range subtitleExtensions {
		env := newTestEnv(t)
		env.write("Section 4/01 - Clip.mp4", []byte("fake"))
		env.write("Section 4/01 - Clip"+extension, []byte("{0}{25}x\n"))
		env.loadCourse()
		lesson := env.lessonsByPath()["Section 4/01 - Clip.mp4"]
		if lesson == nil {
			t.Fatalf("%s: video lesson missing", extension)
		}
		if lesson.SubtitleFile != "Section 4/01 - Clip"+extension {
			t.Errorf("%s: subtitle not attached: %q", extension, lesson.SubtitleFile)
		}
	}
}
