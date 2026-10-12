package offlineu

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// buildMinimalDocx zips a word/document.xml with one bold heading and one
// paragraph, the smallest structure Word and our preview both understand.
func buildMinimalDocx(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(documentXML)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

const sampleDocxXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>行程安排</w:t></w:r></w:p>
<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>第一天：</w:t></w:r><w:r><w:t>维多利亚港</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>城市</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body>
</w:document>`

func TestDocxPreviewRendersParagraphsHeadingsAndTables(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 1/07 - Guide.docx", buildMinimalDocx(t, sampleDocxXML))
	env.loadCourse()

	title, body, truncated, err := PreviewDocument(filepath.Join(env.courseDir, "Section 1", "07 - Guide.docx"))
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if truncated {
		t.Error("a small document must not be truncated")
	}
	if title == "" {
		t.Error("title must not be empty")
	}
	for _, want := range []string{`<h1>行程安排</h1>`, `<strong>第一天：</strong>`, `<table>`, `维多利亚港`} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q: %s", want, body)
		}
	}
}

func TestPreviewEndpointServesAnInlinePage(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 1/07 - Guide.docx", buildMinimalDocx(t, sampleDocxXML))
	env.loadCourse()

	response := env.request(http.MethodGet, "/api/preview?path="+EscapeURLPath("Section 1/07 - Guide.docx"), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("content type = %q", got)
	}
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("preview page must lock scripts down, CSP = %q", csp)
	}
	body := response.Body.String()
	if !strings.Contains(body, "行程安排") || !strings.Contains(body, "docpreview") {
		t.Errorf("page misses the converted document: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Error("the preview page must not contain any script")
	}
}

func TestPreviewEndpointRejectsForeignAndMissingPaths(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	cases := []struct {
		path string
		want int
	}{
		{EscapeURLPath("Section 1/02 - Notes.txt"), http.StatusBadRequest}, // not an office file
		{"..%2F..%2Fsecret.txt", http.StatusForbidden},                     // traversal
		{EscapeURLPath("Section 1/nope.docx"), http.StatusNotFound},
	}
	for _, testCase := range cases {
		response := env.request(http.MethodGet, "/api/preview?path="+testCase.path, nil)
		if response.Code != testCase.want {
			t.Errorf("path %q: status = %d, want %d", testCase.path, response.Code, testCase.want)
		}
	}
}

func TestRtfPreviewKeepsTextAndBold(t *testing.T) {
	env := newTestEnv(t)
	rtf := `{\rtf1\ansi{\b Bold line}\par Second paragraph}`
	path := env.write("Section 1/08 - Notes.rtf", []byte(rtf))
	_, body, _, err := PreviewDocument(path)
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if !strings.Contains(body, "<strong>") || !strings.Contains(body, "Bold line") {
		t.Errorf("bold text lost: %s", body)
	}
	if !strings.Contains(body, "Second paragraph") {
		t.Errorf("paragraph lost: %s", body)
	}
}

func TestImageFilesBecomeRenderableLessons(t *testing.T) {
	env := newTestEnv(t)
	// 1x1 transparent PNG.
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	env.write("Section 3/cover.png", png)
	env.write("Section 3/screenshot.jpg", []byte{0xff, 0xd8, 0xff, 0xdb})
	env.loadCourse()

	lessons := env.lessonsByPath()
	cover := lessons["Section 3/cover.png"]
	if cover == nil {
		t.Fatal("png lesson missing")
	}
	if cover.LessonType != "image" || cover.ImageMime != "image/png" {
		t.Errorf("cover = %+v", cover)
	}
	if cover.ImageSrc() != "/files/Section%203/cover.png" {
		t.Errorf("image src = %q", cover.ImageSrc())
	}
	if cover.HasMedia() {
		t.Error("an image must not be treated as playable media")
	}
	shot := lessons["Section 3/screenshot.jpg"]
	if shot == nil || shot.LessonType != "image" || shot.ImageMime != "image/jpeg" {
		t.Errorf("jpg lesson = %+v", shot)
	}

	response := env.request(http.MethodGet, "/files/"+EscapeURLPath("Section 3/cover.png"), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("content type = %q", got)
	}
	if got := response.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Errorf("content disposition = %q", got)
	}
}

func TestMarkdownLessonsUseTheRenderedMode(t *testing.T) {
	env := newTestEnv(t)
	env.loadCourse()
	lessons := env.lessonsByPath()
	resources := BuildTextResources(lessons["Section 2/resources/extras.md"])
	if len(resources) != 1 || resources[0].Mode != "markdown" {
		t.Errorf("md resources = %+v", resources)
	}
}

// TestDocPreviewSkipsMisalignedSingleByteStretches pins the garbling bug: the
// single byte text inside a .doc must be read as ASCII, not re-paired into
// CJK code points (which is where the mojibake came from).
func TestDocPreviewSkipsMisalignedSingleByteStretches(t *testing.T) {
	env := newTestEnv(t)
	var buffer bytes.Buffer
	buffer.WriteString("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1") // OLE magic
	buffer.WriteString(strings.Repeat("\x00\x01\x02\x03", 16))
	single := "single byte english text inside the doc"
	buffer.WriteString(single)
	buffer.WriteString(strings.Repeat("\x00\x01\x02\x03", 16))
	// Real UTF-16LE Chinese text (as Word stores it in a non-compressed piece).
	for _, unit := range utf16.Encode([]rune("第一天去维多利亚港游玩购物")) {
		var pair [2]byte
		binary.LittleEndian.PutUint16(pair[:], unit)
		buffer.Write(pair[:])
	}
	// What the old reader produced: 's','i' mis-paired into U+6973.
	mispaired := string(rune(single[0]) | rune(single[1])<<8)

	path := env.write("Section 1/09 - Legacy.doc", buffer.Bytes())
	_, body, _, err := PreviewDocument(path)
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if !strings.Contains(body, "第一天去维多利亚港游玩购物") {
		t.Errorf("UTF-16 Chinese text lost: %s", body)
	}
	if !strings.Contains(body, single) {
		t.Errorf("single byte ASCII text lost: %s", body)
	}
	if strings.Contains(body, mispaired) {
		t.Errorf("mispaired mojibake %q leaked into the preview: %s", mispaired, body)
	}
}
