package offlineu

// Remote course sources.
//
// OfflineU started as a viewer for folders mounted into its container. A phone
// or a NAS often keeps its course library somewhere else, though: on a WebDAV
// server, on a Windows/Samba share, or in a folder that should be selectable
// without restarting the container. This file adds those course sources without
// touching the rest of the path handling: every source is exposed under a
// virtual absolute path
//
//	/__offlineu_sources__/<source-id>/<relative path>
//
// (or \\offlineu_sources__\<source-id>\... on Windows) and Config routes every
// file operation for such a path to the matching backend. Course trees, lesson
// URLs and progress keys therefore stay exactly what they were for local
// folders.
//
// Source passwords are stored in StateDir/sources.json with mode 0600. They are
// never returned by the API - the UI only learns whether one is set.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"
)

// Course source kinds.
const (
	SourceKindLocal  = "local"
	SourceKindWebDAV = "webdav"
	SourceKindSMB    = "smb"
)

// ErrSourceNotFound is returned when a virtual path names a source that is no
// longer configured.
var ErrSourceNotFound = errors.New("course source not found")

// Source is one configured course source. Password is persisted but never sent
// back to the browser (see SourceInfo).
type Source struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"` // local folder
	URL      string `json:"url,omitempty"`  // WebDAV collection URL
	Host     string `json:"host,omitempty"` // SMB host, optionally host:port
	Port     int    `json:"port,omitempty"`
	Share    string `json:"share,omitempty"`
	BasePath string `json:"base_path,omitempty"` // folder inside the SMB share
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Domain   string `json:"domain,omitempty"`
	// InsecureTLS accepts a self-signed certificate. NAS boxes commonly use one,
	// and this is a self-hosted LAN application.
	InsecureTLS bool `json:"insecure_tls,omitempty"`
}

