// Package object reads and writes the go6asm relocatable object format
// (".o6", magic "GO6O"), defined in DESIGN §8. The format is small,
// versioned, dependency-free, and inspectable with xxd.
//
// It is a leaf package (stdlib only) and wasm-clean (DESIGN §4): it works
// on byte slices / io, never the filesystem.
package object

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	magic   = "GO6O"
	version = uint16(1)

	// Relocatable / "none" sentinels (DESIGN §8.2).
	originReloc  = uint16(0xFFFF)
	loadAddrNone = uint16(0xFFFF)
	segAbsolute  = uint16(0xFFFF)
)

// Section kinds (DESIGN §8.1).
const (
	kindSegment = uint16(1)
	kindSymbols = uint16(2)
	kindRelocs  = uint16(3)
	kindStrings = uint16(4)
)

// RelType is a relocation kind (DESIGN §8.2).
type RelType uint8

const (
	RelByteLo RelType = 1 // low byte of a 16-bit value
	RelByteHi RelType = 2 // high byte
	RelWord   RelType = 3 // full 16-bit little-endian word
	RelBranch RelType = 4 // 8-bit relative branch displacement
)

// SymFlags is the symbol attribute bitset (DESIGN §8.2).
type SymFlags uint16

const (
	SymExported SymFlags = 1 << 0
	SymImported SymFlags = 1 << 1
	SymLabel    SymFlags = 1 << 2 // label vs constant
	SymLocal    SymFlags = 1 << 3
)

// Reloc patches Size bytes at Offset within its segment using the value
// of Symbols[SymIndex] plus Addend (DESIGN §8.2).
type Reloc struct {
	Offset   uint32
	Type     RelType
	Size     uint8
	SymIndex int
	Addend   int32
}

// Segment is one named, contiguous block. Relocatable segments have no
// fixed origin (the linker places them); absolute segments carry Origin.
type Segment struct {
	Name        string
	Origin      uint16 // valid only when !Relocatable
	LoadAddr    uint16 // hint; HasLoadAddr gates it
	Relocatable bool
	HasLoadAddr bool
	Bytes       []byte
	Relocs      []Reloc
}

// Symbol is a public or external symbol. SegIndex is the index into
// File.Segments, or -1 for an absolute value.
type Symbol struct {
	Name     string
	Value    int32
	SegIndex int
	Flags    SymFlags
	Pos      uint32 // source byte offset, for diagnostics
}

// File is a whole object file.
type File struct {
	Segments []Segment
	Symbols  []Symbol
}

// ---- string table ---------------------------------------------------------

type strtab struct {
	buf bytes.Buffer
	off map[string]uint32
}

func newStrtab() *strtab { return &strtab{off: map[string]uint32{}} }

func (s *strtab) intern(v string) uint32 {
	if o, ok := s.off[v]; ok {
		return o
	}
	o := uint32(s.buf.Len())
	s.buf.WriteString(v)
	s.buf.WriteByte(0)
	s.off[v] = o
	return o
}

func strAt(blob []byte, off uint32) (string, error) {
	if int(off) > len(blob) {
		return "", fmt.Errorf("object: string offset %d out of range", off)
	}
	end := bytes.IndexByte(blob[off:], 0)
	if end < 0 {
		return "", errors.New("object: unterminated string")
	}
	return string(blob[off : int(off)+end]), nil
}

// ---- write ----------------------------------------------------------------

type section struct {
	kind uint16
	data []byte
}

