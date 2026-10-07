package offlineu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RecentCourse is one entry of the "courses you opened" list.
type RecentCourse struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	LastOpened string `json:"last_opened,omitempty"`
	// TotalLessons is cached when the course is parsed, so the picker can show a
	// progress percentage without re-scanning every remembered folder.
	TotalLessons int `json:"total_lessons,omitempty"`
	// CompletedLessons is filled in on every /api/state call by reading the
	// course progress file; it is never persisted.
	CompletedLessons int `json:"completed_lessons,omitempty"`
	// DisplayPath is the path as the UI should show it (relative to the mapped
	// folder); it replaces Path in the browser and is never persisted.
	DisplayPath string `json:"display_path,omitempty"`
}

type stateData struct {
	ActiveCourse  string         `json:"active_course,omitempty"`
	RecentCourses []RecentCourse `json:"recent_courses"`
	// What happens when a lesson ends. The browser player and a cast on a TV keep
	// their own choice: both can play at the same time, so one must not change
	// the other. Both live here - and not only in the browser - so every client
	// (new browser, cleared cache, restart) picks them up again from /api/state.
	PlayMode     string `json:"play_mode,omitempty"`      // browser player
	CastPlayMode string `json:"cast_play_mode,omitempty"` // cast on a renderer
}

// CourseStore is the thread-safe holder of the active course plus the list of
// courses the user opened before. The selection is also written to disk so a
// page reload, a restart or a fresh container never loses the course.
type CourseStore struct {
	mu          sync.RWMutex
	cfg         *Config
	course      *Course
	stateFile   string
	logWarnings bool
}

// NewCourseStore builds a store that persists its bookkeeping next to the
// progress files.
func NewCourseStore(cfg *Config) *CourseStore {
	return &CourseStore{cfg: cfg, stateFile: cfg.StateFile(), logWarnings: true}
}

// NewCourseStoreWithStateFile is used by tests to point the store at a
// temporary bookkeeping file.
func NewCourseStoreWithStateFile(cfg *Config, stateFile string) *CourseStore {
	return &CourseStore{cfg: cfg, stateFile: stateFile, logWarnings: false}
}

// StateFile returns the resolved bookkeeping file.
func (s *CourseStore) StateFile() string { return s.stateFile }

// Get returns the active course, or nil when the picker should be shown.
func (s *CourseStore) Get() *Course {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.course
}

// Set makes course active and remembers it (nil clears the active course while
// keeping the recent list).
func (s *CourseStore) Set(course *Course) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.course = course
	state := s.readState()
	if course == nil {
		state.ActiveCourse = ""
	} else {
		state.ActiveCourse = course.Path
		state.RecentCourses = remember(state.RecentCourses, course)
	}
	s.writeState(state)
}

// Clear stops showing the current course but keeps it in the recent list.
func (s *CourseStore) Clear() { s.Set(nil) }

// RecentCourses lists remembered courses that still exist on disk and are
// allowed by OFFLINEU_ROOTS, most recent first.
func (s *CourseStore) RecentCourses() []RecentCourse {
	s.mu.RLock()
	recent := s.readState().RecentCourses
	s.mu.RUnlock()

	s.cfg.RefreshRoots()
	result := []RecentCourse{}
	for _, item := range recent {
		if item.Path == "" {
			continue
		}
		info, err := os.Stat(item.Path)
		if err != nil || !info.IsDir() {
			continue
		}
		if !s.cfg.InsideRoots(item.Path) {
			continue
		}
		name := item.Name
		if name == "" {
			name = filepath.Base(item.Path)
		}
		result = append(result, RecentCourse{
			Path:         item.Path,
			Name:         name,
			LastOpened:   item.LastOpened,
			TotalLessons: item.TotalLessons,
		})
	}
	return result
}

// SetRecentTotals persists the lesson counters of remembered courses that do not
// have one yet (state files written before the counters existed). Entries that
// already know their total are left untouched.
func (s *CourseStore) SetRecentTotals(entries []RecentCourse) {
	var pending []RecentCourse
	for _, entry := range entries {
		if entry.TotalLessons > 0 {
			pending = append(pending, entry)
		}
	}
	if len(pending) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.readState()
	changed := false
	for index := range state.RecentCourses {
		stored := &state.RecentCourses[index]
		if stored.TotalLessons > 0 {
			continue
		}
		for _, given := range pending {
			if samePath(given.Path, stored.Path) {
				stored.TotalLessons = given.TotalLessons
				changed = true
				break
			}
		}
	}
	if changed {
		s.writeState(state)
	}
}

