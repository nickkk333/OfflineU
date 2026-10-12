package offlineu

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

const (
	testSectorSize = 512
	testMiniSize   = 64
	testMiniCutoff = 4096
	testFreeSector = 0xFFFFFFFF
	testEndOfChain = 0xFFFFFFFE
	testFatSector  = 0xFFFFFFFD
)

type oleTestStream struct {
	name string
	data []byte
}

// buildOleContainer writes a minimal but valid OLE2 container: streams of a
// whole mini cutoff or more are spread over full sectors, smaller ones go into
// the mini stream owned by the root entry - the layout Word uses as well.
func buildOleContainer(t *testing.T, streams []oleTestStream) []byte {
	t.Helper()

	mini := []byte{}
	miniIndex := map[string]int{}
	small := []oleTestStream{}
	large := []oleTestStream{}
	for _, stream := range streams {
		if len(stream.data) >= testMiniCutoff {
			large = append(large, stream)
			continue
		}
		miniIndex[stream.name] = len(mini) / testMiniSize
		small = append(small, stream)
		mini = append(mini, stream.data...)
		for len(mini)%testMiniSize != 0 {
			mini = append(mini, 0)
		}
	}

	dirSectors := (1 + len(streams)) * 128 / testSectorSize
	if (1+len(streams))*128%testSectorSize != 0 {
		dirSectors++
	}
	miniStreamSectors := (len(mini) + testSectorSize - 1) / testSectorSize
	miniFatSectors := 0
	if len(mini) > 0 {
		miniFatSectors = (len(mini)/testMiniSize*4 + testSectorSize - 1) / testSectorSize
	}
	largeSectors := 0
	for _, stream := range large {
		largeSectors += (len(stream.data) + testSectorSize - 1) / testSectorSize
	}

	// The size of the allocation table depends on the number of sectors, which
	// includes the table sectors themselves: iterate until the count settles.
	fatSectors := 0
	for {
		total := miniStreamSectors + largeSectors + dirSectors + miniFatSectors + fatSectors
		needed := (total*4 + testSectorSize - 1) / testSectorSize
		if needed == fatSectors {
			break
		}
		fatSectors = needed
	}
	totalSectors := miniStreamSectors + largeSectors + dirSectors + miniFatSectors + fatSectors

	fat := make([]uint32, totalSectors)
	for i := range fat {
		fat[i] = testFreeSector
	}
	next := 0
	allocate := func(count int) int {
		start := next
		if count == 0 {
			return testEndOfChain
		}
		for i := 0; i < count; i++ {
			fat[next+i] = uint32(next + i + 1)
		}
		next += count
		fat[next-1] = testEndOfChain
		return start
	}

	miniStreamStart := allocate(miniStreamSectors)
	largeStart := map[string]int{}
	for _, stream := range large {
		largeStart[stream.name] = allocate((len(stream.data) + testSectorSize - 1) / testSectorSize)
	}
	dirStart := allocate(dirSectors)
	miniFatStart := allocate(miniFatSectors)
	fatStart := next
	if fatSectors == 0 {
		t.Fatal("container needs at least one allocation sector")
	}
	for i := 0; i < fatSectors; i++ {
		fat[next+i] = testFatSector
	}

	sectors := make([][]byte, totalSectors)
	for i := range sectors {
		sectors[i] = make([]byte, testSectorSize)
	}
	writeAt := func(start int, data []byte) {
		for offset := 0; offset < len(data); offset += testSectorSize {
			copy(sectors[start+offset/testSectorSize], data[offset:])
		}
	}
	if len(mini) > 0 {
		writeAt(miniStreamStart, mini)
	}
	for _, stream := range large {
		writeAt(largeStart[stream.name], stream.data)
	}
	writeAt(dirStart, buildOleDirectory(streams, miniStreamStart, len(mini), miniIndex, largeStart))
	if miniFatSectors > 0 {
		miniFat := make([]uint32, len(mini)/testMiniSize)
		for i := range miniFat {
			miniFat[i] = testFreeSector
		}
		for _, stream := range small {
			start := miniIndex[stream.name]
			count := (len(stream.data) + testMiniSize - 1) / testMiniSize
			for i := 0; i < count; i++ {
				miniFat[start+i] = uint32(start + i + 1)
			}
			miniFat[start+count-1] = testEndOfChain
		}
		writeAt(miniFatStart, packUint32(miniFat))
	}
	for i := 0; i < fatSectors; i++ {
		sector := make([]uint32, testSectorSize/4)
		for j := range sector {
			sector[j] = testFreeSector
		}
		copy(sector, fat[i*(testSectorSize/4):])
		writeAt(fatStart+i, packUint32(sector))
	}

	file := make([]byte, 0, (totalSectors+1)*testSectorSize)
	header := make([]byte, testSectorSize)
	copy(header, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	binary.LittleEndian.PutUint16(header[0x18:], 0x3E)
	binary.LittleEndian.PutUint16(header[0x1A:], 0x03)
	binary.LittleEndian.PutUint16(header[0x1C:], 0xFFFE)
	binary.LittleEndian.PutUint16(header[0x1E:], 9) // 512 byte sectors
	binary.LittleEndian.PutUint16(header[0x20:], 6) // 64 byte mini sectors
	binary.LittleEndian.PutUint32(header[0x2C:], uint32(fatSectors))
	binary.LittleEndian.PutUint32(header[0x30:], uint32(dirStart))
	binary.LittleEndian.PutUint32(header[0x38:], testMiniCutoff)
	binary.LittleEndian.PutUint32(header[0x3C:], uint32(miniFatStart))
	binary.LittleEndian.PutUint32(header[0x40:], uint32(miniFatSectors))
	binary.LittleEndian.PutUint32(header[0x44:], testEndOfChain)
	binary.LittleEndian.PutUint32(header[0x48:], 0)
	for i := 0; i < 109; i++ {
		value := uint32(testFreeSector)
		if i < fatSectors {
			value = uint32(fatStart + i)
		}
		binary.LittleEndian.PutUint32(header[0x4C+4*i:], value)
	}
	file = append(file, header...)
	for _, sector := range sectors {
		file = append(file, sector...)
	}
	return file
}

func packUint32(values []uint32) []byte {
	out := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(out[4*i:], value)
	}
	return out
}

