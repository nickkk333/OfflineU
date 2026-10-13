package offlineu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommonVideoFormatsAreSupported pins down the eight video containers the
// product promises (picker, README): each one must scan as a video lesson with
// a browser-servable MIME, regardless of filename case.
func TestCommonVideoFormatsAreSupported(t *testing.T) {
	cases := map[string]string{
		".mp4":  "video/mp4",
		".mkv":  "video/x-matroska",
		".avi":  "video/x-msvideo",
		".mov":  "video/quicktime",
		".webm": "video/webm",
		".m4v":  "video/mp4",
		".flv":  "video/x-flv",
		".wmv":  "video/x-ms-wmv",
	}
	for extension, wantMime := range cases {
		if !extIn(extension, videoExtensions) {
			t.Errorf("%s is not in videoExtensions", extension)
		}
		if got := GuessMime("lesson"+extension, "application/octet-stream"); got != wantMime {
			t.Errorf("%s: mime = %q, want %q", extension, got, wantMime)
		}
	}

	// End to end through the scanner, including an UPPER-CASE name: the parser
	// lower-cases the extension before matching.
	dir := t.TempDir()
	cfg := Config{}
	for extension, wantMime := range cases {
		name := "01 - Clip" + extension
		if extension == ".mkv" {
			name = "02 - CLIP.MKV"
		}
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte("fake video"), 0o644); err != nil {
			t.Fatal(err)
		}
		lesson := createLessonFromFile(full, dir)
		if lesson == nil {
			t.Fatalf("%s: file was not recognised as a lesson", name)
		}
		if lesson.LessonType != "video" {
			t.Errorf("%s: lesson type = %q, want video", name, lesson.LessonType)
		}
		if lesson.VideoFile == "" || !lesson.HasMedia() {
			t.Errorf("%s: video file was not attached: %+v", name, lesson)
		}
		if lesson.VideoMime != wantMime {
			t.Errorf("%s: video mime = %q, want %q", name, lesson.VideoMime, wantMime)
		}
		if !strings.HasPrefix(lesson.VideoSrc(), "/files/") {
			t.Errorf("%s: video src = %q", name, lesson.VideoSrc())
		}
		if err := os.Remove(full); err != nil {
			t.Fatal(err)
		}
	}
	_ = cfg
}
