package offlineu

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProgressEntry is the stored state of a single lesson.
type ProgressEntry struct {
	Completed       *bool  `json:"completed,omitempty"`
	ProgressSeconds *int   `json:"progress_seconds,omitempty"`
	LastAccessed    string `json:"last_accessed,omitempty"`
}

// progressFile is the flat JSON document: one key per lesson plus a
// "last_accessed_path" marker describing where to resume.
type progressFile struct {
	Entries          map[string]*ProgressEntry
	LastAccessedPath string
}

func newProgressFile() *progressFile {
	return &progressFile{Entries: map[string]*ProgressEntry{}}
}

// UnmarshalJSON reads the flat on-disk format.
func (p *progressFile) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Entries = map[string]*ProgressEntry{}
	for key, value := range raw {
		if key == "last_accessed_path" {
			var path string
			if err := json.Unmarshal(value, &path); err == nil {
				p.LastAccessedPath = path
			}
			continue
		}
		entry := &ProgressEntry{}
		if err := json.Unmarshal(value, entry); err == nil {
			p.Entries[key] = entry
		}
	}
	return nil
}

// MarshalJSON writes the flat on-disk format (entries + last_accessed_path).
func (p *progressFile) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for key, entry := range p.Entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		out[key] = encoded
	}
	if p.LastAccessedPath != "" {
		encoded, err := json.Marshal(p.LastAccessedPath)
		if err != nil {
			return nil, err
		}
		out["last_accessed_path"] = encoded
	}
	return json.MarshalIndent(out, "", "  ")
}

// ProgressStorageError is returned when progress cannot be persisted (e.g. a
// read-only course folder).
type ProgressStorageError struct {
	Message string
}

func (e *ProgressStorageError) Error() string { return e.Message }

// ProgressTracker reads and writes lesson progress. Every write is locked and
// atomic, so an interrupted save can never truncate the file.
type ProgressTracker struct {
	mu sync.RWMutex
}

// Load returns the stored progress of a course. A missing or broken file simply
// yields an empty document.
func (t *ProgressTracker) Load(course *Course) *progressFile {
	t.mu.RLock()
	defer t.mu.RUnlock()
	data, err := os.ReadFile(course.ProgressFile)
	if err != nil {
		return newProgressFile()
	}
	parsed := newProgressFile()
	if err := json.Unmarshal(data, parsed); err != nil {
		return newProgressFile()
	}
	if parsed.Entries == nil {
		parsed.Entries = map[string]*ProgressEntry{}
	}
	return parsed
}

// Save writes the progress document atomically (temp file + rename + fsync).
func (t *ProgressTracker) Save(course *Course, progress *progressFile) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.saveLocked(course, progress)
}

func (t *ProgressTracker) saveLocked(course *Course, progress *progressFile) error {
	encoded, err := json.MarshalIndent(progress, "", "  ")
	if err != nil {
		return err
	}
	target := course.ProgressFile
	temp := target + ".tmp"
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return storageError(target, err)
	}
	handle, err := os.Create(temp)
	if err != nil {
		return storageError(target, err)
	}
	if _, err := handle.Write(encoded); err != nil {
		handle.Close()
		os.Remove(temp)
		return storageError(target, err)
	}
	if err := handle.Sync(); err != nil {
		handle.Close()
		os.Remove(temp)
		return storageError(target, err)
	}
	if err := handle.Close(); err != nil {
		os.Remove(temp)
		return storageError(target, err)
	}
	if err := os.Rename(temp, target); err != nil {
		os.Remove(temp)
		return storageError(target, err)
	}
	return nil
}

func storageError(target string, err error) error {
	return &ProgressStorageError{Message: fmt.Sprintf(
		"cannot write progress to %s: %v. "+
			"Use OFFLINEU_PROGRESS_DIR to store progress somewhere writable.", target, err)}
}

