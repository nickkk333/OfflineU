// Document preview converts word processor files (.docx/.doc/.rtf) to safe
// HTML so the browser can show them without any desktop application.
//
// Only the Go standard library is used (archive/zip + encoding/xml), so the
// result stays a single static binary and works fully offline:
//
//   - .docx is a zip of XML: word/document.xml carries the paragraphs; bold,
//     italic, headings, lists, tables and hyperlinks are mapped to simple
//     HTML. Embedded images (word/media/*) are inlined as data: URLs when
//     they are small, otherwise skipped.
//   - .doc (the legacy OLE binary) cannot be parsed faithfully without a
//     dedicated library, so readable text runs are extracted instead -
//     formatting is lost but the content is still readable.
//   - .rtf is parsed for plain text plus bold/italic/underline, paragraphs
//     and line breaks; everything else (fonts, colours, objects) is skipped.
//
// Everything is capped (maxPreviewBytes) and every string is HTML-escaped, so
// a hostile document can neither blow up memory nor inject scripts. The
// preview page itself carries no <script> at all.
package offlineu

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

// maxPreviewBytes caps how much of a document is read for a preview: a 200 MB
// manual still opens (its beginning is shown with a truncation note) instead
// of exhausting a small NAS.
const maxPreviewBytes = 8 << 20 // 8 MiB

// maxInlineImageBytes caps a single embedded .docx image inlined as data: URL.
const maxInlineImageBytes = 512 << 10 // 512 KiB

// PreviewDocument converts path (inside the active course) to a standalone
// HTML fragment: title, body HTML and whether the source was truncated.
func PreviewDocument(path string) (title, body string, truncated bool, err error) {
	data, truncated, err := readCapped(path, maxPreviewBytes)
	if err != nil {
		return "", "", false, err
	}
	base := CleanLessonName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx":
		body, truncatedDocx, convErr := docxToHTML(data)
		return base, body, truncated || truncatedDocx, convErr
	case ".doc":
		return base, docToHTML(data), truncated, nil
	case ".rtf":
		return base, rtfToHTML(data), truncated, nil
	default:
		return "", "", false, fmt.Errorf("no preview available for %s", filepath.Ext(path))
	}
}

