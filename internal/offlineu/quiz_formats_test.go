package offlineu

import (
	"os"
	"path/filepath"
	"testing"
)

// TestQuizDocumentsAreDetectedByName pins down the quiz rule the product
// promises: any document whose filename contains "quiz", "exam" or "test"
// becomes a quiz lesson - regardless of filename case or document format.
func TestQuizDocumentsAreDetectedByName(t *testing.T) {
	for _, indicator := range []string{"quiz", "exam", "test"} {
		found := false
		for _, candidate := range quizIndicators {
			if candidate == indicator {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q is not in quizIndicators", indicator)
		}
	}

	// End to end through the scanner: every document format keeps its render
	// mode, only the lesson type flips to quiz.
	cases := []struct {
		name     string
		wantType string
		wantMode string
	}{
		{"01 - Quiz.txt", "quiz", "text"},
		{"02 - Final EXAM.md", "quiz", "markdown"},
		{"03 - Test.html", "quiz", "iframe"},
		{"04 - quiz notes.pdf", "quiz", "iframe"},
		{"05 - Exam Guide.docx", "quiz", "office"},
		{"06 - QUIZ.doc", "quiz", "office"},
		{"07 - pretest.rtf", "quiz", "office"},
		{"08 - Notes.txt", "text", "text"},
		{"09 - Contest.md", "quiz", "markdown"},
	}
	dir := t.TempDir()
	for _, testCase := range cases {
		full := filepath.Join(dir, testCase.name)
		if err := os.WriteFile(full, []byte("fake document"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, testCase := range cases {
		full := filepath.Join(dir, testCase.name)
		lesson := createLessonFromFile(full, dir)
		if lesson == nil {
			t.Fatalf("%s: file was not recognised as a lesson", testCase.name)
		}
		if lesson.LessonType != testCase.wantType {
			t.Errorf("%s: lesson type = %q, want %q", testCase.name, lesson.LessonType, testCase.wantType)
		}
		if len(lesson.TextFiles) != 1 {
			t.Fatalf("%s: text files = %v, want one entry", testCase.name, lesson.TextFiles)
		}
		resources := BuildTextResources(lesson)
		if len(resources) != 1 {
			t.Fatalf("%s: resources = %+v, want one entry", testCase.name, resources)
		}
		if resources[0].Mode != testCase.wantMode {
			t.Errorf("%s: mode = %q, want %q (quiz only flips the type)", testCase.name, resources[0].Mode, testCase.wantMode)
		}
	}
}
