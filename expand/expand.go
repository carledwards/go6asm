// Package expand is the Phase-2 token-level expander that sits between
// the scanner and the parser (DESIGN §16 Phase 2). It is the ca65-faithful
// place for eager macro expansion (§17.4), conditional assembly, and
// .repeat unrolling: all are token-stream transforms, so the parser stays
// unaware that expansion happened.
//
// Brick 0 is the identity transform — it forwards tokens unchanged and
// propagates diagnostics — establishing the pipeline seam without
// behavior change. Conditionals, .repeat, and .macro land on top of this.
//
// Depends only on token and diag. wasm-clean (DESIGN §4).
package expand

import (
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/token"
)

// Source is the upstream token producer (a scanner) with diagnostics.
type Source interface {
	Next() token.Token
	Diagnostics() []diag.Diagnostic
}

// Options configures expansion. Defines are the CLI -D constants, visible
// to .ifdef/.if (added in the conditionals brick).
type Options struct {
	Defines map[string]int64
}

// condFrame is one open .if/.ifdef chain.
type condFrame struct {
	active       bool // this branch emits
	taken        bool // some branch in the chain already matched
	elseSeen     bool // .else has appeared (no .elseif/.else after)
	parentActive bool // the enclosing context emits
}

// inFrame is a replayed token slice (a .repeat expansion, later a macro
// expansion). Frames are read before the source, so nested .repeat/.if
// inside replayed tokens flow back through the same Next() machinery.
type inFrame struct {
	toks []token.Token
	i    int
}

// Expander transforms a token stream. It satisfies parser.TokenStream.
type Expander struct {
	src    Source
	opt    Options
	diags  []diag.Diagnostic
	cond   []condFrame
	in     []*inFrame
	la     []token.Token        // pushed-back lookahead
	macros map[string]*macroDef // defined .macro bodies
	last   token.Kind           // kind of the last emitted token
	budget int                  // remaining macro expansions (recursion guard)
	budErr bool                 // budget-exceeded already reported
}

// read returns the next raw token: pushed-back token first, then the
// innermost replay frame, then the source.
func (e *Expander) read() token.Token {
	if n := len(e.la); n > 0 {
		t := e.la[n-1]
		e.la = e.la[:n-1]
		return t
	}
	for len(e.in) > 0 {
		f := e.in[len(e.in)-1]
		if f.i < len(f.toks) {
			t := f.toks[f.i]
			f.i++
			return t
		}
		e.in = e.in[:len(e.in)-1]
	}
	return e.src.Next()
}

// unread pushes one token back so the next read returns it.
func (e *Expander) unread(t token.Token) { e.la = append(e.la, t) }

// New returns an Expander over src.
func New(src Source, opt Options) *Expander {
	return &Expander{
		src:    src,
		opt:    opt,
		macros: map[string]*macroDef{},
		last:   token.Newline, // start of file == start of a line
		budget: 1 << 16,       // total macro expansions before giving up
	}
}

// atStmtStart reports whether the next token begins a statement (start of
// line, or just after a label's ':'), where a macro invocation is legal.
func (e *Expander) atStmtStart() bool {
	return e.last == token.Newline || e.last == token.Colon
}

// emitting reports whether tokens at the current point reach the parser.
func (e *Expander) emitting() bool {
	if len(e.cond) == 0 {
		return true
	}
	f := e.cond[len(e.cond)-1]
	return f.active && f.parentActive
}

// Next returns the next token, applying conditional assembly. Control
// directives (.if/.endif/…) and tokens in inactive branches are consumed
// here and never reach the parser.
func (e *Expander) Next() token.Token {
	for {
		tk := e.read()
		if tk.Kind == token.EOF {
			if len(e.cond) > 0 {
				e.errf(tk.Pos, "unterminated .if (missing .endif)")
				e.cond = nil
			}
			return tk
		}
		if tk.Kind == token.DotIdent && isControl(tk.Lit) {
			e.handleControl(tk)
			continue
		}
		// Macro invocation: a defined macro name at statement start, not
		// followed by ':' or '=' (which would make it a label/constant).
		if e.emitting() && tk.Kind == token.Ident && e.atStmtStart() {
			if _, ok := e.macros[tk.Lit]; ok {
				la := e.read()
				e.unread(la)
				if la.Kind != token.Colon && la.Kind != token.Eq {
					e.expandMacro(tk)
					continue
				}
			}
		}
		if e.emitting() {
			e.last = tk.Kind
			return tk
		}
	}
}

func (e *Expander) errf(pos token.Pos, msg string) {
	e.diags = append(e.diags, diag.Diagnostic{
		Severity: diag.SeverityError, Pos: pos, Message: msg,
	})
}

// Diagnostics returns expansion diagnostics merged with the source's.
func (e *Expander) Diagnostics() []diag.Diagnostic {
	return append(e.diags, e.src.Diagnostics()...)
}
