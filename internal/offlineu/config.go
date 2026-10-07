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
	EnvRootLabel   = "OFFLINEU_ROOTS_LABEL"
	EnvProgressDir = "OFFLINEU_PROGRESS_DIR"
	EnvAutoCourse  = "AUTO_LOAD_COURSE"
	EnvDLNA        = "OFFLINEU_DLNA"
)

// Portable is empty for an ordinary build ("go build", "go run", the Docker
// image). The Windows packaging script sets it with
//
//	-ldflags "-X github.com/nickkk333/offlineu/internal/offlineu.Portable=1"
//
// so the shipped .exe treats the folder it lives in as its course folder: drop
// offlineu.exe into a course directory and double-click it. It has to stay a var
// (not a const) because the linker can only overwrite variables - see Version.
var Portable = ""

// MountHint is printed at startup and shown in the picker when none of the
// configured folders contains anything: in a container this almost always means
// the course folder was never mapped.
const MountHint = `No course material was found in the configured folder(s).

Two folders have to be mapped when the container is created:
  /courses   your course files, mounted read-only
  /app/data  progress + course list, must stay writable

Mount your own folders when you start the container - nothing has to be rebuilt:

  docker run -d --name offlineu -p 5000:5000 \
    -v "/path/to/your/courses:/courses:ro" \
    -v offlineu-data:/app/data \
    ghcr.io/nickkk333/offlineu:main

  # docker-compose.yml
  services:
    offlineu:
      image: ghcr.io/nickkk333/offlineu:main
      ports: ["5000:5000"]
      environment:
        - OFFLINEU_ROOTS=/courses
        - OFFLINEU_PROGRESS_DIR=/app/data
      volumes:
        - ./courses:/courses:ro
        - ./data:/app/data

  # NAS Docker GUI (飞牛OS / fnOS, Synology, Unraid, …): the image declares the two
  # paths above, so the "create container" dialog already asks for a host folder for
  # each of them - map your course folder onto /courses and another writable folder
  # onto /app/data.

Mount one folder per library or the single course you want, e.g.
-v "/home/me/Piano Lessons:/courses/piano:ro" and the picker will list it as
"piano". Restart the container after changing the mapping.`

// MountHintFor returns the mount hint in the language the UI asked for (the SPA
// sends Accept-Language, see requestLanguage).
func MountHintFor(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh") {
		return MountHintZH
	}
	return MountHint
}

// MountHintZH is the Chinese translation of the hint above. The commands are
// kept verbatim - only the prose around them is translated.
const MountHintZH = `在配置的文件夹里没有找到任何课程内容。

创建容器时需要映射两个目录：
  /courses   你的课程文件，建议只读挂载
  /app/data  进度与课程列表，必须可写

启动容器时把你存放课程的文件夹挂载进来就行 —— 不需要重新构建镜像：

  docker run -d --name offlineu -p 5000:5000 \
    -v "/path/to/your/courses:/courses:ro" \
    -v offlineu-data:/app/data \
    ghcr.io/nickkk333/offlineu:main

  # docker-compose.yml
  services:
    offlineu:
      image: ghcr.io/nickkk333/offlineu:main
      ports: ["5000:5000"]
      environment:
        - OFFLINEU_ROOTS=/courses
        - OFFLINEU_PROGRESS_DIR=/app/data
      volumes:
        - ./courses:/courses:ro
        - ./data:/app/data

  # NAS 的 Docker 界面（飞牛OS、群晖、Unraid 等）：镜像已声明上面这两个目录，创建
  # 容器时界面会直接要求你为它们各选一个宿主文件夹 —— 把课程文件夹映射到 /courses，
  # 把另一个可写文件夹映射到 /app/data 即可。

每个课程库映射一个文件夹，或者只映射你想学的那一门课程，例如
-v "/home/me/Piano Lessons:/courses/piano:ro"，选择器里就会把它显示为
"piano"。修改映射后请重启容器。`