// buildOleDirectory writes the root entry (which owns the mini stream) and one
// entry per stream, padded with unused slots to whole sectors.
func buildOleDirectory(streams []oleTestStream, miniStart, miniLength int, miniIndex, largeStart map[string]int) []byte {
	entries := 1 + len(streams)
	size := entries * 128
	if size%testSectorSize != 0 {
		size += testSectorSize - size%testSectorSize
	}
	out := make([]byte, size)
	writeOleEntry(out, 0, "Root Entry", oleKindRoot, miniStart, miniLength)
	for i, stream := range streams {
		start := miniIndex[stream.name]
		if value, ok := largeStart[stream.name]; ok {
			start = value
		}
		writeOleEntry(out, i+1, stream.name, 2, start, len(stream.data))
	}
	return out
}

func writeOleEntry(directory []byte, index int, name string, kind byte, start, size int) {
	entry := directory[index*128:]
	for i, unit := range utf16.Encode([]rune(name)) {
		binary.LittleEndian.PutUint16(entry[2*i:], unit)
	}
	binary.LittleEndian.PutUint16(entry[0x40:], uint16((len([]rune(name))+1)*2))
	entry[0x42] = kind
	entry[0x43] = 1 // black: the reader scans linearly, the colour is cosmetic
	for i := 0x44; i < 0x74; i++ {
		entry[i] = 0xFF // no sibling and no child
	}
	binary.LittleEndian.PutUint32(entry[0x74:], uint32(start))
	binary.LittleEndian.PutUint32(entry[0x78:], uint32(size))
}

