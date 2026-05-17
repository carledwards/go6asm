package ast

import (
	"testing"

	"github.com/carledwards/go6asm/token"
)

// Compile-time proof that every node implements the right interfaces.
var (
	_ Stmt = (*LabelDecl)(nil)
	_ Stmt = (*Assign)(nil)
	_ Stmt = (*Instruction)(nil)
	_ Stmt = (*Directive)(nil)

	_ Expr = (*IntLit)(nil)
	_ Expr = (*CharLit)(nil)
	_ Expr = (*StrLit)(nil)
	_ Expr = (*Ident)(nil)
	_ Expr = (*CurPC)(nil)
	_ Expr = (*UnnamedRef)(nil)
	_ Expr = (*Unary)(nil)
	_ Expr = (*Binary)(nil)
	_ Expr = (*Paren)(nil)

	_ Node = (*File)(nil)
)

func TestPosThreading(t *testing.T) {
	fs := token.NewFileSet()
	f := fs.AddFile("t.s", []byte("LDA #1\n"))
	at := func(off int) token.Pos { return token.Pos{File: f, Offset: off} }

	// LDA #(2 + label)  — Binary.Pos must come from its left operand.
	bin := &Binary{
		Op:    token.Plus,
		OpPos: at(7),
		X:     &IntLit{Lit: "2", LitPos: at(5)},
		Y:     &Ident{Name: "label", NamePos: at(9)},
	}
	if got := bin.Pos(); got != at(5) {
		t.Errorf("Binary.Pos = %v, want left operand pos %v", got, at(5))
	}

	ins := &Instruction{
		Mnemonic:    "LDA",
		MnemonicPos: at(0),
		Operand:     Operand{Form: ImmOperand, Expr: bin},
	}
	file := &File{Name: "t.s", Stmts: []Stmt{ins}}
	if file.Pos() != at(0) {
		t.Errorf("File.Pos = %v, want first stmt pos %v", file.Pos(), at(0))
	}

	if (&File{}).Pos() != token.NoPos {
		t.Error("empty File must have NoPos")
	}
}
