//go:build bundleffmpeg

// Build tag bundleffmpeg: a static ffmpeg is dropped into
// internal/offlineu/ffmpegwin and compiled with -tags bundleffmpeg, so the
// resulting binary carries it inside itself and never reaches out to the
// network to remux an .mkv. Every other build (go build, go test, the Docker
// image) is compiled without the tag and therefore stays free of the ~160 MB.
package offlineu

import "embed"

// ffmpegBundle holds the static binaries placed in
// internal/offlineu/ffmpegwin before running the tagged build.
//
//go:embed ffmpegwin
var ffmpegBundle embed.FS

// BundledFFmpeg returns the ffmpeg and ffprobe binaries embedded into this exe.
// A file that was not bundled yields a nil slice: ffprobe is optional (OfflineU
// then probes with "ffmpeg -i"), so a build may ship ffmpeg alone.
func BundledFFmpeg() (ffmpeg []byte, ffprobe []byte) {
	ffmpeg, _ = ffmpegBundle.ReadFile("ffmpegwin/ffmpeg.exe")
	ffprobe, _ = ffmpegBundle.ReadFile("ffmpegwin/ffprobe.exe")
	return ffmpeg, ffprobe
}
