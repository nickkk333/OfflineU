package offlineu

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// buildSampleMHTML packs one HTML page, one PNG and one stylesheet into the
// multipart/related structure browsers write for "save page as".
func buildSampleMHTML() []byte {
	const boundary = "----=_OfflineU_TestBoundary"
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A}
	css := "body { background-image: url(cid:logo.png); }"
	page := "<html><head><link rel=\"stylesheet\" href=\"cid:style.css\"></head>" +
		"<body><h1>离线课程</h1><img src=\"cid:logo.png\" alt=\"logo\">" +
		"<script>alert(1)</script></body></html>"

	var out strings.Builder
	out.WriteString("From: <Saved by OfflineU test>\r\n")
	out.WriteString("Subject: sample\r\n")
	out.WriteString("MIME-Version: 1.0\r\n")
	out.WriteString("Content-Type: multipart/related;\r\n" + "\tboundary=\"" + boundary + "\"\r\n\r\n")
	// the document part, quoted-printable like most writers use for HTML
	out.WriteString("--" + boundary + "\r\n")
	out.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	out.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	out.WriteString("Content-Location: file:///page.html\r\n\r\n")
	out.WriteString(strings.ReplaceAll(page, "\n", "=\r\n"))
	out.WriteString("\r\n--" + boundary + "\r\n")
	out.WriteString("Content-Type: text/css\r\n")
	out.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	out.WriteString("Content-Location: file:///style.css\r\n\r\n")
	out.WriteString(css)
	out.WriteString("\r\n--" + boundary + "\r\n")
	out.WriteString("Content-Type: image/png\r\n")
	out.WriteString("Content-Transfer-Encoding: base64\r\n")
	out.WriteString("Content-ID: <logo.png>\r\n\r\n")
	out.WriteString(base64.StdEncoding.EncodeToString(png))
	out.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(out.String())
}

func TestMhtmlPreviewInlinesPartsAndDropsScripts(t *testing.T) {
	env := newTestEnv(t)
	path := env.write("Section 1/07 - Page.mhtml", buildSampleMHTML())

	title, body, truncated, err := PreviewDocument(path)
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if truncated {
		t.Error("a small archive must not be truncated")
	}
	if title != "Page" {
		t.Errorf("title = %q, want %q", title, "Page")
	}
	for _, want := range []string{"离线课程", "data:image/png;base64,"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q: %s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Error("the unpacked page must not contain any script")
	}
	if strings.Contains(body, "cid:") {
		t.Errorf("references left unrewritten: %s", body)
	}
	// The stylesheet itself becomes a data: URL - decode it to prove its cid:
	// image reference was rewritten before encoding, otherwise every background
	// image inside the page would silently break.
	cssPrefix := "href=\"data:text/css;base64,"
	start := strings.Index(body, cssPrefix)
	if start < 0 {
		t.Fatalf("stylesheet not inlined: %s", body)
	}
	start += len("href=\"data:text/css;base64,")
	end := start
	for end < len(body) && body[end] != '"' {
		end++
	}
	css, err := base64.StdEncoding.DecodeString(body[start:end])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "url(data:image/png;base64,") {
		t.Errorf("css image reference not rewritten: %s", css)
	}
}

func TestMhtmlLessonIsScannedAndRenderedAsConvertedPreview(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 1/09 - Snapshot.mhtml", buildSampleMHTML())
	env.write("Section 1/10 - Snapshot.mht", buildSampleMHTML())
	env.loadCourse()

	lessons := env.lessonsByPath()
	for _, name := range []string{"Section 1/09 - Snapshot.mhtml", "Section 1/10 - Snapshot.mht"} {
		lesson := lessons[name]
		if lesson == nil {
			t.Fatalf("%s was not scanned into a lesson", name)
		}
		resources := BuildTextResources(lesson)
		if len(resources) != 1 || resources[0].Mode != "office" {
			t.Errorf("%s resources = %+v, want one office preview", name, resources)
		}
	}
}

func TestMhtmlPreviewEndpointServesAnInlinePage(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 1/07 - Page.mhtml", buildSampleMHTML())
	env.loadCourse()

	target := "/api/preview?path=" + EscapeURLPath("Section 1/07 - Page.mhtml")
	response := env.request(http.MethodGet, target, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("content type = %q", got)
	}
	body := response.Body.String()
	if !strings.Contains(body, "离线课程") {
		t.Errorf("page misses the unpacked document: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Error("the preview page must not contain any script")
	}
	// data: URLs are how the archive carries its images and styles, so the CSP
	// has to allow them (scripts stay blocked by default-src 'none').
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "img-src data:") {
		t.Errorf("preview CSP = %q", csp)
	}
}

func TestMhtmlPreviewRejectsGarbageAndForeignPaths(t *testing.T) {
	env := newTestEnv(t)
	env.write("Section 1/11 - Broken.mhtml", []byte("not an archive at all"))
	env.loadCourse()

	// A broken archive must fail loudly (500) instead of showing a blank page.
	broken := env.request(http.MethodGet, "/api/preview?path="+EscapeURLPath("Section 1/11 - Broken.mhtml"), nil)
	if broken.Code != http.StatusInternalServerError {
		t.Errorf("broken archive status = %d, want 500", broken.Code)
	}

	cases := map[string]int{
		EscapeURLPath("Section 1/02 - Notes.txt"): http.StatusBadRequest,
		"..%2F..%2Fsecret.txt":                    http.StatusForbidden,
		EscapeURLPath("Section 1/nope.mhtml"):     http.StatusNotFound,
	}
	for path, want := range cases {
		response := env.request(http.MethodGet, "/api/preview?path="+path, nil)
		if response.Code != want {
			t.Errorf("path %q: status = %d, want %d", path, response.Code, want)
		}
	}
}
