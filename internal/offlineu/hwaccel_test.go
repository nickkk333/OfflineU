package offlineu

import (
	"strings"
	"testing"
)

// The cap shrinks what is too tall and leaves everything else alone - it never
// scales up and never goes below the configured height.
func TestScaleFilterOnlyShrinks(t *testing.T) {
	transcoder := &Transcoder{MaxHeight: DefaultTranscodeHeight}
	if got := transcoder.scaleFilter(MediaInfo{Height: 1080}); got != `scale=-2:min(720\,ih)` {
		t.Errorf("1080p source = %q", got)
	}
	if got := transcoder.scaleFilter(MediaInfo{Height: 720}); got != "" {
		t.Errorf("720p source must not be scaled: %q", got)
	}
	if got := transcoder.scaleFilter(MediaInfo{Height: 480}); got != "" {
		t.Errorf("480p source must stay 480p: %q", got)
	}
	// An unknown height is capped too - that is the whole point of the setting.
	if got := transcoder.scaleFilter(MediaInfo{}); got == "" {
		t.Error("a file of unknown height must still be capped")
	}
	if got := (&Transcoder{}).scaleFilter(MediaInfo{Height: 2160}); got != "" {
		t.Errorf("no cap configured, but got %q", got)
	}
}

// When a GPU answers, both the decoding and the encoding go through it.
func TestHardwareEncoderReplacesLibx264(t *testing.T) {
	transcoder := &Transcoder{MaxHeight: 720, CRF: 26}
	transcoder.hw = &hwAccel{
		Kind:      hwVAAPI,
		Encoder:   "h264_vaapi",
		InputArgs: []string{"-hwaccel", "vaapi", "-vaapi_device", "/dev/dri/renderD128"},
		Quality:   vaapi.Quality,
	}
	transcoder.hwResolved = true

	joined := strings.Join(transcoder.videoOutputArgs(MediaInfo{Height: 1080}), " ")
	for _, expected := range []string{"-c:v h264_vaapi", "-qp 26", `scale=-2:min(720\,ih)`} {
		if !strings.Contains(joined, expected) {
			t.Errorf("%q missing from %q", expected, joined)
		}
	}
	if strings.Contains(joined, "libx264") {
		t.Errorf("the software encoder must not be used: %q", joined)
	}
	// The decode side only pays off when a frame is really re-encoded.
	if got := strings.Join(transcoder.hwInputArgs(true), " "); got != "-hwaccel vaapi -vaapi_device /dev/dri/renderD128" {
		t.Errorf("input args = %q", got)
	}
	if transcoder.hwInputArgs(false) != nil {
		t.Error("a copied stream must not be pushed through the hardware decoder")
	}
}

func TestSoftwareEncoderIsUsedWithoutHardware(t *testing.T) {
	transcoder := &Transcoder{MaxHeight: 720}
	transcoder.hwResolved = true // looked, found nothing

	joined := strings.Join(transcoder.videoOutputArgs(MediaInfo{Height: 2160}), " ")
	for _, expected := range []string{"-c:v libx264", "-preset veryfast", "-crf 23", "-pix_fmt yuv420p", `scale=-2:min(720\,ih)`} {
		if !strings.Contains(joined, expected) {
			t.Errorf("%q missing from %q", expected, joined)
		}
	}
	if transcoder.hwInputArgs(true) != nil {
		t.Error("no hardware, no hardware options")
	}
}

// A driver that fails must not take the lessons down with it: the next
// conversion is plain libx264 again.
func TestDisabledHardwareFallsBackToSoftware(t *testing.T) {
	transcoder := &Transcoder{MaxHeight: 720}
	transcoder.hw = &hwAccel{Kind: hwQSV, Encoder: "h264_qsv", InputArgs: []string{"-hwaccel", hwQSV}}
	transcoder.hwResolved = true

	transcoder.disableHardware()
	if transcoder.hardware() != nil {
		t.Error("the hardware answer was not dropped")
	}
	if joined := strings.Join(transcoder.videoOutputArgs(MediaInfo{Height: 1080}), " "); !strings.Contains(joined, "libx264") {
		t.Errorf("software fallback missing: %q", joined)
	}
}

