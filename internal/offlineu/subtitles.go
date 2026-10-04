package offlineu

import (
	"bytes"
	"log"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// logf writes a diagnostic message to stderr (standard library logger).
func logf(format string, args ...any) {
	log.Printf(format, args...)
}

var (
	timestampPattern = regexp.MustCompile(`(\d{1,3}:\d{2}:\d{2}),(\d{3})`)
	indexLinePattern = regexp.MustCompile(`^\s*\d+\s*$`)
)

// ConvertSRTToVTT converts SubRip subtitles to WebVTT, because browsers only
// understand VTT for <track> elements.
func ConvertSRTToVTT(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		if indexLinePattern.MatchString(line) {
			continue
		}
		lines = append(lines, timestampPattern.ReplaceAllString(line, "$1.$2"))
	}
	body := strings.Trim(strings.Join(lines, "\n"), "\n")
	if body == "" {
		return "WEBVTT\n"
	}
	return "WEBVTT\n\n" + body + "\n"
}

// ReadSubtitleText reads a subtitle file, trying UTF-8 first and falling back to
// Windows-1252 (which is also a superset of Latin-1 for 0xA0-0xFF).
func ReadSubtitleText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return string(data), nil
	}
	return decodeCP1252(data), nil
}

func decodeCP1252(data []byte) string {
	var builder strings.Builder
	for _, value := range data {
		if mapped, ok := cp1252Table[value]; ok {
			builder.WriteRune(mapped)
			continue
		}
		builder.WriteRune(rune(value))
	}
	return builder.String()
}

var cp1252Table = map[byte]rune{
	0x80: '\u20AC', 0x82: '\u201A', 0x83: '\u0192', 0x84: '\u201E',
	0x85: '\u2026', 0x86: '\u2020', 0x87: '\u2021', 0x88: '\u02C6',
	0x89: '\u2030', 0x8A: '\u0160', 0x8B: '\u2039', 0x8C: '\u0152',
	0x8E: '\u017D', 0x91: '\u2018', 0x92: '\u2019', 0x93: '\u201C',
	0x94: '\u201D', 0x95: '\u2022', 0x96: '\u2013', 0x97: '\u2014',
	0x98: '\u02DC', 0x99: '\u2122', 0x9A: '\u0161', 0x9B: '\u203A',
	0x9C: '\u0153', 0x9E: '\u017E', 0x9F: '\u0178',
}
