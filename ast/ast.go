// Package ast defines the syntax tree for the v1 ca65 subset, modeled on
// the standard library's go/ast: small node structs, every node carries a
// token.Pos for diagnostics. The parser builds it; sema and codegen walk
// it. Leaf package (depends only on token). wasm-clean (DESIGN §4, §7).
//
// The tree records *syntax*, not resolved meaning. Addressing-mode and
// zero-page/absolute selection, expression values, and symbol bindings
// are decided later (sema/codegen) against isa and expr.
package ast

import "github.com/carledwards/go6asm/token"

// Node is any syntax-tree node.
type Node interface {
	Pos() token.Pos
}

// File is a whole source file: a sequence of statements in source order.
// One ca65 statement per line (DESIGN §6.1); blank/comment-only lines
// produce no statement.
type File struct {
	Name  string
	Stmts []Stmt
}

func (f *File) Pos() token.Pos {
	if len(f.Stmts) == 0 {
		return token.NoPos
	}
	return f.Stmts[0].Pos()
}

// ---- Statements -----------------------------------------------------------

// Stmt is a single assembler statement.
type Stmt interface {
	Node
	stmtNode()
}

// LabelKind distinguishes the three v1 label forms (DESIGN §6.2).
type LabelKind int

const (
	GlobalLabel  LabelKind = iota // name:
	CheapLabel                    // @name:
	UnnamedLabel                  // :
)

// LabelDecl declares a label at the current PC.
type LabelDecl struct {
	Kind    LabelKind
	Name    string // "" for UnnamedLabel
	NamePos token.Pos
}

// Assign is a constant definition: `name = expr` (DESIGN §6.2).
type Assign struct {
	Name    string
	NamePos token.Pos
	Value   Expr
}

// Index is the optional `,X` / `,Y` index on a memory operand.
type Index int

const (
	NoIndex Index = iota
	IndexX
	IndexY
)

// OperandForm is the syntactic shape of an instruction operand. The
// concrete isa.Mode (and zp-vs-abs sizing) is chosen later from this plus
// the operand value and any Force prefix.
type OperandForm int

const (
	NoOperand   OperandForm = iota // implied:           RTS
	AccOperand                     // accumulator:       ASL A
	ImmOperand                     // immediate:         LDA #expr
	MemOperand                     // expr [,X|,Y]:      LDA $1000,X
	IndOperand                     // (expr):            JMP ($1000)
	IndXOperand                    // (expr,X):          LDA ($10,X)
	IndYOperand                    // (expr),Y:          LDA ($10),Y
)

// Force is an explicit ca65 zero-page / absolute size prefix: z: / a:.
type Force int

const (
	NoForce Force = iota
	ForceZP
	ForceAbs
)

// Operand is an instruction's operand. Expr is nil for No/Acc forms.
type Operand struct {
	Form  OperandForm
	Expr  Expr
	Index Index
	Force Force
}

// Instruction is a mnemonic plus its operand. Mnemonic is kept verbatim
// from source (case-insensitive resolution happens in sema against isa).
type Instruction struct {
	Mnemonic    string
	MnemonicPos token.Pos
	Operand     Operand
}

// Directive is a dot-directive and its argument list, e.g.
// `.byte 1, 2, msg` or `.org $1000`. The v1 directive set is validated in
// sema (DESIGN §6.4); the parser only shapes the arguments.
type Directive struct {
	Name    string // includes the leading dot, e.g. ".byte"
	NamePos token.Pos
	Args    []Expr // string args appear here as *StrLit nodes
}

func (s *LabelDecl) Pos() token.Pos   { return s.NamePos }
func (s *Assign) Pos() token.Pos      { return s.NamePos }
func (s *Instruction) Pos() token.Pos { return s.MnemonicPos }
func (s *Directive) Pos() token.Pos   { return s.NamePos }

func (*LabelDecl) stmtNode()   {}
func (*Assign) stmtNode()      {}
func (*Instruction) stmtNode() {}
func (*Directive) stmtNode()   {}

// ---- Expressions ----------------------------------------------------------

// Expr is an expression node. Values are computed later by expr (DESIGN
// §6.4 "Expression value model"); the tree keeps numeric literals as their
// raw source text so the evaluator owns base/overflow semantics.
type Expr interface {
	Node
	exprNode()
}

// IntLit is a numeric literal; Lit is the raw source ($1A, %1010, 42, …).
type IntLit struct {
	Lit    string
	LitPos token.Pos
}

// CharLit is a 'c' literal already decoded by the scanner to one byte.
type CharLit struct {
	Val    byte
	ValPos token.Pos
}

// StrLit is a decoded double-quoted string. Only valid in the directive
// contexts that accept one (.asciiz, .include, .incbin); using it in an
// arithmetic context is a sema error.
type StrLit struct {
	Val    string
	ValPos token.Pos
}

// Ident references a symbol. Global is true for the `::name` global-scope
// operator (DESIGN §7.3).
// Ident references a symbol. Global is the leading `::` (start from the
// global scope). Scopes is the qualifier path of `A::B::name` (here
// ["A","B"]). Cheap is an `@name` cheap-local reference (DESIGN §6.2).
type Ident struct {
	Name    string
	Scopes  []string
	Global  bool
	Cheap   bool
	NamePos token.Pos
}

// CurPC is the `*` pseudovariable: the current program counter.
type CurPC struct {
	StarPos token.Pos
}

// UnnamedRef is a `:+`, `:-`, `:++`, `:--` reference (DESIGN §6.2). Count
// is the number of sign characters; Forward distinguishes `+` from `-`.
type UnnamedRef struct {
	Forward bool
	Count   int
	RefPos  token.Pos
}

// Unary is a prefix operation: + - ~ ! < (low) > (high) ^ (bank).
type Unary struct {
	Op    token.Kind
	OpPos token.Pos
	X     Expr
}

// Binary is an infix operation; precedence is resolved by the parser.
type Binary struct {
	Op    token.Kind
	OpPos token.Pos
	X, Y  Expr
}

// Paren is a parenthesized expression, preserved so codegen can tell
// `(expr)` indirect addressing from grouping.
type Paren struct {
	LParen token.Pos
	X      Expr
}

func (e *IntLit) Pos() token.Pos     { return e.LitPos }
func (e *CharLit) Pos() token.Pos    { return e.ValPos }
func (e *StrLit) Pos() token.Pos     { return e.ValPos }
func (e *Ident) Pos() token.Pos      { return e.NamePos }
func (e *CurPC) Pos() token.Pos      { return e.StarPos }
func (e *UnnamedRef) Pos() token.Pos { return e.RefPos }
func (e *Unary) Pos() token.Pos      { return e.OpPos }
func (e *Binary) Pos() token.Pos     { return e.X.Pos() }
func (e *Paren) Pos() token.Pos      { return e.LParen }

func (*IntLit) exprNode()     {}
func (*CharLit) exprNode()    {}
func (*StrLit) exprNode()     {}
func (*Ident) exprNode()      {}
func (*CurPC) exprNode()      {}
func (*UnnamedRef) exprNode() {}
func (*Unary) exprNode()      {}
func (*Binary) exprNode()     {}
func (*Paren) exprNode()      {}
