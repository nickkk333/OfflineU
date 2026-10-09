package offlineu

// HTTP Live Streaming for lessons a browser cannot play from the file as it is.
//
// The previous approach - remuxing the whole lesson into an MP4 before the
// first byte reaches the browser - is what makes a big HD file stutter on a
// weak NAS: the server reads and writes gigabytes while the player waits, and a
// file whose codecs the browser rejects is even re-encoded from start to end.
//
// HLS turns that into work the size of a single segment. The lesson is cut into
// a few hundred short pieces whose boundaries sit on keyframes, and the browser
// only asks for the pieces it is about to play. Starting playback costs one
// segment (a couple of seconds of copying), jumping into the middle of a lesson
// costs one segment too, and pieces are cached so a second visit is free.
//
// ffmpeg is invoked per segment; when the streams are already H.264/AAC (the
// usual case for an .mkv) it copies them, which is demuxing and muxing only -
// a fraction of one core even on a repurposed mini PC.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// How long a segment aims to be. Shorter segments start faster but mean
	// more requests; longer ones buffer better but cost more to jump into.
	hlsTargetSegment = 6.0
	// A segment shorter than this is not worth a request of its own.
	hlsMinSegment = 0.25
	// Building the index means demuxing the whole file once, so it is given a
	// generous budget - and it is cached on disk afterwards.
	hlsIndexTimeout = 10 * time.Minute
	// How long a caller waits for an index somebody else is already building.
	// Reading the keyframes of a long lesson means demuxing it once, and the
	// browser is told to retry instead of hanging on a request forever.
	hlsIndexWait = 2 * time.Minute
	// One segment is a few seconds of video; this is plenty even while another
	// conversion is competing for the CPU.
	hlsSegmentTimeout = 5 * time.Minute
	// Bumped whenever the shape of the stored index changes, so an old cache is
	// rebuilt instead of misread.
	hlsIndexVersion = 1
	hlsIndexFile    = "index.json"
)

// Codecs a browser decodes inside an HLS stream. Everything else is re-encoded
// - but only the stream that needs it: a lesson with H.264 video and DTS audio
// keeps its video and only the audio is converted.
var hlsSafeVideoCodecs = []string{"h264"}
var hlsSafeAudioCodecs = []string{"aac", "mp3", "ac3"}

// ErrHLSNotReady is returned while the index or the segment is still being
// built, so the caller can answer with a retry instead of an error.
var ErrHLSNotReady = errors.New("the stream is still being prepared")

// hlsSegment is one piece of a lesson: start offset and length, in seconds.
type hlsSegment struct {
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
}

// hlsIndex is the plan of one lesson: how long it is, which streams can be
// copied and where the pieces begin.
type hlsIndex struct {
	Version    int          `json:"version"`
	Duration   float64      `json:"duration"`
	CopyVideo  bool         `json:"copy_video"`
	CopyAudio  bool         `json:"copy_audio"`
	VideoCodec string       `json:"video_codec,omitempty"`
	AudioCodec string       `json:"audio_codec,omitempty"`
	Height     int          `json:"height,omitempty"`
	Segments   []hlsSegment `json:"segments"`
}

