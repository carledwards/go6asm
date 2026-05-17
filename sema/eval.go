// Package sema is semantic analysis: numeric-literal decoding and
// expression evaluation now; the two-pass symbol/layout driver lands with
// codegen (DESIGN §7.3).
//
// This file is the evaluator — the reusable core that both passes and
// codegen call. It turns an ast.Expr into an expr.Value (the 32-bit
// ca65-parity model, DESIGN §6.4) against a Resolver that supplies symbol
// and program-counter facts. It is decoupled from how the symbol table is
// built and scoped, so it is independently testable. wasm-clean (§4).
package sema

import (
	"strconv"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/expr"
	"github.com/carledwards/go6asm/token"
)

// Resolver supplies the facts an expression needs that are not in the
// expression itself. The layout driver provides a concrete implementation
// bound to the reference's position (current cheap-label region, current
// PC, position within the unnamed-label list).
// SymRef is a fully-described symbol reference from the AST: Name plus an
// optional `::`-qualifier path, the leading-`::` global flag, and the
// `@`-cheap flag (DESIGN §6.2, §7.3).
type SymRef struct {
	Name   string
	Scopes []string
	Global bool
	Cheap  bool
}

type Resolver interface {
	// Lookup resolves a (possibly scoped/cheap/global) symbol reference
	// against the scope active at the reference's position.
	Lookup(ref SymRef) (expr.Value, bool)
	// UnnamedRef resolves :+ / :- (forward=true for '+'); count is the
	// number of sign characters (1 = nearest).
	UnnamedRef(forward bool, count int) (expr.Value, bool)
	// CurrentPC is the value of `*`.
	CurrentPC() expr.Value
}

// ParseIntLit decodes a numeric literal's raw source text (as produced by
// the scanner) into the 32-bit ca65-parity value. ok is false (with a
// human message) on a malformed or out-of-range literal.
func ParseIntLit(lit string) (val expr.Value, msg string, ok bool) {
	base, digits := 10, lit
	switch {
	case len(lit) >= 1 && lit[0] == '$':
		base, digits = 16, lit[1:]
	case len(lit) >= 1 && lit[0] == '%':
		base, digits = 2, lit[1:]
	case len(lit) >= 2 && lit[0] == '0' && (lit[1] == 'x' || lit[1] == 'X'):
		base, digits = 16, lit[2:]
	case len(lit) >= 2 && lit[0] == '0' && (lit[1] == 'b' || lit[1] == 'B'):
		base, digits = 2, lit[2:]
	}
	if digits == "" {
		return 0, "empty numeric literal", false
	}
	n, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, "invalid numeric literal " + strconv.Quote(lit), false
	}
	if n > 0xFFFFFFFF {
		return 0, "numeric literal exceeds 32-bit range: " + lit, false
	}
	return expr.Value(int32(uint32(n))), "", true
}

type evaluator struct {
	r     Resolver
	diags []diag.Diagnostic
}

func (ev *evaluator) errf(pos token.Pos, msg string) {
	ev.diags = append(ev.diags, diag.Diagnostic{
		Severity: diag.SeverityError, Pos: pos, Message: msg,
	})
}

// Eval evaluates e against r. ok is false (and diags non-empty) if the
// expression could not be fully resolved; the returned value is 0 then.
func Eval(e ast.Expr, r Resolver) (val expr.Value, ok bool, diags []diag.Diagnostic) {
	ev := &evaluator{r: r}
	v := ev.eval(e)
	return v, len(ev.diags) == 0, ev.diags
}

func (ev *evaluator) eval(e ast.Expr) expr.Value {
	switch n := e.(type) {
	case *ast.IntLit:
		v, msg, ok := ParseIntLit(n.Lit)
		if !ok {
			ev.errf(n.LitPos, msg)
			return 0
		}
		return v

	case *ast.CharLit:
		return expr.Value(n.Val)

	case *ast.StrLit:
		ev.errf(n.ValPos, "string literal not allowed in an expression")
		return 0

	case *ast.Ident:
		v, ok := ev.r.Lookup(SymRef{
			Name:   n.Name,
			Scopes: n.Scopes,
			Global: n.Global,
			Cheap:  n.Cheap,
		})
		if !ok {
			ev.errf(n.NamePos, "undefined symbol "+strconv.Quote(n.Name))
			return 0
		}
		return v

	case *ast.CurPC:
		return ev.r.CurrentPC()

	case *ast.UnnamedRef:
		v, ok := ev.r.UnnamedRef(n.Forward, n.Count)
		if !ok {
			ev.errf(n.RefPos, "no matching unnamed label")
			return 0
		}
		return v

	case *ast.Paren:
		return ev.eval(n.X)

	case *ast.Unary:
		return ev.evalUnary(n)

	case *ast.Binary:
		return ev.evalBinary(n)
	}
	return 0
}

func (ev *evaluator) evalUnary(n *ast.Unary) expr.Value {
	x := ev.eval(n.X)
	switch n.Op {
	case token.Plus:
		return x
	case token.Minus:
		return x.Neg()
	case token.Tilde:
		return x.Cpl()
	case token.Bang:
		return x.LogNot()
	case token.Lt: // < low byte
		return x.LoByte()
	case token.Gt: // > high byte
		return x.HiByte()
	case token.Caret: // ^ bank byte (0 for 6502)
		return x.BankByte()
	}
	ev.errf(n.OpPos, "unsupported unary operator "+n.Op.String())
	return 0
}

func (ev *evaluator) evalBinary(n *ast.Binary) expr.Value {
	a, b := ev.eval(n.X), ev.eval(n.Y)
	switch n.Op {
	case token.Plus:
		return a.Add(b)
	case token.Minus:
		return a.Sub(b)
	case token.Star:
		return a.Mul(b)
	case token.Slash:
		v, ok := a.Div(b)
		if !ok {
			ev.errf(n.OpPos, "division by zero")
		}
		return v
	case token.Mod:
		v, ok := a.Mod(b)
		if !ok {
			ev.errf(n.OpPos, "modulo by zero")
		}
		return v
	case token.Amp:
		return a.And(b)
	case token.Pipe:
		return a.Or(b)
	case token.Caret:
		return a.Xor(b)
	case token.Shl:
		return a.Shl(b)
	case token.Shr:
		return a.Shr(b)
	case token.Eq:
		return a.Eq(b)
	case token.NotEq:
		return a.Ne(b)
	case token.Lt:
		return a.Lt(b)
	case token.Gt:
		return a.Gt(b)
	case token.Le:
		return a.Le(b)
	case token.Ge:
		return a.Ge(b)
	case token.AndAnd:
		return a.LogAnd(b)
	case token.OrOr:
		return a.LogOr(b)
	case token.BoolXor:
		if a.IsTrue() != b.IsTrue() {
			return 1
		}
		return 0
	}
	ev.errf(n.OpPos, "unsupported binary operator "+n.Op.String())
	return 0
}
