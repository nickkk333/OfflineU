// Package offlineu implements the self-hosted, offline course viewer and
// progress tracker that powers the OfflineU web application.
//
// This file solves the one problem plain DLNA casting always hits: a renderer
// only plays what it understands, and a lesson is usually an .mkv (or an .avi,
// .flv, .wmv …) whose *codecs* the TV would decode happily - it is the
// container it refuses. OfflineU therefore hands the renderer a stream instead
// of the file whenever that is the case: ffmpeg repackages the lesson into
// MPEG-TS (copying both streams, so it costs almost no CPU) and, when the
// codecs themselves are exotic (HEVC, VP9, DTS, …), re-encodes them into
// H.264/AAC, which every renderer plays.
//
// ffmpeg is an optional helper, not a dependency: without it OfflineU keeps
// casting the original file exactly as before.
package offlineu

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Environment variables that point OfflineU at a specific ffmpeg/ffprobe.
const (
	EnvFFmpeg  = "OFFLINEU_FFMPEG"
	EnvFFprobe = "OFFLINEU_FFPROBE"
	// OFFLINEU_NO_AUTOFFMPEG stops the auto-download of a static ffmpeg when none
	// is found on the machine (any OS/arch).
	EnvNoAutoFFmpeg = "OFFLINEU_NO_AUTOFFMPEG"
)

// ffmpegStaticBase points at the eugeneware/ffmpeg-static release, which ships a
// single static ffmpeg binary per platform (no ffprobe, but OfflineU detects
// containers and codecs with "ffmpeg -i" when ffprobe is absent). OfflineU pulls
// it whenever the runtime has no ffmpeg of its own - this is what makes local
// playback and casting work out of the box on Windows and on ARM NAS boxes (e.g.
// fnOS/飞牛OS) whose image may not ship ffmpeg. A previously downloaded copy is
// reused without hitting the network again.
const ffmpegStaticBase = "https://github.com/eugeneware/ffmpeg-static/releases/latest/download"

// Containers a renderer can normally play straight from the file server.
// Everything else (mkv, avi, flv, wmv, webm, …) is repackaged.
var castFriendlyVideo = []string{".mp4", ".m4v", ".mov"}
var castFriendlyAudio = []string{".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg"}

// Codecs almost every renderer decodes. Anything else is re-encoded.
var safeVideoCodecs = []string{"h264", "mpeg1video", "mpeg2video", "mpeg4", "msmpeg4v2", "msmpeg4v3", "wmv1", "wmv2"}
var safeAudioCodecs = []string{"aac", "mp3", "mp2", "ac3", "flac"}

// The MIME types of the two streams OfflineU can produce.
const (
	mpegTSMime  = "video/mp2t"
	aacADTSMime = "audio/aac"
)

// MediaInfo is what ffprobe tells us about a lesson file.
type MediaInfo struct {
	VideoCodec string  `json:"video_codec,omitempty"`
	AudioCodec string  `json:"audio_codec,omitempty"`
	Duration   float64 `json:"duration,omitempty"` // seconds, 0 when unknown
	// Size of the video stream. Height is what decides whether a lesson has to
	// be scaled down before it is re-encoded; 0 means "not read".
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// HasVideo reports whether the file carries a video stream.
func (m MediaInfo) HasVideo() bool { return m.VideoCodec != "" }

// CastPlan is the decision OfflineU made for one lesson, handed to the UI so it
// can explain why a cast is (or is not) converted on the fly.
type CastPlan struct {
	NeedsTranscode     bool   `json:"needs_transcode"`
	TranscodeAvailable bool   `json:"transcode_available"`
	Reason             string `json:"reason,omitempty"`
}

// Defaults of the re-encoding profile. 720p is the floor: a lesson is scaled
// *down* to it, never up to it, and never below it - a 480p source stays 480p.
const (
	DefaultTranscodeHeight = 720
	DefaultTranscodePreset = "veryfast"
	DefaultTranscodeCRF    = 23
)

// TranscodeOptions tunes what happens to a lesson whose streams cannot simply
// be copied - the expensive case, and the one a low powered NAS cannot afford
// at full size.
type TranscodeOptions struct {
	// MaxHeight caps the height a re-encoded video is scaled down to. 720 never
	// upscales and never goes below 720p: a lesson that is already 720p or
	// smaller keeps its size. 0 disables the cap.
	MaxHeight int
	// Preset is libx264's speed/quality trade-off ("ultrafast" … "veryslow").
	Preset string
	// CRF is libx264's constant quality factor (lower is better and slower);
	// it is reused as the quantiser of the hardware encoders.
	CRF int
	// HWAccel is "auto" (use the GPU when ffmpeg offers one), "off", "vaapi" or
	// "qsv".
	HWAccel string
	// ForceReencode re-encodes even when the streams could simply be copied.
	// Some course platforms ship transport streams with deliberately broken
	// packets (one every few seconds): copying hands the damage to the browser,
	// whose decoder is far less forgiving than ffmpeg, while re-encoding drops
	// it and produces a clean stream.
	ForceReencode bool
}

// Transcoder wraps the optional ffmpeg installation.
type Transcoder struct {
	ffmpeg   string
	ffprobe  string
	cacheDir string // remuxed MP4s and HLS segments for local browser playback

	// How a lesson that has to be re-encoded is scaled and encoded.
	MaxHeight int
	Preset    string
	CRF       int
	hwMode    string
	// forceReencode skips the "the streams can be copied" shortcut.
	forceReencode bool

	// initMu guards the lazily created helpers below: a transcoder built by
	// hand (tests) starts without them and gets them on first use.
	initMu sync.Mutex
	// dirMu guards cacheDir.
	dirMu      sync.Mutex
	trim       *cacheTrimmer // keeps the cache inside its budget
	probe      *probeCache   // remembers what a probe already told us
	jobs       *jobRegistry  // never run the same conversion twice
	hlsMu      sync.Mutex
	hlsIndexes map[string]hlsIndexEntry

	hwMu       sync.Mutex
	hw         *hwAccel // nil until the first look, then the answer
	hwResolved bool

	autoMu   sync.Mutex
	autoDone bool // auto-download attempted at most once
}

// NewTranscoder looks for ffmpeg/ffprobe: OFFLINEU_FFMPEG and OFFLINEU_FFPROBE
// win, otherwise the two are searched on PATH. cacheDir is where converted
// media is kept ("" falls back to the system temp directory); limitBytes is the
// budget that folder may use (0 keeps the default, a negative value disables
// trimming).
func NewTranscoder(cacheDir string, limitBytes int64, options TranscodeOptions) *Transcoder {
	transcoder := &Transcoder{
		ffmpeg:        findBinary(EnvFFmpeg, "ffmpeg"),
		ffprobe:       findBinary(EnvFFprobe, "ffprobe"),
		cacheDir:      cacheDir,
		MaxHeight:     options.MaxHeight,
		Preset:        options.Preset,
		CRF:           options.CRF,
		hwMode:        strings.ToLower(strings.TrimSpace(options.HWAccel)),
		forceReencode: options.ForceReencode,
	}
	if transcoder.hwMode == "" {
		transcoder.hwMode = hwAuto
	}
	if limitBytes == 0 {
		limitBytes = DefaultCacheLimitBytes
	}
	// A single statically linked ffmpeg is often dropped somewhere without its
	// sibling: look next to it before giving up on probing.
	if transcoder.ffmpeg != "" && transcoder.ffprobe == "" {
		candidate := filepath.Join(filepath.Dir(transcoder.ffmpeg), "ffprobe"+executableSuffix())
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			transcoder.ffprobe = candidate
		}
	}
	transcoder.probe = newProbeCache()
	transcoder.jobs = newJobRegistry()
	transcoder.trim = newCacheTrimmer(transcoder.CacheDir(), limitBytes)
	return transcoder
}

