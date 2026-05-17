// Package link is the go6asm linker (DESIGN §9.2): it takes relocatable
// object files plus a memory-map config, places segments into memory
// areas, resolves the unified symbol table, applies relocations, and
// emits a single linear image.
//
// Leaf-ish: depends only on object, memmap, diag, token (all wasm-clean).
// It never touches the filesystem — callers hand it parsed inputs — so it
// builds for GOOS=js GOARCH=wasm (DESIGN §4).
package link

import (
	"fmt"
	"sort"

	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/memmap"
	"github.com/carledwards/go6asm/object"
)

// Linker diagnostic codes (E0030+ — the E0001–E0022 range is the
// front-end's; gap left for sema/codegen growth).
const (
	codeDupSymbol  = "E0030"
	codeUndefined  = "E0031"
	codeOverflow   = "E0032"
	codeUnknownMem = "E0033"
	codeRelocRange = "E0034"
	codeUnplaced   = "E0035"
)

// SymKind classifies a resolved symbol in the output map.
type SymKind int

const (
	SymKindLabel SymKind = iota // an address (segment-relative, now resolved)
	SymKindConst                // an absolute constant
)

// Symbol is one entry of the linked, fully resolved symbol table.
type Symbol struct {
	Name  string
	Value int32
	Kind  SymKind
}

// Placement records where one input segment instance landed.
type Placement struct {
	Segment string
	File    int    // index into the input []*object.File
	SegIdx  int    // index into that file's Segments
	Addr    uint16 // resolved base address
	Size    int
}

// Result is a linked image plus its resolved symbol table and the
// segment placement map (the data behind a .map file, DESIGN §9.3).
type Result struct {
	Image      []byte
	Origin     uint16
	Symbols    []Symbol
	Placements []Placement
	Diags      []diag.Diagnostic
}

// HasErrors reports whether any diagnostic is an error.
func (r Result) HasErrors() bool {
	for _, d := range r.Diags {
		if d.Severity == diag.SeverityError {
			return true
		}
	}
	return false
}

type linker struct {
	files []*object.File
	cfg   *memmap.Config
	diags []diag.Diagnostic

	// base[file][segIdx] = resolved address of that segment instance.
	base [][]uint16
	// exported[name] = final value of a globally-visible symbol.
	exported map[string]exportedSym
	placed   []Placement
}

type exportedSym struct {
	value int32
	kind  SymKind
}

// Link places the given object files per cfg and returns a linear image.
// On error the Result carries error Diagnostics and Image is nil; callers
// should check Result.HasErrors.
func Link(files []*object.File, cfg *memmap.Config) Result {
	l := &linker{
		files:    files,
		cfg:      cfg,
		exported: map[string]exportedSym{},
		base:     make([][]uint16, len(files)),
	}
	for i, f := range files {
		l.base[i] = make([]uint16, len(f.Segments))
	}

	l.place()
	if l.hasErrors() {
		return Result{Diags: l.diags}
	}
	l.resolveExports()
	if l.hasErrors() {
		return Result{Diags: l.diags}
	}
	img, origin := l.emit()
	l.applyRelocs(img, origin)

	return Result{
		Image:      img,
		Origin:     origin,
		Symbols:    l.symbolTable(),
		Placements: l.placed,
		Diags:      l.diags,
	}
}

func (l *linker) errf(code, format string, a ...any) {
	l.diags = append(l.diags, diag.Diagnostic{
		Severity: diag.SeverityError,
		Code:     code,
		Message:  fmt.Sprintf(format, a...),
	})
}

func (l *linker) hasErrors() bool {
	for _, d := range l.diags {
		if d.Severity == diag.SeverityError {
			return true
		}
	}
	return false
}

// place assigns every input segment to a memory area per the config.
// Segments are matched to SEGMENTS specs by name; multiple object files
// contributing the same segment name concatenate in input-file order
// (like a conventional linker). A spec with start= pins the first
// instance; the rest pack sequentially within the memory area.
func (l *linker) place() {
	// cursor[area] tracks the next free address within that area.
	cursor := map[string]int{}
	for i := range l.cfg.Memory {
		cursor[l.cfg.Memory[i].Name] = l.cfg.Memory[i].Start
	}

	seen := map[string]bool{} // input segment names placed by some spec

	for _, spec := range l.cfg.Segments {
		area, ok := l.cfg.Area(spec.Load)
		if !ok {
			l.errf(codeUnknownMem,
				"segment %q loads into unknown memory area %q",
				spec.Name, spec.Load)
			continue
		}
		first := true
		for fi, f := range l.files {
			for si := range f.Segments {
				seg := &f.Segments[si]
				if seg.Name != spec.Name {
					continue
				}
				seen[seg.Name] = true

				addr := cursor[area.Name]
				if first && spec.Start >= 0 {
					addr = spec.Start
				}
				first = false

				size := len(seg.Bytes)
				end := addr + size
				if end > area.Start+area.Size {
					l.errf(codeOverflow,
						"segment %q ($%04X..$%04X, %d bytes) overflows memory area %q ($%04X..$%04X)",
						seg.Name, addr, end, size, area.Name,
						area.Start, area.Start+area.Size)
					continue
				}
				l.base[fi][si] = uint16(addr)
				cursor[area.Name] = end
				l.placed = append(l.placed, Placement{
					Segment: seg.Name,
					File:    fi,
					SegIdx:  si,
					Addr:    uint16(addr),
					Size:    size,
				})
			}
		}
	}

	// Any object segment with no matching SEGMENTS spec is a config bug.
	for fi, f := range l.files {
		for si := range f.Segments {
			n := f.Segments[si].Name
			if !seen[n] {
				l.errf(codeUnplaced,
					"object segment %q (file %d) has no SEGMENTS entry in the config",
					n, fi)
			}
		}
	}
}

