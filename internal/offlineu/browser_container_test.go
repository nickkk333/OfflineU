package offlineu

import "testing"

// TestBrowserFriendlyContainer pins down which real containers a browser's
// <video>/<audio> element can play directly. The point is that MPEG-TS, MKV,
// AVI, FLV and WMV are absent even though their codecs (H.264/AAC) are fine -
// exactly the "extension lies about the content" case this code adapts to.
func TestBrowserFriendlyContainer(t *testing.T) {
	friendly := []string{"mov,mp4,m4a,3gp,3g2,mj2", "mp4", "quicktime", "webm", "ogg"}
	for _, container := range friendly {
		if !browserFriendlyContainer(container) {
			t.Errorf("%q should be browser-friendly", container)
		}
	}
	unfriendly := []string{"mpegts", "matroska,webm", "avi", "flv", "wmv", ""}
	for _, container := range unfriendly {
		if browserFriendlyContainer(container) {
			t.Errorf("%q should NOT be browser-friendly", container)
		}
	}
}

// TestContainerMime maps a probed container to the MIME the browser expects,
// so a file whose extension mislabels it still gets the right Content-Type.
func TestContainerMime(t *testing.T) {
	cases := map[string]string{
		"mov,mp4,m4a,3gp,3g2,mj2": "video/mp4",
		"webm":                    "video/webm",
		"ogg":                     "video/ogg",
		"mpegts":                  "",
		"matroska,webm":           "",
	}
	for format, want := range cases {
		if got := containerMime(format); got != want {
			t.Errorf("containerMime(%q) = %q, want %q", format, got, want)
		}
	}
}

// The container alone cannot decide: an .mp4 holding HEVC looks fine and still
// fails to decode, and a WebM (which ffprobe reports as "matroska,webm") holding
// VP9 is playable even though the container name says otherwise.
func TestBrowserPlayableNeedsContainerAndStreams(t *testing.T) {
	cases := []struct {
		container string
		info      MediaInfo
		want      string
	}{
		{"mov,mp4,m4a,3gp,3g2,mj2", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, "video/mp4"},
		{"mp4", MediaInfo{VideoCodec: "h264", AudioCodec: "mp3"}, "video/mp4"},
		// The case that used to be served untouched and simply did not play.
		{"mp4", MediaInfo{VideoCodec: "hevc", AudioCodec: "aac"}, ""},
		{"mp4", MediaInfo{VideoCodec: "h264", AudioCodec: "ac3"}, ""},
		// WebM and Matroska share one container name: their streams tell them apart.
		{"matroska,webm", MediaInfo{VideoCodec: "vp9", AudioCodec: "opus"}, "video/webm"},
		{"matroska,webm", MediaInfo{VideoCodec: "vp8", AudioCodec: "vorbis"}, "video/webm"},
		{"matroska,webm", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, ""},
		{"ogg", MediaInfo{VideoCodec: "theora", AudioCodec: "vorbis"}, "video/ogg"},
		// Containers no browser plays at all.
		{"mpegts", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, ""},
		{"avi", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, ""},
		// A file whose streams could not be read is not handed over either.
		{"mp4", MediaInfo{}, ""},
	}
	for _, testCase := range cases {
		if got := browserPlayable(testCase.container, testCase.info); got != testCase.want {
			t.Errorf("browserPlayable(%q, %+v) = %q, want %q", testCase.container, testCase.info, got, testCase.want)
		}
	}
}
