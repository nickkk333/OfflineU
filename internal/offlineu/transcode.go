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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Environment variables that point OfflineU at a specific ffmpeg/ffprobe.
const (
	EnvFFmpeg  = "OFFLINEU_FFMPEG"
	EnvFFprobe = "OFFLINEU_FFPROBE"
)

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
	VideoCodec string `json:"video_codec,omitempty"`
	AudioCodec string `json:"audio_codec,omitempty"`
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
	ffmpeg  string
	ffprobe string
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

// Probe reads the codecs of a media file with ffprobe.
func (t *Transcoder) Probe(ctx context.Context, file string) (MediaInfo, error) {
	if !t.Available() {
		return MediaInfo{}, errors.New("ffmpeg is not installed")
	}
	if t.ffprobe == "" {
		return MediaInfo{}, errors.New("ffprobe is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, t.ffprobe,
		"-v", "quiet", "-print_format", "json", "-show_streams", file).Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("ffprobe failed: %w", err)
	}
	var payload struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
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
	return info, nil
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