// CacheDir is the folder converted media is written to. It is resolved lazily
// so a transcoder built without one (tests, embedded use) still works.
func (t *Transcoder) CacheDir() string {
	t.dirMu.Lock()
	defer t.dirMu.Unlock()
	if t.cacheDir == "" {
		t.cacheDir = defaultCacheDir()
	}
	return t.cacheDir
}

func defaultCacheDir() string {
	dir := filepath.Join(os.TempDir(), "offlineu-browser-cache")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// helpers returns the probe cache, the job registry and the cache trimmer,
// creating them on first use.
func (t *Transcoder) helpers() (*probeCache, *jobRegistry, *cacheTrimmer) {
	t.initMu.Lock()
	defer t.initMu.Unlock()
	if t.probe == nil {
		t.probe = newProbeCache()
	}
	if t.jobs == nil {
		t.jobs = newJobRegistry()
	}
	if t.trim == nil {
		t.trim = newCacheTrimmer(t.cacheDirOrTemp(), DefaultCacheLimitBytes)
	}
	return t.probe, t.jobs, t.trim
}

// cacheDirOrTemp is CacheDir without taking the lock (called with initMu held).
func (t *Transcoder) cacheDirOrTemp() string {
	if t.cacheDir == "" {
		t.cacheDir = defaultCacheDir()
	}
	return t.cacheDir
}

func (t *Transcoder) probes() *probeCache {
	probe, _, _ := t.helpers()
	return probe
}

func (t *Transcoder) registry() *jobRegistry {
	_, jobs, _ := t.helpers()
	return jobs
}

func (t *Transcoder) trimmer() *cacheTrimmer {
	_, _, trim := t.helpers()
	return trim
}

// Available reports whether casting can fall back to a converted stream.
func (t *Transcoder) Available() bool { return t != nil && t.ffmpeg != "" }

// preset is the libx264 speed/quality trade-off in use. Faster presets are what
// a weak CPU needs; "ultrafast" costs quality, "medium" costs frames.
func (t *Transcoder) preset() string {
	if value := strings.TrimSpace(t.Preset); value != "" {
		return value
	}
	return "veryfast"
}

// quality is the constant quality factor a re-encoded stream is written with
// (and the quantiser the hardware encoders get).
func (t *Transcoder) quality() int {
	if t.CRF < 12 || t.CRF > 35 {
		return 23
	}
	return t.CRF
}

func findBinary(envKey, name string) string {
	if configured := strings.TrimSpace(os.Getenv(envKey)); configured != "" {
		return configured
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// Probe reads the codecs and the duration of a media file. ffprobe is the first
// choice; a lone ffmpeg binary (no ffprobe next to it) is asked to print the
// same facts with "-i", so a cast still knows when a lesson ends.
//
// The answer is remembered until the file changes: probing means demuxing it,
// and a browser asks for the same lesson again and again while it seeks.
func (t *Transcoder) Probe(ctx context.Context, file string) (MediaInfo, error) {
	facts, err := t.Inspect(ctx, file)
	if err != nil {
		return MediaInfo{}, err
	}
	return facts.Info, nil
}

// Inspect reports the real container of a file and the streams inside it. One
// ffprobe run answers both, and the result is cached until the file changes on
// disk (size + modification time), so a lesson is read at most once.
func (t *Transcoder) Inspect(ctx context.Context, file string) (fileFacts, error) {
	if !t.Available() {
		return fileFacts{}, errors.New("ffmpeg is not installed")
	}
	stat, err := os.Stat(file)
	if err != nil {
		return fileFacts{}, err
	}
	key := filepath.Clean(file)
	cache := t.probes()
	if facts, cachedErr, ok := cache.get(key, stat.Size(), stat.ModTime()); ok {
		return facts, cachedErr
	}
	facts, err := t.inspect(ctx, file)
	// A failure is only remembered while ffmpeg is actually there: on a runtime
	// that downloads ffmpeg on demand the first answer would otherwise be
	// "cannot read this file" forever.
	if err != nil && !t.Available() {
		return facts, err
	}
	cache.put(key, stat.Size(), stat.ModTime(), facts, err)
	return facts, err
}

// inspect runs the probe itself: ffprobe first, "ffmpeg -i" when there is none.
func (t *Transcoder) inspect(ctx context.Context, file string) (fileFacts, error) {
	if t.ffprobe != "" {
		if facts, err := t.probeWithFFprobe(ctx, file); err == nil {
			return facts, nil
		}
	}
	// No ffprobe, or a file it refuses: ffmpeg prints the same facts in its
	// header before it complains about the missing output file.
	return t.inspectWithFFmpeg(ctx, file)
}

// probeWithFFprobe reads the streams, the duration and the container from a
// single ffprobe run.
func (t *Transcoder) probeWithFFprobe(ctx context.Context, file string) (fileFacts, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, t.ffprobe,
		"-v", "quiet", "-print_format", "json", "-show_streams", "-show_format", file).Output()
	if err != nil {
		return fileFacts{}, fmt.Errorf("ffprobe failed: %w", err)
	}
	var payload struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return fileFacts{}, fmt.Errorf("cannot read the ffprobe output: %w", err)
	}
	facts := fileFacts{Container: strings.ToLower(strings.TrimSpace(payload.Format.FormatName))}
	for _, stream := range payload.Streams {
		name := strings.ToLower(strings.TrimSpace(stream.CodecName))
		if name == "" {
			continue
		}
		switch stream.CodecType {
		case "video":
			if facts.Info.VideoCodec == "" {
				facts.Info.VideoCodec = name
				facts.Info.Width = stream.Width
				facts.Info.Height = stream.Height
			}
		case "audio":
			if facts.Info.AudioCodec == "" {
				facts.Info.AudioCodec = name
			}
		}
	}
	if facts.Info.VideoCodec == "" && facts.Info.AudioCodec == "" {
		return fileFacts{}, errors.New("no audio or video stream found")
	}
	facts.Info.Duration = parseDurationSeconds(payload.Format.Duration)
	if facts.Info.Duration <= 0 {
		// Some files carry no duration in the format section.
		if fallback := t.probeWithFFmpeg(ctx, file); fallback.Duration > 0 {
			facts.Info.Duration = fallback.Duration
		}
	}
	return facts, nil
}

// inspectWithFFmpeg reads the same facts out of "ffmpeg -i".
func (t *Transcoder) inspectWithFFmpeg(ctx context.Context, file string) (fileFacts, error) {
	if !t.Available() {
		return fileFacts{}, errors.New("ffmpeg is not installed")
	}
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, _ := exec.CommandContext(timeout, t.ffmpeg, "-hide_banner", "-nostdin", "-i", file).CombinedOutput()
	text := string(output)
	info := parseFFmpegHeader(text)
	container := ""
	if match := ffmpegInputFormatPattern.FindStringSubmatch(text); match != nil {
		container = strings.ToLower(strings.TrimSpace(match[1]))
	}
	if info.VideoCodec == "" && info.AudioCodec == "" && container == "" {
		return fileFacts{}, errors.New("ffprobe is not installed and ffmpeg could not read the file")
	}
	return fileFacts{Container: container, Info: info}, nil
}

// probeWithFFmpeg asks ffmpeg itself what is inside the file. Without an output
// file ffmpeg exits with an error, but it prints the header first - which is
// exactly what this reads.
func (t *Transcoder) probeWithFFmpeg(ctx context.Context, file string) MediaInfo {
	if !t.Available() {
		return MediaInfo{}
	}
	timeout, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, _ := exec.CommandContext(timeout, t.ffmpeg, "-hide_banner", "-nostdin", "-i", file).CombinedOutput()
	return parseFFmpegHeader(string(output))
}

var (
	ffmpegStreamPattern   = regexp.MustCompile(`Stream #[^\n]*?: (Video|Audio): ([A-Za-z0-9_.-]+)`)
	ffmpegDurationPattern = regexp.MustCompile(`Duration: (\d+):(\d{2}):(\d{2})(?:\.(\d+))?`)
	// ffmpeg -i prints "Input #0, <format>, from 'file':" on its diagnostics
	// stream; this lets OfflineU read the real container without ffprobe.
	ffmpegInputFormatPattern = regexp.MustCompile(`Input #0, ([^,]+),`)
	// The video line carries the frame size right after the pixel format:
	// "Video: h264 (High), yuv420p(progressive), 1920x1080, 25 fps".
	ffmpegVideoSizePattern = regexp.MustCompile(`Video:[^\n]*?(\d{2,5})x(\d{2,5})`)
)

// parseFFmpegHeader lifts the codecs and the duration out of "ffmpeg -i".
func parseFFmpegHeader(output string) MediaInfo {
	info := MediaInfo{}
	for _, match := range ffmpegStreamPattern.FindAllStringSubmatch(output, -1) {
		codec := strings.ToLower(match[2])
		switch strings.ToLower(match[1]) {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec = codec
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = codec
			}
		}
	}
	if match := ffmpegVideoSizePattern.FindStringSubmatch(output); match != nil {
		info.Width, _ = strconv.Atoi(match[1])
		info.Height, _ = strconv.Atoi(match[2])
	}
	if match := ffmpegDurationPattern.FindStringSubmatch(output); match != nil {
		hours, _ := strconv.Atoi(match[1])
		minutes, _ := strconv.Atoi(match[2])
		seconds, _ := strconv.Atoi(match[3])
		info.Duration = float64(hours*3600 + minutes*60 + seconds)
		if match[4] != "" {
			if fraction, err := strconv.ParseFloat("0."+match[4], 64); err == nil {
				info.Duration += fraction
			}
		}
	}
	return info
}

