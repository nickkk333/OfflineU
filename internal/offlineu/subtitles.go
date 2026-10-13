package offlineu

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// ConvertSubtitleToVTT converts a subtitle file of any format OfflineU
// recognises into WebVTT - the only format browsers accept in a <track>. The
// file's extension decides how the text is parsed; unknown extensions keep the
// legacy SubRip treatment.
func ConvertSubtitleToVTT(path string, text string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".vtt":
		return ensureVTTHeader(normalizeEOL(text))
	case ".sbv":
		return convertIntervalToVTT(text)
	case ".sub":
		if looksLikeMicroDVD(text) {
			return convertMicroDVDToVTT(text)
		}
		// The other common .sub flavour (SubViewer) uses the same
		// "start,end" interval lines as SBV.
		return convertIntervalToVTT(text)
	case ".ass", ".ssa":
		return convertASSToVTT(text)
	default:
		return ConvertSRTToVTT(text)
	}
}

// normalizeEOL makes every line ending a plain "\n" and drops the BOM, so the
// parsers below may assume a single convention.
func normalizeEOL(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// ensureVTTHeader returns a header-complete WebVTT document.
func ensureVTTHeader(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "WEBVTT\n"
	}
	if strings.HasPrefix(trimmed, "WEBVTT") {
		return text
	}
	return "WEBVTT\n\n" + strings.Trim(text, "\n") + "\n"
}

// vttDocument wraps cue blocks ("start --> end\npayload") in a valid file.
func vttDocument(cues []string) string {
	body := strings.TrimSpace(strings.Join(cues, "\n\n"))
	if body == "" {
		return "WEBVTT\n"
	}
	return "WEBVTT\n\n" + body + "\n"
}

// clockFromMS formats a millisecond total as HH:MM:SS.mmm.
func clockFromMS(total int) string {
	if total < 0 {
		total = 0
	}
	hours := total / 3600000
	total %= 3600000
	minutes := total / 60000
	total %= 60000
	seconds := total / 1000
	milliseconds := total % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, milliseconds)
}

// fractionMs interprets a sub-second fraction with 1-3 digits: SBV writes
// milliseconds (3 digits), SubViewer and ASS write centiseconds (2 digits).
func fractionMs(fraction string) int {
	if fraction == "" {
		return 0
	}
	value, err := strconv.Atoi(fraction)
	if err != nil {
		return 0
	}
	switch len(fraction) {
	case 1:
		return value * 100
	case 2:
		return value * 10
	default:
		return value
	}
}

// intervalTimingPattern matches the "start,end" timing line shared by SBV
// (`0:00:01.000,0:00:04.000`) and SubViewer .sub (`0:00:01,00,0:00:04,00`):
// both separate the fields with a comma and the fraction with "." or ",".
var intervalTimingPattern = regexp.MustCompile(
	`^(\d{1,2}):(\d{2}):(\d{2})(?:[.,](\d{1,3}))?\s*,\s*(\d{1,2}):(\d{2}):(\d{2})(?:[.,](\d{1,3}))?$`)

