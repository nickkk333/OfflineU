// The text of a Word 97-2003 document is spread over "pieces" listed in the
// piece table: every piece says which part of the document it covers, at which
// byte offset it sits inside the WordDocument stream and whether it is stored
// as single byte Windows-1252 text (plain ASCII letters, mostly) or as
// UTF-16LE code units.
//
// Reading that index is what makes the preview correct instead of guessed: the
// bytes are decoded with the encoding Word wrote down for each piece, so an
// English piece can no longer be mis-paired into CJK hanzi and a Chinese piece
// no longer loses its characters. When a file has no usable piece table (Word
// 6/95, a truncated download, a writer that skips it) the caller falls back to
// the byte scan in preview.go.
package offlineu

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

const (
	docSignature = 0xA5EC // "Word document" marker (wIdent)

	// Offsets inside the FIB (file information block).
	docFlagsOffset     = 0x0A
	docNfibOffset      = 0x02
	docFcClxOffset     = 0x1A2  // fcClx, the piece table in the table stream
	docCcpTextOffset   = 0x4C   // ccpText, the length of the document body
	docCbRgFcLcbOffset = 0x98   // number of fc/lcb pairs that follow
	docFcMinOffset     = 0x18   // Word 6/95: start of the contiguous text
	docFcMacOffset     = 0x1C   // Word 6/95: end of the contiguous text
	docFibMinLen       = 0x1AA  // FIB up to and including lcbClx
	docWord97Nfib      = 0x00C1 // first nFib that always uses a piece table

	docFlagEncrypted    = 0x0100 // fEncrypted
	docFlagWhichTable   = 0x0200 // fWhichTblStm: 1 means "1Table"
	docPieceTableMarker = 0x02   // clxt of the Pcdt element in the CLX
	docPropertyMarker   = 0x01   // clxt of a Prc element in the CLX

	docMaxPieces = 1 << 16 // guard against a corrupt piece count
)

// docPiece is one entry of the piece table: the character range it covers and
// where its bytes are stored.
type docPiece struct {
	cpStart    uint32
	cpEnd      uint32
	offset     int
	compressed bool
}

// docBodyText returns the body text of a Word 97-2003 .doc file. The second
// result is false when the bytes are not a structured Word document, which is
// the caller's cue to fall back to the heuristic scan.
func docBodyText(data []byte) (string, bool) {
	container, err := parseOLE(data)
	if err != nil {
		return "", false
	}
	word, err := container.stream("WordDocument")
	if err != nil || len(word) < docFibMinLen {
		return "", false
	}
	if binary.LittleEndian.Uint16(word) != docSignature {
		return "", false
	}
	flags := binary.LittleEndian.Uint16(word[docFlagsOffset:])
	if flags&docFlagEncrypted != 0 {
		return "", false // without the password there is nothing readable
	}
	ccpText := binary.LittleEndian.Uint32(word[docCcpTextOffset:])
	if ccpText == 0 {
		return "", false
	}
	if text := docPieceTableText(container, word, flags, ccpText); text != "" {
		return text, true
	}
	return docFlatText(word, ccpText)
}

// docPieceTableText decodes the document through fcClx/lcbClx: the piece table
// lives at fcClx inside the table stream (which one is flagged in the FIB).
func docPieceTableText(container *oleFile, word []byte, flags uint16, ccpText uint32) string {
	fcClx, lcbClx, ok := docPieceTableLocation(word)
	if !ok || lcbClx == 0 {
		return ""
	}
	name := "0Table"
	if flags&docFlagWhichTable != 0 {
		name = "1Table"
	}
	table, err := container.stream(name)
	if err != nil {
		return ""
	}
	if uint64(fcClx)+uint64(lcbClx) > uint64(len(table)) {
		return ""
	}
	return docPiecesText(table[fcClx:fcClx+lcbClx], word, ccpText)
}

// docPieceTableLocation reads the fcClx/lcbClx pair out of the FIB. Both only
// exist when the FIB carries the full FibRgFcLcb97 array (Word 97 and later).
func docPieceTableLocation(word []byte) (offset, length uint32, ok bool) {
	if len(word) < docFibMinLen {
		return 0, 0, false
	}
	if pairs := binary.LittleEndian.Uint16(word[docCbRgFcLcbOffset:]); pairs <= 33 {
		return 0, 0, false // fcClx is the 34th pair
	}
	return binary.LittleEndian.Uint32(word[docFcClxOffset:]), binary.LittleEndian.Uint32(word[docFcClxOffset+4:]), true
}

// docPiecesText decodes every piece of the CLX. Only the first ccpText
// characters belong to the body: the table continues with footnotes, headers,
// text boxes and the like, which this preview does not show.
func docPiecesText(clx, word []byte, ccpText uint32) string {
	pieces, ok := parseDocPieceTable(clx)
	if !ok {
		return ""
	}
	var out strings.Builder
	remaining := ccpText
	for _, piece := range pieces {
		if remaining == 0 {
			break
		}
		count := piece.cpEnd - piece.cpStart
		if count > remaining {
			count = remaining
		}
		remaining -= count
		out.WriteString(docPieceText(word, piece, count))
	}
	return cleanDocText(out.String())
}

