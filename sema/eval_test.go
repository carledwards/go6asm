package sema

import (
	"testing"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/expr"
	"github.com/carledwards/go6asm/parser"
	"github.com/carledwards/go6asm/token"
)

// stub is a fixed Resolver for evaluator tests.
type stub struct {
	g, c map[string]expr.Value
	pc   expr.Value
	un   map[string]expr.Value // key "+1","-2", etc.
}

func (s stub) Lookup(ref SymRef) (expr.Value, bool) {
	if ref.Cheap {
		v, ok := s.c[ref.Name]
		return v, ok
	}
	if v, ok := s.g[ref.Name]; ok {
		return v, ok
	}
	v, ok := s.c[ref.Name]
	return v, ok
}
func (s stub) CurrentPC() expr.Value { return s.pc }
func (s stub) UnnamedRef(fwd bool, c int) (expr.Value, bool) {
	sign := "-"
	if fwd {
		sign = "+"
	}
	v, ok := s.un[sign+string(rune('0'+c))]
	return v, ok
}

// evalExpr parses "R = <src>" and evaluates the right-hand side.
func evalExpr(t *testing.T, src string, r Resolver) (expr.Value, bool, int) {
	t.Helper()
	fs := token.NewFileSet()
	f, d := parser.Parse(fs.AddFile("t.s", []byte("R = "+src+"\n")))
	if len(d) != 0 {
		t.Fatalf("parse %q failed: %v", src, d)
	}
	e := f.Stmts[0].(*ast.Assign).Value
	v, ok, diags := Eval(e, r)
	return v, ok, len(diags)
}

func TestParseIntLit(t *testing.T) {
	cases := []struct {
		in  string
		out expr.Value
		ok  bool
	}{
		{"$1A", 26, true},
		{"0xFF", 255, true},
		{"%1010", 10, true},
		{"0b0101", 5, true},
		{"42", 42, true},
		{"0", 0, true},
		{"$FFFFFFFF", -1, true}, // wraps to signed 32-bit
		{"$1FFFFFFFF", 0, false},
		{"$GG", 0, false},
	}
	for _, c := range cases {
		v, _, ok := ParseIntLit(c.in)
		if ok != c.ok || (ok && v != c.out) {
			t.Errorf("ParseIntLit(%q) = (%v,%v), want (%v,%v)",
				c.in, int32(v), ok, int32(c.out), c.ok)
		}
	}
}

func TestEvalArithmeticAndPrecedence(t *testing.T) {
	r := stub{}
	cases := []struct {
		src string
		out expr.Value
	}{
		{"1 + 2 * 3", 7},
		{"(1 + 2) * 3", 9},
		{"7 .mod 3", 1},
		{"1 << 4", 16},
		{"$1234 & $00FF", 0x34},
		{"<$1234", 0x34},   // low byte
		{">$1234", 0x12},   // high byte
		{"^$123456", 0x12}, // bank byte
		{"-5", -5},
		{"~0", -1},
		{"!0", 1},
		{"3 = 3", 1},
		{"3 <> 3", 0},
		{"1 && 0", 0},
		{"0 || 5", 1},
		{"1 .xor 1", 0},
		{"1 .xor 0", 1},
		{"'A' + 1", 66},
	}
	for _, c := range cases {
		v, ok, nd := evalExpr(t, c.src, r)
		if !ok || nd != 0 || v != c.out {
			t.Errorf("%q = (%v, ok=%v, diags=%d), want %v",
				c.src, int32(v), ok, nd, int32(c.out))
		}
	}
}

func TestEvalSymbols(t *testing.T) {
	r := stub{
		g:  map[string]expr.Value{"base": 0x0600},
		c:  map[string]expr.Value{"loop": 0x0610},
		pc: 0x0620,
		un: map[string]expr.Value{"+1": 0x0630, "-1": 0x0600},
	}
	for src, want := range map[string]expr.Value{
		"base + 2": 0x0602,
		"loop":     0x0610,
		"*":        0x0620,
		"* - base": 0x20,
		":+":       0x0630,
		":-":       0x0600,
	} {
		v, ok, nd := evalExpr(t, src, r)
		if !ok || nd != 0 || v != want {
			t.Errorf("%q = (%v, ok=%v, diags=%d), want %v",
				src, int32(v), ok, nd, int32(want))
		}
	}
}

func TestEvalErrors(t *testing.T) {
	r := stub{}
	for _, src := range []string{
		"10 / 0",
		"10 .mod 0",
		"missing",
		`"a string"`,
		":+", // no unnamed labels defined
	} {
		_, ok, nd := evalExpr(t, src, r)
		if ok || nd == 0 {
			t.Errorf("%q: expected an evaluation error", src)
		}
	}
}

func TestEvalDiagnosticPosition(t *testing.T) {
	fs := token.NewFileSet()
	f, _ := parser.Parse(fs.AddFile("t.s", []byte("R = 1 + bad\n")))
	e := f.Stmts[0].(*ast.Assign).Value
	_, ok, diags := Eval(e, stub{})
	if ok || len(diags) != 1 {
		t.Fatalf("want 1 error, got ok=%v diags=%v", ok, diags)
	}
	p := diags[0].Pos.Resolve()
	if p.Line != 1 || p.Col != 9 { // 'bad' starts at column 9
		t.Errorf("error at %d:%d, want 1:9", p.Line, p.Col)
	}
}
