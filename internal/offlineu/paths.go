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
// case-insensitively, mirroring the Python implementation.
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
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	return entries, nil
}

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
