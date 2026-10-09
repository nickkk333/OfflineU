package offlineu

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ScanCourse parses a directory into a Course (tree + lessons + progress file).
func (c *Config) ScanCourse(coursePath string) (*Course, error) {
	base, err := filepath.Abs(ExpandHome(coursePath))
	if err != nil {
		return nil, fmt.Errorf("invalid course path: %s (%v)", coursePath, err)
	}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("invalid course path: %s", coursePath)
	}

	root := c.buildDirectoryTree(base, base, 0)
	return &Course{
		Name:         filepath.Base(base),
		Path:         base,
		Root:         root,
		ProgressFile: c.ProgressFileFor(base),
	}, nil
}

func (c *Config) buildDirectoryTree(coursePath, currentPath string, depth int) *DirectoryNode {
	name := filepath.Base(currentPath)
	if filepath.Clean(currentPath) == filepath.Clean(coursePath) {
		name = "Course Root"
	}
	node := &DirectoryNode{Name: name, Path: currentPath, Children: []*DirectoryNode{}, Lessons: []*Lesson{}}
	if depth > MaxScanDepth {
		return node
	}

	entries, err := sortedEntries(currentPath)
	if err != nil {
		return node
	}

	var subtitles []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		full := filepath.Join(currentPath, entry.Name())
		if entry.IsDir() {
			child := c.buildDirectoryTree(coursePath, full, depth+1)
			if child.HasContent || len(child.Children) > 0 || len(child.Lessons) > 0 {
				node.Children = append(node.Children, child)
				node.HasContent = true
			}
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extIn(extension, subtitleExtensions) {
			subtitles = append(subtitles, full)
			continue
		}
		if lesson := createLessonFromFile(full, coursePath); lesson != nil {
			node.Lessons = append(node.Lessons, lesson)
			node.HasContent = true
		}
	}

	attachSubtitles(node.Lessons, subtitles, coursePath)
	return node
}

// attachSubtitles hands subtitle files over to the media file they belong to.
// A subtitle matches when the file stems are equal; a folder with exactly one
// media file gets its subtitles attached even without a name match.
func attachSubtitles(lessons []*Lesson, subtitles []string, coursePath string) {
	var mediaLessons []*Lesson
	for _, lesson := range lessons {
		if lesson.HasMedia() {
			mediaLessons = append(mediaLessons, lesson)
		}
	}
	if len(mediaLessons) == 0 {
		return
	}
	for _, subtitle := range subtitles {
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(subtitle), filepath.Ext(subtitle)))
		var target *Lesson
		for _, lesson := range mediaLessons {
			lessonStem := strings.ToLower(strings.TrimSuffix(filepath.Base(lesson.Path), filepath.Ext(lesson.Path)))
			if lessonStem == stem {
				target = lesson
				break
			}
		}
		if target == nil && len(mediaLessons) == 1 {
			target = mediaLessons[0]
		}
		if target == nil || target.SubtitleFile != "" {
			continue
		}
		if relative, err := filepath.Rel(coursePath, subtitle); err == nil {
			target.SubtitleFile = NormalizeRel(relative)
		}
	}
}

// createLessonFromFile builds a lesson from a single file, or nil when the file
// is not course content.
func createLessonFromFile(filePath, coursePath string) *Lesson {
	extension := strings.ToLower(filepath.Ext(filePath))
	if extIn(extension, skipExtensions) {
		return nil
	}
	relative, err := filepath.Rel(coursePath, filePath)
	if err != nil {
		return nil
	}
	relative = NormalizeRel(relative)
	title := CleanLessonName(strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath)))

	lesson := &Lesson{
		Title:      title,
		Path:       filePath,
		RelPath:    relative,
		URL:        relative + "/" + TitleSlug(title),
		LessonType: "text",
		TextFiles:  []string{},
	}

	switch {
	case extIn(extension, videoExtensions) && looksLikeVideo(filePath, extension):
		lesson.LessonType = "video"
		lesson.VideoFile = relative
		lesson.VideoMime = GuessMime(filePath, "video/mp4")
	case extIn(extension, audioExtensions):
		lesson.LessonType = "audio"
		lesson.AudioFile = relative
		lesson.AudioMime = GuessMime(filePath, "audio/mpeg")
	case extIn(extension, textExtensions):
		lesson.TextFiles = append(lesson.TextFiles, relative)
		lowered := strings.ToLower(filepath.Base(filePath))
		for _, indicator := range quizIndicators {
			if strings.Contains(lowered, indicator) {
				lesson.LessonType = "quiz"
				break
			}
		}
	default:
		return nil // unsupported file type
	}
	return lesson
}

// looksLikeVideo is the last check before a file becomes a video lesson. Most
// extensions are trusted as they are; the ones a video shares with other content
// are checked against the file itself.
func looksLikeVideo(path, extension string) bool {
	if !extIn(extension, ambiguousVideoExtensions) {
		return true
	}
	return looksLikeMPEGTS(path)
}

