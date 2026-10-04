package offlineu

import (
	"encoding/json"
	"net/url"
	"path"
	"strings"
)

// Constants that tune scanning, storage and the file types OfflineU knows.
const (
	MaxScanDepth        = 10
	MaxMediaScanEntries = 5000
	MaxRecentCourses    = 20
	ProgressFilename    = ".offlineu_progress.json"
	StateFilename       = "offlineu_state.json"
	Version             = "2.0.0"
)

var videoExtensions = []string{".mp4", ".mkv", ".avi", ".mov", ".webm", ".m4v", ".flv", ".wmv"}
var audioExtensions = []string{".mp3", ".wav", ".m4a", ".aac", ".ogg", ".flac"}
var subtitleExtensions = []string{".srt", ".vtt", ".ass", ".sub", ".sbv"}
var textExtensions = []string{".txt", ".md", ".html", ".htm", ".pdf", ".docx", ".doc", ".rtf"}
var plainTextExtensions = []string{".txt", ".md"}
var inlineFrameExtensions = []string{".html", ".htm", ".pdf"}
var skipExtensions = []string{".log", ".tmp", ".bak", ".swp"}
var quizIndicators = []string{"quiz", "exam", "test", "assessment", "exercise", "assignment", "homework"}

var mimeOverrides = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".flv":  "video/x-flv",
	".wmv":  "video/x-ms-wmv",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".flac": "audio/flac",
}

func extIn(extension string, set []string) bool {
	for _, candidate := range set {
		if extension == candidate {
			return true
		}
	}
	return false
}

// Lesson is one physical file of a course (one file == one lesson).
type Lesson struct {
	Title           string
	Path            string // absolute path on disk
	RelPath         string // course relative path (stable progress key)
	URL             string // value used in /lesson/<url>
	LessonType      string // 'video', 'audio', 'text' or 'quiz'
	VideoFile       string
	VideoMime       string
	AudioFile       string
	AudioMime       string
	SubtitleFile    string
	TextFiles       []string
	Completed       bool
	LastAccessed    string
	ProgressSeconds int
}

// VideoSrc is the URL the browser loads the video from ("" when there is none).
func (l *Lesson) VideoSrc() string { return fileURL("/files/", l.VideoFile) }

// AudioSrc is the URL the browser loads the audio from ("" when there is none).
func (l *Lesson) AudioSrc() string { return fileURL("/files/", l.AudioFile) }

// SubtitleSrc is the WebVTT URL of the attached subtitle track ("" when none).
func (l *Lesson) SubtitleSrc() string { return fileURL("/subtitles/", l.SubtitleFile) }

