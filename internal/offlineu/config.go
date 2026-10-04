// Package offlineu implements the self-hosted, offline course viewer and
// progress tracker that powers the OfflineU web application.
//
// The package only depends on the Go standard library: it can parse a folder of
// videos, audio files, documents and quizzes, remember the course that was
// opened last and store per-lesson progress in a plain JSON file.
package offlineu

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Environment variables understood by OfflineU.
const (
	EnvHost        = "OFFLINEU_HOST"
	EnvPort        = "OFFLINEU_PORT"
	EnvRoots       = "OFFLINEU_ROOTS"
	EnvProgressDir = "OFFLINEU_PROGRESS_DIR"
	EnvAutoCourse  = "AUTO_LOAD_COURSE"
)

// Config carries every runtime setting of the application.
type Config struct {
	Host        string
	Port        int
	Debug       bool
	Roots       []string // resolved allow-list; empty means "browse anything"
	ProgressDir string   // OFFLINEU_PROGRESS_DIR ("" keeps progress next to the course)
	StateDir    string   // where offlineu_state.json lives
}

// ConfigFromEnv builds a Config from the OFFLINEU_* environment variables.
func ConfigFromEnv() Config {
	cfg := Config{
		Host:        envOr(EnvHost, "127.0.0.1"),
		Port:        envIntOr(EnvPort, 5000),
		ProgressDir: strings.TrimSpace(os.Getenv(EnvProgressDir)),
	}
	cfg.RefreshRoots()
	cfg.StateDir = deriveStateDir(cfg.ProgressDir)
	return cfg
}

// RefreshRoots re-reads the allow-list so tests (and long running processes)
// always see the current value of OFFLINEU_ROOTS.
func (c *Config) RefreshRoots() {
	c.Roots = ParseRoots(os.Getenv(EnvRoots))
}

// ParseRoots splits an os.PathListSeparator separated list into absolute,
// existing directories. Entries that do not exist are dropped.
func ParseRoots(raw string) []string {
	roots := []string{}
	for _, chunk := range strings.Split(raw, string(os.PathListSeparator)) {
		chunk = strings.Trim(strings.TrimSpace(chunk), `"`)
		if chunk == "" {
			continue
		}
		absolute, err := filepath.Abs(ExpandHome(chunk))
		if err != nil {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.IsDir() {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		}
		roots = append(roots, absolute)
	}
	return roots
}

// deriveStateDir mirrors the Python implementation: when a central progress
// directory is configured it also keeps the course bookkeeping file, otherwise
// OfflineU writes ./data next to the running binary.
func deriveStateDir(progressDir string) string {
	if progressDir != "" {
		return ExpandHome(progressDir)
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "data")
	}
	return "data"
}

// ProgressFileFor returns the JSON file that stores the progress of a course.
//
// Progress lives next to the course by default; OFFLINEU_PROGRESS_DIR relocates
// it into a central, writable folder (used by the Docker image so that course
// mounts can stay read-only).
func (c *Config) ProgressFileFor(coursePath string) string {
	if strings.TrimSpace(c.ProgressDir) == "" {
		return filepath.Join(coursePath, ProgressFilename)
	}
	key := sha1Hex(coursePath)
	if len(key) > 12 {
		key = key[:12]
	}
	return filepath.Join(ExpandHome(c.ProgressDir), key+"_progress.json")
}

// StateFile returns the path of the course bookkeeping file.
func (c *Config) StateFile() string {
	if c.StateDir == "" {
		c.StateDir = deriveStateDir(c.ProgressDir)
	}
	return filepath.Join(c.StateDir, StateFilename)
}

// InsideRoots reports whether path is allowed by the configured allow-list.
func (c *Config) InsideRoots(path string) bool {
	if len(c.Roots) == 0 {
		return true
	}
	for _, root := range c.Roots {
		if IsWithin(path, root) {
			return true
		}
	}
	return false
}

// IsWithin reports whether path is root itself or lives below it. Symlinks are
// resolved first and ".." escapes (plus prefix tricks such as /courses-ab for
// /courses) are rejected.
func IsWithin(path, root string) bool {
	base := resolveBestEffort(root)
	candidate := resolveBestEffort(path)
	if base == "" || candidate == "" {
		return false
	}
	base = foldCase(base)
	candidate = foldCase(candidate)
	if candidate == base {
		return true
	}
	relative, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// ResolveInside resolves rel below base and returns an error when it would
// escape the directory. rel must already be URL-decoded (the HTTP layer does
// exactly one unescape) so that double encoded traversal attempts stay inert.
func ResolveInside(base, rel string) (string, bool) {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return "", false
	}
	candidate, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	if !IsWithin(candidate, base) {
		return "", false
	}
	return candidate, true
}

func resolveBestEffort(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return absolute
}

func foldCase(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func sha1Hex(value string) string {
	sum := sha1.Sum([]byte(foldCase(value)))
	return hex.EncodeToString(sum[:])
}

// ExpandHome expands a leading "~" into the user's home directory.
func ExpandHome(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") ||
		strings.HasPrefix(trimmed, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			rest := strings.TrimPrefix(trimmed, "~")
			rest = strings.TrimPrefix(rest, "/")
			rest = strings.TrimPrefix(rest, string(filepath.Separator))
			return filepath.Join(home, rest)
		}
	}
	return trimmed
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
