package offlineu

// Hardware assisted encoding.
//
// Re-encoding a 1080p lesson in software costs a low powered NAS more CPU than
// it has: libx264 at "veryfast" barely reaches real time on a Celeron and never
// does for a 4K source. Most machines have a block that does this faster and is
// sitting idle - Quick Sync inside an Intel iGPU, the VAAPI or VideoToolbox
// entry point in front of the same hardware, an NVIDIA NVENC block, an AMD AMF
// one, or the video engine of an ARM SoC - so before OfflineU re-encodes it asks
// ffmpeg which of them this build understands and this machine actually has,
// and lets that block do both the decoding and the encoding.
//
// Detection is two "ffmpeg -hide_banner" runs plus a look at the device nodes,
// and it happens once. A stream that is copied is never touched by any of it,
// and when a hardware run fails - a driver that cannot allocate, an encoder
// that refuses the pixel format - the machine is switched off and the same
// conversion is retried with libx264, so no driver can make a lesson
// unplayable.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The kinds OfflineU knows, and the values OFFLINEU_HWACCEL accepts.
const (
	hwAuto         = "auto"
	hwOff          = "off"
	hwNVENC        = "nvenc"
	hwQSV          = "qsv"
	hwVAAPI        = "vaapi"
	hwVideoToolbox = "videotoolbox"
	hwAMF          = "amf"
	hwV4L2         = "v4l2m2m"
	hwRKMPP        = "rkmpp"
)

// hwDetectTimeout bounds the two probes of the detection.
const hwDetectTimeout = 10 * time.Second

// hwAccel is one usable hardware path: what to put in front of the input, which
// encoder to ask for and how to tell it the wanted quality.
type hwAccel struct {
	Kind      string
	InputArgs []string
	Encoder   string
	Quality   func(int) []string
}

// hwBackend describes one way of encoding on a GPU. Everything that varies
// between them - the decoder name, the encoder names, which operating systems
// it exists on, whether the device is really there and which option carries the
// quality factor - lives here, so adding one is a table entry.
type hwBackend struct {
	Kind     string
	Accel    string   // -hwaccel value; "" when this block has no decoder worth naming
	Encoders []string // first one this ffmpeg build carries wins
	Systems  []string // GOOS values this can exist on
	// Ready reports whether the device is actually there. Encoders are compiled
	// in whether or not the hardware exists, so this is what separates "ffmpeg
	// knows NVENC" from "this machine has an NVIDIA GPU".
	Ready func() bool
	// DeviceArgs adds the global options that name the device.
	DeviceArgs func() []string
	// Quality turns the configured quality factor into this encoder's option.
	// nil means the encoder's default is fine.
	Quality func(int) []string
}

// hwBackends, in the order OfflineU prefers them. The first one this machine
// can actually use wins.
var hwBackends = []*hwBackend{
	&videoToolbox,
	&nvenc,
	&qsv,
	&vaapi,
	&amf,
	&v4l2m2m,
	&rkmpp,
}

var videoToolbox = hwBackend{
	Kind:     hwVideoToolbox,
	Accel:    "videotoolbox",
	Encoders: []string{"h264_videotoolbox"},
	Systems:  []string{"darwin"},
}

var nvenc = hwBackend{
	Kind:     hwNVENC,
	Accel:    "cuda",
	Encoders: []string{"h264_nvenc"},
	Systems:  []string{"linux", "windows"},
	Ready:    nvidiaPresent,
	Quality:  func(quality int) []string { return []string{"-cq", strconv.Itoa(quality)} },
}

var qsv = hwBackend{
	Kind:     hwQSV,
	Accel:    "qsv",
	Encoders: []string{"h264_qsv"},
	Systems:  []string{"linux", "windows"},
	Ready:    qsvPresent,
	Quality:  func(quality int) []string { return []string{"-global_quality", strconv.Itoa(quality)} },
}

var vaapi = hwBackend{
	Kind:     hwVAAPI,
	Accel:    "vaapi",
	Encoders: []string{"h264_vaapi"},
	Systems:  []string{"linux"},
	Ready:    hasDRIDevice,
	// Naming the render node keeps ffmpeg from picking a device it cannot open
	// on a machine with more than one GPU.
	DeviceArgs: func() []string {
		if device := firstRenderNode(); device != "" {
			return []string{"-vaapi_device", device}
		}
		return nil
	},
	Quality: func(quality int) []string { return []string{"-qp", strconv.Itoa(quality)} },
}