// parseDurationSeconds reads the "123.456" ffprobe prints for a duration.
// A live stream or a half broken file reports "N/A" and becomes 0 (unknown).
func parseDurationSeconds(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// Plan decides whether casting a file needs a converted stream. The container
// decides on its own, the codecs only when ffprobe is there to read them - a
// missing ffprobe therefore means "repackage it", never "play it as is".
func (t *Transcoder) Plan(ctx context.Context, file string) CastPlan {
	plan := CastPlan{TranscodeAvailable: t.Available()}
	friendly := t.friendlyContainer(file)
	if info, err := t.Probe(ctx, file); err == nil {
		if !t.canCopyVideo(info, safeVideoCodecs) {
			plan.NeedsTranscode = true
			plan.Reason = "video_codec"
			return plan
		}
		if !t.canCopyAudio(info, safeAudioCodecs) {
			plan.NeedsTranscode = true
			plan.Reason = "audio_codec"
			return plan
		}
	}
	if !friendly {
		plan.NeedsTranscode = true
		plan.Reason = "container"
	}
	return plan
}

// friendlyContainer reports whether a renderer is likely to accept the file's
// container without help.
func (t *Transcoder) friendlyContainer(file string) bool {
	extension := strings.ToLower(filepath.Ext(file))
	if extIn(extension, audioExtensions) {
		return extIn(extension, castFriendlyAudio)
	}
	return extIn(extension, castFriendlyVideo)
}

// canCopyVideo reports whether the video stream can be handed over untouched.
// A lesson without a video stream has nothing to copy, forced or not; a lesson
// with one never can when re-encoding has been forced on (see TranscodeOptions).
func (t *Transcoder) canCopyVideo(info MediaInfo, allowed []string) bool {
	if !info.HasVideo() {
		return true
	}
	if t.forceReencode {
		return false
	}
	return codecIn(info.VideoCodec, allowed)
}

// canCopyAudio is the same question for the audio stream. A lesson without an
// audio stream has nothing to copy, forced or not.
func (t *Transcoder) canCopyAudio(info MediaInfo, allowed []string) bool {
	if info.AudioCodec == "" {
		return true
	}
	if t.forceReencode {
		return false
	}
	return codecIn(info.AudioCodec, allowed)
}

func codecIn(codec string, allowed []string) bool {
	for _, candidate := range allowed {
		if codec == candidate {
			return true
		}
	}
	return false
}

// Args builds the ffmpeg command line for one stream. startSeconds is applied
// as an input seek, so "continue where you left off" also works for a converted
// stream (a renderer cannot seek inside one).
func (t *Transcoder) Args(file string, info MediaInfo, startSeconds int) ([]string, string, error) {
	if !t.Available() {
		return nil, "", errors.New("ffmpeg is not installed")
	}
	copyVideo := t.canCopyVideo(info, safeVideoCodecs)
	copyAudio := t.canCopyAudio(info, safeAudioCodecs)

	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	// Hardware decoding only pays off when a frame is actually re-encoded.
	args = append(args, t.hwInputArgs(!copyVideo || !copyAudio)...)
	if startSeconds > 0 {
		args = append(args, "-ss", formatClockDuration(startSeconds))
	}
	args = append(args, "-i", file)
	// Subtitles and attachments are dropped: MPEG-TS cannot carry most of them
	// and a failed "-c copy" of an .ass track would abort the whole stream.
	args = append(args, "-sn", "-dn", "-map_metadata", "-1")

	if !info.HasVideo() {
		// Pure audio: the target container decides what may be copied. ADTS
		// only carries AAC, so an mp3 is wrapped as mp3 and everything else
		// (flac, wav, vorbis, opus, …) is re-encoded to AAC, which every DLNA
		// speaker accepts.
		switch {
		case copyAudio && info.AudioCodec == "aac":
			args = append(args, "-c:a", "copy", "-f", "adts", "pipe:1")
			return args, aacADTSMime, nil
		case copyAudio && info.AudioCodec == "mp3":
			args = append(args, "-c:a", "copy", "-f", "mp3", "pipe:1")
			return args, "audio/mpeg", nil
		default:
			args = append(args, "-c:a", "aac", "-b:a", "192k", "-f", "adts", "pipe:1")
			return args, aacADTSMime, nil
		}
	}

	if copyVideo && copyAudio {
		// The usual case for an .mkv: the streams are untouched, only the
		// container is rewritten - a few percent of one CPU core.
		args = append(args, "-c:v", "copy", "-c:a", "copy")
	} else {
		args = append(args, t.videoOutputArgs(info)...)
		if copyAudio {
			args = append(args, "-c:a", "copy")
		} else {
			args = append(args, "-c:a", "aac", "-b:a", "192k")
		}
	}
	args = append(args, "-f", "mpegts", "pipe:1")
	return args, mpegTSMime, nil
}

// Stream runs ffmpeg and copies its output to the renderer. The process is
// bound to the request: a device that stops or disconnects kills it at once.
func (t *Transcoder) Stream(ctx context.Context, w http.ResponseWriter, file string, info MediaInfo, startSeconds int) error {
	// On a bare local machine ffmpeg may be missing: fetch the static build so
	// casting works out of the box, just like local browser playback does.
	t.ensureFFmpeg()
	args, mime, err := t.Args(file, info, startSeconds)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, t.ffmpeg, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	// A few kB of ffmpeg's diagnostics are enough to explain a failure.
	diagnostics := &limitedBuffer{limit: 4096}
	command.Stderr = diagnostics
	if err := command.Start(); err != nil {
		return fmt.Errorf("cannot start ffmpeg: %w", err)
	}

	header := w.Header()
	header.Set("Content-Type", mime)
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-OfflineU-Stream", "ffmpeg")
	w.WriteHeader(http.StatusOK)

	_, copyErr := io.Copy(flushWriter{w}, stdout)
	waitErr := command.Wait()
	if waitErr != nil {
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = waitErr.Error()
		}
		logf("dlna: ffmpeg stopped for %s: %s", filepath.Base(file), message)
		if copyErr == nil {
			return fmt.Errorf("ffmpeg could not convert this file: %s", message)
		}
	}
	return copyErr
}