// UnmarshalJSON reads back the shape produced by MarshalJSON (used by tests and
// by anything that persists lesson metadata).
func (l *Lesson) UnmarshalJSON(data []byte) error {
	var raw struct {
		Title           string   `json:"title"`
		Path            string   `json:"path"`
		RelPath         string   `json:"rel_path"`
		URL             string   `json:"url"`
		LessonType      string   `json:"lesson_type"`
		VideoFile       string   `json:"video_file"`
		VideoMime       string   `json:"video_mime"`
		AudioFile       string   `json:"audio_file"`
		AudioMime       string   `json:"audio_mime"`
		SubtitleFile    string   `json:"subtitle_file"`
		TextFiles       []string `json:"text_files"`
		Completed       bool     `json:"completed"`
		ProgressSeconds int      `json:"progress_seconds"`
		LastAccessed    string   `json:"last_accessed"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	l.Title = raw.Title
	l.Path = raw.Path
	l.RelPath = raw.RelPath
	l.URL = raw.URL
	l.LessonType = raw.LessonType
	l.VideoFile = raw.VideoFile
	l.VideoMime = raw.VideoMime
	l.AudioFile = raw.AudioFile
	l.AudioMime = raw.AudioMime
	l.SubtitleFile = raw.SubtitleFile
	l.TextFiles = raw.TextFiles
	l.Completed = raw.Completed
	l.ProgressSeconds = raw.ProgressSeconds
	l.LastAccessed = raw.LastAccessed
	return nil
}

// MarshalJSON emits the snake_case shape the previous Python API used, plus the
// three computed source URLs so the frontend never has to rebuild them.
func (l *Lesson) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Title           string   `json:"title"`
		Path            string   `json:"path"`
		RelPath         string   `json:"rel_path"`
		URL             string   `json:"url"`
		LessonType      string   `json:"lesson_type"`
		VideoFile       string   `json:"video_file,omitempty"`
		VideoMime       string   `json:"video_mime,omitempty"`
		VideoSrc        string   `json:"video_src,omitempty"`
		AudioFile       string   `json:"audio_file,omitempty"`
		AudioMime       string   `json:"audio_mime,omitempty"`
		AudioSrc        string   `json:"audio_src,omitempty"`
		SubtitleFile    string   `json:"subtitle_file,omitempty"`
		SubtitleSrc     string   `json:"subtitle_src,omitempty"`
		TextFiles       []string `json:"text_files"`
		Completed       bool     `json:"completed"`
		ProgressSeconds int      `json:"progress_seconds"`
		LastAccessed    string   `json:"last_accessed,omitempty"`
	}{
		Title:           l.Title,
		Path:            l.Path,
		RelPath:         l.RelPath,
		URL:             l.URL,
		LessonType:      l.LessonType,
		VideoFile:       l.VideoFile,
		VideoMime:       l.VideoMime,
		VideoSrc:        l.VideoSrc(),
		AudioFile:       l.AudioFile,
		AudioMime:       l.AudioMime,
		AudioSrc:        l.AudioSrc(),
		SubtitleFile:    l.SubtitleFile,
		SubtitleSrc:     l.SubtitleSrc(),
		TextFiles:       append([]string{}, l.TextFiles...),
		Completed:       l.Completed,
		ProgressSeconds: l.ProgressSeconds,
		LastAccessed:    l.LastAccessed,
	})
}

// DirectoryNode is one directory of the course tree.
type DirectoryNode struct {
	Name       string
	Path       string
	Children   []*DirectoryNode
	Lessons    []*Lesson
	HasContent bool
	Stats      Stats // completion summary of this subtree
}

// MarshalJSON keeps the JSON keys aligned with the Python dataclass.
func (n *DirectoryNode) MarshalJSON() ([]byte, error) {
	children := n.Children
	if children == nil {
		children = []*DirectoryNode{}
	}
	lessons := n.Lessons
	if lessons == nil {
		lessons = []*Lesson{}
	}
	return json.Marshal(struct {
		Name       string           `json:"name"`
		Path       string           `json:"path"`
		Type       string           `json:"type"`
		Children   []*DirectoryNode `json:"children"`
		Lessons    []*Lesson        `json:"lessons"`
		HasContent bool             `json:"has_content"`
		Stats      Stats            `json:"stats"`
	}{
		Name:       n.Name,
		Path:       n.Path,
		Type:       "directory",
		Children:   children,
		Lessons:    lessons,
		HasContent: n.HasContent,
		Stats:      n.Stats,
	})
}

// Stats is a completion summary, either for the whole course or one folder.
type Stats struct {
	TotalLessons         int     `json:"total_lessons"`
	CompletedLessons     int     `json:"completed_lessons"`
	CompletionPercentage float64 `json:"completion_percentage"`
	LastAccessedPath     string  `json:"last_accessed_path,omitempty"`
	LastAccessedURL      string  `json:"last_accessed_url,omitempty"`
	LastAccessedTitle    string  `json:"last_accessed_title,omitempty"`
}

// Course is a parsed course folder.
type Course struct {
	Name              string
	Path              string
	Root              *DirectoryNode
	ProgressFile      string
	LastAccessedPath  string
	LastAccessedURL   string
	LastAccessedTitle string
}

// TextResource describes how one document of a lesson has to be rendered.
type TextResource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Src  string `json:"src"`
	Mode string `json:"mode"` // 'text', 'iframe' or 'download'
}

func fileURL(prefix, relative string) string {
	if relative == "" {
		return ""
	}
	return prefix + EscapeURLPath(relative)
}

// EscapeURLPath percent-encodes every segment of a slash separated path.
func EscapeURLPath(relative string) string {
	segments := strings.Split(relative, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// BaseName returns the file name of a slash separated path.
func BaseName(relative string) string {
	return path.Base(relative)
}

// HasMedia reports whether the lesson can be played by the browser.
func (l *Lesson) HasMedia() bool { return l.VideoFile != "" || l.AudioFile != "" }
