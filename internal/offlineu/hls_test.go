package offlineu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The segmentation maths is what makes copying safe: every boundary has to land
// on a keyframe, or two neighbouring pieces would overlap or leave a gap.
func TestSegmentsStartOnKeyframesAndCoverTheFile(t *testing.T) {
	keyframes := []float64{2, 4, 6, 8, 10, 12, 14, 16, 18}
	segments := segmentsFromKeyframes(keyframes, 20, 6)
	if len(segments) != 4 {
		t.Fatalf("segments = %+v", segments)
	}
	covered := 0.0
	previous := 0.0
	for _, segment := range segments {
		if segment.Start != previous {
			t.Errorf("segment %+v does not follow %v", segment, previous)
		}
		if !isKeyframe(keyframes, segment.Start) && segment.Start != 0 {
			t.Errorf("segment %+v does not start on a keyframe", segment)
		}
		covered += segment.Duration
		previous = segment.Start + segment.Duration
	}
	if covered != 20 {
		t.Errorf("the pieces cover %v s of a 20 s file", covered)
	}
}

func isKeyframe(keyframes []float64, value float64) bool {
	for _, candidate := range keyframes {
		if candidate == value {
			return true
		}
	}
	return false
}

// Without a keyframe list (no ffprobe) a lesson is re-encoded, which seeks
// exactly - so an even grid is correct there.
func TestFixedGridCoversTheFile(t *testing.T) {
	segments := fixedGrid(20, 6)
	if len(segments) != 4 {
		t.Fatalf("segments = %+v", segments)
	}
	if segments[0].Start != 0 || segments[3].Start != 18 || segments[3].Duration != 2 {
		t.Errorf("segments = %+v", segments)
	}
	// A file shorter than one piece still gets exactly one piece.
	if single := fixedGrid(3, 6); len(single) != 1 || single[0].Duration != 3 {
		t.Errorf("short file = %+v", single)
	}
}

func TestPlaylistListsEverySegment(t *testing.T) {
	index := &hlsIndex{
		Version:   hlsIndexVersion,
		Duration:  20,
		CopyVideo: true,
		CopyAudio: true,
		Segments:  fixedGrid(20, 6),
	}
	playlist := renderPlaylist(index, func(position int) string {
		return "/api/hls/segment?lesson=x&i=" + string(rune('0'+position))
	})
	for _, expected := range []string{
		"#EXTM3U", "#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-TARGETDURATION:6",
		"#EXTINF:6.000,", "#EXT-X-ENDLIST", "/api/hls/segment?lesson=x&i=3",
	} {
		if !strings.Contains(playlist, expected) {
			t.Errorf("%q missing from the playlist:\n%s", expected, playlist)
		}
	}
}

// A stored plan is only used while the file is the same file.
func TestStoredIndexIsRejectedWhenTheFileChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, hlsIndexFile)
	index := &hlsIndex{Version: hlsIndexVersion, Segments: fixedGrid(20, 6)}
	stat := fakeFileInfo{size: 4096, modTime: time.Unix(1000, 0)}
	if err := saveHLSIndex(path, index, stat); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if _, ok := loadHLSIndex(path, stat.size, stat.modTime); !ok {
		t.Fatal("the plan was not read back")
	}
	if _, ok := loadHLSIndex(path, stat.size, time.Unix(2000, 0)); ok {
		t.Error("a replaced file must not reuse the old plan")
	}
	if _, ok := loadHLSIndex(path, 8192, stat.modTime); ok {
		t.Error("a rewritten file must not reuse the old plan")
	}
}

// The probe cache is what keeps a browser from demuxing a big file on every
// Range request.
func TestProbeCacheForgetsWhenTheFileChanges(t *testing.T) {
	cache := newProbeCache()
	modTime := time.Unix(1000, 0)
	facts := fileFacts{Container: "matroska,webm", Info: MediaInfo{VideoCodec: "h264", Duration: 20}}
	cache.put("lesson.mkv", 4096, modTime, facts, nil)

	if _, _, ok := cache.get("lesson.mkv", 4096, modTime); !ok {
		t.Fatal("the answer was not remembered")
	}
	if _, _, ok := cache.get("lesson.mkv", 4096, time.Unix(2000, 0)); ok {
		t.Error("a changed file must be probed again")
	}
	if _, _, ok := cache.get("lesson.mkv", 8192, modTime); ok {
		t.Error("a rewritten file must be probed again")
	}
	if _, _, ok := cache.get("other.mkv", 4096, modTime); ok {
		t.Error("another lesson has its own answer")
	}
}

// Two requests for the same conversion must share one ffmpeg run.
func TestIdenticalConversionsShareOneJob(t *testing.T) {
	registry := newJobRegistry()
	runs := 0
	start := make(chan struct{})
	job := registry.run("lesson", func() (string, error) {
		runs++
		<-start
		return "converted.mp4", nil
	})
	if second := registry.run("lesson", func() (string, error) {
		runs++
		return "", nil
	}); second != job {
		t.Error("the second caller started its own job")
	}
	close(start)
	if !job.wait(context.Background(), time.Second) {
		t.Fatal("the job never finished")
	}
	if runs != 1 {
		t.Errorf("ffmpeg would have run %d times", runs)
	}
	if job.path != "converted.mp4" {
		t.Errorf("path = %q", job.path)
	}
}

// A failing job is dropped so the next request retries it.
func TestFailedJobIsRetried(t *testing.T) {
	registry := newJobRegistry()
	job := registry.run("lesson", func() (string, error) { return "", os.ErrNotExist })
	if !job.wait(context.Background(), time.Second) || job.err == nil {
		t.Fatal("the job should have failed")
	}
	if _, ok := registry.Failure("lesson"); !ok {
		t.Error("the failure was not remembered")
	}
	if again := registry.run("lesson", func() (string, error) { return "ok.mp4", nil }); again == job {
		t.Error("a failed job must not be handed out again")
	}
}

type fakeFileInfo struct {
	size    int64
	modTime time.Time
}

func (f fakeFileInfo) Name() string       { return "lesson.mkv" }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o644 }
func (f fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }
