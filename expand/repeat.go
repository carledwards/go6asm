package expand

import (
	"strconv"
	"strings"

	"github.com/carledwards/go6asm/parser"
	"github.com/carledwards/go6asm/sema"
	"github.com/carledwards/go6asm/token"
)

// maxRepeat caps a single .repeat expansion so a typo can't blow up
// memory. ca65 has no limit; this is a defensive ceiling, not a contract.
const maxRepeat = 1 << 20

// handleRepeat implements `.repeat count [, var] ... .endrepeat`
// (DESIGN §16 Phase 2). The body is captured raw — nested .repeat/.if are
// re-processed when the expansion is replayed through Next().
func (e *Expander) handleRepeat(dir token.Token, args []token.Token) {
	countToks, varName, perr := splitRepeatArgs(args)
	active := e.emitting()

	body, ok := e.captureRepeatBody(dir.Pos)
	if !ok {
		return // unterminated; already reported
	}
	if !active {
		return // inside a dead branch: body consumed and discarded
	}
	if perr != "" {
		e.errf(dir.Pos, perr)
		return
	}
	n := e.evalCount(countToks, dir.Pos)
	if n < 0 {
		return
	}

	var exp []token.Token
	for i := range n {
		for _, t := range body {
			if varName != "" && t.Kind == token.Ident && t.Lit == varName {
				t = token.Token{Kind: token.Int, Lit: strconv.Itoa(i), Pos: t.Pos}
			}
			exp = append(exp, t)
		}
	}
	if len(exp) > 0 {
		e.in = append(e.in, &inFrame{toks: exp})
	}
}

// captureRepeatBody collects tokens up to the matching .endrepeat,
// counting nested .repeat so only the outermost terminator ends capture.
func (e *Expander) captureRepeatBody(pos token.Pos) ([]token.Token, bool) {
	var body []token.Token
	depth := 0
	for {
		t := e.read()
		if t.Kind == token.EOF {
			e.errf(pos, "unterminated .repeat (missing .endrepeat)")
			return nil, false
		}
		if t.Kind == token.DotIdent {
			switch strings.ToLower(t.Lit) {
			case ".repeat":
				depth++
			case ".endrepeat", ".endrep":
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

// splitRepeatArgs separates "count" from an optional ", var". The loop
// variable is a single identifier.
func splitRepeatArgs(args []token.Token) (count []token.Token, varName, errMsg string) {
	comma := -1
	for i, t := range args {
		if t.Kind == token.Comma {
			comma = i
			break
		}
	}
	if comma < 0 {
		if len(args) == 0 {
			return nil, "", ".repeat requires a count"
		}
		return args, "", ""
	}
	count = args[:comma]
	rest := args[comma+1:]
	if len(count) == 0 {
		return nil, "", ".repeat requires a count"
	}
	if len(rest) != 1 || rest[0].Kind != token.Ident {
		return nil, "", ".repeat loop variable must be a single identifier"
	}
	return count, rest[0].Lit, ""
}

func (e *Expander) evalCount(toks []token.Token, pos token.Pos) int {
	ex, ds := parser.ParseExprTokens(toks)
	e.diags = append(e.diags, ds...)
	if ex == nil || len(ds) > 0 {
		return -1
	}
	v, ok, ds2 := sema.Eval(ex, condResolver{e.opt.Defines})
	e.diags = append(e.diags, ds2...)
	if !ok {
		return -1
	}
	if v < 0 {
		e.errf(pos, ".repeat count must not be negative")
		return -1
	}
	if int64(v) > maxRepeat {
		e.errf(pos, ".repeat count too large")
		return -1
	}
	return int(v)
}
