package expand

import "testing"

func TestMacroNoParams(t *testing.T) {
	got, d := idents(t, ".macro M\nNOP\nNOP\n.endmacro\nM\nM\n", Options{})
	if len(d) != 0 || !eq(got, []string{"NOP", "NOP", "NOP", "NOP"}) {
		t.Errorf("got %v diags %v, want 4x NOP", got, d)
	}
}

func TestMacroParams(t *testing.T) {
	src := ".macro LD v\nLDA #v\n.endmacro\nLD 5\nLD 7\n"
	if got := ints(t, src, Options{}); !eq(got, []string{"5", "7"}) {
		t.Errorf("arg substitution: got %v, want [5 7]", got)
	}
	got, _ := idents(t, src, Options{})
	if !eq(got, []string{"LDA", "LDA"}) {
		t.Errorf("got %v, want [LDA LDA]", got)
	}
}

func TestParamCount(t *testing.T) {
	src := ".macro C a, b\n.byte .paramcount\n.endmacro\nC 9\nC 9, 9\n"
	if got := ints(t, src, Options{}); !eq(got, []string{"1", "2"}) {
		t.Errorf(".paramcount: got %v, want [1 2]", got)
	}
}

func TestMacroAfterLabel(t *testing.T) {
	got, _ := idents(t, ".macro M\nNOP\n.endmacro\nstart: M\n", Options{})
	if !eq(got, []string{"start", "NOP"}) {
		t.Errorf("got %v, want [start NOP]", got)
	}
}

func TestMacroNameAsLabelNotExpanded(t *testing.T) {
	got, _ := idents(t, ".macro M\nNOP\n.endmacro\nM:\nRTS\n", Options{})
	if !eq(got, []string{"M", "RTS"}) {
		t.Errorf("got %v, want [M RTS] (M: is a label, not an invocation)", got)
	}
}

func TestMacroArgErrors(t *testing.T) {
	for _, src := range []string{
		".macro Z\nNOP\n.endmacro\nZ 1\n",         // takes no args
		".macro Q a\nNOP\n.endmacro\nQ 1, 2, 3\n", // too many args
		".macro U\nNOP\n",                         // unterminated
	} {
		_, d := idents(t, src, Options{})
		if len(d) == 0 {
			t.Errorf("%q: expected a diagnostic", src)
		}
	}
}

func TestMacroNestedExpansion(t *testing.T) {
	// A macro whose body invokes another macro.
	src := ".macro INNER\nNOP\n.endmacro\n.macro OUTER\nINNER\nINNER\n.endmacro\nOUTER\n"
	got, d := idents(t, src, Options{})
	if len(d) != 0 || !eq(got, []string{"NOP", "NOP"}) {
		t.Errorf("got %v diags %v, want [NOP NOP]", got, d)
	}
}

func TestMacroRecursionTerminates(t *testing.T) {
	// Must terminate (budget guard) and report, not hang.
	_, d := idents(t, ".macro R\nR\n.endmacro\nR\n", Options{})
	if len(d) == 0 {
		t.Error("expected a recursion/budget diagnostic")
	}
}