// flushWriter pushes every chunk to the device right away: a renderer waits for
// data, it does not wait for a buffer to fill up.
type flushWriter struct {
	writer http.ResponseWriter
}

func (f flushWriter) Write(payload []byte) (int, error) {
	written, err := f.writer.Write(payload)
	if flusher, ok := f.writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return written, err
}

// limitedBuffer keeps the first limit bytes written to it.
type limitedBuffer struct {
	limit int
	body  strings.Builder
}

func (b *limitedBuffer) Write(payload []byte) (int, error) {
	free := b.limit - b.body.Len()
	if free > 0 {
		if len(payload) > free {
			payload = payload[:free]
		}
		b.body.Write(payload)
	}
	return len(payload), nil
}

func (b *limitedBuffer) String() string { return b.body.String() }

// formatClockDuration renders seconds the way ffmpeg's -ss wants them.
func formatClockDuration(totalSeconds int) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

// browserFriendlyContainer reports whether a browser's <video>/<audio> element
// can play a file whose real container is format (ffprobe's format_name).
// MPEG-TS, MKV, AVI, FLV and WMV are not played by browsers regardless of their
// codecs, so a file the extension claims is an .mp4 but which is really an
// MPEG-TS stream has to be repackaged before it will play locally.
//
// Note: ffprobe reports both WebM and MKV as "matroska,webm", so the two cannot
// be told apart by container name alone - we treat that name as unfriendly and
// remux to MP4, which is safe for either (WebM's VP8/VP9 copy cleanly into an
// MP4, and an MKV simply gets a browser-native container too).
func browserFriendlyContainer(format string) bool {
	switch format {
	case "mov,mp4,m4a,3gp,3g2,mj2", "mp4", "m4v", "quicktime", "webm", "ogg":
		return true
	}
	return false
}

