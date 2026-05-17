package expand

import (
	"strings"

	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/expr"
	"github.com/carledwards/go6asm/parser"
	"github.com/carledwards/go6asm/sema"
	"github.com/carledwards/go6asm/token"
)

// controlDirectives are handled entirely by the expander and never reach
// the parser (DESIGN §16 Phase 2). .assert is deliberately absent: it
// needs codegen-time symbol values and stays a real statement.
var controlDirectives = map[string]bool{
	".if": true, ".elseif": true, ".else": true, ".endif": true,
	".ifdef": true, ".ifndef": true,
	".error": true, ".warning": true, ".out": true,
	".repeat": true, ".endrepeat": true, ".endrep": true,
	".macro": true, ".endmacro": true, ".endmac": true,
}

func isControl(lit string) bool { return controlDirectives[strings.ToLower(lit)] }

// lineArgs consumes the rest of the current logical line from the source
// (everything up to, but not including, the Newline/EOF).
func (e *Expander) lineArgs() []token.Token {
	var args []token.Token
	for {
		t := e.read()
		if t.Kind == token.Newline || t.Kind == token.EOF {
			return args
		}
		args = append(args, t)
	}
}

func (e *Expander) handleControl(dir token.Token) {
	kw := strings.ToLower(dir.Lit)
	args := e.lineArgs()

	switch kw {
	case ".if", ".ifdef", ".ifndef":
		parent := e.emitting()
		f := condFrame{parentActive: parent}
		if parent {
			var c bool
			switch kw {
			case ".if":
				c = e.evalCond(args, dir.Pos)
			case ".ifdef":
				c = e.isDefined(args, dir.Pos)
			case ".ifndef":
				c = !e.isDefined(args, dir.Pos)
			}
			f.active, f.taken = c, c
		} else {
			// Dead block: don't evaluate (ca65 only tracks nesting).
			f.taken = true
		}
		e.cond = append(e.cond, f)

	case ".elseif":
		f := e.top(dir.Pos, ".elseif")
		if f == nil {
			return
		}
		if f.elseSeen {
			e.errf(dir.Pos, ".elseif after .else")
			return
		}
		switch {
		case !f.parentActive, f.taken:
			f.active = false
		default:
			c := e.evalCond(args, dir.Pos)
			f.active = c
			f.taken = f.taken || c
		}

	case ".else":
		f := e.top(dir.Pos, ".else")
		if f == nil {
			return
		}
		if f.elseSeen {
			e.errf(dir.Pos, "duplicate .else")
			return
		}
		f.elseSeen = true
		f.active = f.parentActive && !f.taken
		f.taken = true

	case ".endif":
		if len(e.cond) == 0 {
			e.errf(dir.Pos, ".endif without .if")
			return
		}
		e.cond = e.cond[:len(e.cond)-1]

	case ".repeat":
		e.handleRepeat(dir, args)

	case ".endrepeat", ".endrep":
		e.errf(dir.Pos, ".endrepeat without .repeat")

	case ".macro":
		e.defineMacro(dir, args)

	case ".endmacro", ".endmac":
		e.errf(dir.Pos, ".endmacro without .macro")

	case ".error", ".warning", ".out":
		if !e.emitting() {
			return
		}
		msg := messageArg(args)
		sev := diag.SeverityError
		switch kw {
		case ".warning":
			sev = diag.SeverityWarning
		case ".out":
			sev = diag.SeverityNote
		}
		e.diags = append(e.diags, diag.Diagnostic{
			Severity: sev, Pos: dir.Pos, Message: msg,
		})
	}
}

// top returns the innermost open conditional frame, or nil (with an
// error) if there is none.
func (e *Expander) top(pos token.Pos, what string) *condFrame {
	if len(e.cond) == 0 {
		e.errf(pos, what+" without .if")
		return nil
	}
	return &e.cond[len(e.cond)-1]
}

// condResolver exposes -D defines (only) to .if expressions. Labels and
// `=` constants are not known pre-codegen and are outside the Phase-2
// strict subset for conditionals (ca65 compatibility is one-way).
type condResolver struct{ d map[string]int64 }

func (r condResolver) Lookup(ref sema.SymRef) (expr.Value, bool) {
	// Only plain -D defines are visible in conditionals (strict subset).
	if ref.Global || ref.Cheap || len(ref.Scopes) > 0 {
		return 0, false
	}
	if v, ok := r.d[ref.Name]; ok {
		return expr.FromInt64(v)
	}
	return 0, false
}
func (condResolver) CurrentPC() expr.Value                   { return 0 }
func (condResolver) UnnamedRef(bool, int) (expr.Value, bool) { return 0, false }

func (e *Expander) evalCond(args []token.Token, pos token.Pos) bool {
	if len(args) == 0 {
		e.errf(pos, ".if requires a condition expression")
		return false
	}
	ex, ds := parser.ParseExprTokens(args)
	e.diags = append(e.diags, ds...)
	if ex == nil || len(ds) > 0 {
		return false
	}
	v, ok, ds2 := sema.Eval(ex, condResolver{e.opt.Defines})
	e.diags = append(e.diags, ds2...)
	if !ok {
		return false
	}
	return v != 0
}

func (e *Expander) isDefined(args []token.Token, pos token.Pos) bool {
	if len(args) != 1 || args[0].Kind != token.Ident {
		e.errf(pos, ".ifdef/.ifndef needs a single symbol name")
		return false
	}
	_, ok := e.opt.Defines[args[0].Lit]
	return ok
}

func messageArg(args []token.Token) string {
	if len(args) == 1 && args[0].Kind == token.String {
		return args[0].Lit
	}
	var b strings.Builder
	for i, t := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t.Lit)
	}
	return b.String()
}