func readCapped(path string, limit int) ([]byte, bool, error) {
	handle, err := openForPreview(path)
	if err != nil {
		return nil, false, err
	}
	defer handle.Close()
	limited := io.LimitReader(handle, int64(limit)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

// openForPreview opens an absolute course file for reading. Callers pass the
// already-validated result of ResolveInside, so this is just os.Open - kept
// as a helper so tests can stub it if needed.
func openForPreview(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// docxToHTML parses word/document.xml out of the .docx zip.
func docxToHTML(data []byte) (string, bool, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// Not a zip at all: fall back to text extraction like .doc.
		return docToHTML(data), false, nil
	}
	var documentXML []byte
	images := map[string][]byte{}
	truncated := false
	for _, file := range reader.File {
		// Archives built on Windows (Compress-Archive, some Office tools) may
		// use backslashes inside the zip; normalise before comparing.
		name := strings.ToLower(strings.ReplaceAll(file.Name, "\\", "/"))
		switch {
		case name == "word/document.xml":
			rc, err := file.Open()
			if err != nil {
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(rc, maxPreviewBytes+1))
			rc.Close()
			if err != nil {
				continue
			}
			if len(raw) > maxPreviewBytes {
				raw = raw[:maxPreviewBytes]
				truncated = true
			}
			documentXML = raw
		case strings.HasPrefix(name, "word/media/"):
			if file.UncompressedSize64 > maxInlineImageBytes {
				continue
			}
			rc, err := file.Open()
			if err != nil {
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(rc, maxInlineImageBytes+1))
			rc.Close()
			if err != nil || len(raw) > maxInlineImageBytes {
				continue
			}
			images[file.Name] = raw
		}
	}
	if documentXML == nil {
		// A zip without word/document.xml (or one whose XML is unreadable):
		// binary text extraction would only produce noise, so say so instead.
		return `<div class="docpreview"><p>The document body could not be read.</p></div>`, truncated, nil
	}
	return renderDocxXML(documentXML, images), truncated, nil
}

// docxNode keeps only what the preview renders.
type docxNode struct {
	XMLName  xml.Name
	Val      string     `xml:"val,attr"`
	Link     string     `xml:"tgt,attr"`
	Embed    string     `xml:"embed,attr"`
	Children []docxNode `xml:",any"`
	Text     string     `xml:",chardata"`
}

func renderDocxXML(raw []byte, images map[string][]byte) string {
	var root docxNode
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity
	if err := decoder.Decode(&root); err != nil {
		return "<p>" + html.EscapeString(stripControl(string(raw))) + "</p>"
	}
	var out strings.Builder
	out.WriteString(`<div class="docpreview">`)
	listOpen := ""
	flushList := func() {
		if listOpen != "" {
			if listOpen == "ol" {
				out.WriteString("</ol>")
			} else {
				out.WriteString("</ul>")
			}
			listOpen = ""
		}
	}
	walkDocx(&root, &out, images, &listOpen, flushList)
	flushList()
	out.WriteString("</div>")
	return out.String()
}

func walkDocx(node *docxNode, out *strings.Builder, images map[string][]byte, listOpen *string, flushList func()) {
	name := strings.ToLower(node.XMLName.Local)
	switch name {
	case "p":
		style := ""
		for i := range node.Children {
			child := &node.Children[i]
			if strings.ToLower(child.XMLName.Local) == "ppr" {
				for j := range child.Children {
					prop := &child.Children[j]
					if strings.ToLower(prop.XMLName.Local) == "pstyle" {
						style = strings.ToLower(prop.Val)
					}
				}
			}
		}
		inner := renderDocxInline(node, images)
		if strings.TrimSpace(stripTags(inner)) == "" && !strings.Contains(inner, "<img") {
			return
		}
		isList := strings.HasPrefix(style, "list") || style == "listparagraph"
		isOrdered := strings.Contains(style, "number") || strings.Contains(style, "ordered")
		switch {
		case strings.HasPrefix(style, "heading"):
			flushList()
			level := 2
			for _, digit := range style {
				if digit >= '1' && digit <= '6' {
					level = int(digit - '0')
					break
				}
			}
			fmt.Fprintf(out, "<h%d>%s</h%d>", level, inner, level)
		case style == "title":
			flushList()
			out.WriteString("<h1>" + inner + "</h1>")
		case style == "quote" || style == "intensequote":
			flushList()
			out.WriteString("<blockquote>" + inner + "</blockquote>")
		case isList:
			want := "ul"
			if isOrdered {
				want = "ol"
			}
			if *listOpen != want {
				flushList()
				out.WriteString("<" + want + ">")
				*listOpen = want
			}
			out.WriteString("<li>" + inner + "</li>")
		default:
			flushList()
			out.WriteString("<p>" + inner + "</p>")
		}
	case "tbl":
		flushList()
		out.WriteString("<table>")
		for i := range node.Children {
			walkDocx(&node.Children[i], out, images, listOpen, flushList)
		}
		out.WriteString("</table>")
	case "tr":
		out.WriteString("<tr>")
		for i := range node.Children {
			walkDocx(&node.Children[i], out, images, listOpen, flushList)
		}
		out.WriteString("</tr>")
	case "tc":
		out.WriteString("<td>")
		for i := range node.Children {
			walkDocx(&node.Children[i], out, images, listOpen, flushList)
		}
		out.WriteString("</td>")
	default:
		for i := range node.Children {
			walkDocx(&node.Children[i], out, images, listOpen, flushList)
		}
	}
}

func renderDocxInline(para *docxNode, images map[string][]byte) string {
	var out strings.Builder
	for i := range para.Children {
		child := &para.Children[i]
		switch strings.ToLower(child.XMLName.Local) {
		case "r":
			out.WriteString(renderDocxRun(child, images))
		case "hyperlink":
			inner := ""
			for j := range child.Children {
				inner += renderDocxRun(&child.Children[j], images)
			}
			href := html.EscapeString(child.Link)
			if href == "" {
				out.WriteString(inner)
			} else {
				out.WriteString(`<a href="` + href + `" target="_blank" rel="noopener">` + inner + "</a>")
			}
		case "drawing", "pict":
			if src, alt := docxImage(child, images); src != "" {
				out.WriteString(`<img src="` + src + `" alt="` + html.EscapeString(alt) + `" loading="lazy">`)
			}
		}
	}
	return out.String()
}

func renderDocxRun(run *docxNode, images map[string][]byte) string {
	bold, italic, underline, strike := false, false, false, false
	var text strings.Builder
	extra := ""
	for i := range run.Children {
		child := &run.Children[i]
		switch strings.ToLower(child.XMLName.Local) {
		case "rpr":
			for j := range child.Children {
				switch strings.ToLower(child.Children[j].XMLName.Local) {
				case "b":
					bold = true
				case "i":
					italic = true
				case "u":
					underline = true
				case "strike", "dstrike":
					strike = true
				}
			}
		case "t":
			text.WriteString(child.Text)
		case "tab":
			text.WriteString("    ")
		case "br":
			extra += "<br>"
		case "drawing", "pict":
			if src, alt := docxImage(child, images); src != "" {
				text.WriteString(`<img src="` + src + `" alt="` + html.EscapeString(alt) + `" loading="lazy">`)
			}
		}
	}
	content := html.EscapeString(text.String()) + extra
	if content == "" {
		return ""
	}
	if strike {
		content = "<s>" + content + "</s>"
	}
	if underline {
		content = "<u>" + content + "</u>"
	}
	if italic {
		content = "<em>" + content + "</em>"
	}
	if bold {
		content = "<strong>" + content + "</strong>"
	}
	return content
}

func docxImage(node *docxNode, images map[string][]byte) (src, alt string) {
	if len(images) == 0 {
		return "", ""
	}
	raw, _ := xml.Marshal(node)
	if m := regexp.MustCompile(`descr="([^"]+)"`).FindSubmatch(raw); len(m) == 2 {
		alt = string(m[1])
	}
	for name, data := range images {
		src = "data:" + guessImageMime(name) + ";base64," + base64.StdEncoding.EncodeToString(data)
		return src, alt
	}
	return "", ""
}

func guessImageMime(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "image/png"
	}
}

// docToHTML renders a legacy binary .doc (OLE) as paragraphs of text.
//
// The document is read through Word's own structures (see docbinary.go): the
// piece table names every text run and the encoding it was stored in, so
// English and CJK passages come out exactly as written. Files without that
// index (Word 6/95, truncated downloads, hand-made test data) still work
// through the byte scan in guessedDocParagraphs.
func docToHTML(data []byte) string {
	paragraphs := docParagraphs(data)
	var out strings.Builder
	out.WriteString(`<div class="docpreview">`)
	if len(paragraphs) == 0 {
		out.WriteString("<p>(no readable text found in this document)</p>")
	} else {
		for _, p := range paragraphs {
			out.WriteString("<p>" + html.EscapeString(p) + "</p>")
		}
	}
	out.WriteString("</div>")
	return out.String()
}

// docParagraphs returns the text of a .doc as paragraphs, preferring the real
// Word structures over the guessed ones.
func docParagraphs(data []byte) []string {
	if text, ok := docBodyText(data); ok {
		if paragraphs := splitParagraphs(text); len(paragraphs) > 0 {
			return paragraphs
		}
	}
	return guessedDocParagraphs(data)
}

// guessedDocParagraphs is the fallback for .doc files no index can be read
// from.
//
// Two text flavours can live in such bytes: UTF-16LE pieces (CJK text) and
// single byte pieces (English text, often stored without padding). Reading the
// single byte stretches as UTF-16 pairs every neighbouring character into a
// CJK code point - "word" becomes gibberish hanzi. To prevent that, every
// contiguous run of printable single byte text is claimed first; the UTF-16
// pass skips any pair that touches such a run and only keeps pairs which
// really look like UTF-16 text (aligned NUL-padded ASCII or CJK).
func guessedDocParagraphs(data []byte) []string {
	// Single byte ASCII stretches (at any alignment).
	type span struct{ start, end int }
	asciiPattern := regexp.MustCompile(`[\x20-\x7e]{8,}`)
	spans := []span{}
	asciiTexts := []string{}
	for _, loc := range asciiPattern.FindAllIndex(data, -1) {
		candidate := string(data[loc[0]:loc[1]])
		// Random Chinese UTF-16 bytes can accidentally form a printable
		// stretch too: only stretches that read like English prose (letters,
		// digits and spaces, with a space in it) claim the bytes - the
		// garbled pairs of a real CJK passage then stay in the UTF-16 pass.
		if !looksLikeSingleByteProse(candidate) {
			continue
		}
		spans = append(spans, span{loc[0], loc[1]})
		asciiTexts = append(asciiTexts, strings.TrimSpace(candidate))
	}
	overlapsASCII := func(i int) bool {
		for _, s := range spans {
			if i < s.end && i+1 >= s.start {
				return true
			}
		}
		return false
	}

	// UTF-16 pass: Word pieces may start at an odd byte offset, so the text is
	// decoded once per alignment and the more productive pass wins - a wrong
	// alignment cannot form runs of the minimum length and scores ~nothing,
	// while the right one yields whole sentences.
	collect := func(align int) []string {
		found := []string{}
		var current []uint16
		flush := func() {
			if len(current) >= 8 {
				text := strings.TrimSpace(string(utf16.Decode(current)))
				if text != "" {
					found = append(found, text)
				}
			}
			current = nil
		}
		for i := align; i+1 < len(data); i += 2 {
			if overlapsASCII(i) {
				flush()
				continue
			}
			lo, hi := data[i], data[i+1]
			r := rune(lo) | rune(hi)<<8
			if hi == 0 && lo >= 0x20 && lo < 0x7f {
				current = append(current, uint16(r))
				continue
			}
			if isCJK(r) || isDocPunct(r) {
				current = append(current, uint16(r))
				continue
			}
			flush()
		}
		flush()
		return found
	}
	score := func(parts []string) int {
		total := 0
		for _, part := range parts {
			total += len([]rune(part))
		}
		return total
	}
	texts := collect(0)
	if other := collect(1); score(other) > score(texts) {
		texts = other
	}
	for _, s := range asciiTexts {
		if s == "" {
			continue
		}
		if strings.Contains(s, "Microsoft") || strings.Contains(s, "Word.Document") ||
			strings.Contains(s, "ObjInfo") || strings.Contains(s, "Root Entry") {
			continue
		}
		dup := false
		for _, t := range texts {
			if strings.Contains(t, s) || strings.Contains(s, t) {
				dup = true
				break
			}
		}
		if !dup {
			texts = append(texts, s)
		}
	}
	paragraphs := splitParagraphs(strings.Join(texts, "\n"))
	if paragraphs == nil {
		return []string{}
	}
	return paragraphs
}

// looksLikeSingleByteProse decides whether a printable run really is English
// text stored in a single byte piece: it needs an ASCII letter, at least one
// space and a high letters/digits/spaces share. Mis-paired bytes of Chinese
// UTF-16 text fail one of the three, so they are read as UTF-16 instead.
func looksLikeSingleByteProse(s string) bool {
	if len(s) < 8 {
		return false
	}
	good, hasSpace, hasLetter := 0, false, false
	for _, r := range s {
		switch {
		case r == ' ':
			good++
			hasSpace = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			good++
			hasLetter = true
		case r >= '0' && r <= '9':
			good++
		}
	}
	return hasLetter && hasSpace && good*4 >= len(s)*3
}

func isCJK(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf) ||
		(r >= 0x3040 && r <= 0x30ff) || (r >= 0xac00 && r <= 0xd7af) ||
		(r >= 0xff00 && r <= 0xffef)
}