type docTestPiece struct {
	text       string
	compressed bool // stored as single byte Windows-1252 text
	outside    bool // stored with the headers/footnotes, past ccpText
}

// buildLegacyDoc writes a Word 97 document around the given pieces: a FIB, the
// text runs and the piece table that indexes them. The decoys are readable
// English strings planted in the file outside every piece - the byte scan
// would happily show them, the piece table must not.
func buildLegacyDoc(t *testing.T, pieces []docTestPiece, decoys ...string) []byte {
	t.Helper()
	const (
		textBase    = 0x400
		documentLen = 8192 // above the mini cutoff: a normal stream, like Word
	)
	word := make([]byte, documentLen)
	table := make([]byte, testMiniCutoff-1)

	if len(decoys) > 0 {
		copy(word[textBase-len(decoys[0])-8:], decoys[0])
	}
	if len(decoys) > 1 {
		copy(table[len(table)-len(decoys[1]):], decoys[1])
	}

	positions := make([]uint32, 0, len(pieces)+1)
	descriptors := make([]byte, 0, 8*len(pieces))
	cursor := uint32(textBase)
	characters := uint32(0)
	for _, piece := range pieces {
		positions = append(positions, characters)
		if !piece.outside {
			characters += uint32(len([]rune(piece.text)))
		}
		field := cursor
		switch {
		case piece.compressed:
			raw := []byte(piece.text)
			copy(word[cursor:], raw)
			field = cursor*2 | 0x40000000
			cursor += uint32(len(raw))
		default:
			raw := packUint16(utf16.Encode([]rune(piece.text)))
			copy(word[cursor:], raw)
			cursor += uint32(len(raw))
		}
		cursor += cursor % 2 // UTF-16 runs start on an even offset
		descriptor := make([]byte, 8)
		binary.LittleEndian.PutUint32(descriptor[2:], field)
		descriptors = append(descriptors, descriptor...)
	}
	positions = append(positions, characters)

	// CLX: the piece table element (marker 0x02) around a PlcPcd.
	plc := append(packUint32(positions), descriptors...)
	clx := make([]byte, 0, 5+len(plc))
	clx = append(clx, docPieceTableMarker)
	clx = append(clx, packUint32([]uint32{uint32(len(plc))})...)
	clx = append(clx, plc...)

	// FIB: signature, version, flags, character count and fcClx/lcbClx.
	binary.LittleEndian.PutUint16(word[0x00:], docSignature)
	binary.LittleEndian.PutUint16(word[docNfibOffset:], 0x00C1)
	binary.LittleEndian.PutUint16(word[0x20:], 0x000E) // csw
	binary.LittleEndian.PutUint16(word[0x3E:], 0x0016) // cslw
	binary.LittleEndian.PutUint16(word[docCbRgFcLcbOffset:], 0x005D)
	binary.LittleEndian.PutUint32(word[docCcpTextOffset:], characters)
	copy(table[0x40:], clx)
	binary.LittleEndian.PutUint32(word[docFcClxOffset:], 0x40)
	binary.LittleEndian.PutUint32(word[docFcClxOffset+4:], uint32(len(clx)))

	return buildOleContainer(t, []oleTestStream{
		{name: "WordDocument", data: word},
		{name: "0Table", data: table},
	})
}

func packUint16(values []uint16) []byte {
	out := make([]byte, 2*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint16(out[2*i:], value)
	}
	return out
}

