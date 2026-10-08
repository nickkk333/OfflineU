package offlineu

// Housekeeping for everything OfflineU keeps on disk: the folder converted
// media lands in, the budget that folder may use, and the memory of what an
// ffmpeg probe already told us about a file.
//
// All three exist for the same reason: a low powered NAS (a repurposed mini PC
// running fnOS/飞牛OS, for instance) cannot afford to re-read a multi gigabyte
// lesson on every request. Probing a big Matroska file means demuxing it, and
// a remuxed copy of it costs as much disk as the original - so the answer of a
// probe is remembered until the file changes, and the copies are capped and
// thrown away oldest first.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The two subdirectories of the cache root that hold converted media. Anything
// else in the cache root (the unpacked or downloaded ffmpeg, for example) is
// never touched by the trimmer.
const (
	browserCacheSubdir = "browser"
	hlsCacheSubdir     = "hls"
)

const (
	// DefaultCacheLimitBytes is the budget a fresh install gives the converted
	// media: enough for a handful of lessons, small enough that it can never
	// fill the system disk of a NAS.
	DefaultCacheLimitBytes = 20 << 30 // 20 GiB
	// After a trim the cache keeps this share of its budget, so the next
	// conversions do not immediately trigger another walk of the folder.
	cacheTrimTarget = 0.75
	// A file this fresh is left alone: it is very likely the one that is being
	// written or read right now.
	cacheProtectedAge = 2 * time.Minute
	// Trimming walks the whole cache, so it is rate limited.
	cacheTrimInterval = 30 * time.Second
)

// probeCacheLimit caps how many files are remembered. A large course library
// must not grow the map without bound.
const probeCacheLimit = 4096

// cacheTrimmer keeps the media cache below its budget, oldest file first.
type cacheTrimmer struct {
	dir   string
	limit int64

	mu      sync.Mutex
	lastRun time.Time
}

func newCacheTrimmer(dir string, limit int64) *cacheTrimmer {
	return &cacheTrimmer{dir: dir, limit: limit}
}

// Touch marks a cached file as recently used so the trimmer throws away
// something else first.
func (c *cacheTrimmer) Touch(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// Trim enforces the budget. It is cheap when nothing has to happen: the walk
// only runs when the cache could plausibly be over its limit, and at most once
// per cacheTrimInterval.
func (c *cacheTrimmer) Trim() {
	if c == nil || c.limit <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.lastRun) < cacheTrimInterval {
		return
	}
	c.lastRun = time.Now()
	c.trimLocked()
}

// trimLocked deletes the oldest cached files until the cache is back under
// three quarters of its budget.
func (c *cacheTrimmer) trimLocked() {
	type entry struct {
		path    string
		size    int64
		modTime time.Time
	}
	entries := []entry{}
	total := int64(0)
	for _, sub := range []string{browserCacheSubdir, hlsCacheSubdir} {
		root := filepath.Join(c.dir, sub)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			// A half written file is not a candidate: the rename into place
			// has not happened yet.
			if strings.HasSuffix(path, ".tmp") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, entry{path: path, size: info.Size(), modTime: info.ModTime()})
			total += info.Size()
			return nil
		})
	}
	if total <= c.limit {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].modTime.Before(entries[j].modTime) })
	target := int64(float64(c.limit) * cacheTrimTarget)
	cutoff := time.Now().Add(-cacheProtectedAge)
	removed := int64(0)
	for _, candidate := range entries {
		if total <= target {
			break
		}
		if candidate.modTime.After(cutoff) {
			continue
		}
		if err := os.Remove(candidate.path); err != nil {
			continue
		}
		total -= candidate.size
		removed += candidate.size
	}
	if removed > 0 {
		logf("cache: trimmed %d MiB of converted media (limit %d MiB)",
			removed>>20, c.limit>>20)
	}
}

// fileFacts is what one ffprobe/ffmpeg run told us about a lesson file: the
// container it really is and the streams inside it.
type fileFacts struct {
	Container string
	Info      MediaInfo
}

