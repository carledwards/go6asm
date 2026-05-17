package expand

import (
	"testing"

	"github.com/carledwards/go6asm/scanner"
	"github.com/carledwards/go6asm/token"
)

// ints returns the Int-literal lexemes the expander emits, in order — a
// proxy for loop-variable substitution.
func ints(t *testing.T, src string, opt Options) []string {
	t.Helper()
	fs := token.NewFileSet()
	ex := New(scanner.New(fs.AddFile("t.s", []byte(src))), opt)
	var out []string
	for {
		tk := ex.Next()
		if tk.Kind == token.EOF {
			break
		}
		if tk.Kind == token.Int {
			out = append(out, tk.Lit)
		}
	}
	return out
}

func TestRepeatPlain(t *testing.T) {
	got, d := idents(t, ".repeat 3\nNOP\n.endrepeat\n", Options{})
	if len(d) != 0 || !eq(got, []string{"NOP", "NOP", "NOP"}) {
		t.Errorf("got %v diags %v, want 3x NOP", got, d)
	}
}

func TestRepeatWithVar(t *testing.T) {
	got := ints(t, ".repeat 4, i\n.byte i\n.endrepeat\n", Options{})
	if !eq(got, []string{"0", "1", "2", "3"}) {
		t.Errorf("got %v, want [0 1 2 3]", got)
	}
}

func TestRepeatNested(t *testing.T) {
	// outer a in 0..1, inner b in 0..1 -> .byte a, b each iteration
	got := ints(t, ".repeat 2, a\n.repeat 2, b\n.byte a, b\n.endrepeat\n.endrepeat\n", Options{})
	want := []string{"0", "0", "0", "1", "1", "0", "1", "1"}
	if !eq(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRepeatCountFromDefine(t *testing.T) {
	got, d := idents(t, ".repeat N\nNOP\n.endrepeat\n",
		Options{Defines: map[string]int64{"N": 2}})
	if len(d) != 0 || !eq(got, []string{"NOP", "NOP"}) {
		t.Errorf("got %v diags %v, want 2x NOP", got, d)
	}
}

func TestRepeatInDeadBranchNotExpanded(t *testing.T) {
	got, d := idents(t, ".if 0\n.repeat 3\nNOP\n.endrepeat\n.endif\nRTS\n", Options{})
	if len(d) != 0 || !eq(got, []string{"RTS"}) {
		t.Errorf("got %v diags %v, want [RTS]", got, d)
	}
}

func TestRepeatUnterminated(t *testing.T) {
	_, d := idents(t, ".repeat 2\nNOP\n", Options{})
	if len(d) == 0 {
		t.Error("expected unterminated .repeat diagnostic")
	}
}
