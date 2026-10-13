package offlineu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommonDocumentFormatsAreSupported pins down the seven document formats
// the product promises: .txt shows as plain text, .md is rendered client
// side, .html/.pdf embed in an iframe, and .docx/.doc/.rtf convert to HTML
// through /api/preview - regardless of filename case.
func TestCommonDocumentFormatsAreSupported(t *testing.T) {
	cases := map[string]struct {
		mime string
		mode string
	}{
		".txt":  {"text/plain; charset=utf-8", "text"},
		".md":   {"text/markdown; charset=utf-8", "markdown"},
		".html": {"text/html; charset=utf-8", "iframe"},
		".pdf":  {"application/pdf", "iframe"},
		".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "office"},
		".doc":  {"application/msword", "office"},
		".rtf":  {"application/rtf", "office"},
	}
	for extension, want := range cases {
		if !extIn(extension, textExtensions) {
			t.Errorf("%s is not in textExtensions", extension)
		}
		if got := GuessMime("lesson"+extension, "application/octet-stream"); got != want.mime {
			t.Errorf("%s: mime = %q, want %q", extension, got, want.mime)
		}
	}

	// End to end through the scanner, including an UPPER-CASE name: the parser
	// lower-cases the extension before matching.
	dir := t.TempDir()
	for extension, want := range cases {
		name := "01 - Notes" + extension
		if extension == ".docx" {
			name = "02 - GUIDE.DOCX"
		}
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte("fake document"), 0o644); err != nil {
			t.Fatal(err)
		}
		lesson := createLessonFromFile(full, dir)
		if lesson == nil {
			t.Fatalf("%s: file was not recognised as a lesson", name)
		}
		if lesson.LessonType != "text" {
			t.Errorf("%s: lesson type = %q, want text", name, lesson.LessonType)
		}
		if len(lesson.TextFiles) != 1 {
			t.Fatalf("%s: text files = %v, want one entry", name, lesson.TextFiles)
		}
		resources := BuildTextResources(lesson)
		if len(resources) != 1 {
			t.Fatalf("%s: resources = %+v, want one entry", name, resources)
		}
		if resources[0].Mode != want.mode {
			t.Errorf("%s: mode = %q, want %q", name, resources[0].Mode, want.mode)
		}
		if !strings.HasPrefix(resources[0].Src, "/files/") {
			t.Errorf("%s: resource src = %q", name, resources[0].Src)
		}
		if err := os.Remove(full); err != nil {
			t.Fatal(err)
		}
	}
}
