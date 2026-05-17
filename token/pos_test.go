package token

import "testing"

func TestResolveLineCol(t *testing.T) {
	fs := NewFileSet()
	f := fs.AddFile("game.s", []byte("LDX #0\nloop:\n    BEQ done\n"))

	cases := []struct {
		off       int
		line, col int
	}{
		{0, 1, 1},   // 'L'
		{5, 1, 6},   // '0'
		{7, 2, 1},   // 'l' of loop
		{13, 3, 1},  // first space of line 3
		{17, 3, 5},  // 'B' of BEQ
		{999, 4, 1}, // clamped past EOF
	}
	for _, c := range cases {
		got := (Pos{File: f, Offset: c.off}).Resolve()
		if got.Line != c.line || got.Col != c.col {
			t.Errorf("off %d: got %d:%d, want %d:%d",
				c.off, got.Line, got.Col, c.line, c.col)
		}
	}
}

func TestInvalidPos(t *testing.T) {
	if NoPos.IsValid() {
		t.Fatal("NoPos must be invalid")
	}
	if got := NoPos.Resolve(); got != (Position{}) {
		t.Errorf("NoPos resolved to %+v, want zero", got)
	}
}

func TestLineText(t *testing.T) {
	fs := NewFileSet()
	f := fs.AddFile("t.s", []byte("alpha\r\nbeta\ngamma"))
	for n, want := range map[int]string{1: "alpha", 2: "beta", 3: "gamma", 9: ""} {
		if got := f.LineText(n); got != want {
			t.Errorf("LineText(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{
		Ident: "Ident", Int: "Int", Newline: "Newline",
		ColonColon: "::", Shl: "<<", AndAnd: "&&",
	} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}