// Packet sizes a transport stream comes in: plain TS uses 188-byte packets, a
// Blu-ray style .m2ts prefixes each of them with a 4-byte timestamp (192).
var tsPacketSizes = []int{188, 192}

// tsSyncPackets is how many consecutive sync bytes have to line up before a file
// is believed to be a transport stream. Three in a row cannot happen by chance,
// and it is cheap to check.
const tsSyncPackets = 3

// looksLikeMPEGTS reports whether a file is an MPEG transport stream, which is
// what ".ts" means in a course folder. The extension is ambiguous - it is a
// TypeScript source file just as often - so the parser looks at the packets
// instead of trusting the name.
//
// The sync byte is searched for rather than assumed to sit at offset 0: course
// platforms regularly put their own header in front of the stream (this very
// file carries 188 bytes of one), so what identifies a transport stream is the
// 0x47 repeating at an exact packet interval.
func looksLikeMPEGTS(path string) bool {
	handle, err := os.Open(path)
	if err != nil {
		return false
	}
	defer handle.Close()

	window := 0
	for _, size := range tsPacketSizes {
		if size*tsSyncPackets > window {
			window = size * tsSyncPackets
		}
	}
	// A few packets are enough; a whole file need never be read during a scan.
	buffer := make([]byte, window+1024)
	read, _ := io.ReadFull(handle, buffer)
	if read <= 0 {
		return false
	}

	for _, size := range tsPacketSizes {
		needed := size * tsSyncPackets
		if read < needed {
			continue
		}
		for offset := 0; offset+needed <= read; offset++ {
			if buffer[offset] != tsSyncByte {
				continue
			}
			aligned := true
			for packet := 1; packet < tsSyncPackets; packet++ {
				if buffer[offset+packet*size] != tsSyncByte {
					aligned = false
					break
				}
			}
			if aligned {
				return true
			}
		}
	}
	return false
}

// AllLessons flattens the tree into the order lessons are shown in.
func AllLessons(node *DirectoryNode) []*Lesson {
	lessons := []*Lesson{}
	var collect func(current *DirectoryNode)
	collect = func(current *DirectoryNode) {
		lessons = append(lessons, current.Lessons...)
		for _, child := range current.Children {
			collect(child)
		}
	}
	collect(node)
	return lessons
}

// FindLessonInTree resolves a lesson from its url / relative path (including
// the legacy "<path>/<Title_With_Underscores>" format).
func FindLessonInTree(node *DirectoryNode, target string) *Lesson {
	target = NormalizeRel(target)
	if target == "" {
		return nil
	}
	for _, lesson := range node.Lessons {
		legacy := lesson.RelPath + "/" + strings.ReplaceAll(lesson.Title, " ", "_")
		if target == lesson.URL || target == lesson.RelPath || target == lesson.Path || target == legacy {
			return lesson
		}
	}
	for _, child := range node.Children {
		if found := FindLessonInTree(child, target); found != nil {
			return found
		}
	}
	return nil
}

// BuildTextResources describes how every document of a lesson is rendered.
func BuildTextResources(lesson *Lesson) []TextResource {
	resources := []TextResource{}
	for _, fileName := range lesson.TextFiles {
		extension := strings.ToLower(filepath.Ext(fileName))
		mode := "download"
		switch {
		case extIn(extension, inlineFrameExtensions):
			mode = "iframe" // html and pdf can be embedded by the browser
		case extIn(extension, plainTextExtensions):
			mode = "text" // fetched and shown as plain text
		}
		resources = append(resources, TextResource{
			Name: filepath.Base(fileName),
			URL:  fileName,
			Src:  "/files/" + EscapeURLPath(fileName),
			Mode: mode,
		})
	}
	return resources
}

// CompletionStats counts the lessons and the completed lessons of a subtree.
func CompletionStats(node *DirectoryNode) Stats {
	stats := Stats{}
	var count func(current *DirectoryNode)
	count = func(current *DirectoryNode) {
		for _, lesson := range current.Lessons {
			stats.TotalLessons++
			if lesson.Completed {
				stats.CompletedLessons++
			}
		}
		for _, child := range current.Children {
			count(child)
		}
	}
	count(node)
	if stats.TotalLessons > 0 {
		stats.CompletionPercentage = round1(float64(stats.CompletedLessons) / float64(stats.TotalLessons) * 100)
	}
	return stats
}

// AttachNodeStats fills the per-folder Stats of the whole tree so the UI can
// show a progress bar for every section.
func AttachNodeStats(node *DirectoryNode) {
	for _, child := range node.Children {
		AttachNodeStats(child)
	}
	node.Stats = CompletionStats(node)
}

func round1(value float64) float64 {
	return float64(int(value*10+0.5)) / 10
}
