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