// Write serializes f to w (DESIGN §8.1).
func (f *File) Write(w io.Writer) error {
	st := newStrtab()
	var secs []section

	for i := range f.Segments {
		seg := &f.Segments[i]
		var b bytes.Buffer
		putU32(&b, st.intern(seg.Name))
		origin := seg.Origin
		if seg.Relocatable {
			origin = originReloc
		}
		putU16(&b, origin)
		load := loadAddrNone
		if seg.HasLoadAddr {
			load = seg.LoadAddr
		}
		putU16(&b, load)
		putU32(&b, uint32(len(seg.Bytes)))
		b.Write(seg.Bytes)
		secs = append(secs, section{kindSegment, b.Bytes()})
	}

	if len(f.Symbols) > 0 {
		var b bytes.Buffer
		putU32(&b, uint32(len(f.Symbols)))
		for _, s := range f.Symbols {
			putU32(&b, st.intern(s.Name))
			putU32(&b, uint32(s.Value))
			seg := segAbsolute
			if s.SegIndex >= 0 {
				seg = uint16(s.SegIndex)
			}
			putU16(&b, seg)
			putU16(&b, uint16(s.Flags))
			putU32(&b, s.Pos)
		}
		secs = append(secs, section{kindSymbols, b.Bytes()})
	}

	for i := range f.Segments {
		rs := f.Segments[i].Relocs
		if len(rs) == 0 {
			continue
		}
		var b bytes.Buffer
		putU16(&b, uint16(i))
		putU32(&b, uint32(len(rs)))
		for _, r := range rs {
			putU32(&b, r.Offset)
			b.WriteByte(byte(r.Type))
			b.WriteByte(r.Size)
			putU16(&b, 0) // flags, reserved
			putU32(&b, uint32(r.SymIndex))
			putU32(&b, uint32(r.Addend))
		}
		secs = append(secs, section{kindRelocs, b.Bytes()})
	}

	secs = append(secs, section{kindStrings, st.buf.Bytes()})

	// Header + section table, then payloads.
	var out bytes.Buffer
	out.WriteString(magic)
	putU16(&out, version)
	putU16(&out, 0) // flags
	putU16(&out, uint16(len(secs)))
	out.Write(make([]byte, 6)) // reserved

	offset := uint32(16 + len(secs)*16)
	for _, s := range secs {
		putU16(&out, s.kind)
		putU16(&out, 0) // flags
		putU32(&out, offset)
		putU32(&out, uint32(len(s.data)))
		out.Write(make([]byte, 4)) // reserved
		offset += uint32(len(s.data))
	}
	for _, s := range secs {
		out.Write(s.data)
	}

	_, err := w.Write(out.Bytes())
	return err
}

// ---- read -----------------------------------------------------------------

// Read parses an object file from raw bytes.
func Read(data []byte) (*File, error) {
	if len(data) < 16 || string(data[:4]) != magic {
		return nil, errors.New("object: bad magic (not a GO6O file)")
	}
	if v := binary.LittleEndian.Uint16(data[4:]); v != version {
		return nil, fmt.Errorf("object: unsupported version %d", v)
	}
	nsec := int(binary.LittleEndian.Uint16(data[8:]))

	type entry struct {
		kind        uint16
		off, length uint32
	}
	entries := make([]entry, nsec)
	p := 16
	for i := range entries {
		if p+16 > len(data) {
			return nil, errors.New("object: truncated section table")
		}
		entries[i] = entry{
			kind:   binary.LittleEndian.Uint16(data[p:]),
			off:    binary.LittleEndian.Uint32(data[p+4:]),
			length: binary.LittleEndian.Uint32(data[p+8:]),
		}
		p += 16
	}

	slice := func(e entry) ([]byte, error) {
		if int(e.off)+int(e.length) > len(data) || e.off < 16 {
			return nil, errors.New("object: section out of range")
		}
		return data[e.off : e.off+e.length], nil
	}

	// Strings first; everything else resolves names through it.
	var blob []byte
	for _, e := range entries {
		if e.kind == kindStrings {
			b, err := slice(e)
			if err != nil {
				return nil, err
			}
			blob = b
		}
	}

	f := &File{}
	relocsBySeg := map[int][]Reloc{}

	for _, e := range entries {
		b, err := slice(e)
		if err != nil {
			return nil, err
		}
		switch e.kind {
		case kindSegment:
			seg, err := readSegment(b, blob)
			if err != nil {
				return nil, err
			}
			f.Segments = append(f.Segments, seg)
		case kindSymbols:
			syms, err := readSymbols(b, blob)
			if err != nil {
				return nil, err
			}
			f.Symbols = syms
		case kindRelocs:
			idx, rs, err := readRelocs(b)
			if err != nil {
				return nil, err
			}
			relocsBySeg[idx] = rs
		case kindStrings:
			// already loaded
		default:
			// unknown (listing/comments/debug): ignored for forward-compat
		}
	}
	for i := range f.Segments {
		f.Segments[i].Relocs = relocsBySeg[i]
	}
	return f, nil
}

