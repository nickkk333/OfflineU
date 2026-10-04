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

// Transcoder wraps the optional ffmpeg installation.
type Transcoder struct {
	ffmpeg   string
	ffprobe  string
	cacheDir string // remuxed MP4s for local browser playback

	autoMu   sync.Mutex
	autoDone bool // auto-download attempted at most once
}

// NewTranscoder looks for ffmpeg/ffprobe: OFFLINEU_FFMPEG and OFFLINEU_FFPROBE
// win, otherwise the two are searched on PATH.
func NewTranscoder() *Transcoder {
	transcoder := &Transcoder{
		ffmpeg:  findBinary(EnvFFmpeg, "ffmpeg"),
		ffprobe: findBinary(EnvFFprobe, "ffprobe"),
	}
	// A single statically linked ffmpeg is often dropped somewhere without its
	// sibling: look next to it before giving up on probing.
	if transcoder.ffmpeg != "" && transcoder.ffprobe == "" {
		candidate := filepath.Join(filepath.Dir(transcoder.ffmpeg), "ffprobe"+executableSuffix())
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			transcoder.ffprobe = candidate
		}
	}
	// Remuxed MP4s for local playback are kept here between requests (and across
	// restarts) so a lesson is only transcoded once and seeking reuses the file.
	transcoder.cacheDir = filepath.Join(os.TempDir(), "offlineu-browser-cache")
	_ = os.MkdirAll(transcoder.cacheDir, 0o755)
	return transcoder
}

// Available reports whether casting can fall back to a converted stream.
func (t *Transcoder) Available() bool { return t != nil && t.ffmpeg != "" }

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
func (t *Transcoder) Probe(ctx context.Context, file string) (MediaInfo, error) {
	if !t.Available() {
		return MediaInfo{}, errors.New("ffmpeg is not installed")
	}
	if t.ffprobe == "" {
		info := t.probeWithFFmpeg(ctx, file)
		if info.VideoCodec == "" && info.AudioCodec == "" && info.Duration <= 0 {
			return MediaInfo{}, errors.New("ffprobe is not installed and ffmpeg could not read the file")
		}
		return info, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, t.ffprobe,
		"-v", "quiet", "-print_format", "json", "-show_streams", "-show_format", file).Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("ffprobe failed: %w", err)
	}
	var payload struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return MediaInfo{}, fmt.Errorf("cannot read the ffprobe output: %w", err)
	}
	info := MediaInfo{}
	for _, stream := range payload.Streams {
		name := strings.ToLower(strings.TrimSpace(stream.CodecName))
		if name == "" {
			continue
		}
		switch stream.CodecType {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec = name
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = name
			}
		}
	}
	if info.VideoCodec == "" && info.AudioCodec == "" {
		return MediaInfo{}, errors.New("no audio or video stream found")
	}
	info.Duration = parseDurationSeconds(payload.Format.Duration)
	if info.Duration <= 0 {
		// Some files carry no duration in the format section.
		if fallback := t.probeWithFFmpeg(ctx, file); fallback.Duration > 0 {
			info.Duration = fallback.Duration
		}
	}
	return info, nil
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
		if info.HasVideo() && !codecIn(info.VideoCodec, safeVideoCodecs) {
			plan.NeedsTranscode = true
			plan.Reason = "video_codec"
			return plan
		}
		if info.AudioCodec != "" && !codecIn(info.AudioCodec, safeAudioCodecs) {
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
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if startSeconds > 0 {
		args = append(args, "-ss", formatClockDuration(startSeconds))
	}
	args = append(args, "-i", file)
	// Subtitles and attachments are dropped: MPEG-TS cannot carry most of them
	// and a failed "-c copy" of an .ass track would abort the whole stream.
	args = append(args, "-sn", "-dn", "-map_metadata", "-1")

	copyVideo := !info.HasVideo() || codecIn(info.VideoCodec, safeVideoCodecs)
	copyAudio := info.AudioCodec == "" || codecIn(info.AudioCodec, safeAudioCodecs)

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
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p")
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

// realContainer probes the actual container format of a file, independent of its
// (possibly lying) extension. ffprobe is preferred; when it is absent (the
// auto-downloaded static build ships only ffmpeg) OfflineU falls back to parsing
// "ffmpeg -i". An empty result means the container could not be read, in which
// case the caller serves the bytes as-is.
func (t *Transcoder) realContainer(ctx context.Context, file string) string {
	if !t.Available() {
		return ""
	}
	if t.ffprobe != "" {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, t.ffprobe,
			"-v", "quiet", "-show_format", "-of", "json", file).Output()
		if err == nil {
			var payload struct {
				Format struct {
					FormatName string `json:"format_name"`
				} `json:"format"`
			}
			if json.Unmarshal(output, &payload) == nil {
				return strings.ToLower(payload.Format.FormatName)
			}
		}
	}
	return t.realContainerFFmpeg(ctx, file)
}

