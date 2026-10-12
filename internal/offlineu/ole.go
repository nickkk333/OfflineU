// Legacy .doc preview reads the OLE2 (Compound File Binary) container the
// document lives in: Word keeps its text in the "WordDocument" stream and the
// index that says where every text piece sits in the "0Table"/"1Table" stream.
//
// Only the reading side is implemented, with the standard library alone:
// header, allocation table (plus DIFAT for large files), directory and the
// mini stream that small streams live in. Anything unexpected makes the parser
// fail, so the caller can fall back to the heuristic byte scan in preview.go.
package offlineu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	// "D0 CF 11 E0 A1 B1 1A E1" read as a little endian uint32.
	oleSignature = 0xE011CFD0
	oleHeaderLen = 512
	oleDirLen    = 128

	oleEndOfChain = 0xFFFFFFFE
	oleFreeSector = 0xFFFFFFFF

	oleKindRoot = 5 // directory entry type of the root storage

	// The root storage holds the mini stream; entries below the cutoff live in
	// it instead of occupying whole sectors, which is how Word stores the
	// table stream of a short document.
	oleDefaultMiniCutoff = 4096
	oleMiniShift         = 6 // 64 byte mini sectors
)

// oleEntry is one directory entry: a stream (most of them), a storage, or the
// root that carries the mini stream.
type oleEntry struct {
	name  string
	kind  byte
	start uint32
	size  uint32
}

// oleFile is a parsed OLE2 container over the bytes it was built from.
type oleFile struct {
	data       []byte
	sectorSize int
	miniSize   int
	miniCutoff uint32
	fat        []uint32
	miniFat    []uint32
	entries    []oleEntry
}

// parseOLE validates the 512 byte header and reads the allocation table plus
// the directory. It returns an error for anything it does not understand - the
// caller then keeps to the heuristic text scan instead of guessing further.
func parseOLE(data []byte) (*oleFile, error) {
	if len(data) < oleHeaderLen || binary.LittleEndian.Uint32(data) != oleSignature {
		return nil, errors.New("not an OLE2 container")
	}
	if binary.LittleEndian.Uint16(data[0x1C:]) != 0xFFFE {
		return nil, errors.New("unsupported OLE2 byte order")
	}
	shift := binary.LittleEndian.Uint16(data[0x1E:])
	if shift < 7 || shift > 20 {
		return nil, fmt.Errorf("unsupported OLE2 sector shift %d", shift)
	}
	file := &oleFile{
		data:       data,
		sectorSize: 1 << shift,
		miniSize:   1 << oleMiniShift,
		miniCutoff: oleDefaultMiniCutoff,
	}
	if miniShift := binary.LittleEndian.Uint16(data[0x20:]); miniShift >= 4 && miniShift <= shift {
		file.miniSize = 1 << miniShift
	}
	if cutoff := binary.LittleEndian.Uint32(data[0x38:]); cutoff > 0 {
		file.miniCutoff = cutoff
	}
	if err := file.loadFAT(); err != nil {
		return nil, err
	}
	if err := file.loadDirectory(); err != nil {
		return nil, err
	}
	return file, nil
}

// loadFAT reads the sector allocation table: the header holds the first 109
// entries, and files with more sectors continue in DIFAT sectors, each of
// which carries a full sector of entries plus the next DIFAT sector number.
func (f *oleFile) loadFAT() error {
	wanted := binary.LittleEndian.Uint32(f.data[0x2C:])
	sectors := []uint32{}
	for i := 0; i < 109 && uint32(len(sectors)) < wanted; i++ {
		sector := binary.LittleEndian.Uint32(f.data[0x4C+4*i:])
		if sector == oleFreeSector || sector == oleEndOfChain {
			break
		}
		sectors = append(sectors, sector)
	}
	next := binary.LittleEndian.Uint32(f.data[0x44:])
	for guard := 0; guard < 1<<16 && uint32(len(sectors)) < wanted; guard++ {
		if next == oleEndOfChain || next == oleFreeSector {
			break
		}
		chunk := f.sector(next)
		if chunk == nil {
			break
		}
		entries := len(chunk)/4 - 1 // the last entry links the next DIFAT sector
		for i := 0; i < entries && uint32(len(sectors)) < wanted; i++ {
			value := binary.LittleEndian.Uint32(chunk[4*i:])
			if value == oleFreeSector || value == oleEndOfChain {
				continue
			}
			sectors = append(sectors, value)
		}
		next = binary.LittleEndian.Uint32(chunk[4*entries:])
	}
	if len(sectors) == 0 {
		return errors.New("OLE2 container has no allocation table")
	}
	for _, sector := range sectors {
		chunk := f.sector(sector)
		if chunk == nil {
			return errors.New("OLE2 allocation table is out of range")
		}
		for i := 0; i+4 <= len(chunk); i += 4 {
			f.fat = append(f.fat, binary.LittleEndian.Uint32(chunk[i:]))
		}
	}
	return nil
}

// sector returns the bytes of one sector, or nil when it lies outside the
// file: truncated download included, the preview must never panic.
func (f *oleFile) sector(index uint32) []byte {
	offset := (uint64(index) + 1) * uint64(f.sectorSize)
	if offset >= uint64(len(f.data)) {
		return nil
	}
	end := offset + uint64(f.sectorSize)
	if end > uint64(len(f.data)) {
		end = uint64(len(f.data))
	}
	return f.data[offset:end]
}