// Every kind OfflineU claims to support has to be reachable: through the table
// in auto mode and through its name (or an alias) when it is forced.
func TestEveryBackendIsReachable(t *testing.T) {
	known := map[string]bool{}
	for _, backend := range hwBackends {
		known[backend.Kind] = true
	}
	for _, kind := range []string{hwNVENC, hwQSV, hwVAAPI, hwVideoToolbox, hwAMF, hwV4L2, hwRKMPP} {
		if !known[kind] {
			t.Errorf("%s is not part of hwBackends", kind)
		}
		if backend := backendFor(kind); backend == nil || backend.Kind != kind {
			t.Errorf("OFFLINEU_HWACCEL=%s does not resolve to the %s backend", kind, kind)
		}
	}
	// Aliases an operator is likely to type.
	for alias, kind := range map[string]string{
		"cuda":     hwNVENC,
		"nvidia":   hwNVENC,
		"vt":       hwVideoToolbox,
		"v4l2":     hwV4L2,
		"rockchip": hwRKMPP,
	} {
		if backend := backendFor(alias); backend == nil || backend.Kind != kind {
			t.Errorf("%q should mean %s", alias, kind)
		}
	}
	if backendFor("something else") != nil {
		t.Error("an unknown name must not resolve")
	}
}

// A backend is only offered when ffmpeg carries its encoder, the machine has
// the device and - where there is one - ffmpeg knows the decoder.
func TestBackendNeedsEncoderDeviceAndDecoder(t *testing.T) {
	backend := &hwBackend{
		Kind:     hwNVENC,
		Accel:    "cuda",
		Encoders: []string{"h264_nvenc"},
		Ready:    func() bool { return false },
	}
	if backend.build(map[string]bool{"cuda": true}, map[string]bool{"h264_nvenc": true}) != nil {
		t.Error("a backend whose device is missing must not be offered")
	}
	backend.Ready = func() bool { return true }
	if backend.build(map[string]bool{"cuda": true}, map[string]bool{"h264_nvenc": true}) == nil {
		t.Error("a ready backend with its encoder must be offered")
	}
	if backend.build(map[string]bool{"cuda": true}, map[string]bool{"h264_qsv": true}) != nil {
		t.Error("a build without this encoder must not be offered")
	}
	// The decoder is a bonus, never a requirement: an encoder on its own still
	// offloads the expensive half.
	accel := backend.build(map[string]bool{}, map[string]bool{"h264_nvenc": true})
	if accel == nil {
		t.Fatal("the encoder alone should still be used")
	}
	if len(accel.InputArgs) != 0 {
		t.Errorf("no decoder listed, but got %q", accel.InputArgs)
	}
}

// The quality factor has to reach whatever option the encoder understands.
func TestQualityReachesEveryEncoder(t *testing.T) {
	cases := map[string]string{
		"h264_nvenc": "-cq 26",
		"h264_qsv":   "-global_quality 26",
		"h264_vaapi": "-qp 26",
		"h264_amf":   "-rc cqp -qp_i 26 -qp_p 26",
		// These two drive the hardware through its own rate control; there is
		// no portable quality option to hand them.
		"h264_videotoolbox": "",
		"h264_v4l2m2m":      "",
		"h264_rkmpp":        "",
	}
	for _, backend := range hwBackends {
		expected, ok := cases[backend.Encoders[0]]
		if !ok {
			t.Fatalf("%s has no expectation", backend.Kind)
		}
		if backend.Quality == nil {
			if expected != "" {
				t.Errorf("%s should carry a quality option", backend.Kind)
			}
			continue
		}
		if got := strings.Join(backend.Quality(26), " "); got != expected {
			t.Errorf("%s quality = %q, want %q", backend.Kind, got, expected)
		}
	}
}

func TestHardwareDetectionHonoursTheSwitch(t *testing.T) {
	if detectHardware("ffmpeg", hwOff) != nil {
		t.Error("OFFLINEU_HWACCEL=off must not detect anything")
	}
	if detectHardware("", hwAuto) != nil {
		t.Error("without ffmpeg nothing can be detected")
	}
	// A forced kind that ffmpeg does not offer stays unused instead of being
	// handed to ffmpeg as an option it will refuse.
	if detectHardware("definitely-not-ffmpeg", hwQSV) != nil {
		t.Error("a missing ffmpeg must not report hardware")
	}
}

// Without ffprobe the frame size has to come out of "ffmpeg -i".
func TestFFmpegHeaderCarriesTheFrameSize(t *testing.T) {
	header := "Input #0, matroska,webm, from 'lesson.mkv':\n" +
		"  Duration: 00:10:05.50, start: 0.000000, bitrate: 2048 kb/s\n" +
		"  Stream #0:0: Video: h264 (High), yuv420p(progressive), 1920x1080, 25 fps\n" +
		"  Stream #0:1: Audio: aac (LC), 44100 Hz, stereo, fltp\n"
	info := parseFFmpegHeader(header)
	if info.Width != 1920 || info.Height != 1080 {
		t.Errorf("size = %dx%d", info.Width, info.Height)
	}
}
