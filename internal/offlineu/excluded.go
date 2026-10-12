package offlineu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ExcludedFilename is the per-course exclusion list, stored next to the
// progress file so exclusions survive restarts and work for read-only course
// mounts (OFFLINEU_PROGRESS_DIR) as well.
const ExcludedFilename = ".offlineu_excluded.json"

// ExclusionStore reads and writes the per-course exclusion list: relative
// paths (files or folders) the user hid from the course content. Every write
// is locked and atomic, so an interrupted save can never truncate the file.
type ExclusionStore struct {
	mu sync.RWMutex
}

// exclusionFile is the on-disk format: just the sorted list of relatives.
type exclusionFile struct {
	Excluded []string `json:"excluded"`
}

// ExcludedFileFor returns the JSON file that stores the exclusions of a
// course, mirroring ProgressFileFor so both live in the same place.
func (c *Config) ExcludedFileFor(coursePath string) string {
	if strings.TrimSpace(c.ProgressDir) == "" {
		return filepath.Join(coursePath, ExcludedFilename)
	}
	return filepath.Join(ExpandHome(c.ProgressDir), excludedKey(coursePath)+"_excluded.json")
}

func excludedKey(coursePath string) string {
	key := sha1Hex(coursePath)
	if len(key) > 12 {
		key = key[:12]
	}
	return key
}

// Load returns the stored exclusion list of a course. A missing or broken
// file simply yields an empty list.
func (s *ExclusionStore) Load(course *Course) []string {
	return s.LoadFile(excludedFileFor(course, nil))
}

// LoadFile is Load for an explicit file path (used where no Course exists).
func (s *ExclusionStore) LoadFile(path string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}
	parsed := exclusionFile{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return []string{}
	}
	return normalizeExcluded(parsed.Excluded)
}

// Save replaces the exclusion list of a course atomically.
func (s *ExclusionStore) Save(course *Course, excluded []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(excludedFileFor(course, nil), excluded)
}

// SaveFile is Save for an explicit file path (used where no Course exists).
func (s *ExclusionStore) SaveFile(path string, excluded []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(path, excluded)
}

func (s *ExclusionStore) saveLocked(target string, excluded []string) error {
	cleaned := normalizeExcluded(excluded)
	encoded, err := json.MarshalIndent(exclusionFile{Excluded: cleaned}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return &ProgressStorageError{Message: "cannot write exclusions to " + target + ": " + err.Error() + ". Use OFFLINEU_PROGRESS_DIR to store progress somewhere writable."}
	}
	temp := target + ".tmp"
	if err := os.WriteFile(temp, append(encoded, '\n'), 0o644); err != nil {
		return &ProgressStorageError{Message: "cannot write exclusions to " + target + ": " + err.Error() + ". Use OFFLINEU_PROGRESS_DIR to store progress somewhere writable."}
	}
	if err := os.Rename(temp, target); err != nil {
		os.Remove(temp)
		return &ProgressStorageError{Message: "cannot write exclusions to " + target + ": " + err.Error() + ". Use OFFLINEU_PROGRESS_DIR to store progress somewhere writable."}
	}
	return nil
}

// Delete removes the exclusion file of a course, so forgetting a course in
// the picker really clears its data instead of only hiding the entry. A file
// that does not exist is not an error.
func (s *ExclusionStore) Delete(target string) error {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// normalizeExcluded de-duplicates, drops empties and sorts the list so the
// file stays stable and comparable.
func normalizeExcluded(excluded []string) []string {
	seen := map[string]bool{}
	cleaned := []string{}
	for _, item := range excluded {
		relative := NormalizeRel(item)
		if relative == "" || seen[relative] {
			continue
		}
		seen[relative] = true
		cleaned = append(cleaned, relative)
	}
	sort.Strings(cleaned)
	return cleaned
}

// excludedFileFor resolves the exclusion file of a course. cfg is only used
// by tests to point at an explicit file; production callers pass nil and the
// Course's own ExcludedFile is used.
func excludedFileFor(course *Course, cfg *Config) string {
	if course.ExcludedFile != "" {
		return course.ExcludedFile
	}
	if cfg != nil {
		return cfg.ExcludedFileFor(course.Path)
	}
	return ExcludedFilename
}

// ApplyExclusions removes excluded lessons and folders from the course tree.
// A folder exclusion hides its whole subtree; excluding a single file hides
// just that lesson. Empty folders left behind are dropped as well. It returns
// the surviving exclusion entries (missing paths included, so the UI can
// still offer to restore them).
func ApplyExclusions(root *DirectoryNode, excluded []string) []string {
	set := map[string]bool{}
	for _, relative := range normalizeExcluded(excluded) {
		set[relative] = true
	}
	pruneNode(root, "", set)
	surviving := []string{}
	for _, relative := range normalizeExcluded(excluded) {
		surviving = append(surviving, relative)
	}
	return surviving
}

func pruneNode(node *DirectoryNode, dir string, excluded map[string]bool) {
	keptLessons := []*Lesson{}
	for _, lesson := range node.Lessons {
		if excluded[lesson.RelPath] {
			continue
		}
		keptLessons = append(keptLessons, lesson)
	}
	node.Lessons = keptLessons

	keptChildren := []*DirectoryNode{}
	for _, child := range node.Children {
		relative := dirOf(child, dir)
		if excluded[relative] {
			continue
		}
		pruneNode(child, relative, excluded)
		if len(child.Lessons) == 0 && len(child.Children) == 0 {
			continue
		}
		keptChildren = append(keptChildren, child)
	}
	node.Children = keptChildren
	node.HasContent = len(node.Lessons) > 0 || len(node.Children) > 0
}

// dirOf resolves the course relative path of a child node. DirectoryNode only
// carries an absolute Path, so the parent relative prefix plus the folder
// name is used.
func dirOf(child *DirectoryNode, parent string) string {
	name := filepath.Base(child.Path)
	relative := name
	if parent != "" {
		relative = parent + "/" + name
	}
	return NormalizeRel(relative)
}