// parseDocPieceTable walks the CLX: property exceptions (Prc, marker 0x01)
// come first and are skipped, then comes the piece table (Pcdt, marker 0x02)
// whose payload is a PlcPcd - n+1 character positions followed by n piece
// descriptors of 8 bytes each.
func parseDocPieceTable(clx []byte) ([]docPiece, bool) {
	for offset := 0; offset < len(clx); {
		switch clx[offset] {
		case docPropertyMarker:
			if offset+3 > len(clx) {
				return nil, false
			}
			offset += 3 + int(binary.LittleEndian.Uint16(clx[offset+1:]))
		case docPieceTableMarker:
			if offset+5 > len(clx) {
				return nil, false
			}
			length := int(binary.LittleEndian.Uint32(clx[offset+1:]))
			if length < 4 || offset+5+length > len(clx) {
				return nil, false
			}
			return parseDocPcdTable(clx[offset+5 : offset+5+length])
		default:
			return nil, false
		}
	}
	return nil, false
}

func parseDocPcdTable(plc []byte) ([]docPiece, bool) {
	if len(plc) < 12 || (len(plc)-4)%12 != 0 {
		return nil, false
	}
	count := (len(plc) - 4) / 12
	if count > docMaxPieces {
		return nil, false
	}
	pieces := make([]docPiece, 0, count)
	for i := 0; i < count; i++ {
		cpStart := binary.LittleEndian.Uint32(plc[4*i:])
		cpEnd := binary.LittleEndian.Uint32(plc[4*(i+1):])
		if cpEnd < cpStart {
			return nil, false
		}
		// Descriptor: 2 bytes of flags, then the 32 bit fc field whose top
		// bits say whether the piece is compressed (single byte, offset
		// halved) and a 2 byte property modifier.
		field := binary.LittleEndian.Uint32(plc[4*(count+1)+8*i+2:])
		offset := int(field & 0x3FFFFFFF)
		compressed := field&0x40000000 != 0
		if compressed {
			offset /= 2
		}
		pieces = append(pieces, docPiece{cpStart: cpStart, cpEnd: cpEnd, offset: offset, compressed: compressed})
	}
	return pieces, len(pieces) > 0
}

// docPieceText decodes count characters of one piece: single byte pieces go
// through Windows-1252, the others are UTF-16LE code units.
func docPieceText(word []byte, piece docPiece, count uint32) string {
	if count == 0 || piece.offset < 0 {
		return ""
	}
	if piece.compressed {
		end := piece.offset + int(count)
		if end > len(word) {
			return ""
		}
		return decodeCP1252(word[piece.offset:end])
	}
	end := piece.offset + int(count)*2
	if end > len(word) {
		return ""
	}
	units := make([]uint16, 0, count)
	for i := piece.offset; i+1 < end; i += 2 {
		units = append(units, binary.LittleEndian.Uint16(word[i:]))
	}
	return string(utf16.Decode(units))
}

// docFlatText reads documents without a usable piece table: Word 6/95 files
// keep the whole body contiguous between fcMin and fcMac, and comparing that
// length with ccpText says whether it is single byte or UTF-16 text. Word 97
// and later always write a piece table, so the fields are not consulted there
// (they hold reserved values in those files).
func docFlatText(word []byte, ccpText uint32) (string, bool) {
	if binary.LittleEndian.Uint16(word[docNfibOffset:]) >= docWord97Nfib {
		return "", false
	}
	if len(word) < docFcMacOffset+4 {
		return "", false
	}
	fcMin := binary.LittleEndian.Uint32(word[docFcMinOffset:])
	fcMac := binary.LittleEndian.Uint32(word[docFcMacOffset:])
	if fcMac <= fcMin || uint64(fcMac) > uint64(len(word)) {
		return "", false
	}
	run := word[fcMin:fcMac]
	switch {
	case uint64(ccpText)*2 == uint64(len(run)):
		units := make([]uint16, 0, ccpText)
		for i := 0; i+1 < len(run); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(run[i:]))
		}
		return cleanDocText(string(utf16.Decode(units))), true
	case uint64(ccpText) <= uint64(len(run)):
		return cleanDocText(decodeCP1252(run[:ccpText])), true
	}
	return "", false
}

// cleanDocText turns Word's control characters into the text a reader expects:
// paragraph, line and page breaks become newlines, a table cell boundary
// becomes a tab, and the instruction half of a field (everything between 0x13
// and 0x14, e.g. the target of a HYPERLINK field) is dropped while the visible
// result of the field is kept.
func cleanDocText(text string) string {
	var out strings.Builder
	inField := false
	for _, r := range text {
		if inField {
			if r == 0x14 || r == 0x15 {
				inField = false
			}
			continue
		}
		switch r {
		case 0x0D, 0x0A, 0x0B, 0x0C: // paragraph mark, line break, page break
			out.WriteRune('\n')
		case 0x07: // end of a table cell or row
			out.WriteRune('\t')
		case 0x09:
			out.WriteRune('\t')
		case 0x13: // field begin: skip the instruction that follows
			inField = true
		case 0x1E: // non-breaking hyphen
			out.WriteRune('-')
		case 0x14, 0x15: // stray field separator / end
		default:
			if r >= 0x20 {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}