var amf = hwBackend{
	Kind:     hwAMF,
	Accel:    "d3d11va",
	Encoders: []string{"h264_amf"},
	Systems:  []string{"windows", "linux"},
	Ready:    amfPresent,
	Quality: func(quality int) []string {
		return []string{"-rc", "cqp", "-qp_i", strconv.Itoa(quality), "-qp_p", strconv.Itoa(quality)}
	},
}

// v4l2m2m is the memory-to-memory video engine of ARM boards (a Raspberry Pi
// among them): it encodes H.264 in a block the CPU never sees.
var v4l2m2m = hwBackend{
	Kind:     hwV4L2,
	Accel:    "v4l2m2m",
	Encoders: []string{"h264_v4l2m2m"},
	Systems:  []string{"linux"},
	Ready:    func() bool { return dirHasPrefix("/dev", "video") },
}

// rkmpp is the Rockchip equivalent (RK3399, RK3588, …).
var rkmpp = hwBackend{
	Kind:     hwRKMPP,
	Accel:    "rkmpp",
	Encoders: []string{"h264_rkmpp"},
	Systems:  []string{"linux"},
	Ready:    func() bool { return hasDRIDevice() || fileExists("/dev/mpp_service") },
}

// onThisSystem reports whether this backend can exist here at all.
func (b *hwBackend) onThisSystem() bool {
	for _, system := range b.Systems {
		if system == runtime.GOOS {
			return true
		}
	}
	return false
}

// build turns a backend into a usable path, or nil when this ffmpeg build or
// this machine cannot do it.
func (b *hwBackend) build(accels, encoders map[string]bool) *hwAccel {
	encoder := ""
	for _, candidate := range b.Encoders {
		if encoders[candidate] {
			encoder = candidate
			break
		}
	}
	if encoder == "" {
		return nil
	}
	if b.Ready != nil && !b.Ready() {
		return nil
	}
	args := []string{}
	// The decoder is a bonus: an encoder without one still offloads the
	// expensive half, and a build that does not list the decoder must not be
	// handed an option it will refuse.
	if b.Accel != "" && accels[b.Accel] {
		args = append(args, "-hwaccel", b.Accel)
	}
	if b.DeviceArgs != nil {
		args = append(args, b.DeviceArgs()...)
	}
	return &hwAccel{Kind: b.Kind, InputArgs: args, Encoder: encoder, Quality: b.Quality}
}

// backendFor resolves one OFFLINEU_HWACCEL value (aliases included).
func backendFor(mode string) *hwBackend {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case hwNVENC, "cuda", "nvidia":
		return &nvenc
	case hwQSV, "quick sync", "quicksync":
		return &qsv
	case hwVAAPI:
		return &vaapi
	case hwVideoToolbox, "vt", "videotoolbox hwaccel":
		return &videoToolbox
	case hwAMF:
		return &amf
	case hwV4L2, "v4l2":
		return &v4l2m2m
	case hwRKMPP, "rockchip", "mpp":
		return &rkmpp
	}
	return nil
}

// isDisabled reports whether the operator asked for software only.
func isDisabled(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case hwOff, "0", "false", "no", "none", "disabled", "cpu", "software":
		return true
	}
	return false
}

// hardware reports the hardware encoder to use, or nil when the software
// encoder is the only option. The answer is remembered; a failure switches the
// machine off for good (see disableHardware).
func (t *Transcoder) hardware() *hwAccel {
	t.hwMu.Lock()
	defer t.hwMu.Unlock()
	if t.hwResolved {
		return t.hw
	}
	if t.ffmpeg == "" {
		// ffmpeg may still arrive (the on-demand download); try again later.
		return nil
	}
	t.hwResolved = true
	t.hw = detectHardware(t.ffmpeg, t.hwMode)
	if t.hw != nil {
		logf("transcoder: re-encoding with %s (%s)", t.hw.Kind, t.hw.Encoder)
	}
	return t.hw
}

// disableHardware stops offering hardware encoding after a run failed, so the
// next conversion goes straight to libx264.
func (t *Transcoder) disableHardware() {
	t.hwMu.Lock()
	defer t.hwMu.Unlock()
	if t.hw != nil {
		logf("transcoder: %s encoding failed, falling back to libx264", t.hw.Kind)
	}
	t.hw = nil
	t.hwResolved = true
}

// hwInputArgs are the options that have to precede the input. They are only
// worth anything when the video is actually re-encoded - copying the stream
// never touches a frame.
func (t *Transcoder) hwInputArgs(reencoding bool) []string {
	if !reencoding {
		return nil
	}
	accel := t.hardware()
	if accel == nil {
		return nil
	}
	return append([]string{}, accel.InputArgs...)
}