// containerMime maps a probed container to the MIME a browser expects for it.
func containerMime(format string) string {
	switch format {
	case "mov,mp4,m4a,3gp,3g2,mj2", "mp4", "m4v", "quicktime":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "ogg", "ogv":
		return "video/ogg"
	}
	return ""
}

// Streams a browser decodes, per container. The container alone is not enough
// to decide whether a file can be handed over as it is: an .mp4 holding HEVC
// (or a WebM holding something exotic) looks fine by name and still fails to
// decode, which used to show up as "the file simply does not play".
var (
	mp4VideoCodecs  = []string{"h264", "av1"}
	mp4AudioCodecs  = []string{"aac", "mp3", "opus", "flac", "alac"}
	webmVideoCodecs = []string{"vp8", "vp9", "av1"}
	webmAudioCodecs = []string{"opus", "vorbis"}
	oggVideoCodecs  = []string{"theora", "vp8"}
	oggAudioCodecs  = []string{"vorbis", "opus", "flac"}
)

// browserCodecs returns the MIME, and the video and audio codecs a browser
// decodes inside it, for one probed container. An empty MIME means the
// container is not one browsers play at all.
//
// ffprobe reports WebM and Matroska under the same name ("matroska,webm"), so
// the two are told apart by their streams: VP8/VP9/AV1 means a browser can play
// it, an H.264 stream inside that container cannot be served as WebM.
func browserCodecs(container string) (mime string, video, audio []string) {
	switch {
	case strings.Contains(container, "webm"), strings.Contains(container, "matroska"):
		return "video/webm", webmVideoCodecs, webmAudioCodecs
	case strings.Contains(container, "ogg"):
		return "video/ogg", oggVideoCodecs, oggAudioCodecs
	case isMP4Family(container):
		return "video/mp4", mp4VideoCodecs, mp4AudioCodecs
	}
	return "", nil, nil
}

func isMP4Family(container string) bool {
	for _, marker := range []string{"mp4", "mov", "m4v", "quicktime", "3gp", "mj2"} {
		if strings.Contains(container, marker) {
			return true
		}
	}
	return false
}

// browserPlayable reports the MIME the file can be served with as it is, or ""
// when a browser cannot decode it. Both the container and the streams have to
// fit - that is what keeps an HEVC .mp4 from being handed over untouched.
func browserPlayable(container string, info MediaInfo) string {
	mime, video, audio := browserCodecs(container)
	if mime == "" {
		return ""
	}
	// A file whose streams could not be read is not handed over on the strength
	// of its container name alone.
	if !info.HasVideo() && info.AudioCodec == "" {
		return ""
	}
	if info.HasVideo() && !codecIn(info.VideoCodec, video) {
		return ""
	}
	if info.AudioCodec != "" && !codecIn(info.AudioCodec, audio) {
		return ""
	}
	return mime
}