func readSegment(b, blob []byte) (Segment, error) {
	if len(b) < 12 {
		return Segment{}, errors.New("object: short segment section")
	}
	name, err := strAt(blob, binary.LittleEndian.Uint32(b))
	if err != nil {
		return Segment{}, err
	}
	origin := binary.LittleEndian.Uint16(b[4:])
	load := binary.LittleEndian.Uint16(b[6:])
	size := binary.LittleEndian.Uint32(b[8:])
	if int(size) > len(b)-12 {
		return Segment{}, errors.New("object: segment size overflows section")
	}
	seg := Segment{
		Name:        name,
		Relocatable: origin == originReloc,
		Bytes:       append([]byte(nil), b[12:12+size]...),
	}
	if !seg.Relocatable {
		seg.Origin = origin
	}
	if load != loadAddrNone {
		seg.HasLoadAddr = true
		seg.LoadAddr = load
	}
	return seg, nil
}

func readSymbols(b, blob []byte) ([]Symbol, error) {
	if len(b) < 4 {
		return nil, errors.New("object: short symbol section")
	}
	n := binary.LittleEndian.Uint32(b)
	p := 4
	syms := make([]Symbol, 0, n)
	for range n {
		if p+16 > len(b) {
			return nil, errors.New("object: truncated symbol")
		}
		name, err := strAt(blob, binary.LittleEndian.Uint32(b[p:]))
		if err != nil {
			return nil, err
		}
		seg := int(binary.LittleEndian.Uint16(b[p+8:]))
		if seg == int(segAbsolute) {
			seg = -1
		}
		syms = append(syms, Symbol{
			Name:     name,
			Value:    int32(binary.LittleEndian.Uint32(b[p+4:])),
			SegIndex: seg,
			Flags:    SymFlags(binary.LittleEndian.Uint16(b[p+10:])),
			Pos:      binary.LittleEndian.Uint32(b[p+12:]),
		})
		p += 16
	}
	return syms, nil
}

func readRelocs(b []byte) (int, []Reloc, error) {
	if len(b) < 6 {
		return 0, nil, errors.New("object: short reloc section")
	}
	idx := int(binary.LittleEndian.Uint16(b))
	n := binary.LittleEndian.Uint32(b[2:])
	p := 6
	rs := make([]Reloc, 0, n)
	for range n {
		if p+16 > len(b) {
			return 0, nil, errors.New("object: truncated reloc")
		}
		rs = append(rs, Reloc{
			Offset:   binary.LittleEndian.Uint32(b[p:]),
			Type:     RelType(b[p+4]),
			Size:     b[p+5],
			SymIndex: int(int32(binary.LittleEndian.Uint32(b[p+8:]))),
			Addend:   int32(binary.LittleEndian.Uint32(b[p+12:])),
		})
		p += 16
	}
	return idx, rs, nil
}

func putU16(b *bytes.Buffer, v uint16) {
	b.Write(binary.LittleEndian.AppendUint16(nil, v))
}
func putU32(b *bytes.Buffer, v uint32) {
	b.Write(binary.LittleEndian.AppendUint32(nil, v))
}
