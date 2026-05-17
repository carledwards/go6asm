package link

import (
	"testing"

	"github.com/carledwards/go6asm/memmap"
	"github.com/carledwards/go6asm/object"
)

func mustCfg(t *testing.T, name string) *memmap.Config {
	t.Helper()
	c, err := memmap.Builtin(name)
	if err != nil {
		t.Fatalf("Builtin(%q): %v", name, err)
	}
	return c
}

// TestPlaceAndResolve: a CODE segment with a RelWord reloc to a label
// in a RODATA segment. flat-64k packs CODE at $0200, RODATA right after.
func TestPlaceAndResolve(t *testing.T) {
	f := &object.File{
		Segments: []object.Segment{
			{
				Name:        "CODE",
				Relocatable: true,
				// LDA $0000 (abs) — operand at offset 1, patched to msg.
				Bytes: []byte{0xAD, 0x00, 0x00},
				Relocs: []object.Reloc{
					{Offset: 1, Type: object.RelWord, Size: 2, SymIndex: 0},
				},
			},
			{
				Name:        "RODATA",
				Relocatable: true,
				Bytes:       []byte{'h', 'i', 0},
			},
		},
		Symbols: []object.Symbol{
			// "msg" = offset 0 within segment index 1 (RODATA).
			{Name: "msg", Value: 0, SegIndex: 1,
				Flags: object.SymExported | object.SymLabel},
		},
	}

	r := Link([]*object.File{f}, mustCfg(t, "flat-64k"))
	if r.HasErrors() {
		t.Fatalf("link errors: %+v", r.Diags)
	}
	if r.Origin != 0x0200 {
		t.Errorf("origin = $%04X, want $0200", r.Origin)
	}
	// CODE at $0200 (3 bytes), RODATA packs at $0203.
	want := []byte{0xAD, 0x03, 0x02, 'h', 'i', 0}
	if string(r.Image) != string(want) {
		t.Errorf("image = % X, want % X", r.Image, want)
	}
	if len(r.Symbols) != 1 || r.Symbols[0].Name != "msg" ||
		r.Symbols[0].Value != 0x0203 {
		t.Errorf("symbols = %+v", r.Symbols)
	}
}

// TestCrossFileImport: file B imports a symbol exported by file A.
func TestCrossFileImport(t *testing.T) {
	a := &object.File{
		Segments: []object.Segment{
			{Name: "RODATA", Relocatable: true, Bytes: []byte{0x42}},
		},
		Symbols: []object.Symbol{
			{Name: "answer", Value: 0, SegIndex: 0,
				Flags: object.SymExported | object.SymLabel},
		},
	}
	b := &object.File{
		Segments: []object.Segment{
			{
				Name:        "CODE",
				Relocatable: true,
				Bytes:       []byte{0xAD, 0, 0}, // LDA answer
				Relocs: []object.Reloc{
					{Offset: 1, Type: object.RelWord, Size: 2, SymIndex: 0},
				},
			},
		},
		Symbols: []object.Symbol{
			{Name: "answer", SegIndex: -1, Flags: object.SymImported},
		},
	}

	r := Link([]*object.File{a, b}, mustCfg(t, "flat-64k"))
	if r.HasErrors() {
		t.Fatalf("link errors: %+v", r.Diags)
	}
	// flat-64k: CODE at $0200 (spec order: CODE before RODATA), RODATA $0203.
	answer := uint16(0x0203)
	lo, hi := byte(answer), byte(answer>>8)
	// image origin $0200: CODE bytes then RODATA byte.
	if r.Image[1] != lo || r.Image[2] != hi {
		t.Errorf("patched operand = $%02X%02X, want $%04X",
			r.Image[2], r.Image[1], answer)
	}
}

func TestDuplicateSymbol(t *testing.T) {
	mk := func() *object.File {
		return &object.File{
			Segments: []object.Segment{
				{Name: "CODE", Relocatable: true, Bytes: []byte{0xEA}},
			},
			Symbols: []object.Symbol{
				{Name: "dup", Value: 5, SegIndex: -1,
					Flags: object.SymExported},
			},
		}
	}
	// Same name, different value → conflict.
	a, b := mk(), mk()
	b.Symbols[0].Value = 9
	r := Link([]*object.File{a, b}, mustCfg(t, "flat-64k"))
	if !r.HasErrors() {
		t.Fatal("expected duplicate-symbol error")
	}
	if !hasCode(r, codeDupSymbol) {
		t.Errorf("want %s, got %+v", codeDupSymbol, r.Diags)
	}
}