func TestOleContainerServesLargeAndMiniStreams(t *testing.T) {
	pages := bytes.Repeat([]byte("word-document-payload "), 400) // above the mini cutoff
	table := []byte("the piece table of a short document is small")
	container := buildOleContainer(t, []oleTestStream{
		{name: "WordDocument", data: pages},
		{name: "0Table", data: table},
	})

	file, err := parseOLE(container)
	if err != nil {
		t.Fatalf("parseOLE: %v", err)
	}
	got, err := file.stream("wordDOCUMENT")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !bytes.Equal(got, pages) {
		t.Errorf("WordDocument = %d bytes, want %d", len(got), len(pages))
	}
	got, err = file.stream("0Table")
	if err != nil {
		t.Fatalf("mini stream: %v", err)
	}
	if !bytes.Equal(got, table) {
		t.Errorf("0Table = %q, want %q", got, table)
	}
	if _, err := file.stream("1Table"); err == nil {
		t.Error("a missing stream must report an error")
	}
}

func TestOleContainerRejectsForeignBytes(t *testing.T) {
	if _, err := parseOLE([]byte("just text, not a container at all")); err == nil {
		t.Error("plain text must not parse as an OLE2 container")
	}
	truncated := buildOleContainer(t, []oleTestStream{{name: "WordDocument", data: make([]byte, 8192)}})
	if _, err := parseOLE(truncated[:300]); err == nil {
		t.Error("a truncated header must not parse")
	}
}

// TestDocPreviewReadsThePieceTable pins the fix for the garbled .doc preview:
// every piece is decoded with the encoding the piece table records for it, so
// the single byte English run stays English while the CJK runs keep their
// characters, and nothing outside the pieces leaks into the page.
func TestDocPreviewReadsThePieceTable(t *testing.T) {
	env := newTestEnv(t)
	english := "Chapter one introduces the offline viewer"
	first := "第一章：离线课程与进度追踪"
	second := "第二天：维多利亚港与太平山顶"
	const (
		decoyBody  = "DECOY SENTENCE INSIDE THE STREAM"
		decoyTable = "DECOY SENTENCE INSIDE THE TABLE STREAM"
	)
	document := buildLegacyDoc(t, []docTestPiece{
		{text: english, compressed: true},
		{text: first},
		{text: second},
		{text: "header text that is not part of the body", outside: true},
	}, decoyBody, decoyTable)

	_, body, _, err := PreviewDocument(env.write("Section 1/09 - Legacy.doc", document))
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	for _, want := range []string{english, first, second} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q: %s", want, body)
		}
	}
	// The old reader paired the first two ASCII bytes into one CJK code point.
	mispaired := string(rune(english[0]) | rune(english[1])<<8)
	for _, unwanted := range []string{mispaired, decoyBody, decoyTable, "header text that is not part"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body must not contain %q: %s", unwanted, body)
		}
	}
}

func TestDocPreviewDropsFieldInstructions(t *testing.T) {
	env := newTestEnv(t)
	field := string(rune(0x13)) + `HYPERLINK "https://example.org"` + string(rune(0x14)) + "example link" + string(rune(0x15))
	document := buildLegacyDoc(t, []docTestPiece{{text: "See " + field + " for details.", compressed: true}})

	_, body, _, err := PreviewDocument(env.write("Section 1/10 - Fields.doc", document))
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if !strings.Contains(body, "example link") {
		t.Errorf("the visible half of the field is missing: %s", body)
	}
	if strings.Contains(body, "HYPERLINK") || strings.Contains(body, "example.org") {
		t.Errorf("the field instruction leaked into the preview: %s", body)
	}
}

// TestDocPreviewFallsBackForUnstructuredFiles keeps the byte scan alive for
// the files Word's own index cannot describe.
func TestDocPreviewFallsBackForUnstructuredFiles(t *testing.T) {
	env := newTestEnv(t)
	junk := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, bytes.Repeat([]byte{0x07}, 64)...)
	junk = append(junk, []byte("loose english sentence in a container nobody can index")...)
	_, body, _, err := PreviewDocument(env.write("Section 1/11 - Broken.doc", junk))
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if !strings.Contains(body, "loose english sentence") {
		t.Errorf("the fallback lost the readable text: %s", body)
	}
}
