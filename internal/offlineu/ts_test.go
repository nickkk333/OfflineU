package offlineu

import (
	"os"
	"path/filepath"
	"testing"
)

// ".ts" is genuinely ambiguous: it is a transport stream in a course folder and
// a TypeScript source file in a code folder. The parser has to look at the file
// instead of trusting the name.
func TestTSExtensionIsDecidedByContent(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "第01课时.课程概述.ts")
	source := filepath.Join(dir, "player.ts")

	writeFile(t, video, tsStream(4))
	writeFile(t, source, []byte("export function play(): void {\n  console.log('hi')\n}\n"))

	if !looksLikeVideo(video, ".ts") {
		t.Error("a transport stream must be recognised as a video")
	}
	if looksLikeVideo(source, ".ts") {
		t.Error("a TypeScript source file must not become a video lesson")
	}
	// Other extensions are trusted without a look inside.
	if !looksLikeVideo(source, ".mp4") {
		t.Error("an unambiguous extension must not be checked")
	}
}

// A Blu-ray style .m2ts wraps every 188-byte packet in a 4-byte timestamp.
func TestM2TSPacketsAreRecognised(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lesson.m2ts")
	// Every packet of a .m2ts stream carries the 4-byte timestamp, so the sync
	// byte sits at 4 and then every 192 bytes after it.
	prefixed := append([]byte{0x00, 0x01, 0x02, 0x03}, tsPacket()...)
	stream := []byte{}
	for index := 0; index < 4; index++ {
		stream = append(stream, prefixed...)
	}
	writeFile(t, path, stream)

	if !looksLikeVideo(path, ".m2ts") {
		t.Error("a timestamped .m2ts stream must be recognised")
	}
}

// A .ts file that is too short to be a stream is not one.
func TestEmptyTSFileIsNotAVideo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.ts")
	writeFile(t, path, nil)
	if looksLikeVideo(path, ".ts") {
		t.Error("an empty file must not become a video lesson")
	}
	if looksLikeVideo(filepath.Join(dir, "missing.ts"), ".ts") {
		t.Error("a missing file must not become a video lesson")
	}
}

func tsPacket() []byte {
	packet := make([]byte, 188)
	packet[0] = tsSyncByte
	for index := range packet[1:] {
		packet[index+1] = byte(index)
	}
	return packet
}

// tsStream builds a stream of packets. A platform header in front of the first
// packet is what real course files look like, so the parser must find the sync
// byte rather than expecting it at offset 0.
func tsStream(packets int) []byte {
	stream := []byte{}
	for index := 0; index < packets; index++ {
		stream = append(stream, tsPacket()...)
	}
	return stream
}

// A course platform's own header in front of the stream must not hide it.
func TestTSWithALeadingHeaderIsRecognised(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "第01课时.课程概述.ts")
	header := make([]byte, 188)
	for index := range header {
		header[index] = 0xff
	}
	writeFile(t, path, append(header, tsStream(4)...))

	if !looksLikeVideo(path, ".ts") {
		t.Error("a transport stream behind a 188-byte header must be recognised")
	}
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("cannot write %s: %v", path, err)
	}
}