// Config carries every runtime setting of the application.
type Config struct {
	Host        string
	Port        int
	Debug       bool
	Roots       []string // resolved allow-list; empty means "browse anything"
	RootLabel   string   // OFFLINEU_ROOTS_LABEL: friendly name shown instead of the root path
	ProgressDir string   // OFFLINEU_PROGRESS_DIR ("" keeps progress next to the course)
	StateDir    string   // where offlineu_state.json lives
	DLNAEnabled bool     // OFFLINEU_DLNA: look for cast devices on the LAN (default: on)
}

// ConfigFromEnv builds a Config from the OFFLINEU_* environment variables.
func ConfigFromEnv() Config {
	cfg := Config{
		Host:        envOr(EnvHost, "127.0.0.1"),
		Port:        envIntOr(EnvPort, 5000),
		RootLabel:   strings.TrimSpace(os.Getenv(EnvRootLabel)),
		ProgressDir: strings.TrimSpace(os.Getenv(EnvProgressDir)),
		DLNAEnabled: dlnaEnabled(),
	}
	cfg.RefreshRoots()
	cfg.StateDir = deriveStateDir(cfg.ProgressDir)
	if dir, ok := PortableDir(); ok && cfg.ProgressDir == "" {
		// A portable exe keeps its bookkeeping next to itself, not in whatever
		// folder a shortcut happened to be started from.
		cfg.StateDir = filepath.Join(dir, "data")
	}
	return cfg
}