// chain follows the allocation table from start and concatenates the sectors
// it finds, stopping after limit bytes when limit is positive.
func (f *oleFile) chain(start uint32, limit int) []byte {
	out := []byte{}
	for sector, seen := start, 0; sector != oleEndOfChain && sector != oleFreeSector; seen++ {
		if seen > len(f.fat) || uint32(len(f.fat)) <= sector {
			break
		}
		chunk := f.sector(sector)
		if chunk == nil {
			break
		}
		out = append(out, chunk...)
		if limit > 0 && len(out) >= limit {
			break
		}
		sector = f.fat[sector]
	}
	return out
}

// chainBytes is chain with the length a directory entry recorded, which may be
// smaller than the sectors that hold it.
func (f *oleFile) chainBytes(start, size uint32) []byte {
	data := f.chain(start, int(size))
	if uint32(len(data)) > size {
		data = data[:size]
	}
	return data
}

// loadDirectory walks the whole directory chain. The entries form a red-black
// tree that would need a splay, but every consumer here simply looks a stream
// up by name, so a linear scan over all entries is enough.
func (f *oleFile) loadDirectory() error {
	raw := f.chain(binary.LittleEndian.Uint32(f.data[0x30:]), 0)
	if len(raw) < oleDirLen {
		return errors.New("OLE2 container has no directory")
	}
	for offset := 0; offset+oleDirLen <= len(raw); offset += oleDirLen {
		entry := parseOleDirEntry(raw[offset : offset+oleDirLen])
		if entry.kind == 0 { // unused slot
			continue
		}
		f.entries = append(f.entries, entry)
	}
	if len(f.entries) == 0 {
		return errors.New("OLE2 directory is empty")
	}
	return nil
}

func parseOleDirEntry(raw []byte) oleEntry {
	nameLen := int(binary.LittleEndian.Uint16(raw[0x40:]))
	if nameLen > 64 { // the name field is 64 bytes, terminator included
		nameLen = 64
	}
	units := []uint16{}
	for i := 0; i+1 < nameLen; i += 2 {
		unit := binary.LittleEndian.Uint16(raw[i:])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return oleEntry{
		name:  string(utf16.Decode(units)),
		kind:  raw[0x42],
		start: binary.LittleEndian.Uint32(raw[0x74:]),
		size:  binary.LittleEndian.Uint32(raw[0x78:]),
	}
}

// stream returns the bytes of a named stream. Names are compared
// case-insensitively: Word writes "WordDocument", "0Table" and "1Table", and
// some producers vary the casing.
func (f *oleFile) stream(name string) ([]byte, error) {
	entry := f.find(name)
	if entry == nil {
		return nil, fmt.Errorf("stream %s not found", name)
	}
	if entry.size == 0 && entry.kind != oleKindRoot {
		return nil, nil
	}
	if entry.kind != oleKindRoot && entry.size < f.miniCutoff {
		return f.miniStream(entry)
	}
	return f.chainBytes(entry.start, entry.size), nil
}

// miniStream reads an entry stored inside the mini stream: the root entry
// carries the stream itself, the mini allocation table says where its small
// sectors point. The table stream of a short document is a few hundred bytes
// and lives here.
func (f *oleFile) miniStream(entry *oleEntry) ([]byte, error) {
	root := f.find("Root Entry")
	if root == nil {
		return nil, errors.New("OLE2 root entry not found")
	}
	source := f.chainBytes(root.start, root.size)
	links := f.miniAllocation()
	if links == nil {
		return nil, errors.New("OLE2 mini allocation table missing")
	}
	out := []byte{}
	for sector, seen := entry.start, 0; seen <= len(links); seen++ {
		if sector == oleEndOfChain || sector == oleFreeSector || uint32(len(links)) <= sector {
			break
		}
		offset := int(sector) * f.miniSize
		if offset >= len(source) {
			break
		}
		end := offset + f.miniSize
		if end > len(source) {
			end = len(source)
		}
		out = append(out, source[offset:end]...)
		if uint32(len(out)) >= entry.size {
			break
		}
		sector = links[sector]
	}
	if uint32(len(out)) > entry.size {
		out = out[:entry.size]
	}
	return out, nil
}

// miniAllocation reads the mini allocation table once and caches it.
func (f *oleFile) miniAllocation() []uint32 {
	if f.miniFat != nil {
		return f.miniFat
	}
	start := binary.LittleEndian.Uint32(f.data[0x3C:])
	if start == oleEndOfChain || start == oleFreeSector {
		return nil
	}
	raw := f.chainBytes(start, binary.LittleEndian.Uint32(f.data[0x40:])*uint32(f.sectorSize))
	if len(raw) == 0 {
		return nil
	}
	for i := 0; i+4 <= len(raw); i += 4 {
		f.miniFat = append(f.miniFat, binary.LittleEndian.Uint32(raw[i:]))
	}
	return f.miniFat
}

func (f *oleFile) find(name string) *oleEntry {
	for i := range f.entries {
		if strings.EqualFold(f.entries[i].name, name) {
			return &f.entries[i]
		}
	}
	return nil
}
