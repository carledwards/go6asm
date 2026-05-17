package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/parser"
	"github.com/carledwards/go6asm/token"
)

func asm(t *testing.T, src string) Result {
	t.Helper()
	fs := token.NewFileSet()
	f, d := parser.Parse(fs.AddFile("t.s", []byte(src)))
	if len(d) != 0 {
		t.Fatalf("parse errors: %v", d)
	}
	return Generate(f, Options{})
}

func symMap(r Result) map[string]uint16 {
	m := map[string]uint16{}
	for _, s := range r.Symbols {
		m[s.Name] = s.Addr
	}
	return m
}

// End-to-end: forward refs (abs default), relative branches both
// directions, zp-vs-abs, a directive, and symbol addresses.
func TestEndToEnd(t *testing.T) {
	src := `.org $0600
start:
	LDX #0
loop:
	LDA msg,X
	BEQ done
	STA $0400,X
	INX
	BNE loop
done:
	JMP done
msg:
	.asciiz "HI"
`
	r := asm(t, src)
	if len(r.Diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", r.Diags)
	}
	want := []byte{
		0xA2, 0x00, // LDX #0
		0xBD, 0x10, 0x06, // LDA msg,X   (msg forward -> abs,X)
		0xF0, 0x06, // BEQ done     (+6)
		0x9D, 0x00, 0x04, // STA $0400,X
		0xE8,       // INX
		0xD0, 0xF5, // BNE loop     (-11)
		0x4C, 0x0D, 0x06, // JMP done
		0x48, 0x49, 0x00, // "HI", 0
	}
	if r.Origin != 0x0600 {
		t.Errorf("origin = $%04X, want $0600", r.Origin)
	}
	if !bytes.Equal(r.Image, want) {
		t.Fatalf("image mismatch\n got %X\nwant %X", r.Image, want)
	}
	sm := symMap(r)
	for name, addr := range map[string]uint16{
		"start": 0x0600, "loop": 0x0602, "done": 0x060D, "msg": 0x0610,
	} {
		if sm[name] != addr {
			t.Errorf("%s = $%04X, want $%04X", name, sm[name], addr)
		}
	}
}

func TestAddressingSelection(t *testing.T) {
	cases := []struct {
		src  string
		want []byte
	}{
		{"LDA $10\n", []byte{0xA5, 0x10}},           // auto zero page
		{"LDA $1234\n", []byte{0xAD, 0x34, 0x12}},   // auto absolute
		{"LDA a:$10\n", []byte{0xAD, 0x10, 0x00}},   // forced absolute
		{"LDA z:$10\n", []byte{0xA5, 0x10}},         // forced zero page
		{"LDX #$05\n", []byte{0xA2, 0x05}},          // immediate
		{"JMP ($1234)\n", []byte{0x6C, 0x34, 0x12}}, // indirect
	}
	for _, c := range cases {
		r := asm(t, c.src)
		if len(r.Diags) != 0 {
			t.Errorf("%q: diagnostics %v", c.src, r.Diags)
			continue
		}
		if !bytes.Equal(r.Image, c.want) {
			t.Errorf("%q: got %X, want %X", c.src, r.Image, c.want)
		}
	}
}

func TestForcedZeroPageOutOfRange(t *testing.T) {
	r := asm(t, "LDA z:$1234\n")
	if !hasCode(r, "E0010") {
		t.Errorf("expected E0010 for forced-zp out of range, got %v", r.Diags)
	}
}

func TestBranchOutOfRange(t *testing.T) {
	r := asm(t, ".org $0000\n\tBNE far\n\t.res 200\nfar:\n\tNOP\n")
	if !hasCode(r, "E0014") {
		t.Errorf("expected E0014 branch out of range, got %v", r.Diags)
	}
}

func TestUndefinedSymbol(t *testing.T) {
	r := asm(t, "\tLDA missing\n")
	if len(r.Diags) == 0 {
		t.Error("expected an undefined-symbol diagnostic")
	}
}

func TestDuplicateLabel(t *testing.T) {
	r := asm(t, "dup:\n\tNOP\ndup:\n\tNOP\n")
	if !hasCode(r, "E0002") {
		t.Errorf("expected E0002 redefinition, got %v", r.Diags)
	}
}