// Update partially updates one lesson entry.
//
// A nil completed / progressSeconds pointer means "keep what is stored", so
// simply opening a lesson no longer wipes its completion flag. Watched seconds
// are never allowed to move backwards.
func (t *ProgressTracker) Update(course *Course, lessonKey string, completed *bool, seconds *int) (*ProgressEntry, error) {
	lessonKey = NormalizeRel(lessonKey)
	t.mu.Lock()
	defer t.mu.Unlock()

	progress, err := t.loadLocked(course)
	if err != nil {
		return nil, err
	}
	entry, ok := progress.Entries[lessonKey]
	if !ok || entry == nil {
		entry = &ProgressEntry{}
	}
	if completed != nil {
		value := *completed
		entry.Completed = &value
	}
	if seconds != nil {
		current := 0
		if entry.ProgressSeconds != nil {
			current = *entry.ProgressSeconds
		}
		if *seconds > current {
			current = *seconds
		}
		value := current
		entry.ProgressSeconds = &value
	}
	entry.LastAccessed = time.Now().Format("2006-01-02T15:04:05")
	progress.Entries[lessonKey] = entry
	progress.LastAccessedPath = lessonKey
	if err := t.saveLocked(course, progress); err != nil {
		return nil, err
	}
	return entry, nil
}

// RecordAccess remembers that a lesson was opened without touching its values.
func (t *ProgressTracker) RecordAccess(course *Course, lessonKey string) (*ProgressEntry, error) {
	return t.Update(course, lessonKey, nil, nil)
}

// loadLocked reads the file while the write lock is already held.

// ApplyToTree pushes the stored values back onto the parsed tree and refreshes
// the "continue where you left off" information of the course.
func (t *ProgressTracker) ApplyToTree(course *Course) {
	progress := t.Load(course)
	var apply func(node *DirectoryNode)
	apply = func(node *DirectoryNode) {
		for _, lesson := range node.Lessons {
			entry := lookupEntry(progress, lesson)
			if entry == nil {
				continue
			}
			if entry.Completed != nil {
				lesson.Completed = *entry.Completed
			}
			if entry.ProgressSeconds != nil {
				lesson.ProgressSeconds = *entry.ProgressSeconds
			}
			lesson.LastAccessed = entry.LastAccessed
		}
		for _, child := range node.Children {
			apply(child)
		}
	}
	apply(course.Root)

	course.LastAccessedPath = progress.LastAccessedPath
	course.LastAccessedURL = ""
	course.LastAccessedTitle = ""
	if course.LastAccessedPath != "" {
		if lesson := FindLessonInTree(course.Root, course.LastAccessedPath); lesson != nil {
			course.LastAccessedURL = lesson.URL
			course.LastAccessedTitle = lesson.Title
		}
	}
}

func lookupEntry(progress *progressFile, lesson *Lesson) *ProgressEntry {
	keys := []string{
		lesson.RelPath,
		lesson.URL,
		lesson.RelPath + "/" + strings.ReplaceAll(lesson.Title, " ", "_"),
	}
	for _, key := range keys {
		if entry, ok := progress.Entries[key]; ok && entry != nil {
			return entry
		}
	}
	return nil
}

// Stats returns the course wide completion counters plus the resume target.
func (t *ProgressTracker) Stats(course *Course) Stats {
	stats := CompletionStats(course.Root)
	stats.LastAccessedPath = course.LastAccessedPath
	stats.LastAccessedURL = course.LastAccessedURL
	stats.LastAccessedTitle = course.LastAccessedTitle
	return stats
}

// CountCompleted reads the progress file stored at path and returns how many
// lessons were marked as completed. A missing or unreadable file counts as 0, so
// callers can use it to summarise courses that were never opened.
func CountCompleted(path string) int {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	parsed := newProgressFile()
	if err := json.Unmarshal(data, parsed); err != nil {
		return 0
	}
	completed := 0
	for _, entry := range parsed.Entries {
		if entry != nil && entry.Completed != nil && *entry.Completed {
			completed++
		}
	}
	return completed
}

func (t *ProgressTracker) loadLocked(course *Course) (*progressFile, error) {
	data, err := os.ReadFile(course.ProgressFile)
	if err != nil {
		return newProgressFile(), nil
	}
	parsed := newProgressFile()
	if err := json.Unmarshal(data, parsed); err != nil {
		return newProgressFile(), nil
	}
	if parsed.Entries == nil {
		parsed.Entries = map[string]*ProgressEntry{}
	}
	return parsed, nil
}