// Forget drops a single course from the recent list. The course folder itself
// is never touched; its stored progress file is deleted by App.forgetCourse.
func (s *CourseStore) Forget(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.readState()
	kept := []RecentCourse{}
	for _, item := range state.RecentCourses {
		if samePath(item.Path, path) {
			continue
		}
		kept = append(kept, item)
	}
	state.RecentCourses = kept
	s.writeState(state)
}

// PlayMode is what the browser player does when a lesson ends. An empty or
// unknown value means the product default ("once"), so a state file written by
// an older release (or edited by hand) can never break the player.
func (s *CourseStore) PlayMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return normalizePlayMode(s.readState().PlayMode)
}

// SetPlayMode remembers the browser setting for every client and returns what
// was stored.
func (s *CourseStore) SetPlayMode(mode string) string {
	normalized := normalizePlayMode(mode)
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.readState()
	state.PlayMode = normalized
	s.writeState(state)
	return normalized
}

// CastPlayMode is what a cast does when the lesson it plays ends - its own
// setting, next to the one the browser player uses. Until the cast mode is
// chosen on its own it follows the shared value older releases stored.
func (s *CourseStore) CastPlayMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.readState()
	if strings.TrimSpace(state.CastPlayMode) == "" {
		return normalizePlayMode(state.PlayMode)
	}
	return normalizePlayMode(state.CastPlayMode)
}

// SetCastPlayMode remembers the cast setting and returns what was stored.
func (s *CourseStore) SetCastPlayMode(mode string) string {
	normalized := normalizePlayMode(mode)
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.readState()
	state.CastPlayMode = normalized
	s.writeState(state)
	return normalized
}

// Restore reloads the remembered course (home page visit, restart, new worker).
func (s *CourseStore) Restore() *Course {
	s.mu.RLock()
	remembered := s.readState().ActiveCourse
	s.mu.RUnlock()
	if remembered == "" {
		return nil
	}

	s.cfg.RefreshRoots()
	info, err := os.Stat(remembered)
	if err != nil || !info.IsDir() {
		s.Clear()
		return nil
	}
	if !s.cfg.InsideRoots(remembered) {
		s.Clear()
		return nil
	}
	course, err := s.cfg.ScanCourse(remembered)
	if err != nil {
		s.Clear()
		return nil
	}
	s.Set(course)
	return course
}

func remember(recent []RecentCourse, course *Course) []RecentCourse {
	entry := RecentCourse{
		Path:         course.Path,
		Name:         course.Name,
		LastOpened:   time.Now().Format("2006-01-02T15:04:05"),
		TotalLessons: len(AllLessons(course.Root)),
	}
	kept := []RecentCourse{entry}
	for _, item := range recent {
		if samePath(item.Path, course.Path) {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) > MaxRecentCourses {
		kept = kept[:MaxRecentCourses]
	}
	return kept
}

func samePath(left, right string) bool {
	return foldCase(filepath.Clean(left)) == foldCase(filepath.Clean(right))
}

func (s *CourseStore) readState() stateData {
	state := stateData{RecentCourses: []RecentCourse{}}
	data, err := os.ReadFile(s.stateFile)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return stateData{RecentCourses: []RecentCourse{}}
	}
	if state.RecentCourses == nil {
		state.RecentCourses = []RecentCourse{}
	}
	return state
}

func (s *CourseStore) writeState(state stateData) {
	if state.RecentCourses == nil {
		state.RecentCourses = []RecentCourse{}
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	target := s.stateFile
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		if s.logWarnings {
			logf("warning: cannot remember courses in %s: %v", target, err)
		}
		return
	}
	temp := target + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o644); err != nil {
		if s.logWarnings {
			logf("warning: cannot remember courses in %s: %v", target, err)
		}
		return
	}
	if err := os.Rename(temp, target); err != nil {
		os.Remove(temp)
		if s.logWarnings {
			logf("warning: cannot remember courses in %s: %v", target, err)
		}
	}
}