// SourceInfo is the public shape of a source returned by /api/sources.
type SourceInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Root        string `json:"root"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Share       string `json:"share,omitempty"`
	BasePath    string `json:"base_path,omitempty"`
	Username    string `json:"username,omitempty"`
	HasPassword bool   `json:"has_password"`
	InsecureTLS bool   `json:"insecure_tls,omitempty"`
}

// SourceBasePath is the virtual folder that contains every configured source.
func SourceBasePath() string {
	if runtime.GOOS == "windows" {
		return `\\offlineu_sources__`
	}
	return "/__offlineu_sources__"
}

// SourceRoot is the virtual root path of one source.
func SourceRoot(id string) string {
	return filepath.Join(SourceBasePath(), id)
}

// IsSourcePath reports whether path lives in the virtual source namespace.
func IsSourcePath(path string) bool {
	_, ok := sourceRelative(path)
	return ok
}

// sourceRelative strips the virtual base from a path. filepath.Rel cannot be
// used for this on Windows: a UNC server+share pair is the volume, so two
// source ids would look like two different volumes. A plain prefix match keeps
// the namespace usable on every platform.
func sourceRelative(value string) (string, bool) {
	base := filepath.Clean(SourceBasePath())
	cleaned := filepath.Clean(value)
	if samePath(cleaned, base) {
		return "", false
	}
	prefix := base + string(filepath.Separator)
	if !strings.HasPrefix(foldCase(cleaned), foldCase(prefix)) {
		return "", false
	}
	relative := cleaned[len(prefix):]
	if relative == "" || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

// sourceRegistryFile is the on-disk JSON document.
type sourceRegistryFile struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
}

// SourceRegistry owns the configured sources and their open backends.
type SourceRegistry struct {
	mu         sync.RWMutex
	file       string
	sources    []Source
	backends   map[string]sourceBackend
	localRoots []string
}

// NewSourceRegistry loads the source list from file. A missing or malformed
// file is treated as an empty list: a broken source file must not keep the
// whole application from starting.
func NewSourceRegistry(file string) *SourceRegistry {
	registry := &SourceRegistry{file: file, backends: map[string]sourceBackend{}}
	data, err := os.ReadFile(file)
	if err != nil {
		return registry
	}
	parsed := sourceRegistryFile{}
	if json.Unmarshal(data, &parsed) != nil {
		return registry
	}
	seen := map[string]bool{}
	for _, source := range parsed.Sources {
		source = cleanSource(source)
		if source.ID == "" || seen[source.ID] {
			continue
		}
		seen[source.ID] = true
		registry.sources = append(registry.sources, source)
	}
	return registry
}

// SetLocalRoots limits where a *new* local source may point when
// OFFLINEU_ROOTS is configured. Remote sources are not affected.
func (r *SourceRegistry) SetLocalRoots(roots []string) {
	r.mu.Lock()
	r.localRoots = append([]string{}, roots...)
	r.mu.Unlock()
}

// File returns the JSON file the registry is stored in.
func (r *SourceRegistry) File() string { return r.file }

// List returns every configured source, without passwords.
func (r *SourceRegistry) List() []SourceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]SourceInfo, 0, len(r.sources))
	for _, source := range r.sources {
		infos = append(infos, source.info())
	}
	return infos
}

// Find returns a copy of one configured source.
func (r *SourceRegistry) Find(id string) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, source := range r.sources {
		if source.ID == id {
			return source, true
		}
	}
	return Source{}, false
}

// Add validates, stores and returns a new source.
func (r *SourceRegistry) Add(source Source) (SourceInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	source = cleanSource(source)
	if source.ID == "" {
		source.ID = r.newIDLocked()
	}
	for _, existing := range r.sources {
		if existing.ID == source.ID {
			return SourceInfo{}, errors.New("a course source with this id already exists")
		}
	}
	if err := r.validateLocked(source); err != nil {
		return SourceInfo{}, err
	}
	if source.Name == "" {
		source.Name = defaultSourceName(source)
	}
	r.sources = append(r.sources, source)
	if err := r.saveLocked(); err != nil {
		r.sources = r.sources[:len(r.sources)-1]
		return SourceInfo{}, err
	}
	return source.info(), nil
}

// Remove deletes a source and closes its backend.
func (r *SourceRegistry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := -1
	for index, source := range r.sources {
		if source.ID == id {
			found = index
			break
		}
	}
	if found < 0 {
		return ErrSourceNotFound
	}
	if backend := r.backends[id]; backend != nil {
		if closer, ok := backend.(io.Closer); ok {
			_ = closer.Close()
		}
		delete(r.backends, id)
	}
	removed := r.sources[found]
	r.sources = append(r.sources[:found], r.sources[found+1:]...)
	if err := r.saveLocked(); err != nil {
		r.sources = append(r.sources[:found], append([]Source{removed}, r.sources[found:]...)...)
		return err
	}
	return nil
}

// Update replaces a stored source, keeping its id (and therefore its virtual
// path, course tree and progress key). An empty password means "keep the saved
// one", which lets the edit form stay password-free. The cached backend is
// closed so the next request reconnects with the new settings.
func (r *SourceRegistry) Update(source Source) (SourceInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	source = cleanSource(source)
	index := -1
	for i := range r.sources {
		if r.sources[i].ID == source.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return SourceInfo{}, ErrSourceNotFound
	}
	existing := r.sources[index]
	if source.Password == "" {
		source.Password = existing.Password
	}
	if source.Username == "" {
		source.Username = existing.Username
	}
	if source.Domain == "" {
		source.Domain = existing.Domain
	}
	if source.Name == "" {
		source.Name = existing.Name
	}
	if err := r.validateLocked(source); err != nil {
		return SourceInfo{}, err
	}
	if source.Name == "" {
		source.Name = defaultSourceName(source)
	}
	previous := r.sources[index]
	r.sources[index] = source
	if err := r.saveLocked(); err != nil {
		r.sources[index] = previous
		return SourceInfo{}, err
	}
	if backend := r.backends[source.ID]; backend != nil {
		if closer, ok := backend.(io.Closer); ok {
			_ = closer.Close()
		}
		delete(r.backends, source.ID)
	}
	return source.info(), nil
}

// sourceValidationError marks a malformed configuration, as opposed to a
// server that could not be reached, so the HTTP layer can return 400 instead
// of 502.
type sourceValidationError struct{ err error }

func (e sourceValidationError) Error() string { return e.err.Error() }
func (e sourceValidationError) Unwrap() error { return e.err }

// sourceTargetKey identifies a source by the place it points at. Signing in
// twice with the same WebDAV URL must not produce duplicate sources.
func sourceTargetKey(source Source) string {
	switch source.Kind {
	case SourceKindLocal:
		return "local|" + foldCase(filepath.Clean(source.Path))
	case SourceKindWebDAV:
		return "webdav|" + foldCase(strings.TrimRight(source.URL, "/"))
	case SourceKindSMB:
		port := source.Port
		if port <= 0 {
			port = 445
		}
		return fmt.Sprintf("smb|%s|%d|%s|%s", foldCase(source.Host), port,
			foldCase(source.Share), foldCase(source.BasePath))
	default:
		return source.Kind + "|" + source.ID
	}
}

// Connect is the login entry point used by the web login page and the Android
// app: verify the connection first, then reuse a source pointing at the same
// place, update its credentials, or register a new one. The returned action is
// one of "created", "updated" or "reused".
func (r *SourceRegistry) Connect(source Source) (SourceInfo, string, error) {
	source = cleanSource(source)
	source.ID = "" // a login identifies a source by its target, never by its id

	r.mu.RLock()
	localRoots := append([]string{}, r.localRoots...)
	key := sourceTargetKey(source)
	var match *Source
	for i := range r.sources {
		if sourceTargetKey(r.sources[i]) == key {
			existing := r.sources[i]
			match = &existing
			break
		}
	}
	r.mu.RUnlock()

	if err := validateSource(source, localRoots); err != nil {
		return SourceInfo{}, "", sourceValidationError{err}
	}
	if source.Name == "" {
		// Signing in again with just the URL must not rename a known source.
		if match != nil {
			source.Name = match.Name
		} else {
			source.Name = defaultSourceName(source)
		}
	}

	// Verify before anything is written. For a known target an empty password
	// means "use the one already saved on the server".
	verification := source
	if match != nil && verification.Password == "" {
		verification.Password = match.Password
	}
	if err := r.Test(verification); err != nil {
		return SourceInfo{}, "", err
	}

	if match == nil {
		info, err := r.Add(source)
		return info, "created", err
	}
	merged := source
	merged.ID = match.ID
	if merged.Password == "" {
		merged.Password = match.Password
	}
	if merged.Username == "" {
		merged.Username = match.Username
	}
	if merged.Domain == "" {
		merged.Domain = match.Domain
	}
	if !merged.InsecureTLS {
		merged.InsecureTLS = match.InsecureTLS
	}
	if merged == *match {
		return match.info(), "reused", nil
	}
	info, err := r.Update(merged)
	return info, "updated", err
}

// Test opens a source and lists its root. When an existing id is supplied the
// stored password is reused, so the UI can test a source without asking for it
// again.
func (r *SourceRegistry) Test(source Source) error {
	r.mu.RLock()
	if source.ID != "" {
		for _, existing := range r.sources {
			if existing.ID != source.ID {
				continue
			}
			if source.Password == "" {
				source.Password = existing.Password
			}
			if source.Username == "" {
				source.Username = existing.Username
			}
			if source.Kind == "" {
				source.Kind = existing.Kind
			}
			// A test of an already stored source may only send the id: fill in
			// every connection detail the client left out.
			if source.Path == "" {
				source.Path = existing.Path
			}
			if source.URL == "" {
				source.URL = existing.URL
			}
			if source.Host == "" {
				source.Host = existing.Host
			}
			if source.Port == 0 {
				source.Port = existing.Port
			}
			if source.Share == "" {
				source.Share = existing.Share
			}
			if source.BasePath == "" {
				source.BasePath = existing.BasePath
			}
			if source.Domain == "" {
				source.Domain = existing.Domain
			}
			if !source.InsecureTLS {
				source.InsecureTLS = existing.InsecureTLS
			}
			break
		}
	}
	localRoots := append([]string{}, r.localRoots...)
	r.mu.RUnlock()

	source = cleanSource(source)
	if err := validateSource(source, localRoots); err != nil {
		return err
	}
	backend, err := newSourceBackend(source)
	if err != nil {
		return err
	}
	if closer, ok := backend.(io.Closer); ok {
		defer closer.Close()
	}
	return backend.Test()
}

// Resolve maps a virtual path to its source and the path relative to the
// source root. The returned relative path never escapes the source.
func (r *SourceRegistry) Resolve(virtual string) (Source, string, bool) {
	relative, ok := sourceRelative(virtual)
	if !ok {
		return Source{}, "", false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	id := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = NormalizeRel(filepath.Join(parts[1:]...))
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, source := range r.sources {
		if source.ID == id {
			return source, rest, true
		}
	}
	return Source{}, "", false
}

// Backend resolves the backend for a virtual path.
func (r *SourceRegistry) Backend(virtual string) (sourceBackend, Source, string, error) {
	source, relative, ok := r.Resolve(virtual)
	if !ok {
		return nil, Source{}, "", ErrSourceNotFound
	}
	backend, err := r.backendFor(source)
	if err != nil {
		return nil, source, relative, err
	}
	return backend, source, relative, nil
}

// Stat returns the info of a path inside a source.
func (r *SourceRegistry) Stat(virtual string) (fs.FileInfo, error) {
	backend, _, relative, err := r.Backend(virtual)
	if err != nil {
		return nil, err
	}
	return backend.Stat(relative)
}

// ReadDir lists a directory inside a source.
func (r *SourceRegistry) ReadDir(virtual string) ([]fs.DirEntry, error) {
	backend, _, relative, err := r.Backend(virtual)
	if err != nil {
		return nil, err
	}
	return backend.ReadDir(relative)
}

// Open opens a file inside a source.
func (r *SourceRegistry) Open(virtual string) (io.ReadSeekCloser, error) {
	backend, _, relative, err := r.Backend(virtual)
	if err != nil {
		return nil, err
	}
	return backend.Open(relative)
}

// Close releases every cached backend (used on shutdown / by tests).
func (r *SourceRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, backend := range r.backends {
		if closer, ok := backend.(io.Closer); ok {
			_ = closer.Close()
		}
		delete(r.backends, id)
	}
}

func (r *SourceRegistry) backendFor(source Source) (sourceBackend, error) {
	r.mu.RLock()
	backend := r.backends[source.ID]
	r.mu.RUnlock()
	if backend != nil {
		return backend, nil
	}
	created, err := newSourceBackend(source)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if existing := r.backends[source.ID]; existing != nil {
		r.mu.Unlock()
		if closer, ok := created.(io.Closer); ok {
			_ = closer.Close()
		}
		return existing, nil
	}
	if r.backends == nil {
		r.backends = map[string]sourceBackend{}
	}
	r.backends[source.ID] = created
	r.mu.Unlock()
	return created, nil
}

func (r *SourceRegistry) newIDLocked() string {
	for {
		buffer := make([]byte, 5)
		if _, err := rand.Read(buffer); err != nil {
			// crypto/rand should never fail; a timestamp keeps the application
			// usable if it does.
			return strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		id := hex.EncodeToString(buffer)
		taken := false
		for _, source := range r.sources {
			if source.ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
	}
}

func (r *SourceRegistry) validateLocked(source Source) error {
	return validateSource(source, r.localRoots)
}

func validateSource(source Source, localRoots []string) error {
	switch source.Kind {
	case SourceKindLocal:
		if strings.TrimSpace(source.Path) == "" {
			return errors.New("a local source needs a folder path")
		}
		info, err := os.Stat(source.Path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("local source folder not found: %s", source.Path)
		}
		if len(localRoots) > 0 {
			allowed := false
			for _, root := range localRoots {
				if IsWithin(source.Path, root) {
					allowed = true
					break
				}
			}
			if !allowed {
				return errors.New("this folder is outside OFFLINEU_ROOTS; add it to OFFLINEU_ROOTS or configure it as a WebDAV/SMB source")
			}
		}
	case SourceKindWebDAV:
		parsed, err := url.Parse(source.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("a WebDAV source needs an http:// or https:// URL")
		}
	case SourceKindSMB:
		if strings.TrimSpace(source.Host) == "" {
			return errors.New("an SMB source needs a server host")
		}
		if strings.TrimSpace(source.Share) == "" {
			return errors.New("an SMB source needs a share name")
		}
	default:
		return errors.New("source kind must be local, webdav or smb")
	}
	return nil
}

func cleanSource(source Source) Source {
	source.ID = strings.TrimSpace(source.ID)
	source.Name = strings.TrimSpace(source.Name)
	source.Kind = strings.ToLower(strings.TrimSpace(source.Kind))
	source.Path = strings.TrimSpace(source.Path)
	source.URL = strings.TrimSpace(source.URL)
	source.Host = strings.TrimSpace(source.Host)
	source.Share = strings.Trim(strings.TrimSpace(source.Share), `\/`)
	source.BasePath = NormalizeRel(source.BasePath)
	source.Username = strings.TrimSpace(source.Username)
	source.Domain = strings.TrimSpace(source.Domain)
	if source.Kind == SourceKindLocal && source.Path != "" {
		expanded := ExpandHome(source.Path)
		if absolute, err := filepath.Abs(expanded); err == nil {
			expanded = absolute
		}
		if resolved, err := filepath.EvalSymlinks(expanded); err == nil {
			expanded = resolved
		}
		source.Path = filepath.Clean(expanded)
	}
	if source.Kind == SourceKindWebDAV && source.URL != "" {
		source.URL = strings.TrimRight(source.URL, "/")
	}
	if source.Port < 0 || source.Port > 65535 {
		source.Port = 0
	}
	return source
}

func defaultSourceName(source Source) string {
	switch source.Kind {
	case SourceKindLocal:
		return filepath.Base(filepath.Clean(source.Path))
	case SourceKindWebDAV:
		if parsed, err := url.Parse(source.URL); err == nil {
			name := path.Base(strings.TrimRight(parsed.Path, "/"))
			if name != "" && name != "." && name != "/" {
				return name
			}
			return parsed.Hostname()
		}
	case SourceKindSMB:
		name := source.Share
		if source.BasePath != "" {
			name = path.Base(source.BasePath)
		}
		if name != "" {
			return name
		}
		return source.Host
	}
	return "Course source"
}

func (s Source) info() SourceInfo {
	return SourceInfo{
		ID:          s.ID,
		Name:        s.Name,
		Kind:        s.Kind,
		Root:        SourceRoot(s.ID),
		Path:        s.Path,
		URL:         s.URL,
		Host:        s.Host,
		Port:        s.Port,
		Share:       s.Share,
		BasePath:    s.BasePath,
		Username:    s.Username,
		HasPassword: s.Password != "",
		InsecureTLS: s.InsecureTLS,
	}
}

func (r *SourceRegistry) saveLocked() error {
	if strings.TrimSpace(r.file) == "" {
		return errors.New("the source file is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(r.file), 0o755); err != nil {
		return err
	}
	document := sourceRegistryFile{Version: 1, Sources: r.sources}
	if document.Sources == nil {
		document.Sources = []Source{}
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	temp := r.file + ".tmp"
	if err := os.WriteFile(temp, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, r.file); err != nil {
		_ = os.Remove(temp)
		return err
	}
	_ = os.Chmod(r.file, 0o600)
	return nil
}

// sourceBackend is the small set of file operations the rest of OfflineU needs.
type sourceBackend interface {
	Stat(relative string) (fs.FileInfo, error)
	ReadDir(relative string) ([]fs.DirEntry, error)
	Open(relative string) (io.ReadSeekCloser, error)
	Test() error
}

func newSourceBackend(source Source) (sourceBackend, error) {
	switch source.Kind {
	case SourceKindLocal:
		return &localSourceBackend{root: source.Path}, nil
	case SourceKindWebDAV:
		return newWebDAVBackend(source)
	case SourceKindSMB:
		return &smbSourceBackend{source: source}, nil
	default:
		return nil, errors.New("unknown course source kind: " + source.Kind)
	}
}

// --- Local source ---------------------------------------------------------

type localSourceBackend struct {
	root string
}

func (b *localSourceBackend) resolve(relative string) (string, error) {
	full := filepath.Join(b.root, filepath.FromSlash(NormalizeRel(relative)))
	if relative != "" && !IsWithin(full, b.root) {
		return "", fs.ErrPermission
	}
	return full, nil
}

func (b *localSourceBackend) Stat(relative string) (fs.FileInfo, error) {
	full, err := b.resolve(relative)
	if err != nil {
		return nil, err
	}
	return os.Stat(full)
}

func (b *localSourceBackend) ReadDir(relative string) ([]fs.DirEntry, error) {
	full, err := b.resolve(relative)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(full)
}

func (b *localSourceBackend) Open(relative string) (io.ReadSeekCloser, error) {
	full, err := b.resolve(relative)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

func (b *localSourceBackend) Test() error {
	info, err := os.Stat(b.root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a folder: " + b.root)
	}
	return nil
}

// --- WebDAV source --------------------------------------------------------

type webDAVBackend struct {
	source Source
	base   *url.URL
	client *http.Client
}

func newWebDAVBackend(source Source) (*webDAVBackend, error) {
	base, err := url.Parse(source.URL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
	}
	if source.InsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 - explicit self-hosted option
	}
	return &webDAVBackend{
		source: source,
		base:   base,
		client: &http.Client{Transport: transport},
	}, nil
}

func (b *webDAVBackend) urlFor(relative string) string {
	copied := *b.base
	relative = NormalizeRel(relative)
	basePath := strings.TrimRight(copied.Path, "/")
	if relative == "" {
		copied.Path = basePath
		if copied.Path == "" {
			copied.Path = "/"
		}
	} else {
		copied.Path = basePath + "/" + relative
	}
	copied.RawPath = ""
	copied.Fragment = ""
	return copied.String()
}

func (b *webDAVBackend) newRequest(method, endpoint string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if b.source.Username != "" || b.source.Password != "" {
		request.SetBasicAuth(b.source.Username, b.source.Password)
	}
	return request, nil
}

// metadataRequest adds a deadline for PROPFIND. A GET must not have one: a
// video stream can legitimately run longer than any fixed timeout.
func (b *webDAVBackend) metadataRequest(method, endpoint string, body io.Reader) (*http.Request, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if b.source.Username != "" || b.source.Password != "" {
		request.SetBasicAuth(b.source.Username, b.source.Password)
	}
	return request, cancel, nil
}

func (b *webDAVBackend) propfind(relative, depth string) ([]davResource, error) {
	const body = `<?xml version="1.0" encoding="utf-8"?>` +
		`<d:propfind xmlns:d="DAV:"><d:prop>` +
		`<d:resourcetype/><d:getcontentlength/><d:getlastmodified/><d:displayname/>` +
		`</d:prop></d:propfind>`
	request, cancel, err := b.metadataRequest("PROPFIND", b.urlFor(relative), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer cancel()
	request.Header.Set("Depth", depth)
	request.Header.Set("Content-Type", "application/xml; charset=utf-8")
	response, err := b.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fs.ErrNotExist
	}
	if response.StatusCode != http.StatusMultiStatus && response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WebDAV PROPFIND failed: %s", response.Status)
	}
	limited := io.LimitReader(response.Body, 4<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	document := davMultistatus{}
	if len(data) > 0 {
		if err := xml.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("cannot read WebDAV response: %w", err)
		}
	}
	return document.Resources, nil
}

type davMultistatus struct {
	Resources []davResource `xml:"response"`
}

type davResource struct {
	Href  string `xml:"href"`
	Props []struct {
		Status string `xml:"status"`
		Prop   struct {
			DisplayName   string `xml:"displayname"`
			ContentLength string `xml:"getcontentlength"`
			LastModified  string `xml:"getlastmodified"`
			ResourceType  struct {
				Collection *struct{} `xml:"collection"`
			} `xml:"resourcetype"`
		} `xml:"prop"`
	} `xml:"propstat"`
}

func (r davResource) succeeded() bool {
	if len(r.Props) == 0 {
		return true
	}
	for _, prop := range r.Props {
		if strings.Contains(prop.Status, " 200 ") || strings.HasSuffix(prop.Status, " 200") {
			return true
		}
	}
	return false
}

func (r davResource) isDir() bool {
	for _, prop := range r.Props {
		if strings.Contains(prop.Status, " 200 ") && prop.Prop.ResourceType.Collection != nil {
			return true
		}
	}
	return false
}

func (r davResource) size() int64 {
	for _, prop := range r.Props {
		if strings.Contains(prop.Status, " 200 ") {
			if value, err := strconv.ParseInt(strings.TrimSpace(prop.Prop.ContentLength), 10, 64); err == nil {
				return value
			}
		}
	}
	return 0
}

func (r davResource) modTime() time.Time {
	for _, prop := range r.Props {
		if strings.Contains(prop.Status, " 200 ") {
			if parsed, err := http.ParseTime(strings.TrimSpace(prop.Prop.LastModified)); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

// relativeHref turns a PROPFIND href into the path part, decoded.
func relativeHref(href string) string {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	value := parsed.Path
	if value == "" {
		value = parsed.Opaque
	}
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return strings.TrimRight(value, "/")
}

func (b *webDAVBackend) rootPath() string {
	value := strings.TrimRight(b.base.Path, "/")
	if value == "" {
		return "/"
	}
	return value
}

func (b *webDAVBackend) resourceInfo(resource davResource, fallbackName string) fs.FileInfo {
	name := fallbackName
	if name == "" {
		name = path.Base(relativeHref(resource.Href))
	}
	mode := fs.FileMode(0o644)
	if resource.isDir() {
		mode = fs.ModeDir | 0o755
	}
	return sourceFileInfo{name: name, size: resource.size(), mode: mode, modTime: resource.modTime()}
}

func (b *webDAVBackend) Stat(relative string) (fs.FileInfo, error) {
	resources, err := b.propfind(relative, "0")
	if err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		return nil, fs.ErrNotExist
	}
	basePath := b.rootPath()
	wanted := basePath
	if relative != "" {
		wanted = strings.TrimRight(basePath, "/") + "/" + NormalizeRel(relative)
	}
	for _, resource := range resources {
		if relativeHref(resource.Href) != strings.TrimRight(wanted, "/") {
			continue
		}
		if !resource.succeeded() {
			return nil, fs.ErrNotExist
		}
		name := path.Base(NormalizeRel(relative))
		if relative == "" {
			name = b.source.Name
		}
		return b.resourceInfo(resource, name), nil
	}
	// Some servers answer Depth 0 with a redirect-style href. A single
	// successful resource is still the thing that was asked for.
	if len(resources) == 1 && resources[0].succeeded() {
		name := path.Base(NormalizeRel(relative))
		if relative == "" {
			name = b.source.Name
		}
		return b.resourceInfo(resources[0], name), nil
	}
	return nil, fs.ErrNotExist
}

func (b *webDAVBackend) ReadDir(relative string) ([]fs.DirEntry, error) {
	resources, err := b.propfind(relative, "1")
	if err != nil {
		return nil, err
	}
	basePath := b.rootPath()
	if relative != "" {
		basePath = strings.TrimRight(basePath, "/") + "/" + NormalizeRel(relative)
	}
	basePath = strings.TrimRight(basePath, "/")
	entries := []fs.DirEntry{}
	for _, resource := range resources {
		if !resource.succeeded() {
			continue
		}
		href := relativeHref(resource.Href)
		if href == "" || href == basePath {
			continue
		}
		prefix := basePath + "/"
		if basePath == "" {
			prefix = "/"
		}
		if !strings.HasPrefix(href, prefix) {
			continue
		}
		rest := strings.TrimPrefix(href, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		name := path.Base(rest)
		info := b.resourceInfo(resource, name)
		entries = append(entries, fs.FileInfoToDirEntry(info))
	}
	sort.SliceStable(entries, func(i, j int) bool { return compareNames(entries[i].Name(), entries[j].Name()) < 0 })
	return entries, nil
}

func (b *webDAVBackend) Test() error {
	_, err := b.Stat("")
	return err
}

func (b *webDAVBackend) Open(relative string) (io.ReadSeekCloser, error) {
	info, err := b.Stat(relative)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("is a directory")
	}
	return &davFile{
		backend: b,
		url:     b.urlFor(relative),
		size:    info.Size(),
		name:    info.Name(),
	}, nil
}

type davFile struct {
	backend *webDAVBackend
	url     string
	size    int64
	name    string
	pos     int64
	body    io.ReadCloser
	start   int64
}

func (f *davFile) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if f.size >= 0 && f.pos >= f.size {
		return 0, io.EOF
	}
	if f.body == nil {
		if err := f.openAt(f.pos); err != nil {
			return 0, err
		}
	}
	read, err := f.body.Read(buffer)
	f.pos += int64(read)
	if err == io.EOF {
		f.closeBody()
		if f.size >= 0 && f.pos >= f.size {
			return read, io.EOF
		}
	}
	return read, err
}

func (f *davFile) openAt(offset int64) error {
	request, err := f.backend.newRequest(http.MethodGet, f.url, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := f.backend.client.Do(request)
	if err != nil {
		return err
	}
	switch response.StatusCode {
	case http.StatusPartialContent:
		f.body = response.Body
		f.start = offset
		return nil
	case http.StatusOK:
		// The server ignored Range. Skip the bytes already consumed.
		if offset > 0 {
			if _, err := io.CopyN(io.Discard, response.Body, offset); err != nil && err != io.EOF {
				response.Body.Close()
				return err
			}
		}
		f.body = response.Body
		f.start = offset
		return nil
	case http.StatusRequestedRangeNotSatisfiable:
		response.Body.Close()
		return io.EOF
	default:
		response.Body.Close()
		return fmt.Errorf("WebDAV GET failed: %s", response.Status)
	}
}

func (f *davFile) Seek(offset int64, whence int) (int64, error) {
	target := f.pos
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target += offset
	case io.SeekEnd:
		target = f.size + offset
	default:
		return 0, errors.New("invalid seek whence")
	}
	if target < 0 {
		return 0, errors.New("negative seek position")
	}
	if target != f.pos {
		f.closeBody()
	}
	f.pos = target
	return target, nil
}

func (f *davFile) Close() error {
	f.closeBody()
	return nil
}

func (f *davFile) closeBody() {
	if f.body != nil {
		_ = f.body.Close()
		f.body = nil
	}
}

// --- SMB source -----------------------------------------------------------

type smbSourceBackend struct {
	source  Source
	mu      sync.Mutex
	conn    net.Conn
	session *smb2.Session
	share   *smb2.Share
}

func (b *smbSourceBackend) address() string {
	host := strings.TrimSpace(b.source.Host)
	if host == "" {
		host = "localhost"
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	port := b.source.Port
	if port <= 0 {
		port = 445
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func (b *smbSourceBackend) mountHost() string {
	host := strings.TrimSpace(b.source.Host)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return strings.Trim(host, "[]")
}

func (b *smbSourceBackend) mounted() (*smb2.Share, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.share != nil {
		return b.share, nil
	}
	connection, err := net.DialTimeout("tcp", b.address(), 20*time.Second)
	if err != nil {
		return nil, err
	}
	user := b.source.Username
	if user == "" {
		user = "Guest"
	}
	dialer := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     user,
			Password: b.source.Password,
			Domain:   b.source.Domain,
		},
	}
	session, err := dialer.Dial(connection)
	if err != nil {
		connection.Close()
		return nil, err
	}
	sharePath := `\\` + b.mountHost() + `\` + b.source.Share
	share, err := session.Mount(sharePath)
	if err != nil {
		_ = session.Logoff()
		connection.Close()
		return nil, err
	}
	b.conn = connection
	b.session = session
	b.share = share
	return share, nil
}

func (b *smbSourceBackend) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.share != nil {
		_ = b.share.Umount()
		b.share = nil
	}
	if b.session != nil {
		_ = b.session.Logoff()
		b.session = nil
	}
	if b.conn != nil {
		_ = b.conn.Close()
		b.conn = nil
	}
}

func (b *smbSourceBackend) Close() error {
	b.reset()
	return nil
}

func (b *smbSourceBackend) pathFor(relative string) string {
	parts := []string{}
	if base := NormalizeRel(b.source.BasePath); base != "" {
		parts = append(parts, base)
	}
	if rel := NormalizeRel(relative); rel != "" {
		parts = append(parts, rel)
	}
	return strings.ReplaceAll(strings.Join(parts, "/"), "/", `\`)
}

func (b *smbSourceBackend) withShare(operation func(*smb2.Share) error) error {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		share, err := b.mounted()
		if err != nil {
			last = err
			b.reset()
			continue
		}
		if err := operation(share); err != nil {
			last = err
			// A dropped connection is the one case worth reconnecting for. A
			// missing path is retried too - it is one cheap request and keeps
			// the retry rule simple.
			b.reset()
			continue
		}
		return nil
	}
	return last
}

func (b *smbSourceBackend) Stat(relative string) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := b.withShare(func(share *smb2.Share) error {
		found, err := share.Stat(b.pathFor(relative))
		if err != nil {
			return err
		}
		info = found
		return nil
	})
	return info, err
}

func (b *smbSourceBackend) ReadDir(relative string) ([]fs.DirEntry, error) {
	var entries []fs.DirEntry
	err := b.withShare(func(share *smb2.Share) error {
		found, err := share.ReadDir(b.pathFor(relative))
		if err != nil {
			return err
		}
		entries = make([]fs.DirEntry, 0, len(found))
		for _, info := range found {
			entries = append(entries, fs.FileInfoToDirEntry(info))
		}
		return nil
	})
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return compareNames(entries[i].Name(), entries[j].Name()) < 0
	})
	return entries, err
}

func (b *smbSourceBackend) Open(relative string) (io.ReadSeekCloser, error) {
	share, err := b.mounted()
	if err != nil {
		return nil, err
	}
	handle, err := share.Open(b.pathFor(relative))
	if err != nil {
		b.reset()
		share, retryErr := b.mounted()
		if retryErr != nil {
			return nil, err
		}
		handle, err = share.Open(b.pathFor(relative))
	}
	if err != nil {
		return nil, err
	}
	return handle, nil
}

func (b *smbSourceBackend) Test() error {
	var info fs.FileInfo
	err := b.withShare(func(share *smb2.Share) error {
		found, err := share.Stat(b.pathFor(""))
		if err != nil {
			return err
		}
		info = found
		return nil
	})
	if err != nil {
		return err
	}
	if info == nil || !info.IsDir() {
		return errors.New("the configured SMB folder is not a directory")
	}
	return nil
}

// sourceFileInfo is the minimal fs.FileInfo implementation for WebDAV entries.
type sourceFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (i sourceFileInfo) Name() string       { return i.name }
func (i sourceFileInfo) Size() int64        { return i.size }
func (i sourceFileInfo) Mode() fs.FileMode  { return i.mode }
func (i sourceFileInfo) ModTime() time.Time { return i.modTime }
func (i sourceFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i sourceFileInfo) Sys() any           { return nil }

// --- Config integration ---------------------------------------------------

// SourcesFile is where the configured sources are stored.
func (c *Config) SourcesFile() string {
	dir := strings.TrimSpace(c.StateDir)
	if dir == "" {
		dir = deriveStateDir(c.ProgressDir)
	}
	return filepath.Join(ExpandHome(dir), "sources.json")
}

// ensureSources lazily creates the registry for Config values built by hand
// (tests) and refreshes the local-folder allow-list.
func (c *Config) ensureSources() {
	if c.Sources == nil {
		c.Sources = NewSourceRegistry(c.SourcesFile())
	}
	c.Sources.SetLocalRoots(c.Roots)
}

// SourceInfos returns the public source list.
func (c *Config) SourceInfos() []SourceInfo {
	c.ensureSources()
	return c.Sources.List()
}

// SourceFor returns the configured source that owns a virtual path.
func (c *Config) SourceFor(virtual string) (Source, string, bool) {
	c.ensureSources()
	return c.Sources.Resolve(virtual)
}

// SourceDisplayPath is the user-facing name of a virtual source path.
func (c *Config) SourceDisplayPath(virtual string) (string, bool) {
	source, relative, ok := c.SourceFor(virtual)
	if !ok {
		return "", false
	}
	if relative == "" {
		return source.Name, true
	}
	return source.Name + "/" + relative, true
}

// IsRemotePath reports whether path belongs to a configured course source
// (including a local source, which is served through the virtual namespace).
func (c *Config) IsRemotePath(path string) bool {
	return IsSourcePath(path)
}

// StatPath is os.Stat for both local and source paths.
func (c *Config) StatPath(path string) (fs.FileInfo, error) {
	if c.IsRemotePath(path) {
		c.ensureSources()
		return c.Sources.Stat(path)
	}
	return os.Stat(path)
}

// ReadDirPath is os.ReadDir for both local and source paths.
func (c *Config) ReadDirPath(path string) ([]fs.DirEntry, error) {
	if c.IsRemotePath(path) {
		c.ensureSources()
		return c.Sources.ReadDir(path)
	}
	return os.ReadDir(path)
}

// OpenPath is os.Open for both local and source paths.
func (c *Config) OpenPath(path string) (io.ReadSeekCloser, error) {
	if c.IsRemotePath(path) {
		c.ensureSources()
		return c.Sources.Open(path)
	}
	return os.Open(path)
}

// ReadFilePath reads a whole file (bounded by limit when it is positive) from a
// local or source path.
func (c *Config) ReadFilePath(path string, limit int64) ([]byte, error) {
	if !c.IsRemotePath(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if limit > 0 && int64(len(data)) > limit {
			return data[:limit], nil
		}
		return data, nil
	}
	handle, err := c.OpenPath(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	var reader io.Reader = handle
	if limit > 0 {
		reader = io.LimitReader(handle, limit)
	}
	return io.ReadAll(reader)
}

// ResolveCoursePath resolves a course-relative path inside a local folder or a
// virtual source without ever touching the OS for a source path.
func (c *Config) ResolveCoursePath(coursePath, relative string) (string, bool) {
	relative = NormalizeRel(relative)
	for _, part := range strings.Split(relative, "/") {
		if part == ".." {
			// filepath.Join silently clamps ".." at a Windows UNC share root; a
			// course-relative path must never pretend a sibling is a child.
			return "", false
		}
	}
	if relative == "" {
		if info, err := c.StatPath(coursePath); err == nil && info.IsDir() {
			return filepath.Clean(coursePath), true
		}
		return "", false
	}
	if !c.IsRemotePath(coursePath) {
		return ResolveInside(coursePath, relative)
	}
	base := filepath.Clean(coursePath)
	full := filepath.Join(base, filepath.FromSlash(relative))
	inside, err := filepath.Rel(base, full)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", false
	}
	if _, _, ok := c.SourceFor(full); !ok {
		return "", false
	}
	return full, true
}

// CourseBaseName names a course path for the UI. Source roots use the friendly
// source name instead of the random virtual id.
func (c *Config) CourseBaseName(path string) string {
	if source, relative, ok := c.SourceFor(path); ok {
		if relative == "" {
			return source.Name
		}
		return filepath.Base(filepath.FromSlash(relative))
	}
	return filepath.Base(filepath.Clean(path))
}

// ParentPath returns the parent of a local or source path. Source roots have no
// parent inside the virtual namespace, so the picker's "up" button stops there.
func (c *Config) ParentPath(path string) (string, bool) {
	if c.IsRemotePath(path) {
		if source, relative, ok := c.SourceFor(path); ok && relative == "" {
			_ = source
			return "", false
		}
		parent := filepath.Dir(filepath.Clean(path))
		if !IsSourcePath(parent) {
			return "", false
		}
		return parent, true
	}
	parent := filepath.Dir(filepath.Clean(path))
	if samePath(parent, path) || !c.InsideRoots(parent) {
		return "", false
	}
	return parent, true
}

// sourceSortedEntries mirrors sortedEntries for a Config (and therefore also
// for remote sources).
func (c *Config) sortedEntries(dir string) ([]fs.DirEntry, error) {
	entries, err := c.ReadDirPath(dir)
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

// sourceCountMediaFiles mirrors countMediaFiles for a Config.
func (c *Config) sourceCountMediaFiles(root string, limit int) (int, error) {
	initial, err := c.ReadDirPath(root)
	if err != nil {
		return 0, err
	}
	media := 0
	seen := 0
	stack := [][]fs.DirEntry{initial}
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
				children, err := c.ReadDirPath(child)
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