// realContainer reports the actual container of a file, independent of its
// (possibly lying) extension. It answers from the cached probe, so what used to
// be one ffprobe process per request is now one per file. An empty result means
// the container could not be read, in which case the caller serves the bytes
// as-is.
func (t *Transcoder) realContainer(ctx context.Context, file string) string {
	facts, err := t.Inspect(ctx, file)
	if err != nil {
		return ""
	}
	return facts.Container
}

// browserFileArgs builds the ffmpeg command that writes a browser-native MP4
// (H.264/AAC) to outPath for local playback. Streams are copied when the codecs
// already suit the browser and re-encoded otherwise; -movflags +faststart moves
// the moov box to the front so a plain <video> element can start playback before
// the whole file is downloaded, and the resulting file stays seekable.
func (t *Transcoder) browserFileArgs(file string, info MediaInfo, outPath string) ([]string, error) {
	if !t.Available() {
		return nil, errors.New("ffmpeg is not installed")
	}
	copyVideo := t.canCopyVideo(info, safeVideoCodecs)
	copyAudio := t.canCopyAudio(info, safeAudioCodecs)

	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	args = append(args, t.hwInputArgs(!copyVideo || !copyAudio)...)
	args = append(args, "-i", file)
	// Subtitles and attachments are dropped: a failed "-c copy" of an .ass track
	// would abort the whole job, and the browser cannot show them anyway.
	args = append(args, "-sn", "-dn", "-map_metadata", "-1")

	if !info.HasVideo() {
		args = append(args, "-c:a", "aac", "-b:a", "192k")
	} else if copyVideo && copyAudio {
		// The usual case for an MPEG-TS lesson: the streams are untouched, only
		// the container is rewritten - a few percent of one CPU core.
		args = append(args, "-c:v", "copy", "-c:a", "copy")
		if info.AudioCodec == "aac" {
			// AAC inside MPEG-TS is ADTS framed; MP4 wants the ASC form, so the
			// bitstream filter must run or the muxer rejects the audio packets.
			args = append(args, "-bsf:a", "aac_adtstoasc")
		}
	} else {
		args = append(args, t.videoOutputArgs(info)...)
		if copyAudio {
			args = append(args, "-c:a", "copy")
			if info.AudioCodec == "aac" {
				args = append(args, "-bsf:a", "aac_adtstoasc")
			}
		} else {
			args = append(args, "-c:a", "aac", "-b:a", "192k")
		}
	}
	args = append(args, "-f", "mp4", "-movflags", "+faststart", outPath)
	return args, nil
}

// cacheKey identifies the converted copies of one lesson. The forced re-encode
// switch is part of it: a segment (or remuxed MP4) made by copying the streams
// must not be served again once the same lesson has to be re-encoded, and the
// other way round.
func (t *Transcoder) cacheKey(file string) string {
	key := filepath.Clean(file)
	if t.forceReencode {
		key += "\x00force-reencode"
	}
	return key
}

// browserCachePath returns the path of the remuxed MP4 for file: a stable name
// derived from its absolute path, so concurrent requests for the same lesson
// share one job and a finished conversion is reused on the next play.
func (t *Transcoder) browserCachePath(file string) string {
	sum := sha1.Sum([]byte(t.cacheKey(file)))
	return filepath.Join(t.CacheDir(), browserCacheSubdir, fmt.Sprintf("%x.mp4", sum))
}

// BrowserCacheReady reports the path of the remuxed MP4 and whether it is there
// yet. The cached file tracks the source's modification time, so editing the
// lesson invalidates it automatically.
func (t *Transcoder) BrowserCacheReady(file string) (string, bool) {
	cachePath := t.browserCachePath(file)
	source, err := os.Stat(file)
	if err != nil {
		return cachePath, false
	}
	cached, err := os.Stat(cachePath)
	if err != nil || cached.Size() == 0 || source.ModTime().After(cached.ModTime()) {
		return cachePath, false
	}
	return cachePath, true
}

// StartBrowserCache converts file into a browser-native MP4 in the background,
// unless that already happened or is running. Nothing waits for it: the browser
// asks /api/media/status, is told that the lesson is being prepared and gets the
// file as soon as BrowserCacheReady says it is there - which is what keeps a
// multi gigabyte lesson from blocking the request (and the player) for minutes.
func (t *Transcoder) StartBrowserCache(file string, info MediaInfo) {
	if !t.Available() {
		return
	}
	cachePath := t.browserCachePath(file)
	t.registry().run("browser-cache:"+filepath.Clean(file), func() (string, error) {
		return t.buildBrowserCache(file, info, cachePath)
	})
}

// BrowserCacheFailure reports why the last background conversion of a file gave
// up, so the UI can say so instead of waiting forever.
func (t *Transcoder) BrowserCacheFailure(file string) error {
	err, _ := t.registry().Failure("browser-cache:" + filepath.Clean(file))
	return err
}