func isDocPunct(r rune) bool {
	switch r {
	case '，', '。', '、', '；', '：', '？', '！', '「', '」', '『', '』', '（', '）',
		'《', '》', '—', '…', '·', '　', '“', '”', '‘', '’':
		return true
	}
	return false
}

// rtfToHTML parses RTF control words for text and basic formatting.
func rtfToHTML(data []byte) string {
	text := string(data)
	if len(text) > maxPreviewBytes {
		text = text[:maxPreviewBytes]
	}
	var out strings.Builder
	out.WriteString(`<div class="docpreview">`)
	var para strings.Builder
	var seg strings.Builder // plain text since the last formatting change
	bold, italic, underline := 0, 0, 0
	flushSeg := func() {
		if seg.Len() == 0 {
			return
		}
		esc := html.EscapeString(seg.String())
		seg.Reset()
		if underline > 0 {
			esc = "<u>" + esc + "</u>"
		}
		if italic > 0 {
			esc = "<em>" + esc + "</em>"
		}
		if bold > 0 {
			esc = "<strong>" + esc + "</strong>"
		}
		para.WriteString(esc)
	}
	flushPara := func() {
		flushSeg()
		p := strings.TrimSpace(para.String())
		para.Reset()
		if p == "" {
			return
		}
		out.WriteString("<p>" + p + "</p>")
	}
	write := func(s string) { seg.WriteString(s) }
	depth := 0
	skipDepth := -1
	// Formatting is scoped to the group it was opened in: every "{" snapshots
	// the current bold/italic/underline state, every "}" restores it, so
	// {\b Bold} Regular renders only "Bold" in bold.
	type rtfState struct{ bold, italic, underline int }
	stack := []rtfState{}
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == '{':
			depth++
			stack = append(stack, rtfState{bold, italic, underline})
			if i+1 < len(text) && text[i+1] == '\\' {
				rest := text[i+1:]
				if strings.HasPrefix(rest, "\\*\\") || strings.HasPrefix(rest, "\\fonttbl") ||
					strings.HasPrefix(rest, "\\colortbl") || strings.HasPrefix(rest, "\\stylesheet") ||
					strings.HasPrefix(rest, "\\header") || strings.HasPrefix(rest, "\\footer") ||
					strings.HasPrefix(rest, "\\pict") || strings.HasPrefix(rest, "\\object") ||
					strings.HasPrefix(rest, "\\info") {
					skipDepth = depth
				}
			}
			i++
		case c == '}':
			if skipDepth == depth {
				skipDepth = -1
			}
			if len(stack) > 0 {
				state := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if state.bold != bold || state.italic != italic || state.underline != underline {
					flushSeg()
					bold, italic, underline = state.bold, state.italic, state.underline
				}
			}
			depth--
			i++
		case c == '\\' && skipDepth < 0:
			j := i + 1
			if j >= len(text) {
				i++
				break
			}
			nc := text[j]
			switch nc {
			case '\\', '{', '}':
				write(string(nc))
				i += 2
			case '\'':
				if j+2 < len(text) {
					var v byte
					_, _ = fmt.Sscanf(text[j+1:j+3], "%02x", &v)
					write(string([]byte{v}))
					i += 4
				} else {
					i += 2
				}
			case '\n', '\r':
				i += 2
			case '*':
				skipDepth = depth
				i += 2
			default:
				k := j
				for k < len(text) && ((text[k] >= 'a' && text[k] <= 'z') || (text[k] >= 'A' && text[k] <= 'Z')) {
					k++
				}
				word := text[j:k]
				m := k
				neg := false
				if m < len(text) && text[m] == '-' {
					neg = true
					m++
				}
				num := 0
				hasNum := false
				for m < len(text) && text[m] >= '0' && text[m] <= '9' {
					num = num*10 + int(text[m]-'0')
					m++
					hasNum = true
				}
				if neg {
					num = -num
				}
				if m < len(text) && text[m] == ' ' {
					m++
				}
				switch word {
				case "par", "line", "row":
					flushPara()
				case "tab":
					write("    ")
				case "b":
					flushSeg()
					if !hasNum || num != 0 {
						bold++
					} else {
						bold = 0
					}
				case "i":
					flushSeg()
					if !hasNum || num != 0 {
						italic++
					} else {
						italic = 0
					}
				case "ul", "ulw", "uld":
					flushSeg()
					if !hasNum || num != 0 {
						underline++
					} else {
						underline = 0
					}
				case "ulnone":
					flushSeg()
					underline = 0
				case "plain", "pard":
					flushSeg()
					bold, italic, underline = 0, 0, 0
				case "ldblquote":
					write("\u201c")
				case "rdblquote":
					write("\u201d")
				case "endash":
					write("–")
				case "emdash":
					write("—")
				case "bullet":
					write("• ")
				case "lquote":
					write("'")
				case "rquote":
					write("'")
				case "u":
					write(string(rune(num)))
					if m < len(text) {
						m++
					}
				case "fonttbl", "colortbl", "stylesheet", "header", "footer", "pict", "object", "info":
					skipDepth = depth
				}
				i = m
			}
		case skipDepth >= 0:
			i++
		case c == '\r' || c == '\n':
			i++
		default:
			write(string(c))
			i++
		}
	}
	flushPara()
	out.WriteString("</div>")
	return out.String()
}

func splitParagraphs(text string) []string {
	lines := regexp.MustCompile(`\r?\n`).Split(text, -1)
	paragraphs := []string{}
	var current []string
	flush := func() {
		p := strings.TrimSpace(strings.Join(current, " "))
		current = nil
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, strings.TrimSpace(line))
		if len(strings.Join(current, " ")) > 600 {
			flush()
		}
	}
	flush()
	return paragraphs
}

var tagStripper = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return tagStripper.ReplaceAllString(s, "") }

func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
