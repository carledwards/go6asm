package asm

import (
	"bytes"
	"testing"

	"github.com/carledwards/go6asm/codegen"
	"github.com/carledwards/go6asm/diag"
)

func sf(name, content string) SourceFile {
	return SourceFile{Name: name, Content: []byte(content)}
}

func TestIncludeFlatten(t *testing.T) {
	in := Input{
		Entry:  "main.s",
		Origin: 0x0600,
		Files: []SourceFile{
			sf("main.s", ".include \"lib.s\"\nstart:\n\tJSR helper\n\tRTS\n"),
			sf("lib.s", "helper:\n\tNOP\n\tRTS\n"),
		},
	}
	r := Assemble(in)
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	want := []byte{0xEA, 0x60, 0x20, 0x00, 0x06, 0x60}
	if !bytes.Equal(r.Image, want) {
		t.Fatalf("image %X, want %X", r.Image, want)
	}
	sm := map[string]uint16{}
	for _, s := range r.Symbols {
		sm[s.Name] = s.Addr
	}
	if sm["helper"] != 0x0600 || sm["start"] != 0x0602 {
		t.Errorf("symbols = %v", sm)
	}
}

func TestDefines(t *testing.T) {
	r := Assemble(Input{
		Files:   []SourceFile{sf("main.s", "\tLDX #VAL\n")},
		Defines: map[string]int64{"VAL": 5},
	})
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if !bytes.Equal(r.Image, []byte{0xA2, 0x05}) {
		t.Errorf("image %X, want A2 05", r.Image)
	}
}

func TestMissingInclude(t *testing.T) {
	r := Assemble(Input{Files: []SourceFile{sf("main.s", ".include \"nope.s\"\n")}})
	if r.Ok() || !hasCode(r.Errors, "E0020") {
		t.Errorf("expected E0020 missing include, got %v", r.Errors)
	}
}

func TestIncludeCycle(t *testing.T) {
	r := Assemble(Input{
		Entry: "a.s",
		Files: []SourceFile{
			sf("a.s", ".include \"b.s\"\n"),
			sf("b.s", ".include \"a.s\"\n"),
		},
	})
	if !hasCode(r.Errors, "E0021") {
		t.Errorf("expected E0021 include cycle, got %v", r.Errors)
	}
}

func TestErrorSplit(t *testing.T) {
	r := Assemble(Input{Files: []SourceFile{sf("main.s", "\tLDA missing\n")}})
	if r.Ok() || len(r.Errors) == 0 {
		t.Errorf("expected errors for undefined symbol, got ok=%v", r.Ok())
	}
}