// buildBrowserCache runs the conversion itself.
func (t *Transcoder) buildBrowserCache(file string, info MediaInfo, cachePath string) (string, error) {
	source, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if cached, err := os.Stat(cachePath); err == nil && cached.Size() > 0 &&
		!source.ModTime().After(cached.ModTime()) {
		return cachePath, nil
	}
	_ = os.Remove(cachePath)

	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "browser-*.mp4.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	tmp.Close()
	// On any failure the partial file is removed so a later request retries.
	defer os.Remove(tmpName)

	args, err := t.browserFileArgs(file, info, tmpName)
	if err != nil {
		return "", err
	}
	// A whole lesson can take a long time on a slow machine; it runs detached
	// from the request that started it.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()
	command := exec.CommandContext(ctx, t.ffmpeg, args...)
	diagnostics := &limitedBuffer{limit: 4096}
	command.Stderr = diagnostics
	if err := command.Run(); err != nil && t.hardware() != nil {
		// The GPU could not do it: switch it off and redo the whole file with
		// libx264 rather than leaving the lesson unplayable.
		t.disableHardware()
		if args, err = t.browserFileArgs(file, info, tmpName); err == nil {
			command = exec.CommandContext(ctx, t.ffmpeg, args...)
			command.Stderr = diagnostics
			err = command.Run()
		}
	}
	if err != nil {
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = err.Error()
		}
		logf("browser: remux failed for %s: %s", filepath.Base(file), message)
		return "", fmt.Errorf("ffmpeg could not convert this file: %s", message)
	}
	if err := os.Rename(tmpName, cachePath); err != nil {
		return "", err
	}
	t.trimmer().Trim()
	return cachePath, nil
}

// ensureFFmpeg procures ffmpeg (and ffprobe) on demand when the runtime has none.
// It runs at most once and is cross-platform: it downloads the static build for
// the current OS/arch (Windows, Linux amd64/arm64/arm, macOS) into the cache
// directory, which makes local browser playback and casting work out of the box
// even on an ARM NAS box (e.g. fnOS/飞牛OS) whose image does not ship ffmpeg. When
// ffmpeg is already installed (on PATH, via OFFLINEU_FFMPEG, or bundled in a Docker
// image) this is a no-op. Failures are logged and ignored - OfflineU then keeps
// serving the original bytes, exactly as it did before ffmpeg was involved. Set
// OFFLINEU_NO_AUTOFFMPEG=1 to disable the download entirely.
func (t *Transcoder) ensureFFmpeg() {
	t.autoMu.Lock()
	defer t.autoMu.Unlock()
	if t.autoDone {
		return
	}
	t.autoDone = true
	if t.Available() {
		return
	}
	if os.Getenv(EnvNoAutoFFmpeg) != "" {
		return
	}
	// A build that carries its own ffmpeg (build tag bundleffmpeg) never touches
	// the network: unpack it from the exe and use it.
	if ffmpegPath, ffprobePath, ok := t.installBundledFFmpeg(); ok {
		t.ffmpeg = ffmpegPath
		t.ffprobe = ffprobePath
		logf("transcoder: using the ffmpeg bundled in this exe (%s; ffprobe=%s)", ffmpegPath, ffprobePath)
		return
	}
	ffmpegPath, ffprobePath, err := t.downloadFFmpeg()
	if err != nil {
		logf("transcoder: ffmpeg auto-download failed, local remux disabled: %v", err)
		return
	}
	t.ffmpeg = ffmpegPath
	t.ffprobe = ffprobePath
	logf("transcoder: ffmpeg fetched for local playback (%s; ffprobe=%s)", ffmpegPath, ffprobePath)
}

// installBundledFFmpeg unpacks the ffmpeg that was embedded into this exe into
// the cache directory and reports its paths. This is what keeps the portable
// Windows exe offline: without a bundled copy the first remux would have to
// download one. An existing copy of the same size is reused, so only the first
// run (and the first run after an update) pays for the extraction.
func (t *Transcoder) installBundledFFmpeg() (string, string, bool) {
	ffmpegBytes, ffprobeBytes := BundledFFmpeg()
	if len(ffmpegBytes) == 0 {
		return "", "", false
	}
	dir := filepath.Join(t.CacheDir(), "ffmpeg-bundled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", false
	}
	ffmpegPath := filepath.Join(dir, "ffmpeg"+executableSuffix())
	ffprobePath := filepath.Join(dir, "ffprobe"+executableSuffix())
	if err := writeFileOnce(ffmpegPath, ffmpegBytes); err != nil {
		logf("transcoder: could not unpack the bundled ffmpeg: %v", err)
		return "", "", false
	}
	if len(ffprobeBytes) > 0 {
		if err := writeFileOnce(ffprobePath, ffprobeBytes); err != nil {
			logf("transcoder: could not unpack the bundled ffprobe, falling back to ffmpeg -i: %v", err)
			ffprobePath = ""
		}
	}
	return ffmpegPath, ffprobePath, true
}

// writeFileOnce writes data unless an equally sized file already sits at path.
func writeFileOnce(path string, data []byte) error {
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(data)) {
		return nil
	}
	return os.WriteFile(path, data, 0o755)
}

// ffmpegStaticAssets returns the eugeneware/ffmpeg-static asset names for the
// running OS/arch (ffmpeg and, when available, ffprobe), or "" when no prebuilt
// binary is offered.
func ffmpegStaticAssets() (string, string) {
	switch runtime.GOOS {
	case "windows":
		return "ffmpeg-win32-x64", "ffprobe-win32-x64"
	case "linux":
		switch runtime.GOARCH {
		case "arm64":
			return "ffmpeg-linux-arm64", "ffprobe-linux-arm64"
		case "arm":
			return "ffmpeg-linux-arm", "ffprobe-linux-arm"
		default:
			return "ffmpeg-linux-x64", "ffprobe-linux-x64"
		}
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "ffmpeg-darwin-arm64", "ffprobe-darwin-arm64"
		}
		return "ffmpeg-darwin-x64", "ffprobe-darwin-x64"
	}
	return "", ""
}