// realContainerFFmpeg reads the container from ffmpeg's "Input #0, <format>," line
// when ffprobe is unavailable.
func (t *Transcoder) realContainerFFmpeg(ctx context.Context, file string) string {
	timeout, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, _ := exec.CommandContext(timeout, t.ffmpeg, "-hide_banner", "-nostdin", "-i", file).CombinedOutput()
	if match := ffmpegInputFormatPattern.FindStringSubmatch(string(output)); match != nil {
		return strings.ToLower(strings.TrimSpace(match[1]))
	}
	return ""
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
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", file}
	// Subtitles and attachments are dropped: a failed "-c copy" of an .ass track
	// would abort the whole job, and the browser cannot show them anyway.
	args = append(args, "-sn", "-dn", "-map_metadata", "-1")

	copyVideo := !info.HasVideo() || codecIn(info.VideoCodec, safeVideoCodecs)
	copyAudio := info.AudioCodec == "" || codecIn(info.AudioCodec, safeAudioCodecs)

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
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p")
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

// browserCachePath returns the path of the remuxed MP4 for file: a stable name
// derived from its absolute path, so concurrent requests for the same lesson
// share one job and a finished conversion is reused on the next play.
func (t *Transcoder) browserCachePath(file string) string {
	sum := sha1.Sum([]byte(filepath.Clean(file)))
	return filepath.Join(t.cacheDir, fmt.Sprintf("%x.mp4", sum))
}

// ensureBrowserCache transcodes file into a browser-native MP4 (or reuses a
// still-valid one) and returns that path. The cached file tracks the source's
// modification time, so editing the lesson invalidates it automatically.
func (t *Transcoder) ensureBrowserCache(r *http.Request, file string, info MediaInfo) (string, error) {
	cachePath := t.browserCachePath(file)
	source, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if cached, err := os.Stat(cachePath); err == nil && !source.ModTime().After(cached.ModTime()) {
		return cachePath, nil
	}
	_ = os.Remove(cachePath)

	tmp, err := os.CreateTemp(t.cacheDir, "browser-*.mp4.tmp")
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
	command := exec.CommandContext(r.Context(), t.ffmpeg, args...)
	diagnostics := &limitedBuffer{limit: 4096}
	command.Stderr = diagnostics
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("ffmpeg could not convert this file: %s", message)
	}
	if err := os.Rename(tmpName, cachePath); err != nil {
		return "", err
	}
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
	ffmpegPath, ffprobePath, err := t.downloadFFmpeg()
	if err != nil {
		logf("transcoder: ffmpeg auto-download failed, local remux disabled: %v", err)
		return
	}
	t.ffmpeg = ffmpegPath
	t.ffprobe = ffprobePath
	logf("transcoder: ffmpeg fetched for local playback (%s; ffprobe=%s)", ffmpegPath, ffprobePath)
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
// once and saves them into cacheDir/ffmpeg. A previously downloaded copy is reused
// without hitting the network again. A missing ffprobe download is not fatal: the
// caller falls back to "ffmpeg -i" for probing.
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

	if _, err := os.Stat(ffmpegPath); err == nil {
		return ffmpegPath, ffprobePath, nil
	}

	logf("transcoder: downloading ffmpeg for local playback (one time)...")
	if err := downloadFile(ffmpegStaticBase+"/"+ffmpegAsset, ffmpegPath); err != nil {
		return "", "", err
	}
	if ffprobePath != "" {
		if err := downloadFile(ffmpegStaticBase+"/"+ffprobeAsset, ffprobePath); err != nil {
			logf("transcoder: ffprobe download failed, falling back to ffmpeg -i: %v", err)
			ffprobePath = ""
		}
	}
	return ffmpegPath, ffprobePath, nil
}

// downloadFile streams url into dest, creating (or truncating) the file and making
// it executable on non-Windows platforms.
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
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, response.Body); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dest, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ServeBrowser adapts a media file to what a browser can actually play. Files
// whose real container matches their extension (or is otherwise browser-native)
// are served with the correct MIME; files in an unplayable container (MPEG-TS,
// MKV, AVI, …) are repackaged to a seekable MP4 and served via http.ServeContent,
// so the browser can both play from the start and seek. It returns true when it
// wrote the response, false when the caller should fall back to serving the raw
// bytes - which happens without ffmpeg, when the container is unreadable, or if
// ffmpeg fails to convert the file.
func (t *Transcoder) ServeBrowser(w http.ResponseWriter, r *http.Request, file string) bool {
	// Make sure ffmpeg is available before we decide anything; on a runtime that
	// has none this downloads the static build for the current OS/arch (Windows,
	// Linux amd64/arm64, macOS) - that is what lets an ARM NAS box like fnOS/飞牛OS
	// repackage lessons for the browser without ffmpeg preinstalled.
	t.ensureFFmpeg()
	container := t.realContainer(r.Context(), file)
	if container == "" {
		// Still unreadable even after ffmpeg is in place: give up and let the
		// caller serve the raw bytes.
		return false
	}
	if container == "" {
		return false
	}
	if browserFriendlyContainer(container) {
		if mime := containerMime(container); mime != "" {
			serveCourseFile(w, r, file, mime)
			return true
		}
		return false
	}
	if !t.Available() {
		return false
	}
	info, _ := t.Probe(r.Context(), file)
	cachePath, err := t.ensureBrowserCache(r, file, info)
	if err != nil {
		logf("browser: remux failed for %s: %v", filepath.Base(file), err)
		return false
	}
	mime := "video/mp4"
	if !info.HasVideo() {
		mime = "audio/mp4"
	}
	serveCourseFile(w, r, cachePath, mime)
	return true
}