// PortableDir returns the folder the executable lives in - but only for a
// portable build (Portable set by -ldflags). ok is false for an ordinary build,
// so "go run ." never picks up the temporary build directory.
func PortableDir() (string, bool) {
	if strings.TrimSpace(Portable) == "" {
		return "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir, err := filepath.Abs(filepath.Dir(exe))
	if err != nil {
		return "", false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// RefreshRoots re-reads the allow-list so tests (and long running processes)
// always see the current value of OFFLINEU_ROOTS.
func (c *Config) RefreshRoots() {
	c.Roots = ParseRoots(os.Getenv(EnvRoots))
	if len(c.Roots) == 0 {
		// Portable exe: the folder it lives in is the only folder it serves, so
		// a copy dropped into a course directory cannot wander off into the rest
		// of the disk.
		if dir, ok := PortableDir(); ok {
			c.Roots = ParseRoots(dir)
		}
	}
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

// DisplayPath rewrites an absolute path for the user interface.
//
// Everything below a configured root is shown relative to that root and the root
// itself is replaced by its friendly label, so container internals such as
// "/courses" never leak into the browser. Without an allow-list (local use) the
// absolute path is returned unchanged.
func (c *Config) DisplayPath(value string) string {
	if strings.TrimSpace(value) == "" || len(c.Roots) == 0 {
		return value
	}
	for _, root := range c.Roots {
		if !IsWithin(value, root) {
			continue
		}
		label := c.labelFor(root)
		relative, err := filepath.Rel(root, value)
		if err != nil || relative == "." {
			return label
		}
		return label + "/" + filepath.ToSlash(relative)
	}
	return value
}

// labelFor is the name a root is shown under: OFFLINEU_ROOTS_LABEL when it is
// set, otherwise the name of the folder that was mounted.
func (c *Config) labelFor(root string) string {
	if label := strings.TrimSpace(c.RootLabel); label != "" {
		return label
	}
	base := filepath.Base(filepath.Clean(root))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return root
	}
	return base
}

// RootDisplays returns the UI labels of the configured roots (same order as
// Roots, so the two slices can be zipped by the frontend).
func (c *Config) RootDisplays() []string {
	labels := []string{}
	for _, root := range c.Roots {
		labels = append(labels, c.labelFor(root))
	}
	return labels
}

// MountIssue names *why* the picker has nothing to show. "Nothing was found"
// alone cannot tell a forgotten mapping apart from a mapped but empty folder or
// from a folder the container user may not read - all three used to be reported
// as "no course folder is mapped", which sends NAS users hunting for a mapping
// problem that does not exist.
type MountIssue string

const (
	MountIssueNone       MountIssue = ""            // there is something to show
	MountIssueNoRoots    MountIssue = "no_roots"    // OFFLINEU_ROOTS is empty or resolved to nothing
	MountIssueMissing    MountIssue = "missing"     // the configured path does not exist in the container
	MountIssueUnreadable MountIssue = "unreadable"  // mapped, but the container user may not read it
	MountIssueNotMounted MountIssue = "not_mounted" // empty and backed by no mount at all
	MountIssueVolume     MountIssue = "volume"      // a Docker managed volume instead of your folder
	MountIssueEmpty      MountIssue = "empty"       // your folder is mounted, but there is no file inside
)

// How a root folder is backed according to /proc/self/mountinfo. This is what
// tells "the mapping never arrived" (Docker attached an anonymous volume, or
// nothing is mounted at all) apart from "the host folder really is empty" - both
// look like a plain empty directory to os.ReadDir.
const (
	MountKindBind   = "bind"   // a host folder was mapped onto the path
	MountKindVolume = "volume" // Docker attached an anonymous or named volume
	MountKindNone   = "none"   // nothing is mounted on the path
)

// RootStatus describes one configured course folder for the UI and the startup
// log: whether the mapping exists, whether the container may read it, what backs
// it and how much is inside.
type RootStatus struct {
	Path        string `json:"path"`                   // path inside the container
	Display     string `json:"display"`                // label shown in the UI
	Exists      bool   `json:"exists"`                 // the path is an existing directory
	Readable    bool   `json:"readable"`               // the container user may list it
	Entries     int    `json:"entries"`                // non-hidden children (0 when unreadable)
	HasFiles    bool   `json:"has_files"`              // any non-hidden file, at any depth
	Mount       string `json:"mount"`                  // bind / volume / none, "" when unknown
	MountDetail string `json:"mount_detail,omitempty"` // e.g. "ext4 /dev/sda1 root=/vol1/1000/courses"
	MountKnown  bool   `json:"mount_known"`            // false where mountinfo does not exist (Windows)
	Error       string `json:"error,omitempty"`        // the OS error, e.g. "permission denied"
}

// RootStatus inspects a single root folder. A read error never fails the call:
// the status records what went wrong so the UI can explain it in plain words.
func (c *Config) RootStatus(root string) RootStatus {
	status := RootStatus{Path: root, Display: c.labelFor(root)}
	status.Mount, status.MountDetail, status.MountKnown = classifyMount(root)
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		status.Exists = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Exists = true
	status.Readable = true
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			status.Entries++
		}
	}
	status.HasFiles = rootHasContent(root, MaxMediaScanEntries)
	return status
}

// RootStatuses reports the state of every configured root, in the same order as
// Roots (so the frontend can pair the two slices).
func (c *Config) RootStatuses() []RootStatus {
	statuses := []RootStatus{}
	for _, root := range c.Roots {
		statuses = append(statuses, c.RootStatus(root))
	}
	return statuses
}

// Summary renders a status as a single log line.
func (s RootStatus) Summary() string {
	switch {
	case !s.Exists:
		return "not found: the configured path does not exist" + errorSuffix(s.Error)
	case !s.Readable:
		return unreadableSummary(s.Error)
	case s.HasFiles:
		if !s.MountKnown {
			return "readable, " + strconv.Itoa(s.Entries) + " entries"
		}
		return "mapped, " + strconv.Itoa(s.Entries) + " entries (" + mountKindLabel(s.Mount) + ")"
	case s.Mount == MountKindVolume:
		return "a Docker volume instead of your folder - the mapping did not arrive"
	case s.Mount == MountKindNone:
		return "no mount and no file: nothing was mapped here"
	case !s.MountKnown:
		return "readable, but empty (no file at any depth)"
	default:
		return "your folder is mounted, but empty (no file at any depth)"
	}
}

// mountKindLabel names a mount kind inside the log line.
func mountKindLabel(kind string) string {
	switch kind {
	case MountKindBind:
		return "host folder"
	case MountKindVolume:
		return "Docker volume"
	case MountKindNone:
		return "no mount"
	default:
		return "unknown mount type"
	}
}

// errorSuffix appends the raw OS error to a status line when there is one.
func errorSuffix(err string) string {
	if strings.TrimSpace(err) == "" {
		return ""
	}
	return " - " + err
}

// mountInfoEntry is the part of one /proc/self/mountinfo line that says what
// backs a path.
type mountInfoEntry struct {
	fstype string
	source string
	root   string
}

// mountInfoForPath looks path up in /proc/self/mountinfo. found tells whether the
// path is a mount point at all, known whether the table could be read - it does
// not exist on Windows or macOS, where nothing may be concluded about mappings.
func mountInfoForPath(path string) (entry mountInfoEntry, found bool, known bool) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return mountInfoEntry{}, false, false
	}
	target := filepath.Clean(path)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		// <id> <parent> <major:minor> <root> <mountpoint> <options> … - <fstype> <source> <super options>
		if len(fields) < 6 || unescapeMountField(fields[4]) != target {
			continue
		}
		entry.root = unescapeMountField(fields[3])
		for index := 6; index < len(fields)-1; index++ {
			if fields[index] != "-" {
				continue
			}
			entry.fstype = fields[index+1]
			if index+2 < len(fields) {
				entry.source = fields[index+2]
			}
			break
		}
		return entry, true, true
	}
	return mountInfoEntry{}, false, true
}