// downloadFFmpeg fetches the static ffmpeg (and ffprobe) build for this platform
// once and saves them into cacheDir/ffmpeg. A previously downloaded copy is only
// reused when it actually starts: an interrupted download leaves a truncated file
// that looks complete to os.Stat, and the OS refuses to execute half a binary -
// which used to silently disable every conversion until the cache was deleted by
// hand. A missing or broken ffprobe is not fatal: the caller falls back to
// "ffmpeg -i" for probing.
func (t *Transcoder) downloadFFmpeg() (string, string, error) {
	ffmpegAsset, ffprobeAsset := ffmpegStaticAssets()
	if ffmpegAsset == "" {
		return "", "", fmt.Errorf("no bundled ffmpeg build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := filepath.Join(t.cacheDir, "ffmpeg")
	_ = os.MkdirAll(dir, 0o755)

	ffmpegPath := filepath.Join(dir, ffmpegAsset)
	ffprobePath := ""
	if ffprobeAsset != "" {
		ffprobePath = filepath.Join(dir, ffprobeAsset)
	}
	if runtime.GOOS == "windows" {
		// The assets ship without an extension; keep the conventional .exe locally.
		ffmpegPath += ".exe"
		if ffprobePath != "" {
			ffprobePath += ".exe"
		}
	}

	// A copy from an earlier run is trusted only when it starts ("-version"
	// prints its banner and exits). Anything else - a half downloaded file, a
	// wrong architecture - is thrown away and fetched again.
	if binaryRuns(ffmpegPath) {
		if !binaryRuns(ffprobePath) {
			ffprobePath = "" // probe falls back to "ffmpeg -i"
		}
		return ffmpegPath, ffprobePath, nil
	}
	if _, err := os.Stat(ffmpegPath); err == nil {
		logf("transcoder: the cached ffmpeg does not run, downloading it again...")
		_ = os.Remove(ffmpegPath)
	}

	logf("transcoder: downloading ffmpeg for local playback (one time)...")
	if err := downloadFile(ffmpegStaticBase+"/"+ffmpegAsset, ffmpegPath); err != nil {
		return "", "", err
	}
	if !binaryRuns(ffmpegPath) {
		_ = os.Remove(ffmpegPath)
		return "", "", fmt.Errorf("the downloaded ffmpeg does not run on this system")
	}
	if ffprobePath != "" {
		if err := downloadFile(ffmpegStaticBase+"/"+ffprobeAsset, ffprobePath); err != nil || !binaryRuns(ffprobePath) {
			if err == nil {
				_ = os.Remove(ffprobePath)
				err = fmt.Errorf("the downloaded ffprobe does not run")
			}
			logf("transcoder: ffprobe download failed, falling back to ffmpeg -i: %v", err)
			ffprobePath = ""
		}
	}
	return ffmpegPath, ffprobePath, nil
}

// binaryRuns reports whether the program at path starts and answers "-version".
// An empty path or a file the OS refuses to execute both count as "no".
func binaryRuns(path string) bool {
	if path == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "-version").Run() == nil
}

// downloadFile streams url into dest, making it executable on non-Windows
// platforms. The bytes go into a temporary file first and are only renamed into
// place once the whole body arrived and matches the announced length: a
// connection that drops halfway must never leave a truncated binary at dest,
// where the next start would happily reuse it.
func downloadFile(url, dest string) error {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	partial := dest + ".part"
	out, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, response.Body)
	if copyErr == nil && response.ContentLength >= 0 && written != response.ContentLength {
		copyErr = fmt.Errorf("only %d of %d bytes arrived", written, response.ContentLength)
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(partial)
		return copyErr
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(partial, 0o755); err != nil {
			_ = os.Remove(partial)
			return err
		}
	}
	// Windows refuses to rename onto an existing file.
	_ = os.Remove(dest)
	if err := os.Rename(partial, dest); err != nil {
		_ = os.Remove(partial)
		return err
	}
	return nil
}

// ServeBrowser adapts a media file to what a browser can actually play. Files
// whose real container matches their extension (or is otherwise browser-native)
// are served with the correct MIME; files in an unplayable container (MPEG-TS,
// MKV, AVI, …) are served from the remuxed MP4 when that copy is finished.
//
// It never starts a conversion: repackaging a whole lesson takes minutes on a
// slow machine, and a request must not sit there waiting for it. /api/media/status
// starts the job in the background and the browser comes back for the file once
// it is ready (see StartBrowserCache and BrowserCacheReady). It returns true
// when it wrote the response and false when the caller should serve the raw
// bytes - without ffmpeg, when the container is unreadable, or while the copy is
// still being made.
func (t *Transcoder) ServeBrowser(w http.ResponseWriter, r *http.Request, file string) bool {
	// Make sure ffmpeg is available before we decide anything; on a runtime that
	// has none this downloads the static build for the current OS/arch (Windows,
	// Linux amd64/arm64, macOS) - that is what lets an ARM NAS box like fnOS/飞牛OS
	// repackage lessons for the browser without ffmpeg preinstalled.
	t.ensureFFmpeg()
	// One cached probe answers the whole request: the container, the codecs and
	// the duration come from the same ffprobe run.
	facts, err := t.Inspect(r.Context(), file)
	if err != nil || facts.Container == "" {
		// Still unreadable even after ffmpeg is in place: give up and let the
		// caller serve the raw bytes.
		return false
	}
	// Only a file the browser can really decode is handed over untouched -
	// an .mp4 holding HEVC would otherwise be served as-is and fail to play.
	if mime := browserPlayable(facts.Container, facts.Info); mime != "" {
		serveCourseFile(w, r, file, mime)
		return true
	}
	if !t.Available() {
		return false
	}
	cachePath, ready := t.BrowserCacheReady(file)
	if !ready {
		return false
	}
	// Touch the copy so the cache trimmer throws away something else first.
	t.trimmer().Touch(cachePath)
	mime := "video/mp4"
	if !facts.Info.HasVideo() {
		mime = "audio/mp4"
	}
	serveCourseFile(w, r, cachePath, mime)
	return true
}
