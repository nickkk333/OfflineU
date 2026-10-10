//go:build !bundleffmpeg

package offlineu

// BundledFFmpeg reports that this build carries no ffmpeg. Only a build with the
// bundleffmpeg tag embeds one; every other build keeps using a system ffmpeg,
// OFFLINEU_FFMPEG or the usual on-demand download.
func BundledFFmpeg() (ffmpeg []byte, ffprobe []byte) { return nil, nil }
