package offlineu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommonAudioFormatsAreSupported pins down the six audio containers the
// product promises (picker, README): each one must scan as an audio lesson
// with a browser-servable MIME, regardless of filename case.
func TestCommonAudioFormatsAreSupported(t *testing.T) {
	cases := map[string]string{
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".m4a":  "audio/mp4",
		".aac":  "audio/aac",
		".ogg":  "audio/ogg",
		".flac": "audio/flac",
	}
	for extension, wantMime := range cases {
		if !extIn(extension, audioExtensions) {
			t.Errorf("%s is not in audioExtensions", extension)
		}
		if got := GuessMime("lesson"+extension, "application/octet-stream"); got != wantMime {
			t.Errorf("%s: mime = %q, want %q", extension, got, wantMime)
		}
	}

	// End to end through the scanner, including an UPPER-CASE name: the parser
	// lower-cases the extension before matching.
	dir := t.TempDir()
	for extension, wantMime := range cases {
		name := "01 - Track" + extension
		if extension == ".flac" {
			name = "02 - TRACK.FLAC"
		}
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte("fake audio"), 0o644); err != nil {
			t.Fatal(err)
		}
		lesson := createLessonFromFile(full, dir)
		if lesson == nil {
			t.Fatalf("%s: file was not recognised as a lesson", name)
		}
		if lesson.LessonType != "audio" {
			t.Errorf("%s: lesson type = %q, want audio", name, lesson.LessonType)
		}
		if lesson.AudioFile == "" || !lesson.HasMedia() {
			t.Errorf("%s: audio file was not attached: %+v", name, lesson)
		}
		if lesson.AudioMime != wantMime {
			t.Errorf("%s: audio mime = %q, want %q", name, lesson.AudioMime, wantMime)
		}
		if !strings.HasPrefix(lesson.AudioSrc(), "/files/") {
			t.Errorf("%s: audio src = %q", name, lesson.AudioSrc())
		}
		if err := os.Remove(full); err != nil {
			t.Fatal(err)
		}
	}
}