func hasCode(ds []diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// TestObjectLinkMode: Target selects a built-in memory map; the unit is
// assembled to a relocatable object and linked. flat-64k pins CODE at
// $0200, so a self-referential JMP resolves there.
func TestObjectLinkMode(t *testing.T) {
	in := Input{
		Entry:      "m.s",
		Target:     "flat-64k",
		EmitObject: true,
		Files: []SourceFile{
			sf("m.s", ".export start\nstart:\n\tLDA data\n\tJMP start\ndata:\n\t.byte $7E\n"),
		},
	}
	r := Assemble(in)
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Origin != 0x0200 {
		t.Errorf("origin $%04X, want $0200", r.Origin)
	}
	// LDA data(abs) ; JMP start ; .byte $7E
	// $0200 AD 06 02   $0203 4C 00 02   $0206 7E
	want := []byte{0xAD, 0x06, 0x02, 0x4C, 0x00, 0x02, 0x7E}
	if !bytes.Equal(r.Image, want) {
		t.Fatalf("image % X, want % X", r.Image, want)
	}
	if len(r.Object) == 0 || string(r.Object[:4]) != "GO6O" {
		t.Errorf("Object not a GO6O blob (%d bytes)", len(r.Object))
	}
	got := map[string]uint16{}
	for _, s := range r.Symbols {
		got[s.Name] = s.Addr
	}
	if got["start"] != 0x0200 {
		t.Errorf("symbol start = $%04X, want $0200 (%v)", got["start"], got)
	}
}

func TestObjectBadTarget(t *testing.T) {
	r := Assemble(Input{
		Entry:  "m.s",
		Target: "no-such-machine",
		Files:  []SourceFile{sf("m.s", "RTS\n")},
	})
	if r.Ok() {
		t.Fatal("expected an error for an unknown target")
	}
}

// hasCode reports whether any error diagnostic carries the given code.
func hasErrCode(r Result, code string) bool {
	for _, d := range r.Errors {
		if d.Code == code {
			return true
		}
	}
	return false
}

func infoCodes(r Result) map[string]bool {
	m := map[string]bool{}
	for _, d := range r.Infos {
		m[d.Code] = true
	}
	return m
}

// TestLayer0SimTUI: "just instructions" → loads at $E000, a $2000 ROM
// image, with an auto NMI/RESET/IRQ block at $FFFA and RESET → $E000.
func TestLayer0SimTUI(t *testing.T) {
	r := Assemble(Input{
		Layer0: true,
		Files:  []SourceFile{sf("a.s", "\tLDA #$42\n\tRTS\n")},
	})
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Origin != 0xE000 || len(r.Image) != 0x2000 {
		t.Fatalf("origin $%04X len %d, want $E000 / $2000", r.Origin, len(r.Image))
	}
	if r.Image[0] != 0xA9 || r.Image[1] != 0x42 || r.Image[2] != 0x60 {
		t.Errorf("code = % X, want A9 42 60", r.Image[:3])
	}
	// $FFFA NMI=$0000, $FFFC RESET=$E000, $FFFE IRQ=$0000.
	v := func(off int) uint16 { return uint16(r.Image[off]) | uint16(r.Image[off+1])<<8 }
	if v(0x1FFA) != 0x0000 || v(0x1FFC) != 0xE000 || v(0x1FFE) != 0x0000 {
		t.Errorf("vectors NMI=%04X RESET=%04X IRQ=%04X",
			v(0x1FFA), v(0x1FFC), v(0x1FFE))
	}
	ic := infoCodes(r)
	if !ic["I0001"] || !ic["I0002"] || !ic["I0003"] {
		t.Errorf("missing inference infos: %v", r.Infos)
	}
}

func TestLayer0Raw(t *testing.T) {
	r := Assemble(Input{
		Layer0: true, Target: "raw",
		Files: []SourceFile{sf("a.s", "\tLDA #$42\n\tRTS\n")},
	})
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Origin != 0x0200 || len(r.Image) != 3 {
		t.Errorf("raw: origin $%04X len %d, want $0200 / 3", r.Origin, len(r.Image))
	}
}

func TestLayer0LoadOverride(t *testing.T) {
	r := Assemble(Input{
		Layer0: true,
		Files:  []SourceFile{sf("a.s", ".load $0600\n\tLDA #$01\n\tRTS\n")},
	})
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Origin != 0x0600 {
		t.Errorf("origin $%04X, want $0600", r.Origin)
	}
	// RESET vector ($FFFC) → $0600.
	off := 0xFFFC - 0x0600
	if got := uint16(r.Image[off]) | uint16(r.Image[off+1])<<8; got != 0x0600 {
		t.Errorf("RESET = $%04X, want $0600", got)
	}
}

// TestLayer0SymbolPack: the platform pack lets Layer-0 code name a
// register (RegFrame = $A802) without defining it.
func TestLayer0SymbolPack(t *testing.T) {
	r := Assemble(Input{
		Layer0: true,
		Files:  []SourceFile{sf("a.s", "\tSTA RegFrame\n\tRTS\n")},
	})
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Image[0] != 0x8D || r.Image[1] != 0x02 || r.Image[2] != 0xA8 {
		t.Errorf("STA RegFrame = % X, want 8D 02 A8", r.Image[:3])
	}
}

func TestLayer0RejectsSegment(t *testing.T) {
	r := Assemble(Input{
		Layer0: true,
		Files:  []SourceFile{sf("a.s", ".segment \"CODE\"\n\tRTS\n")},
	})
	if r.Ok() || !hasErrCode(r, "E0028") {
		t.Errorf("expected E0028 for .segment in Layer 0, got %v", r.Errors)
	}
}

func TestLayer0UnknownTarget(t *testing.T) {
	r := Assemble(Input{
		Layer0: true, Target: "nope",
		Files: []SourceFile{sf("a.s", "\tRTS\n")},
	})
	if r.Ok() || !hasErrCode(r, "E0025") {
		t.Errorf("expected E0025 for unknown target, got %v", r.Errors)
	}
}

// TestLayer0ExportedConstants: an exported ZP constant reaches
// Result.Symbols as a const at its address — the path sim-tui uses
// to name memory ($10 -> "X") in its label view.
func TestLayer0ExportedConstants(t *testing.T) {
	r := Assemble(Input{
		Layer0: true,
		Files: []SourceFile{sf("a.s",
			".exportzp X\nX = $10\nTMP = $20\n\tLDX X\n\tINX\n\tSTX X\n\tRTS\n")},
	})
	if !r.Ok() {
		t.Fatalf("errors: %v", r.Errors)
	}
	var x *codegen.Symbol
	for i := range r.Symbols {
		if r.Symbols[i].Name == "X" {
			x = &r.Symbols[i]
		}
		if r.Symbols[i].Name == "TMP" {
			t.Errorf("TMP must not be exported, got %+v", r.Symbols[i])
		}
	}
	if x == nil {
		t.Fatalf("symbol X not surfaced; got %+v", r.Symbols)
	}
	if x.Addr != 0x0010 || x.Kind != codegen.ConstSym {
		t.Errorf("X = %+v, want Addr $0010 Kind ConstSym", *x)
	}
}
