package scanner

import (
	"testing"

	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/token"
)

func scan(t *testing.T, src string) ([]token.Token, []diag.Diagnostic) {
	t.Helper()
	fs := token.NewFileSet()
	return ScanAll(fs.AddFile("t.s", []byte(src)))
}

type want struct {
	k token.Kind
	l string
}

func assertTokens(t *testing.T, src string, ws []want) {
	t.Helper()
	toks, diags := scan(t, src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for %q: %v", src, diags)
	}
	if len(toks) != len(ws)+1 { // +1 for EOF
		t.Fatalf("%q: got %d tokens, want %d\n%v", src, len(toks), len(ws)+1, toks)
	}
	for i, w := range ws {
		if toks[i].Kind != w.k || (w.l != "" && toks[i].Lit != w.l) {
			t.Errorf("%q tok[%d] = (%s %q), want (%s %q)",
				src, i, toks[i].Kind, toks[i].Lit, w.k, w.l)
		}
	}
	if toks[len(toks)-1].Kind != token.EOF {
		t.Errorf("%q: last token not EOF", src)
	}
}

func TestNumbers(t *testing.T) {
	assertTokens(t, "$1A %1010 0xFF 0b01 42 0", []want{
		{token.Int, "$1A"}, {token.Int, "%1010"}, {token.Int, "0xFF"},
		{token.Int, "0b01"}, {token.Int, "42"}, {token.Int, "0"},
	})
}

func TestIdentAndDirective(t *testing.T) {
	assertTokens(t, "loop_1 .byte .mod LDA", []want{
		{token.Ident, "loop_1"}, {token.DotIdent, ".byte"},
		{token.DotIdent, ".mod"}, {token.Ident, "LDA"},
	})
}

func TestLabelsAndScope(t *testing.T) {
	assertTokens(t, "name: @local: :: :+ :-- :", []want{
		{token.Ident, "name"}, {token.Colon, ":"},
		{token.At, "@"}, {token.Ident, "local"}, {token.Colon, ":"},
		{token.ColonColon, "::"},
		{token.UnnamedRef, "+"}, {token.UnnamedRef, "--"},
		{token.Colon, ":"},
	})
}

func TestOperators(t *testing.T) {
	assertTokens(t, "<< >> <= >= <> && || < > = + - * / & | ^ ~ ! # ( ) ,", []want{
		{token.Shl, "<<"}, {token.Shr, ">>"}, {token.Le, "<="}, {token.Ge, ">="},
		{token.NotEq, "<>"}, {token.AndAnd, "&&"}, {token.OrOr, "||"},
		{token.Lt, "<"}, {token.Gt, ">"}, {token.Eq, "="},
		{token.Plus, "+"}, {token.Minus, "-"}, {token.Star, "*"}, {token.Slash, "/"},
		{token.Amp, "&"}, {token.Pipe, "|"}, {token.Caret, "^"},
		{token.Tilde, "~"}, {token.Bang, "!"},
		{token.Hash, "#"}, {token.LParen, "("}, {token.RParen, ")"}, {token.Comma, ","},
	})
}

func TestCharAndString(t *testing.T) {
	assertTokens(t, `'A' '\n' '\x41' "hi\t\"x\"\x2A"`, []want{
		{token.Char, "A"}, {token.Char, "\n"}, {token.Char, "A"},
		{token.String, "hi\t\"x\"*"},
	})
}

func TestNewlineAndComments(t *testing.T) {
	assertTokens(t, "LDA  ; load\n  RTS\n", []want{
		{token.Ident, "LDA"}, {token.Newline, ""},
		{token.Ident, "RTS"}, {token.Newline, ""},
	})
}

func TestInstructionLine(t *testing.T) {
	assertTokens(t, "loop:\tLDA msg,X\n", []want{
		{token.Ident, "loop"}, {token.Colon, ":"}, {token.Ident, "LDA"},
		{token.Ident, "msg"}, {token.Comma, ","}, {token.Ident, "X"},
		{token.Newline, ""},
	})
}

func TestErrorRecovery(t *testing.T) {
	toks, diags := scan(t, "LDA `\nRTS\n")
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %v", len(diags), diags)
	}
	kinds := []token.Kind{
		token.Ident, token.Illegal, token.Newline,
		token.Ident, token.Newline, token.EOF,
	}
	if len(toks) != len(kinds) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(kinds), toks)
	}
	for i, k := range kinds {
		if toks[i].Kind != k {
			t.Errorf("tok[%d] = %s, want %s", i, toks[i].Kind, k)
		}
	}
}

func TestBadLiterals(t *testing.T) {
	for _, src := range []string{
		`"unterminated`, `'`, `''`, `'\q'`, `"\xZZ"`, `$`, `%`, `0x`,
	} {
		_, diags := scan(t, src)
		if len(diags) == 0 {
			t.Errorf("%q: expected a diagnostic, got none", src)
		}
	}
}

func TestDiagnosticPosition(t *testing.T) {
	_, diags := scan(t, "RTS\nLDA `")
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %v", diags)
	}
	p := diags[0].Pos.Resolve()
	if p.Line != 2 || p.Col != 5 {
		t.Errorf("illegal token at %d:%d, want 2:5", p.Line, p.Col)
	}
}