// unescapeMountField decodes the octal escapes mountinfo uses for spaces, tabs,
// newlines and backslashes inside paths.
func unescapeMountField(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}

// classifyMount tells how a path is backed: by a mapped host folder, by a Docker
// managed volume (the "mapping was forgotten or points elsewhere" case) or by
// nothing at all. known is false where the platform has no mountinfo.
func classifyMount(path string) (kind string, detail string, known bool) {
	entry, found, known := mountInfoForPath(path)
	if !known {
		return "", "", false
	}
	if !found {
		return MountKindNone, "", true
	}
	detail = strings.TrimSpace(entry.fstype + " " + entry.source)
	if entry.root != "" && entry.root != "/" {
		detail = strings.TrimSpace(detail + " root=" + entry.root)
	}
	if strings.Contains(entry.root, "/docker/volumes/") || strings.Contains(entry.source, "/docker/volumes/") {
		return MountKindVolume, detail, true
	}
	return MountKindBind, detail, true
}

// MountIssue summarises the whole mount situation: it is the value the picker
// turns into an accurate message.
func (c *Config) MountIssue() MountIssue {
	if len(c.Roots) == 0 {
		return MountIssueNoRoots
	}
	unreadable, volume, notMounted, empty := false, false, false, false
	for _, status := range c.RootStatuses() {
		if status.HasFiles {
			return MountIssueNone
		}
		switch {
		case !status.Exists:
			// The path is gone entirely; the default branch below reports it.
		case !status.Readable:
			unreadable = true
		case status.Mount == MountKindVolume:
			volume = true
		case status.Mount == MountKindNone:
			notMounted = true
		default:
			empty = true
		}
	}
	switch {
	case unreadable:
		return MountIssueUnreadable
	case volume:
		return MountIssueVolume
	case notMounted:
		return MountIssueNotMounted
	case empty:
		return MountIssueEmpty
	default:
		return MountIssueMissing
	}
}

// NeedsMount reports whether the picker has nothing to show at all: every
// configured root is missing, unreadable or empty. MountIssue() says which of
// those it is, so the UI can point at the real problem. When no root is
// configured at all the picker browses freely, which is the desktop case.
func (c *Config) NeedsMount() bool {
	if len(c.Roots) == 0 {
		return false
	}
	return c.MountIssue() != MountIssueNone
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

// dlnaEnabled reads OFFLINEU_DLNA. Casting is on unless it is switched off
// explicitly: a Docker container without host networking cannot see the SSDP
// multicast of the LAN, and its owner may prefer a quiet startup log.
func dlnaEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvDLNA))) {
	case "0", "off", "false", "no", "disabled", "none":
		return false
	default:
		return true
	}
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