// probeCache remembers fileFacts until the file changes on disk. The identity
// of a file is its size plus its modification time - cheap to read, and enough
// to notice that a lesson was replaced.
type probeCache struct {
	mu      sync.RWMutex
	entries map[string]probeEntry
}

type probeEntry struct {
	size    int64
	modTime time.Time
	facts   fileFacts
	err     error
}

func newProbeCache() *probeCache {
	return &probeCache{entries: map[string]probeEntry{}}
}

// get returns the remembered answer for a file, and whether there was one.
// Failures are cached too: a file ffmpeg cannot read will not be read again on
// the next Range request either.
func (c *probeCache) get(key string, size int64, modTime time.Time) (fileFacts, error, bool) {
	c.mu.RLock()
	entry, found := c.entries[key]
	c.mu.RUnlock()
	if !found || entry.size != size || !entry.modTime.Equal(modTime) {
		return fileFacts{}, nil, false
	}
	return entry.facts, entry.err, true
}

func (c *probeCache) put(key string, size int64, modTime time.Time, facts fileFacts, err error) {
	c.mu.Lock()
	if len(c.entries) >= probeCacheLimit {
		// Drop a chunk of the map; which entries go is irrelevant because
		// every miss is only a probe away.
		removed := 0
		for candidate := range c.entries {
			delete(c.entries, candidate)
			removed++
			if removed >= probeCacheLimit/4 {
				break
			}
		}
	}
	c.entries[key] = probeEntry{size: size, modTime: modTime, facts: facts, err: err}
	c.mu.Unlock()
}

// conversionJob is one running (or finished) ffmpeg conversion.
type conversionJob struct {
	done  chan struct{}
	path  string
	err   error
	start time.Time
}

// jobRegistry makes sure the same conversion never runs twice: the first
// caller starts it, everybody else waits for its result. Without it a browser
// that asks for a lesson twice (or two browsers on the same lesson) would each
// spawn their own ffmpeg and fight for the CPU of a small NAS.
type jobRegistry struct {
	mu      sync.Mutex
	jobs    map[string]*conversionJob
	failure map[string]error // why the last attempt of a key gave up
}

func newJobRegistry() *jobRegistry {
	return &jobRegistry{jobs: map[string]*conversionJob{}, failure: map[string]error{}}
}

// Failure reports why the last background conversion of a key failed. The
// caller uses it to stop waiting and to tell the browser why.
func (r *jobRegistry) Failure(key string) (error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	err, found := r.failure[key]
	return err, found
}

// run starts convert unless an identical job is already running, and returns
// the job either way. A failed job is dropped from the registry so the next
// request retries it.
func (r *jobRegistry) run(key string, convert func() (string, error)) *conversionJob {
	r.mu.Lock()
	if existing, ok := r.jobs[key]; ok {
		r.mu.Unlock()
		return existing
	}
	job := &conversionJob{done: make(chan struct{}), start: time.Now()}
	r.jobs[key] = job
	r.mu.Unlock()

	go func() {
		path, err := convert()
		job.path = path
		job.err = err
		close(job.done)
		r.mu.Lock()
		if err != nil {
			r.failure[key] = err
			// Only drop the job that is still registered - a retry may have
			// already replaced it.
			if current, ok := r.jobs[key]; ok && current == job {
				delete(r.jobs, key)
			}
		} else {
			delete(r.failure, key)
		}
		r.mu.Unlock()
	}()
	return job
}

// wait blocks until the job finishes, the context is done or timeout passes.
// It reports whether the job is finished.
func (j *conversionJob) wait(ctx context.Context, timeout time.Duration) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	select {
	case <-j.done:
		return true
	case <-ctx.Done():
		return false
	}
}

// ErrUnreadable is returned when a media file cannot be inspected at all.
var ErrUnreadable = errors.New("the file could not be read")
