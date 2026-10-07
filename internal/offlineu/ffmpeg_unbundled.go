//go:build !bundleffmpeg

package offlineu

// BundledFFmpeg reports that this build carries no ffmpeg. build-windows.ps1 is
// the only build that embeds one (build tag bundleffmpeg); everyone else keeps
// using a system ffmpeg, OFFLINEU_FFMPEG or the usual on-demand download.
func BundledFFmpeg() (ffmpeg []byte, ffprobe []byte) { return nil, nil }
