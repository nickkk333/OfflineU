package offlineu

import (
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// NormalizeRel turns a path into the forward-slash, course relative form used
// everywhere (progress keys, lesson URLs, API payloads).
func NormalizeRel(value string) string {
	return strings.Trim(strings.ReplaceAll(value, "\\", "/"), "/")
}

var slugPattern = regexp.MustCompile(`[^0-9A-Za-z._-]+`)

// TitleSlug is the URL friendly representation of a lesson title.
func TitleSlug(title string) string {
	slug := strings.Trim(slugPattern.ReplaceAllString(title, "_"), "._")
	if slug == "" {
		return "lesson"
	}
	return slug
}

// GuessMime resolves a MIME type from the file extension, so the result never
// depends on the operating system registry.
func GuessMime(filePath string, fallback string) string {
	extension := strings.ToLower(filepath.Ext(filePath))
	if override, ok := mimeOverrides[extension]; ok {
		return override
	}
	if guessed := mime.TypeByExtension(extension); guessed != "" {
		return guessed
	}
	return fallback
}

// countMediaFiles counts video/audio files below root, giving up early on huge
// trees so the directory browser stays fast. The error is only reported when the
// root itself cannot be read (a nested, unreadable folder is skipped).
func countMediaFiles(root string, limit int) (int, error) {
	initial, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	media := 0
	seen := 0
	stack := [][]os.DirEntry{initial}
	dirs := []string{root}
	for len(stack) > 0 {
		level := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		current := dirs[len(dirs)-1]
		dirs = dirs[:len(dirs)-1]
		for _, entry := range level {
			seen++
			if seen > limit {
				return media, nil
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if entry.IsDir() {
				if entry.Type()&fs.ModeSymlink != 0 {
					continue
				}
				child := filepath.Join(current, entry.Name())
				children, err := os.ReadDir(child)
				if err != nil {
					continue
				}
				stack = append(stack, children)
				dirs = append(dirs, child)
				continue
			}
			extension := strings.ToLower(filepath.Ext(entry.Name()))
			if extIn(extension, videoExtensions) || extIn(extension, audioExtensions) {
				media++
			}
		}
	}
	return media, nil
}

// rootHasContent reports whether a folder holds anything OfflineU could present:
// any non-hidden file, at any depth, bounded by limit. It tells "nothing was
// mounted" (an empty folder) apart from a course that just has few files.
func rootHasContent(root string, limit int) bool {
	initial, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	seen := 0
	stack := [][]os.DirEntry{initial}
	dirs := []string{root}
	for len(stack) > 0 {
		level := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		current := dirs[len(dirs)-1]
		dirs = dirs[:len(dirs)-1]
		for _, entry := range level {
			seen++
			// Lots of entries: the folder is clearly not empty, stop walking.
			if seen > limit {
				return true
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if entry.IsDir() {
				if entry.Type()&fs.ModeSymlink != 0 {
					continue
				}
				child := filepath.Join(current, entry.Name())
				children, err := os.ReadDir(child)
				if err != nil {
					continue
				}
				stack = append(stack, children)
				dirs = append(dirs, child)
				continue
			}
			return true
		}
	}
	return false
}

// sortedEntries lists a directory with folders first and everything sorted
// case-insensitively and naturally, so "第2集" comes before "第10集".
func sortedEntries(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		leftDir := entries[i].IsDir()
		rightDir := entries[j].IsDir()
		if leftDir != rightDir {
			return leftDir
		}
		return compareNames(entries[i].Name(), entries[j].Name()) < 0
	})
	return entries, nil
}

// compareNames orders two names the way a reader expects: case insensitively,
// with every run of digits compared as a number. "第2集" therefore sorts before
// "第10集" and "Section 2" before "Section 10". Digit runs of equal value (for
// example "1" and "01") compare equal, so the stable sort keeps the order the
// file system reported.
func compareNames(left, right string) int {
	lowerLeft := []rune(strings.ToLower(left))
	lowerRight := []rune(strings.ToLower(right))
	i, j := 0, 0
	for i < len(lowerLeft) && j < len(lowerRight) {
		if isDigitRune(lowerLeft[i]) && isDigitRune(lowerRight[j]) {
			leftRun, nextLeft := digitRun(lowerLeft, i)
			rightRun, nextRight := digitRun(lowerRight, j)
			if order := compareDigitRuns(leftRun, rightRun); order != 0 {
				return order
			}
			i, j = nextLeft, nextRight
			continue
		}
		if lowerLeft[i] != lowerRight[j] {
			if lowerLeft[i] < lowerRight[j] {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch {
	case i < len(lowerLeft):
		return 1
	case j < len(lowerRight):
		return -1
	default:
		return 0
	}
}

// digitRun reads the consecutive digits starting at start.
func digitRun(runes []rune, start int) (string, int) {
	end := start
	for end < len(runes) && isDigitRune(runes[end]) {
		end++
	}
	return string(runes[start:end]), end
}

// compareDigitRuns compares two runs of digits by their numeric value, so the
// runs are not limited by the size of an integer (a course may be named
// "第20240101集").
func compareDigitRuns(left, right string) int {
	trimmedLeft := strings.TrimLeft(left, "0")
	trimmedRight := strings.TrimLeft(right, "0")
	if len(trimmedLeft) != len(trimmedRight) {
		if len(trimmedLeft) < len(trimmedRight) {
			return -1
		}
		return 1
	}
	switch {
	case trimmedLeft < trimmedRight:
		return -1
	case trimmedLeft > trimmedRight:
		return 1
	default:
		return 0
	}
}

func isDigitRune(value rune) bool { return value >= '0' && value <= '9' }

// CleanLessonName turns a file name into a human readable title:
// "01 - Intro" becomes "Intro".
func CleanLessonName(name string) string {
	name = regexp.MustCompile(`^\d+[.\-_\s]*`).ReplaceAllString(name, "")
	name = regexp.MustCompile(`[-_]+`).ReplaceAllString(name, " ")
	words := strings.Fields(name)
	for index, word := range words {
		words[index] = capitalize(word)
	}
	title := strings.Join(words, " ")
	if strings.TrimSpace(title) == "" {
		return "Untitled Lesson"
	}
	return title
}

// capitalize mirrors Python's str.capitalize: first rune upper case, the rest
// lower case, for every word of the title.
func capitalize(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return word
	}
	return strings.ToUpper(string(runes[0])) + strings.ToLower(string(runes[1:]))
}

// SafeJoin resolves name inside dir without escaping it (used for static files).
func SafeJoin(dir, name string) (string, bool) {
	clean := path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))
	if strings.Contains(clean, "..") {
		return "", false
	}
	return filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(clean, "/"))), true
}