// convertIntervalToVTT converts the "start,end on one line" family (SBV and
// SubViewer). Text outside a cue block - the SubViewer [INFORMATION] header
// and [SUBTITLE] markers - is dropped, everything after a timing line is cue
// payload.
func convertIntervalToVTT(text string) string {
	cues := []string{}
	current := []string{}
	flush := func() {
		if len(current) > 0 {
			cues = append(cues, strings.Join(current, "\n"))
			current = nil
		}
	}
	for _, line := range strings.Split(normalizeEOL(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if match := intervalTimingPattern.FindStringSubmatch(trimmed); match != nil {
			flush()
			start := clockFromMS(hoursToMS(match[1], match[2], match[3]) + fractionMs(match[4]))
			end := clockFromMS(hoursToMS(match[5], match[6], match[7]) + fractionMs(match[8]))
			current = []string{start + " --> " + end}
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		if len(current) == 0 {
			continue // header / section marker before the first cue
		}
		current = append(current, line)
	}
	flush()
	return vttDocument(cues)
}

// hoursToMS adds up the h/m/s groups of a timing match (validation-free: the
// regex already constrained them).
func hoursToMS(hours, minutes, seconds string) int {
	h, _ := strconv.Atoi(hours)
	m, _ := strconv.Atoi(minutes)
	s, _ := strconv.Atoi(seconds)
	return ((h*60+m)*60 + s) * 1000
}

// assTimePattern matches the ASS/SSA timestamp "H:MM:SS.cc" (hours may grow
// beyond one digit in long files).
var assTimePattern = regexp.MustCompile(`^(\d+):(\d{2}):(\d{2})\.(\d{1,3})$`)

// assOverridePattern removes "{\i1}"-style override blocks from cue text.
var assOverridePattern = regexp.MustCompile(`\{\\[^}]*\}`)

// convertASSToVTT parses Advanced/SubStation Alpha events: header sections
// are skipped, the Format line of [Events] decides the column layout (Start,
// End and Text move around between writers), and override tags plus the \N
// line breaks are translated into plain WebVTT markup.
func convertASSToVTT(text string) string {
	cues := []string{}
	section := ""
	startCol, endCol, textCol := 1, 2, 9 // V4+ default: Layer,Start,End,...,Text
	for _, line := range strings.Split(normalizeEOL(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(trimmed)
			continue
		}
		if section != "" && section != "[events]" {
			continue // [Script Info], [V4+ Styles], ...
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "format:") {
			if s, e, t, ok := parseASSColumns(trimmed[len("format:"):]); ok {
				startCol, endCol, textCol = s, e, t
			}
			continue
		}
		if !strings.HasPrefix(strings.ToLower(trimmed), "dialogue:") {
			continue
		}
		fields := splitASSFields(trimmed[len("dialogue:"):], textCol+1)
		if len(fields) <= textCol || len(fields) <= startCol || len(fields) <= endCol {
			continue
		}
		start, okStart := parseASSTime(fields[startCol])
		end, okEnd := parseASSTime(fields[endCol])
		if !okStart || !okEnd {
			continue
		}
		payload := cleanASSText(fields[textCol])
		if payload == "" {
			continue
		}
		cues = append(cues, start+" --> "+end+"\n"+payload)
	}
	return vttDocument(cues)
}

// parseASSColumns locates the Start/End/Text columns of an ASS Format line.
func parseASSColumns(header string) (start, end, text int, ok bool) {
	start, end, text = -1, -1, -1
	for index, name := range strings.Split(header, ",") {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "start":
			start = index
		case "end":
			end = index
		case "text":
			text = index
		}
	}
	return start, end, text, start >= 0 && end >= 0 && text >= 0
}

// splitASSFields splits a Dialogue payload, keeping commas inside the Text
// column (it is always the last one of interest, so a bounded SplitN is safe).
func splitASSFields(payload string, want int) []string {
	parts := strings.SplitN(payload, ",", want)
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

// parseASSTime converts "H:MM:SS.cc" to a WebVTT timestamp.
func parseASSTime(value string) (string, bool) {
	match := assTimePattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return "", false
	}
	hours, _ := strconv.Atoi(match[1])
	minutes, _ := strconv.Atoi(match[2])
	seconds, _ := strconv.Atoi(match[3])
	return clockFromMS(((hours*60+minutes)*60+seconds)*1000 + fractionMs(match[4])), true
}

// cleanASSText strips override blocks and maps ASS line breaks to newlines.
func cleanASSText(payload string) string {
	payload = assOverridePattern.ReplaceAllString(payload, "")
	payload = strings.ReplaceAll(payload, `\N`, "\n")
	payload = strings.ReplaceAll(payload, `\n`, "\n")
	return strings.TrimSpace(payload)
}

// microDVDPattern matches "{startFrame}{endFrame}text" lines of MicroDVD
// .sub files (frames, not timestamps - see convertMicroDVDToVTT).
var microDVDPattern = regexp.MustCompile(`^\{(\d+)\}\{(\d+)\}(.*)$`)

// microDVDCodePattern removes "{y:i}"-style MicroDVD formatting codes.
var microDVDCodePattern = regexp.MustCompile(`\{[^}]*\}`)

// looksLikeMicroDVD sniffs the frame based .sub flavour: every cue line is a
// {start}{end} pair (the alternative, SubViewer, uses "start,end" timings and
// is handled by convertIntervalToVTT).
func looksLikeMicroDVD(text string) bool {
	for _, line := range strings.Split(normalizeEOL(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return microDVDPattern.MatchString(trimmed)
	}
	return false
}

// convertMicroDVDToVTT turns frame numbers into timestamps. MicroDVD stores
// no frame rate, so the declared rate of the first line ({1}{1}25.000) wins
// and 25 fps is the documented fallback - the standard guess for such files.
func convertMicroDVDToVTT(text string) string {
	const defaultFPS = 25.0
	fps := defaultFPS
	cues := []string{}
	firstLine := true
	for _, line := range strings.Split(normalizeEOL(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		match := microDVDPattern.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		startFrame, _ := strconv.Atoi(match[1])
		endFrame, _ := strconv.Atoi(match[2])
		payload := strings.TrimSpace(match[3])
		if firstLine {
			firstLine = false
			// "{1}{1}25.000" style header: a single number is the frame rate.
			if startFrame == endFrame {
				if declared, err := strconv.ParseFloat(payload, 64); err == nil && declared > 0 {
					fps = declared
					continue
				}
			}
		}
		payload = strings.TrimSpace(microDVDCodePattern.ReplaceAllString(payload, ""))
		if payload == "" || endFrame < startFrame {
			continue
		}
		start := clockFromMS(int(float64(startFrame) * 1000 / fps))
		end := clockFromMS(int(float64(endFrame) * 1000 / fps))
		cues = append(cues, start+" --> "+end+"\n"+payload)
	}
	return vttDocument(cues)
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