func TestUndefinedImport(t *testing.T) {
	f := &object.File{
		Segments: []object.Segment{
			{
				Name:        "CODE",
				Relocatable: true,
				Bytes:       []byte{0xAD, 0, 0},
				Relocs: []object.Reloc{
					{Offset: 1, Type: object.RelWord, Size: 2, SymIndex: 0},
				},
			},
		},
		Symbols: []object.Symbol{
			{Name: "missing", SegIndex: -1, Flags: object.SymImported},
		},
	}
	r := Link([]*object.File{f}, mustCfg(t, "flat-64k"))
	if !hasCode(r, codeUndefined) {
		t.Errorf("want %s, got %+v", codeUndefined, r.Diags)
	}
}

func TestSegmentOverflow(t *testing.T) {
	big := make([]byte, 0x9000) // > 8 KB ROM in sim-tui
	f := &object.File{
		Segments: []object.Segment{
			{Name: "CODE", Relocatable: true, Bytes: big},
		},
	}
	r := Link([]*object.File{f}, mustCfg(t, "sim-tui"))
	if !hasCode(r, codeOverflow) {
		t.Errorf("want %s, got %+v", codeOverflow, r.Diags)
	}
}

func TestUnplacedSegment(t *testing.T) {
	f := &object.File{
		Segments: []object.Segment{
			{Name: "WEIRD", Relocatable: true, Bytes: []byte{1}},
		},
	}
	r := Link([]*object.File{f}, mustCfg(t, "flat-64k"))
	if !hasCode(r, codeUnplaced) {
		t.Errorf("want %s, got %+v", codeUnplaced, r.Diags)
	}
}

func TestUnknownMemoryArea(t *testing.T) {
	cfg, err := memmap.Parse([]byte(
		"MEMORY { M: start=$0, size=$100, type=rw; } " +
			"SEGMENTS { CODE: load=NOPE, type=rw; }"))
	if err != nil {
		t.Fatal(err)
	}
	f := &object.File{Segments: []object.Segment{
		{Name: "CODE", Relocatable: true, Bytes: []byte{1}},
	}}
	r := Link([]*object.File{f}, cfg)
	if !hasCode(r, codeUnknownMem) {
		t.Errorf("want %s, got %+v", codeUnknownMem, r.Diags)
	}
}

// TestExplicitStartAndBranch: VECTORS pinned at $FFFA via the cfg, plus a
// PC-relative branch reloc that resolves in-range.
func TestExplicitStartAndBranch(t *testing.T) {
	f := &object.File{
		Segments: []object.Segment{
			{
				Name:        "CODE",
				Relocatable: true,
				// BNE target ; target: NOP
				//  $0200 D0 ??   (operand at offset 1)
				//  $0202 EA
				Bytes: []byte{0xD0, 0x00, 0xEA},
				Relocs: []object.Reloc{
					{Offset: 1, Type: object.RelBranch, Size: 1, SymIndex: 0},
				},
			},
			{
				Name:        "VECTORS",
				Relocatable: true,
				Bytes:       []byte{0x00, 0x02, 0x00, 0x02, 0x00, 0x02},
			},
		},
		Symbols: []object.Symbol{
			{Name: "target", Value: 2, SegIndex: 0,
				Flags: object.SymExported | object.SymLabel},
		},
	}
	r := Link([]*object.File{f}, mustCfg(t, "flat-64k"))
	if r.HasErrors() {
		t.Fatalf("link errors: %+v", r.Diags)
	}
	// target = $0202, branch operand at $0201, PC-after = $0202.
	// disp = $0202 - $0202 = 0.
	if r.Image[1] != 0x00 {
		t.Errorf("branch disp = %d, want 0", int8(r.Image[1]))
	}
	// VECTORS pinned at $FFFA: image must span $0200..$10000.
	if r.Origin != 0x0200 || len(r.Image) != 0x10000-0x0200 {
		t.Errorf("origin=$%04X len=%d", r.Origin, len(r.Image))
	}
	v := r.Image[0xFFFA-0x0200]
	if v != 0x00 || r.Image[0xFFFA-0x0200+1] != 0x02 {
		t.Errorf("VECTORS not at $FFFA: got $%02X", v)
	}
}

func TestBranchOutOfRange(t *testing.T) {
	// 200-byte segment; branch operand at the end targets offset 0.
	// disp = 0 - (199 + 1) = -200, well past -128.
	body := make([]byte, 200)
	f := &object.File{
		Segments: []object.Segment{
			{
				Name:        "CODE",
				Relocatable: true,
				Bytes:       body,
				Relocs: []object.Reloc{
					{Offset: 199, Type: object.RelBranch, Size: 1, SymIndex: 0},
				},
			},
		},
		Symbols: []object.Symbol{
			{Name: "back", Value: 0, SegIndex: 0,
				Flags: object.SymExported | object.SymLabel},
		},
	}
	r := Link([]*object.File{f}, mustCfg(t, "flat-64k"))
	if !hasCode(r, codeRelocRange) {
		t.Errorf("want %s, got %+v", codeRelocRange, r.Diags)
	}
}

func hasCode(r Result, code string) bool {
	for _, d := range r.Diags {
		if d.Code == code {
			return true
		}
	}
	return false
}
