package parser

import (
	"testing"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/token"
)

func parse(t *testing.T, src string) (*ast.File, []diag.Diagnostic) {
	t.Helper()
	fs := token.NewFileSet()
	return Parse(fs.AddFile("t.s", []byte(src)))
}

func parseOK(t *testing.T, src string) []ast.Stmt {
	t.Helper()
	f, d := parse(t, src)
	if len(d) != 0 {
		t.Fatalf("%q: unexpected diagnostics: %v", src, d)
	}
	return f.Stmts
}

func TestInstructionForms(t *testing.T) {
	stmts := parseOK(t, "RTS\nASL A\nLDA #$10\nLDA z:$10\nSTA a:$1000,X\n")
	want := []struct {
		mn    string
		form  ast.OperandForm
		force ast.Force
		index ast.Index
	}{
		{"RTS", ast.NoOperand, ast.NoForce, ast.NoIndex},
		{"ASL", ast.AccOperand, ast.NoForce, ast.NoIndex},
		{"LDA", ast.ImmOperand, ast.NoForce, ast.NoIndex},
		{"LDA", ast.MemOperand, ast.ForceZP, ast.NoIndex},
		{"STA", ast.MemOperand, ast.ForceAbs, ast.IndexX},
	}
	if len(stmts) != len(want) {
		t.Fatalf("got %d stmts, want %d", len(stmts), len(want))
	}
	for i, w := range want {
		ins, ok := stmts[i].(*ast.Instruction)
		if !ok {
			t.Fatalf("stmt %d not Instruction: %T", i, stmts[i])
		}
		if ins.Mnemonic != w.mn || ins.Operand.Form != w.form ||
			ins.Operand.Force != w.force || ins.Operand.Index != w.index {
			t.Errorf("stmt %d = %+v, want %+v", i, ins.Operand, w)
		}
	}
}

func TestIndirectForms(t *testing.T) {
	stmts := parseOK(t, "JMP ($1000)\nLDA ($10,X)\nLDA ($10),Y\n")
	forms := []ast.OperandForm{ast.IndOperand, ast.IndXOperand, ast.IndYOperand}
	for i, f := range forms {
		ins := stmts[i].(*ast.Instruction)
		if ins.Operand.Form != f {
			t.Errorf("stmt %d form = %v, want %v", i, ins.Operand.Form, f)
		}
	}
}

func TestLabelsSameLine(t *testing.T) {
	stmts := parseOK(t, "loop: @inner: LDA msg,X\n:\nBNE loop\n")
	if len(stmts) != 5 {
		t.Fatalf("got %d stmts: %v", len(stmts), stmts)
	}
	l0 := stmts[0].(*ast.LabelDecl)
	if l0.Kind != ast.GlobalLabel || l0.Name != "loop" {
		t.Errorf("stmt0 = %+v", l0)
	}
	if stmts[1].(*ast.LabelDecl).Kind != ast.CheapLabel {
		t.Errorf("stmt1 not cheap label")
	}
	if _, ok := stmts[2].(*ast.Instruction); !ok {
		t.Errorf("stmt2 not instruction")
	}
	if stmts[3].(*ast.LabelDecl).Kind != ast.UnnamedLabel {
		t.Errorf("stmt3 not unnamed label")
	}
}

func TestAssignAndPrecedence(t *testing.T) {
	// 1 + 2 * 3  -> +(1, *(2,3))   (multiplicative binds tighter)
	stmts := parseOK(t, "FOO = 1 + 2 * 3\n")
	a := stmts[0].(*ast.Assign)
	if a.Name != "FOO" {
		t.Fatalf("name = %q", a.Name)
	}
	add := a.Value.(*ast.Binary)
	if add.Op != token.Plus {
		t.Fatalf("top op = %v, want +", add.Op)
	}
	if _, ok := add.X.(*ast.IntLit); !ok {
		t.Errorf("add.X not IntLit: %T", add.X)
	}
	mul := add.Y.(*ast.Binary)
	if mul.Op != token.Star {
		t.Errorf("nested op = %v, want *", mul.Op)
	}
}

func TestWordOperatorAndUnary(t *testing.T) {
	// .mod is a word-only operator; < is the low-byte unary.
	stmts := parseOK(t, "A = 7 .mod 3\nB = <$1234\n")
	if op := stmts[0].(*ast.Assign).Value.(*ast.Binary).Op; op != token.Mod {
		t.Errorf("op = %v, want .mod", op)
	}
	u := stmts[1].(*ast.Assign).Value.(*ast.Unary)
	if u.Op != token.Lt {
		t.Errorf("unary op = %v, want < (low byte)", u.Op)
	}
}

func TestDirectives(t *testing.T) {
	stmts := parseOK(t, ".byte 1, 2, 'A'\n.asciiz \"HI\"\n.org $E000\n")
	d0 := stmts[0].(*ast.Directive)
	if d0.Name != ".byte" || len(d0.Args) != 3 {
		t.Errorf("d0 = %+v", d0)
	}
	if _, ok := d0.Args[2].(*ast.CharLit); !ok {
		t.Errorf("d0 arg2 not CharLit: %T", d0.Args[2])
	}
	d1 := stmts[1].(*ast.Directive)
	if d1.Name != ".asciiz" {
		t.Fatalf("d1 name %q", d1.Name)
	}
	if s, ok := d1.Args[0].(*ast.StrLit); !ok || s.Val != "HI" {
		t.Errorf("d1 arg not StrLit \"HI\": %#v", d1.Args[0])
	}
}

func TestErrorRecovery(t *testing.T) {
	// Two broken lines, two good ones; all four errors collected, good
	// lines still produce statements, in source order.
	f, d := parse(t, "LDA #\ngood1: RTS\n.byte )\ngood2: NOP\n")
	if len(d) < 2 {
		t.Fatalf("want >=2 diagnostics, got %d: %v", len(d), d)
	}
	for i := 1; i < len(d); i++ {
		if d[i].Pos.Offset < d[i-1].Pos.Offset {
			t.Errorf("diagnostics not in source order: %v", d)
		}
	}
	var labels []string
	for _, s := range f.Stmts {
		if l, ok := s.(*ast.LabelDecl); ok {
			labels = append(labels, l.Name)
		}
	}
	if len(labels) != 2 || labels[0] != "good1" || labels[1] != "good2" {
		t.Errorf("recovered labels = %v, want [good1 good2]", labels)
	}
}