// storedHLSIndex is the index plus the identity of the file it belongs to, so a
// rebuilt or replaced lesson is noticed instead of served from a stale plan.
type storedHLSIndex struct {
	Version int       `json:"version"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Index   hlsIndex  `json:"index"`
}

// hlsIndexEntry is a plan held in memory, together with the file it was made
// from.
type hlsIndexEntry struct {
	size    int64
	modTime time.Time
	index   *hlsIndex
}

// hlsDir is the folder holding the segments and the index of one lesson.
func (t *Transcoder) hlsDir(file string) string {
	return filepath.Join(t.CacheDir(), hlsCacheSubdir, sha1Hex(t.cacheKey(file)))
}

// HLSIndex returns the segment plan of a file, building it when it is missing.
// The answer is kept in memory and on disk until the file changes.
func (t *Transcoder) HLSIndex(ctx context.Context, file string) (*hlsIndex, error) {
	stat, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	key := filepath.Clean(file)

	if entry, ok := t.hlsIndexMemory(key, stat.Size(), stat.ModTime()); ok {
		return entry, nil
	}
	if entry, ok := loadHLSIndex(filepath.Join(t.hlsDir(file), hlsIndexFile), stat.Size(), stat.ModTime()); ok {
		t.rememberHLSIndex(key, stat, entry)
		return entry, nil
	}

	job := t.registry().run("hls-index:"+key, func() (string, error) {
		index, buildErr := t.buildHLSIndex(context.Background(), file, stat)
		if buildErr != nil {
			return "", buildErr
		}
		_ = saveHLSIndex(filepath.Join(t.hlsDir(file), hlsIndexFile), index, stat)
		t.rememberHLSIndex(key, stat, index)
		return "", nil
	})
	if !job.wait(ctx, hlsIndexWait) {
		return nil, ErrHLSNotReady
	}
	if job.err != nil {
		return nil, job.err
	}
	if entry, ok := t.hlsIndexMemory(key, stat.Size(), stat.ModTime()); ok {
		return entry, nil
	}
	return nil, ErrHLSNotReady
}

func (t *Transcoder) hlsIndexMemory(key string, size int64, modTime time.Time) (*hlsIndex, bool) {
	t.hlsMu.Lock()
	defer t.hlsMu.Unlock()
	entry, found := t.hlsIndexes[key]
	if !found || entry.size != size || !entry.modTime.Equal(modTime) {
		return nil, false
	}
	return entry.index, true
}

func (t *Transcoder) rememberHLSIndex(key string, stat os.FileInfo, index *hlsIndex) {
	t.hlsMu.Lock()
	if t.hlsIndexes == nil {
		t.hlsIndexes = map[string]hlsIndexEntry{}
	}
	t.hlsIndexes[key] = hlsIndexEntry{size: stat.Size(), modTime: stat.ModTime(), index: index}
	t.hlsMu.Unlock()
}

// buildHLSIndex reads the duration, the codecs and the keyframes of a file and
// turns them into the segment plan.
func (t *Transcoder) buildHLSIndex(ctx context.Context, file string, stat os.FileInfo) (*hlsIndex, error) {
	if !t.Available() {
		return nil, errors.New("ffmpeg is not installed")
	}
	facts, err := t.Inspect(ctx, file)
	if err != nil {
		return nil, err
	}
	info := facts.Info
	if !info.HasVideo() {
		return nil, errors.New("only lessons with a video stream are streamed as HLS")
	}
	if info.Duration <= 0 {
		return nil, errors.New("the length of this file is unknown")
	}

	index := &hlsIndex{
		Version:    hlsIndexVersion,
		Duration:   info.Duration,
		CopyVideo:  t.canCopyVideo(info, hlsSafeVideoCodecs),
		CopyAudio:  t.canCopyAudio(info, hlsSafeAudioCodecs),
		VideoCodec: info.VideoCodec,
		AudioCodec: info.AudioCodec,
		Height:     info.Height,
	}

	// Copying the video stream means cutting at keyframes: anywhere else the
	// pieces would overlap or leave a gap. Without a keyframe list the only
	// honest answer is to re-encode, which is exactly what a slow NAS must not
	// do - so the caller falls back to remuxing the whole file instead.
	keyframes, keyErr := t.keyframes(ctx, file)
	switch {
	case keyErr == nil && len(keyframes) > 0:
		index.Segments = segmentsFromKeyframes(keyframes, info.Duration, hlsTargetSegment)
	case !index.CopyVideo:
		index.Segments = fixedGrid(info.Duration, hlsTargetSegment)
	default:
		return nil, fmt.Errorf("no keyframes found in %s, so it cannot be cut without re-encoding", filepath.Base(file))
	}
	if len(index.Segments) == 0 {
		return nil, errors.New("the file could not be split into segments")
	}
	return index, nil
}

// keyframes lists the timestamps of the video keyframes of a file.
func (t *Transcoder) keyframes(ctx context.Context, file string) ([]float64, error) {
	if t.ffprobe == "" {
		return nil, errors.New("ffprobe is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, hlsIndexTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, t.ffprobe,
		"-v", "quiet", "-select_streams", "v:0", "-skip_frame", "nokey",
		"-show_frames", "-show_entries", "frame=pts_time", "-of", "csv=p=0", file).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe could not list the keyframes: %w", err)
	}
	timestamps := []float64{}
	seen := map[float64]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// A csv row can carry more than one field; the timestamp is the first.
		if comma := strings.Index(line, ","); comma >= 0 {
			line = line[:comma]
		}
		value, err := strconv.ParseFloat(line, 64)
		if err != nil || value <= 0 || math.IsNaN(value) {
			continue
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		timestamps = append(timestamps, value)
	}
	sort.Float64s(timestamps)
	if len(timestamps) == 0 {
		return nil, errors.New("ffprobe reported no keyframe")
	}
	return timestamps, nil
}

// segmentsFromKeyframes groups keyframes into segments of roughly target
// seconds. Every boundary lands on a keyframe, so copying the video stream
// produces pieces that neither overlap nor leave a gap.
func segmentsFromKeyframes(keyframes []float64, duration, target float64) []hlsSegment {
	segments := []hlsSegment{}
	start := 0.0
	for _, stamp := range keyframes {
		if stamp <= start+hlsMinSegment {
			continue
		}
		if stamp-start < target {
			continue
		}
		segments = append(segments, hlsSegment{Start: start, Duration: stamp - start})
		start = stamp
		if start >= duration-hlsMinSegment {
			break
		}
	}
	switch {
	case duration-start > hlsMinSegment:
		segments = append(segments, hlsSegment{Start: start, Duration: duration - start})
	case len(segments) > 0:
		// The tail is too short for its own piece: give it to the last one.
		segments[len(segments)-1].Duration = duration - segments[len(segments)-1].Start
	default:
		segments = append(segments, hlsSegment{Start: 0, Duration: duration})
	}
	return segments
}

// fixedGrid cuts a file into equal pieces. Only safe when the video is
// re-encoded (which seeks exactly) - copying would drift away from the grid.
func fixedGrid(duration, target float64) []hlsSegment {
	segments := []hlsSegment{}
	for start := 0.0; start < duration-hlsMinSegment; start += target {
		length := target
		if start+length > duration {
			length = duration - start
		}
		if length < hlsMinSegment {
			break
		}
		segments = append(segments, hlsSegment{Start: start, Duration: length})
	}
	if len(segments) == 0 {
		segments = append(segments, hlsSegment{Start: 0, Duration: duration})
	}
	return segments
}

func loadHLSIndex(path string, size int64, modTime time.Time) (*hlsIndex, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var stored storedHLSIndex
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, false
	}
	if stored.Version != hlsIndexVersion || stored.Size != size || !stored.ModTime.Equal(modTime) {
		return nil, false
	}
	if len(stored.Index.Segments) == 0 {
		return nil, false
	}
	index := stored.Index
	return &index, true
}

func saveHLSIndex(path string, index *hlsIndex, stat os.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	stored := storedHLSIndex{
		Version: hlsIndexVersion,
		Size:    stat.Size(),
		ModTime: stat.ModTime(),
		Index:   *index,
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// HLSPlaylist renders the media playlist of a lesson. segmentURL builds the URL
// of one piece, which only the HTTP layer can do (it owns the lesson path).
func (t *Transcoder) HLSPlaylist(ctx context.Context, file string, segmentURL func(int) string) (string, error) {
	index, err := t.HLSIndex(ctx, file)
	if err != nil {
		return "", err
	}
	return renderPlaylist(index, segmentURL), nil
}

// renderPlaylist writes the media playlist of a finished segment plan.
func renderPlaylist(index *hlsIndex, segmentURL func(int) string) string {
	longest := 1
	for _, segment := range index.Segments {
		if rounded := int(math.Ceil(segment.Duration)); rounded > longest {
			longest = rounded
		}
	}
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:3\n")
	playlist.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	playlist.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	fmt.Fprintf(&playlist, "#EXT-X-TARGETDURATION:%d\n", longest)
	for position, segment := range index.Segments {
		fmt.Fprintf(&playlist, "#EXTINF:%.3f,\n%s\n", segment.Duration, segmentURL(position))
	}
	playlist.WriteString("#EXT-X-ENDLIST\n")
	return playlist.String()
}

// HLSSegment returns the path of the wanted piece, converting it when it is not
// cached yet. Concurrent requests for the same piece share one ffmpeg run.
func (t *Transcoder) HLSSegment(ctx context.Context, file string, position int) (string, error) {
	index, err := t.HLSIndex(ctx, file)
	if err != nil {
		return "", err
	}
	if position < 0 || position >= len(index.Segments) {
		return "", fmt.Errorf("segment %d is out of range (the lesson has %d)", position, len(index.Segments))
	}
	dir := t.hlsDir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, fmt.Sprintf("seg_%05d.ts", position))
	if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
		return dest, nil
	}
	job := t.registry().run(fmt.Sprintf("hls-segment:%s:%d", filepath.Clean(file), position), func() (string, error) {
		return t.buildHLSSegment(file, index, position, dest)
	})
	if !job.wait(ctx, hlsSegmentTimeout) {
		return "", ErrHLSNotReady
	}
	return job.path, job.err
}

// buildHLSSegment converts one piece into an MPEG-TS file. A piece is the one
// thing a slow machine has to be able to finish quickly, so it is where the GPU
// matters most - and if the hardware run fails, the piece is simply redone with
// libx264 instead of being lost.
func (t *Transcoder) buildHLSSegment(file string, index *hlsIndex, position int, dest string) (string, error) {
	if !t.Available() {
		return "", errors.New("ffmpeg is not installed")
	}
	segment := index.Segments[position]
	info := MediaInfo{
		VideoCodec: index.VideoCodec,
		AudioCodec: index.AudioCodec,
		Height:     index.Height,
	}
	tmp := dest + ".tmp"
	_ = os.Remove(tmp)

	err := t.runSegment(file, segment, index, info, tmp)
	if err != nil && t.hardware() != nil {
		// The GPU refused this piece (driver, pixel format, …): stop asking for
		// it and do the same piece again in software.
		t.disableHardware()
		err = t.runSegment(file, segment, index, info, tmp)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	// Windows refuses to rename onto an existing file.
	_ = os.Remove(dest)
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	t.trimmer().Trim()
	return dest, nil
}

func (t *Transcoder) runSegment(file string, segment hlsSegment, index *hlsIndex, info MediaInfo, dest string) error {
	args := t.segmentArgs(file, segment, index, info, dest)
	ctx, cancel := context.WithTimeout(context.Background(), hlsSegmentTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, t.ffmpeg, args...)
	diagnostics := &limitedBuffer{limit: 4096}
	command.Stderr = diagnostics
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("ffmpeg could not convert this piece of %s: %s", filepath.Base(file), message)
	}
	return nil
}

// segmentArgs builds the ffmpeg command of one piece: an input seek to the
// keyframe this piece starts on, then the copy (or the scaled, possibly
// hardware) encode of the streams.
func (t *Transcoder) segmentArgs(file string, segment hlsSegment, index *hlsIndex, info MediaInfo, dest string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	// Hardware decoding only pays off when a frame is re-encoded.
	args = append(args, t.hwInputArgs(!index.CopyVideo || !index.CopyAudio)...)
	// -ss before -i is the fast, keyframe accurate input seek: ffmpeg does not
	// decode everything up to the offset.
	args = append(args, "-ss", fmt.Sprintf("%.3f", segment.Start))
	args = append(args, "-i", file, "-t", fmt.Sprintf("%.3f", segment.Duration))
	// Subtitles and attachments are dropped: MPEG-TS cannot carry most of them
	// and a failed copy of an .ass track would abort the whole piece.
	args = append(args, "-sn", "-dn", "-map_metadata", "-1")
	if index.CopyVideo {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, t.videoOutputArgs(info)...)
	}
	if index.CopyAudio {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "192k")
	}
	// make_zero keeps every piece starting at 0 instead of at the timestamp of
	// the source, so the player never sees a gap between two pieces.
	args = append(args, "-avoid_negative_ts", "make_zero", "-f", "mpegts", "-muxdelay", "0", "-muxpreload", "0", "-y", dest)
	return args
}

// CanStreamHLS reports whether a lesson can be played as HLS instead of being
// remuxed as a whole. It answers from the cached probe, so it costs nothing -
// the real work (reading the keyframes) only starts when the playlist is asked
// for.
func (t *Transcoder) CanStreamHLS(ctx context.Context, file string) bool {
	if !t.Available() {
		return false
	}
	facts, err := t.Inspect(ctx, file)
	if err != nil {
		return false
	}
	if !facts.Info.HasVideo() || facts.Info.Duration <= 0 {
		return false
	}
	// Copying the video stream means cutting at keyframes: anywhere else the
	// pieces would overlap or leave a gap. Without a keyframe list the only
	// honest answer is to re-encode - which is exactly what a slow NAS must not
	// do by surprise - so when the video will be copied and there is no ffprobe
	// to list the keyframes, the caller falls back to remuxing the whole file
	// instead. Re-encoding (forced or because of the codec) needs no keyframe
	// list: the pieces are cut on a fixed grid.
	if t.canCopyVideo(facts.Info, hlsSafeVideoCodecs) && t.ffprobe == "" {
		return false
	}
	return true
}

// HLSIndexReady reports whether the segment plan is finished, without waiting
// for it. Reading the keyframes of a long lesson means demuxing it once, which
// takes minutes on a slow machine - the UI asks for this and shows that the
// lesson is being prepared instead of letting the player give up.
func (t *Transcoder) HLSIndexReady(file string) bool {
	stat, err := os.Stat(file)
	if err != nil {
		return false
	}
	key := filepath.Clean(file)
	if _, ok := t.hlsIndexMemory(key, stat.Size(), stat.ModTime()); ok {
		return true
	}
	if _, ok := loadHLSIndex(filepath.Join(t.hlsDir(file), hlsIndexFile), stat.Size(), stat.ModTime()); ok {
		return true
	}
	return false
}

// EnsureHLSIndex starts building the segment plan in the background so the
// first request for the playlist finds it already there.
func (t *Transcoder) EnsureHLSIndex(file string) {
	if !t.Available() {
		return
	}
	t.registry().run("hls-index:"+filepath.Clean(file), func() (string, error) {
		stat, err := os.Stat(file)
		if err != nil {
			return "", err
		}
		if _, ok := t.hlsIndexMemory(filepath.Clean(file), stat.Size(), stat.ModTime()); ok {
			return "", nil
		}
		if _, ok := loadHLSIndex(filepath.Join(t.hlsDir(file), hlsIndexFile), stat.Size(), stat.ModTime()); ok {
			return "", nil
		}
		index, buildErr := t.buildHLSIndex(context.Background(), file, stat)
		if buildErr != nil {
			logf("hls: %s cannot be streamed: %v", filepath.Base(file), buildErr)
			return "", buildErr
		}
		_ = saveHLSIndex(filepath.Join(t.hlsDir(file), hlsIndexFile), index, stat)
		t.rememberHLSIndex(filepath.Clean(file), stat, index)
		return "", nil
	})
}