func TestDataDirectives(t *testing.T) {
	r := asm(t, "v = $1234\n.byte 1, $FF, 'A'\n.word v, $00\n")
	if len(r.Diags) != 0 {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
	want := []byte{0x01, 0xFF, 0x41, 0x34, 0x12, 0x00, 0x00}
	if !bytes.Equal(r.Image, want) {
		t.Errorf("got %X, want %X", r.Image, want)
	}
}

func TestConstFromLabelFixedPoint(t *testing.T) {
	// end depends on a label declared *after* the constant; the
	// fixed-point pass must still resolve it.
	r := asm(t, ".org $0200\nsize = end - start\nstart:\n\tNOP\n\tNOP\nend:\n.byte size\n")
	if len(r.Diags) != 0 {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
	// NOP NOP = 2 bytes, then .byte size -> the size value (2).
	if got := r.Image[len(r.Image)-1]; got != 2 {
		t.Errorf("size byte = %d, want 2", got)
	}
}

func TestProcScopeAndQualifiedRefs(t *testing.T) {
	src := `g = $05
.scope LIB
val = $42
.endscope
.proc routine
	LDA #g
	LDX #LIB::val
inner:
	RTS
.endproc
	JMP routine
	JMP routine::inner
`
	r := asm(t, src)
	if len(r.Diags) != 0 {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
	want := []byte{0xA9, 0x05, 0xA2, 0x42, 0x60, 0x4C, 0x00, 0x00, 0x4C, 0x04, 0x00}
	if !bytes.Equal(r.Image, want) {
		t.Fatalf("got % X\nwant % X", r.Image, want)
	}
	sm := symMap(r)
	if sm["routine"] != 0 || sm["routine::inner"] != 4 || sm["LIB::val"] != 0x42 {
		t.Errorf("symbols = %v", sm)
	}
}

func TestScopeShadowingAllowed(t *testing.T) {
	// Same name in two different scopes is not a redefinition.
	r := asm(t, ".proc A\nx:\n\tRTS\n.endproc\n.proc B\nx:\n\tRTS\n.endproc\n")
	if hasCode(r, "E0002") {
		t.Errorf("same name in distinct scopes must not be a redefinition: %v", r.Diags)
	}
}

func TestUnmatchedEndproc(t *testing.T) {
	r := asm(t, "\tNOP\n.endproc\n")
	if !hasCode(r, "E0007") {
		t.Errorf("expected E0007 for stray .endproc, got %v", r.Diags)
	}
}

func TestLexicalResolutionOutward(t *testing.T) {
	// A symbol referenced inside a proc resolves to the enclosing scope.
	r := asm(t, "g = 7\n.proc P\n\tLDA #g\n.endproc\n")
	if len(r.Diags) != 0 || !bytes.Equal(r.Image, []byte{0xA9, 0x07}) {
		t.Errorf("got % X diags %v, want A9 07", r.Image, r.Diags)
	}
}

func TestAssert(t *testing.T) {
	if r := asm(t, ".assert 1, error, \"ok\"\n\tRTS\n"); len(r.Diags) != 0 {
		t.Errorf("true assertion must not diagnose: %v", r.Diags)
	}
	r := asm(t, "\tRTS\n.assert 0, error, \"boom\"\n")
	if !hasCode(r, "E0019") || r.Diags[0].Message != "boom" {
		t.Errorf("expected E0019 'boom', got %v", r.Diags)
	}
	r = asm(t, "\tRTS\n.assert 0, warning, \"meh\"\n")
	if len(r.Diags) != 1 || r.Diags[0].Severity != diag.SeverityWarning {
		t.Errorf("expected a warning, got %v", r.Diags)
	}
}

func TestListing(t *testing.T) {
	fs := token.NewFileSet()
	src := "start:\n\tLDA #$01\n\tSTA $0200\n"
	f, d := parser.Parse(fs.AddFile("t.s", []byte(src)))
	if len(d) != 0 {
		t.Fatalf("parse: %v", d)
	}
	lst := Generate(f, Options{Origin: 0x0600, Listing: true}).Listing
	for _, want := range []string{
		"Addr  Code", "A9 01", "8D 00 02", "LDA #$01", "STA $0200", "0600", "0602",
	} {
		if !strings.Contains(lst, want) {
			t.Errorf("listing missing %q\n%s", want, lst)
		}
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

// Per-PC comments (DESIGN §10.2): trailing same-line comments, a
// comment-only line attached to the next instruction, and a ';' inside
// a string literal which must NOT be read as a comment.
func TestPerPCComments(t *testing.T) {
	src := ".org $0200\n" +
		"start:\n" +
		"\tLDA #1   ; load one\n" + // trailing
		"\t; bump it\n" + // leading, blank-skip below
		"\n" +
		"\tINX\n" + // gets the "bump it" leading comment
		"\tNOP\n" + // no comment
		"\t.byte \"a;b\"\n" // ';' in string is data, not a comment
	r := asm(t, src)
	if len(r.Diags) != 0 {
		t.Fatalf("diags: %v", r.Diags)
	}
	// $0200 LDA #1 (2)  $0202 INX (1)  $0203 NOP (1)  $0204 "a;b"
	if got := r.Comments[0x0200]; got != "load one" {
		t.Errorf("comment@$0200 = %q, want %q", got, "load one")
	}
	if got := r.Comments[0x0202]; got != "bump it" {
		t.Errorf("comment@$0202 = %q, want %q", got, "bump it")
	}
	if got, ok := r.Comments[0x0203]; ok {
		t.Errorf("comment@$0203 = %q, want none", got)
	}
	if got, ok := r.Comments[0x0204]; ok {
		t.Errorf("comment@$0204 = %q, want none (';' was in a string)", got)
	}
}
