// MHTML preview unpacks .mhtml / .mht archives: one HTML page plus every image
// and stylesheet it references, packed as a multipart/related message (the
// format browsers write for "save page as" / single-file web archives).
//
// Browsers open such files inconsistently - Chrome renders them, Firefox and
// Safari download them - so the server extracts the HTML body itself and
// inlines the embedded parts as data: URLs. The result is a plain HTML
// fragment shown through the same script-free preview page as office
// documents (see previewPage), so it works offline in every browser.
//
// Parsing uses the standard library only (net/mime + mime/multipart), keeping
// the preview a single static binary. The HTML is not sanitized beyond
// stripping <script>: the preview page is served with a CSP of
// default-src 'none', so nothing it references can load and no script runs.
package offlineu

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"regexp"
	"sort"
	"strings"
)

// mhtmlPart is one decoded part of an archive: its media type, how the rest of
// the document referred to it (Content-Location / Content-ID) and its bytes.
type mhtmlPart struct {
	mediaType string
	location  string
	contentID string
	body      []byte
}

// mhtmlToHTML unpacks a whole MHTML file into an HTML fragment. The first
// text/html part is the document; every other part becomes a data: URL that
// the document's cid: / Content-Location / file name references are rewritten
// to. A file that cannot be parsed yields an error (the preview endpoint
// answers 500).
func mhtmlToHTML(data []byte) (string, error) {
	parts, err := readMHTMLParts(data)
	if err != nil {
		return "", err
	}
	root := ""
	assets := map[string]string{} // reference -> data URL
	inlined := []*mhtmlPart{}     // non-html parts, kept so stylesheets can be rewritten
	// register adds every way the rest of the document may refer to a part.
	register := func(part *mhtmlPart, dataURL string) {
		if id := strings.Trim(strings.TrimSpace(part.contentID), "<>"); id != "" {
			assets["cid:"+id] = dataURL
		}
		if part.location != "" {
			assets[part.location] = dataURL
			// Some writers reference a part by file name only, with or without
			// a cid: prefix even though only the location was declared.
			if name := fileNameOf(part.location); name != "" {
				assets[name] = dataURL
				assets["cid:"+name] = dataURL
			}
		}
	}
	for _, part := range parts {
		if root == "" && strings.HasPrefix(strings.ToLower(part.mediaType), "text/html") {
			root = string(part.body)
			continue
		}
		current := part
		inlined = append(inlined, &current)
		dataURL := "data:" + part.mediaType + ";base64," + base64.StdEncoding.EncodeToString(part.body)
		register(&current, dataURL)
	}
	if root == "" {
		return "", fmt.Errorf("mhtml archive has no html part")
	}
	// Stylesheets reference images and fonts through the same cid: / URL scheme,
	// so they are rewritten with the collected assets before being encoded -
	// otherwise every background image inside the page would break.
	for _, part := range inlined {
		if !strings.HasPrefix(strings.ToLower(part.mediaType), "text/css") {
			continue
		}
		part.body = []byte(replaceReferences(string(part.body), assets))
		register(part, "data:"+part.mediaType+";base64,"+base64.StdEncoding.EncodeToString(part.body))
	}
	root = replaceReferences(root, assets)
	// The preview page promises to carry no script; archives saved from the
	// web routinely contain some. The CSP blocks execution as well - this is
	// the second layer and keeps the promise visible in the markup.
	root = mhtmlScriptPattern.ReplaceAllString(root, "")
	return root, nil
}

// replaceReferences swaps every known reference for its data: URL. Longest
// keys go first, so a full Content-Location wins over a bare file name that is
// only a suffix of it ("cid:style.css" must not be half-eaten by "style.css").
func replaceReferences(document string, assets map[string]string) string {
	keys := make([]string, 0, len(assets))
	for key := range assets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		document = strings.ReplaceAll(document, key, assets[key])
	}
	return document
}

// readMHTMLParts splits the archive into its decoded parts. The top-level
// headers are scanned by hand because MHTML writers disagree on preamble lines
// (some prepend a "From: <Saved by ...>" banner); only the boundary matters,
// after which mime/multipart does the rest.
func readMHTMLParts(data []byte) ([]mhtmlPart, error) {
	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	separator := 4
	if headerEnd < 0 {
		headerEnd = bytes.Index(data, []byte("\n\n"))
		separator = 2
	}
	if headerEnd < 0 {
		return nil, fmt.Errorf("mhtml archive has no header block")
	}
	boundary := mhtmlBoundary(string(data[:headerEnd]))
	if boundary == "" {
		return nil, fmt.Errorf("mhtml archive declares no boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(data[headerEnd+separator:]), boundary)
	parts := []mhtmlPart{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Truncated (maxPreviewBytes) or malformed archives keep the parts
			// read so far - a partial page beats an error page.
			if len(parts) > 0 {
				break
			}
			return nil, err
		}
		decoded, decodeErr := decodeMHTMLPart(part)
		if decodeErr != nil {
			continue // one broken part must not hide the whole document
		}
		parts = append(parts, decoded)
	}
	return parts, nil
}

// mhtmlBoundary extracts the boundary parameter of the top-level Content-Type
// header, tolerating folded lines and quoted values.
var (
	mhtmlBoundaryPattern = regexp.MustCompile(`(?is)boundary\s*=\s*"?([^";\s]+)"?`)
	mhtmlFoldPattern     = regexp.MustCompile(`\r?\n[ \t]+`)
)

func mhtmlBoundary(headers string) string {
	// Folded continuation lines ("...\r\n\tboundary=...") become one line so
	// the parameter is found no matter how the writer wrapped it.
	folded := mhtmlFoldPattern.ReplaceAllString(headers, " ")
	for _, line := range strings.Split(folded, "\n") {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "content-type:") {
			continue
		}
		if match := mhtmlBoundaryPattern.FindStringSubmatch(line); match != nil {
			return match[1]
		}
	}
	return ""
}

// decodeMHTMLPart turns one multipart part into bytes plus its headers. The
// body encoding follows Content-Transfer-Encoding: base64 (the usual choice
// for images), quoted-printable (common for HTML) or 7bit as it is.
func decodeMHTMLPart(part *multipart.Part) (mhtmlPart, error) {
	mediaType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		mediaType = "application/octet-stream"
	}
	raw, err := io.ReadAll(part)
	if err != nil {
		return mhtmlPart{}, err
	}
	switch strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		// Writers wrap base64 at 76 columns; whitespace is not part of it.
		compact := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, raw)
		if body, err := base64.StdEncoding.DecodeString(string(compact)); err == nil {
			raw = body
		}
	case "quoted-printable":
		if body, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw))); err == nil {
			raw = body
		}
	}
	return mhtmlPart{
		mediaType: mediaType,
		location:  strings.TrimSpace(part.Header.Get("Content-Location")),
		contentID: strings.TrimSpace(part.Header.Get("Content-ID")),
		body:      raw,
	}, nil
}

var mhtmlScriptPattern = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)

// fileNameOf returns the last path segment of a Content-Location value, which
// is how older archives (Internet Explorer style) reference their parts.
func fileNameOf(location string) string {
	cut := strings.LastIndexAny(location, "/\\")
	if cut < 0 {
		return ""
	}
	return location[cut+1:]
}
