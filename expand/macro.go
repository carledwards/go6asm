package expand

import (
	"strconv"
	"strings"

	"github.com/carledwards/go6asm/token"
)

// maxFrames caps replay-frame nesting so a runaway recursive macro errors
// instead of exhausting memory. Not a contract; a safety net.
const maxFrames = 256

type macroDef struct {
	params []string
	body   []token.Token
}

// defineMacro implements `.macro name [p1, p2, …] … .endmacro`
// (DESIGN §16 Phase 2). The body is captured raw; parameter and
// .paramcount substitution happens at each invocation.
func (e *Expander) defineMacro(dir token.Token, args []token.Token) {
	if len(args) == 0 || args[0].Kind != token.Ident {
		e.errf(dir.Pos, ".macro requires a name")
		e.captureMacroBody(dir.Pos) // consume to keep the stream sane
		return
	}
	name := args[0].Lit

	var params []string
	for _, t := range args[1:] {
		switch t.Kind {
		case token.Comma:
			// separator
		case token.Ident:
			params = append(params, t.Lit)
		default:
			e.errf(dir.Pos, ".macro parameters must be identifiers")
		}
	}

	body, ok := e.captureMacroBody(dir.Pos)
	if !ok {
		return
	}
	if e.emitting() {
		e.macros[name] = &macroDef{params: params, body: body}
	}
}

func (e *Expander) captureMacroBody(pos token.Pos) ([]token.Token, bool) {
	var body []token.Token
	depth := 0
	for {
		t := e.read()
		if t.Kind == token.EOF {
			e.errf(pos, "unterminated .macro (missing .endmacro)")
			return nil, false
		}
		if t.Kind == token.DotIdent {
			switch strings.ToLower(t.Lit) {
			case ".macro":
				depth++
			case ".endmacro", ".endmac":
				if depth == 0 {
					for x := e.read(); x.Kind != token.Newline && x.Kind != token.EOF; x = e.read() {
					}
					return body, true
				}
				depth--
			}
		}
		body = append(body, t)
	}
}

// expandMacro consumes the invocation's argument line and replays the
// macro body with parameters and .paramcount substituted.
func (e *Expander) expandMacro(nameTok token.Token) {
	m := e.macros[nameTok.Lit]

	// Raw argument tokens up to end of line.
	var argToks []token.Token
	for {
		t := e.read()
		if t.Kind == token.Newline || t.Kind == token.EOF {
			break
		}
		argToks = append(argToks, t)
	}
	groups := splitMacroArgs(argToks)

	if len(m.params) > 0 && len(groups) > len(m.params) {
		e.errf(nameTok.Pos, "too many arguments to macro "+nameTok.Lit)
		return
	}
	if len(m.params) == 0 && len(groups) > 0 {
		e.errf(nameTok.Pos, "macro "+nameTok.Lit+" takes no arguments")
		return
	}
	if len(e.in) > maxFrames || e.budget <= 0 {
		if !e.budErr {
			e.errf(nameTok.Pos, "macro expansion too deep (recursive macro?)")
			e.budErr = true
		}
		return
	}
	e.budget--

	idx := make(map[string]int, len(m.params))
	for i, p := range m.params {
		idx[p] = i
	}

	exp := make([]token.Token, 0, len(m.body)+1)
	for _, t := range m.body {
		switch {
		case t.Kind == token.Ident:
			if i, ok := idx[t.Lit]; ok {
				if i < len(groups) {
					exp = append(exp, groups[i]...)
				}
				continue // missing arg → empty
			}
			exp = append(exp, t)
		case t.Kind == token.DotIdent && strings.EqualFold(t.Lit, ".paramcount"):
			exp = append(exp, token.Token{
				Kind: token.Int, Lit: strconv.Itoa(len(groups)), Pos: t.Pos,
			})
		default:
			exp = append(exp, t)
		}
	}
	// Terminate the synthetic line so the invocation behaves like one.
	exp = append(exp, token.Token{Kind: token.Newline, Pos: nameTok.Pos})
	e.in = append(e.in, &inFrame{toks: exp})
}

// splitMacroArgs splits a token list into comma-separated groups at paren
// depth 0. An empty source yields zero groups; empty groups (e.g. a, ,c)
// are preserved so positional params line up.
func splitMacroArgs(toks []token.Token) [][]token.Token {
	if len(toks) == 0 {
		return nil
	}
	var groups [][]token.Token
	cur := []token.Token{}
	depth := 0
	for _, t := range toks {
		switch t.Kind {
		case token.LParen:
			depth++
		case token.RParen:
			if depth > 0 {
				depth--
			}
		case token.Comma:
			if depth == 0 {
				groups = append(groups, cur)
				cur = []token.Token{}
				continue
			}
		}
		cur = append(cur, t)
	}
	return append(groups, cur)
}