// resolveExports builds the global symbol table from every file's
// exported symbols, reporting duplicate definitions.
func (l *linker) resolveExports() {
	for _, f := range l.files {
		for _, s := range f.Symbols {
			if s.Flags&object.SymExported == 0 {
				continue
			}
			v, kind := l.symValue(f, s)
			if prev, dup := l.exported[s.Name]; dup {
				if prev.value != v || prev.kind != kind {
					l.errf(codeDupSymbol,
						"symbol %q defined more than once", s.Name)
				}
				continue
			}
			l.exported[s.Name] = exportedSym{value: v, kind: kind}
		}
	}
}

// symValue resolves a symbol to its final value within file f. A symbol
// with SegIndex < 0 is an absolute constant; otherwise it is an address
// relative to its (now-placed) segment.
func (l *linker) symValue(f *object.File, s object.Symbol) (int32, SymKind) {
	if s.SegIndex < 0 {
		return s.Value, SymKindConst
	}
	fi := l.fileIndex(f)
	base := int32(l.base[fi][s.SegIndex])
	return base + s.Value, SymKindLabel
}

func (l *linker) fileIndex(f *object.File) int {
	for i := range l.files {
		if l.files[i] == f {
			return i
		}
	}
	return 0
}

// emit lays every placed segment into a single linear image spanning the
// lowest placed address through the highest placed end, applying memory
// fills. Returns the image and its load origin.
func (l *linker) emit() ([]byte, uint16) {
	if len(l.placed) == 0 {
		return nil, 0
	}
	lo, hi := 0x10000, 0
	for _, p := range l.placed {
		if int(p.Addr) < lo {
			lo = int(p.Addr)
		}
		if int(p.Addr)+p.Size > hi {
			hi = int(p.Addr) + p.Size
		}
	}
	img := make([]byte, hi-lo)

	// Pre-fill spans of fill=yes memory areas that overlap the image.
	for i := range l.cfg.Memory {
		m := &l.cfg.Memory[i]
		if !m.Fill {
			continue
		}
		for a := m.Start; a < m.Start+m.Size; a++ {
			if a >= lo && a < hi {
				img[a-lo] = m.FillVal
			}
		}
	}

	for _, p := range l.placed {
		seg := &l.files[p.File].Segments[p.SegIdx]
		copy(img[int(p.Addr)-lo:], seg.Bytes)
	}
	return img, uint16(lo)
}

// applyRelocs walks every segment's relocations and patches the image in
// place. Reloc SymIndex points into the owning file's symbol table; an
// imported symbol is resolved through the global export table.
func (l *linker) applyRelocs(img []byte, origin uint16) {
	for fi, f := range l.files {
		for si := range f.Segments {
			seg := &f.Segments[si]
			segBase := int(l.base[fi][si])
			for _, r := range seg.Relocs {
				if r.SymIndex < 0 || r.SymIndex >= len(f.Symbols) {
					l.errf(codeRelocRange,
						"segment %q: relocation symbol index %d out of range",
						seg.Name, r.SymIndex)
					continue
				}
				sym := f.Symbols[r.SymIndex]
				val, ok := l.relocValue(f, sym)
				if !ok {
					continue
				}
				val += r.Addend

				absOperand := segBase + int(r.Offset)
				at := absOperand - int(origin)
				if at < 0 || at >= len(img) {
					l.errf(codeRelocRange,
						"segment %q: relocation at $%04X is outside the image",
						seg.Name, absOperand)
					continue
				}
				l.patch(img, at, absOperand, r, val, seg.Name)
			}
		}
	}
}

// relocValue resolves the value a relocation should write. Imported
// symbols come from the global table; everything else is file-local.
func (l *linker) relocValue(f *object.File, s object.Symbol) (int32, bool) {
	if s.Flags&object.SymImported != 0 {
		e, ok := l.exported[s.Name]
		if !ok {
			l.errf(codeUndefined, "undefined symbol %q", s.Name)
			return 0, false
		}
		return e.value, true
	}
	v, _ := l.symValue(f, s)
	return v, true
}

// patch writes val into the image. at is the image index of the patched
// byte; absOperand is its absolute 6502 address (needed for the
// PC-relative branch form).
func (l *linker) patch(img []byte, at, absOperand int, r object.Reloc, val int32, seg string) {
	switch r.Type {
	case object.RelByteLo:
		img[at] = byte(val)
	case object.RelByteHi:
		img[at] = byte(val >> 8)
	case object.RelWord:
		if at+1 >= len(img) {
			l.errf(codeRelocRange,
				"segment %q: word relocation runs off the image", seg)
			return
		}
		img[at] = byte(val)
		img[at+1] = byte(val >> 8)
	case object.RelBranch:
		// 6502 branch: target = (operand addr + 1) + signed(operand).
		disp := int(val) - (absOperand + 1)
		if disp < -128 || disp > 127 {
			l.errf(codeRelocRange,
				"segment %q: relative branch out of range (%d bytes)",
				seg, disp)
			return
		}
		img[at] = byte(int8(disp))
	default:
		l.errf(codeRelocRange,
			"segment %q: unknown relocation type %d", seg, r.Type)
	}
}

func (l *linker) symbolTable() []Symbol {
	out := make([]Symbol, 0, len(l.exported))
	for name, e := range l.exported {
		out = append(out, Symbol{Name: name, Value: e.value, Kind: e.kind})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	return out
}