// videoOutputArgs is the encoder side of a video stream that cannot be copied,
// plus the downscale filter when the source is taller than the configured cap.
func (t *Transcoder) videoOutputArgs(info MediaInfo) []string {
	args := []string{}
	if scale := t.scaleFilter(info); scale != "" {
		args = append(args, "-vf", scale)
	}
	if accel := t.hardware(); accel != nil {
		args = append(args, "-c:v", accel.Encoder)
		if accel.Quality != nil {
			args = append(args, accel.Quality(t.quality())...)
		}
		return args
	}
	return append(args, "-c:v", "libx264", "-preset", t.preset(), "-crf", strconv.Itoa(t.quality()), "-pix_fmt", "yuv420p")
}

// scaleFilter shrinks a lesson that is taller than the cap. A source that is
// already small enough is left alone, and the cap never scales anything up.
func (t *Transcoder) scaleFilter(info MediaInfo) string {
	if t.MaxHeight <= 0 {
		return ""
	}
	if info.Height > 0 && info.Height <= t.MaxHeight {
		return ""
	}
	// -2 keeps the width even (what H.264 needs) and proportional.
	return "scale=-2:min(" + strconv.Itoa(t.MaxHeight) + "\\,ih)"
}

// detectHardware asks ffmpeg what this build and this machine can do.
func detectHardware(ffmpeg, mode string) *hwAccel {
	if ffmpeg == "" {
		return nil
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = hwAuto
	}
	if isDisabled(mode) {
		return nil
	}
	accels := ffmpegHwAccels(ffmpeg)
	encoders := ffmpegEncoders(ffmpeg)

	if mode != hwAuto {
		backend := backendFor(mode)
		if backend == nil || !backend.onThisSystem() {
			logf("transcoder: OFFLINEU_HWACCEL=%s is not available on %s, using libx264", mode, runtime.GOOS)
			return nil
		}
		if accel := backend.build(accels, encoders); accel != nil {
			return accel
		}
		logf("transcoder: OFFLINEU_HWACCEL=%s was asked for but no usable device was found, using libx264", mode)
		return nil
	}
	for _, backend := range hwBackends {
		if !backend.onThisSystem() {
			continue
		}
		if accel := backend.build(accels, encoders); accel != nil {
			return accel
		}
	}
	return nil
}

// ffmpegHwAccels lists the "-hwaccel" values this ffmpeg build understands.
func ffmpegHwAccels(ffmpeg string) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), hwDetectTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-hwaccels").CombinedOutput()
	if err != nil {
		return nil
	}
	found := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		name := strings.ToLower(strings.TrimSpace(line))
		// The header ("Hardware acceleration methods:") and anything with a
		// space is not a method name.
		if name == "" || strings.ContainsAny(name, " :") {
			continue
		}
		found[name] = true
	}
	return found
}

// ffmpegEncoders lists the encoders this ffmpeg build carries.
func ffmpegEncoders(ffmpeg string) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), hwDetectTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders").CombinedOutput()
	if err != nil {
		return nil
	}
	found := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		// " V..... h264_nvenc    NVIDIA NVENC H.264 encoder (codec h264)"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		found[strings.ToLower(fields[1])] = true
	}
	return found
}

// Device checks. Encoders are compiled into ffmpeg whether or not the hardware
// is installed, so each backend asks for its device before it is offered.
func nvidiaPresent() bool {
	// Windows has no device node to look at; NVENC either works or the run
	// fails and OfflineU falls back to software.
	if runtime.GOOS != "linux" {
		return true
	}
	return dirHasPrefix("/dev", "nvidia")
}

func qsvPresent() bool {
	// Quick Sync reaches the iGPU through /dev/dri; inside a container that
	// node has to be passed through. On Windows it is a DLL, not a node.
	if runtime.GOOS != "linux" {
		return true
	}
	return hasDRIDevice()
}

func amfPresent() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	return fileExists("/dev/kfd")
}

// hasDRIDevice reports whether the machine exposes a DRM render node, which is
// what VAAPI, Quick Sync and Rockchip need inside a container.
func hasDRIDevice() bool {
	return firstRenderNode() != "" || dirHasPrefix("/dev/dri", "card")
}

func firstRenderNode() string {
	entries, err := os.ReadDir("/dev/dri")
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "renderD") {
			return filepath.Join("/dev/dri", entry.Name())
		}
	}
	return ""
}

func dirHasPrefix(dir, prefix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
