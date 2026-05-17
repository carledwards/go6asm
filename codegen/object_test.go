package codegen

import (
	"testing"

	"github.com/carledwards/go6asm/link"
	"github.com/carledwards/go6asm/memmap"
	"github.com/carledwards/go6asm/object"
	"github.com/carledwards/go6asm/parser"
	"github.com/carledwards/go6asm/token"
)

func obj(t *testing.T, src string) ObjectResult {
	t.Helper()
	fs := token.NewFileSet()
	a, d := parser.Parse(fs.AddFile("t.s", []byte(src)))
	if len(d) != 0 {
		t.Fatalf("parse errors: %v", d)
	}
	return GenerateObject(a, ObjectOptions{})
}

// TestObjectStructure checks segments, symbol flags, and relocs.
func TestObjectStructure(t *testing.T) {
	r := obj(t, `
.export start
.import putc
.segment "CODE"
start:
	JSR putc
	LDA data
	JMP start
.segment "RODATA"
data:
	.byte $11, $22
`)
	if r.HasErrors() {
		t.Fatalf("object errors: %+v", r.Diags)
	}
	f := r.File
	if len(f.Segments) != 2 ||
		f.Segments[0].Name != "CODE" || f.Segments[1].Name != "RODATA" {
		t.Fatalf("segments = %+v", f.Segments)
	}
	// JSR(3) + LDA abs(3) + JMP(3) = 9 bytes of CODE.
	if len(f.Segments[0].Bytes) != 9 {
		t.Errorf("CODE size = %d, want 9", len(f.Segments[0].Bytes))
	}
	if len(f.Segments[1].Bytes) != 2 {
		t.Errorf("RODATA size = %d, want 2", len(f.Segments[1].Bytes))
	}

	sym := map[string]object.Symbol{}
	for _, s := range f.Symbols {
		sym[s.Name] = s
	}
	if s := sym["start"]; s.Flags&object.SymExported == 0 || s.SegIndex != 0 {
		t.Errorf("start = %+v, want exported in seg 0", s)
	}
	if s := sym["putc"]; s.Flags&object.SymImported == 0 || s.SegIndex != -1 {
		t.Errorf("putc = %+v, want imported", s)
	}
	if s, ok := sym["data"]; !ok || s.SegIndex != 1 || s.Flags&object.SymLocal == 0 {
		t.Errorf("data = %+v, want local in seg 1", s)
	}

	// CODE relocs: JSR putc (import, word), LDA data (anchor RODATA, word),
	// JMP start (anchor CODE, word).
	rs := f.Segments[0].Relocs
	if len(rs) != 3 {
		t.Fatalf("CODE relocs = %d, want 3: %+v", len(rs), rs)
	}
	if rs[0].Offset != 1 || rs[0].Type != object.RelWord {
		t.Errorf("JSR reloc = %+v", rs[0])
	}
	if rs[2].Offset != 7 || rs[2].Addend != 0 {
		t.Errorf("JMP start reloc = %+v", rs[2])
	}
}

// TestObjectFlatEquivalence is the oracle: the same program assembled
// flat (.org $0200) and assembled-then-linked (CODE pinned at $0200)
// must produce byte-identical images.
func TestObjectFlatEquivalence(t *testing.T) {
	body := `
start:
	LDX #0
loop:
	LDA msg,X
	BEQ done
	STA $00
	INX
	BNE loop
done:
	JMP start
	.word msg
	.byte <msg, >msg
msg:
	.asciiz "HI"
`
	// Flat reference.
	fsF := token.NewFileSet()
	af, d := parser.Parse(fsF.AddFile("f.s", []byte(".org $0200\n"+body)))
	if len(d) != 0 {
		t.Fatalf("flat parse: %v", d)
	}
	flat := Generate(af, Options{})
	if len(flat.Diags) != 0 {
		t.Fatalf("flat diags: %v", flat.Diags)
	}

	// Object → link at $0200.
	r := obj(t, body)
	if r.HasErrors() {
		t.Fatalf("object diags: %+v", r.Diags)
	}
	cfg, err := memmap.Parse([]byte(
		"MEMORY { MAIN: start=$0000, size=$10000, type=rw, fill=yes, fillval=$00; }" +
			"SEGMENTS { CODE: load=MAIN, type=rw, start=$0200; }"))
	if err != nil {
		t.Fatal(err)
	}
	lk := link.Link([]*object.File{r.File}, cfg)
	if lk.HasErrors() {
		t.Fatalf("link diags: %+v", lk.Diags)
	}

	if flat.Origin != lk.Origin {
		t.Errorf("origin: flat $%04X, linked $%04X", flat.Origin, lk.Origin)
	}
	if string(flat.Image) != string(lk.Image) {
		t.Fatalf("image mismatch:\n flat  = % X\n linked= % X",
			flat.Image, lk.Image)
	}
}

func TestObjectRejectsScopes(t *testing.T) {
	r := obj(t, ".proc foo\nRTS\n.endproc\n")
	if !r.HasErrors() {
		t.Fatal("expected an error for .proc in object mode")
	}
}

// Exported `=` constants surface as absolute object symbols (4b);
// un-exported ones stay local.
func TestObjectExportedConstants(t *testing.T) {
	r := obj(t, `
.exportzp ZPX
.export   MAGIC
ZPX   = $10
MAGIC = $1234
TEMP  = $99          ; not exported -> must NOT appear
start:
	LDA ZPX
	RTS
`)
	if r.HasErrors() {
		t.Fatalf("errors: %+v", r.Diags)
	}
	got := map[string]object.Symbol{}
	for _, s := range r.File.Symbols {
		got[s.Name] = s
	}
	if s, ok := got["ZPX"]; !ok || s.SegIndex != -1 || s.Value != 0x10 ||
		s.Flags&object.SymExported == 0 {
		t.Errorf("ZPX = %+v, want exported abs $10", s)
	}
	if s, ok := got["MAGIC"]; !ok || s.SegIndex != -1 || s.Value != 0x1234 {
		t.Errorf("MAGIC = %+v, want exported abs $1234", s)
	}
	if s, ok := got["TEMP"]; ok {
		t.Errorf("TEMP must not be exported, got %+v", s)
	}
}

// `*` in an instruction operand is the instruction's start address
// (ca65), not the byte after it — regression for the CurPC timing bug.
func TestObjectStarIsInstructionStart(t *testing.T) {
	r := obj(t, ".export loop\nloop:\n\tJMP *\n\tLDA #<*\n")
	if r.HasErrors() {
		t.Fatalf("errors: %+v", r.Diags)
	}
	cfg, err := memmap.Parse([]byte(
		"MEMORY { M: start=$0000, size=$10000, type=rw; }" +
			"SEGMENTS { CODE: load=M, type=rw, start=$0400; }"))
	if err != nil {
		t.Fatal(err)
	}
	lk := link.Link([]*object.File{r.File}, cfg)
	if lk.HasErrors() {
		t.Fatalf("link: %+v", lk.Diags)
	}
	// CODE @ $0400: JMP * must target $0400 (self), not $0403.
	// LDA #<* at $0403 → low byte of $0403 = $03.
	want := []byte{0x4C, 0x00, 0x04, 0xA9, 0x03}
	if string(lk.Image[:5]) != string(want) {
		t.Fatalf("image % X, want % X (JMP self + LDA #<*)", lk.Image[:5], want)
	}
}
