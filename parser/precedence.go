package parser

import "github.com/carledwards/go6asm/token"

// ca65 binary-operator precedence (DESIGN §6.4: "Precedence matches ca65
// exactly"). Higher binds tighter; 0 means "not a binary operator".
//
// Levels, highest → lowest, mirroring ca65's expr.c parse chain:
//
//	5  * / .mod  & .bitand  ^ .bitxor  << .shl  >> .shr
//	4  + -  | .bitor
//	3  = <>  < > <= >=
//	2  && .and
//	1  || .or .xor
//
// ca65 also accepts word forms (.MOD, .BITAND, …) alongside the symbols;
// symbols are canonical in listings. The exact placement of .xor and the
// shift sign behavior are pinned by the ca65 differential suite (§6.5),
// which is runnable here since ca65 is installed locally.
const (
	precNone = iota
	precBoolOr
	precBoolAnd
	precCompare
	precAdditive
	precMultiplicative
)

// binOp resolves a token (symbolic or a .word-operator DotIdent) to the
// ast operator kind and its precedence. ok is false if tok cannot begin a
// binary operator.
func binOp(tok token.Token) (op token.Kind, prec int, ok bool) {
	switch tok.Kind {
	case token.Star, token.Slash, token.Amp, token.Caret, token.Shl, token.Shr:
		return tok.Kind, precMultiplicative, true
	case token.Plus, token.Minus, token.Pipe:
		return tok.Kind, precAdditive, true
	case token.Eq, token.NotEq, token.Lt, token.Gt, token.Le, token.Ge:
		return tok.Kind, precCompare, true
	case token.AndAnd:
		return token.AndAnd, precBoolAnd, true
	case token.OrOr:
		return token.OrOr, precBoolOr, true
	case token.DotIdent:
		if w, found := wordOps[lower(tok.Lit)]; found {
			return w.op, w.prec, true
		}
	}
	return 0, precNone, false
}

type wordOp struct {
	op   token.Kind
	prec int
}

// ca65 word-operator aliases. Each maps to the same op/precedence as its
// symbolic twin; .mod and .xor have no symbol (their dedicated kinds).
var wordOps = map[string]wordOp{
	".mod":    {token.Mod, precMultiplicative},
	".bitand": {token.Amp, precMultiplicative},
	".bitxor": {token.Caret, precMultiplicative},
	".shl":    {token.Shl, precMultiplicative},
	".shr":    {token.Shr, precMultiplicative},
	".bitor":  {token.Pipe, precAdditive},
	".and":    {token.AndAnd, precBoolAnd},
	".or":     {token.OrOr, precBoolOr},
	".xor":    {token.BoolXor, precBoolOr},
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
