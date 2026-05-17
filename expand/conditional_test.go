package expand

import (
	"strings"
	"testing"

	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/scanner"
	"github.com/carledwards/go6asm/token"
)

// idents returns the identifier lexemes the expander lets through, which
// is a compact proxy for "which lines survived conditional assembly".
func idents(t *testing.T, src string, opt Options) ([]string, []diag.Diagnostic) {
	t.Helper()
	fs := token.NewFileSet()
	ex := New(scanner.New(fs.AddFile("t.s", []byte(src))), opt)
	var out []string
	for {
		tk := ex.Next()
		if tk.Kind == token.EOF {
			break
		}
		if tk.Kind == token.Ident {
			out = append(out, tk.Lit)
		}
	}
	return out, ex.Diagnostics()
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIfElseEndif(t *testing.T) {
	got, d := idents(t, ".if 1\nAAA\n.else\nBBB\n.endif\nCCC\n", Options{})
	if len(d) != 0 || !eq(got, []string{"AAA", "CCC"}) {
		t.Errorf("got %v diags %v, want [AAA CCC]", got, d)
	}
	got, _ = idents(t, ".if 0\nAAA\n.else\nBBB\n.endif\n", Options{})
	if !eq(got, []string{"BBB"}) {
		t.Errorf("got %v, want [BBB]", got)
	}
}

func TestElseifChain(t *testing.T) {
	src := ".if 0\nAAA\n.elseif 1\nBBB\n.elseif 1\nCCC\n.else\nDDD\n.endif\n"
	got, _ := idents(t, src, Options{})
	if !eq(got, []string{"BBB"}) { // first matching .elseif only
		t.Errorf("got %v, want [BBB]", got)
	}
}

func TestNestedDeadBlockNotEvaluated(t *testing.T) {
	// Inner .if must stay skipped, and its bad condition must NOT error
	// because the outer block is dead (ca65 only tracks nesting there).
	src := ".if 0\n.if 1/0\nXXX\n.endif\nYYY\n.endif\nZZZ\n"
	got, d := idents(t, src, Options{})
	if len(d) != 0 {
		t.Errorf("dead block evaluated: %v", d)
	}
	if !eq(got, []string{"ZZZ"}) {
		t.Errorf("got %v, want [ZZZ]", got)
	}
}

func TestIfdefIfndef(t *testing.T) {
	opt := Options{Defines: map[string]int64{"DBG": 1}}
	got, _ := idents(t, ".ifdef DBG\nAAA\n.endif\n.ifndef DBG\nBBB\n.endif\n.ifndef OTHER\nCCC\n.endif\n", opt)
	if !eq(got, []string{"AAA", "CCC"}) {
		t.Errorf("got %v, want [AAA CCC]", got)
	}
}

func TestConditionUsesDefine(t *testing.T) {
	opt := Options{Defines: map[string]int64{"LEVEL": 2}}
	got, _ := idents(t, ".if LEVEL >= 2\nAAA\n.else\nBBB\n.endif\n", opt)
	if !eq(got, []string{"AAA"}) {
		t.Errorf("got %v, want [AAA]", got)
	}
}

func TestErrorWarningOut(t *testing.T) {
	src := ".if 0\n.error \"dead\"\n.endif\n.warning \"live\"\n.out \"note\"\n"
	_, d := idents(t, src, Options{})
	if len(d) != 2 {
		t.Fatalf("want 2 diagnostics (warning+note, not the dead error), got %v", d)
	}
	if d[0].Severity != diag.SeverityWarning || !strings.Contains(d[0].Message, "live") {
		t.Errorf("d0 = %+v", d[0])
	}
	if d[1].Severity != diag.SeverityNote {
		t.Errorf("d1 severity = %v, want note", d[1].Severity)
	}
}

func TestStructuralErrors(t *testing.T) {
	for _, src := range []string{
		".if 1\nAAA\n",                  // missing .endif
		".endif\n",                      // .endif without .if
		".else\n",                       // .else without .if
		".if 1\n.else\n.else\n.endif\n", // duplicate .else
	} {
		_, d := idents(t, src, Options{})
		if len(d) == 0 {
			t.Errorf("%q: expected a structural diagnostic", src)
		}
	}
}
