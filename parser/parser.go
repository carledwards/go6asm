// Package parser turns a token stream into an ast.File: recursive descent
// for statements and directives, a Pratt loop for expressions on the exact
// ca65 precedence table (DESIGN §6.4, §7.2). Modeled on go/parser.
//
// It recovers at the next newline so one bad line never aborts the file
// (multiple errors per pass, DESIGN §7.2, §14). Scanner-reported lexical
// errors are merged in, in source order. wasm-clean (DESIGN §4).
package parser

import (
	"sort"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/scanner"
	"github.com/carledwards/go6asm/token"
)

// TokenStream is a source of tokens with its own diagnostics. Both
// scanner.Scanner and the Phase-2 macro/conditional expander satisfy it,
// so the parser is agnostic to whether expansion happened upstream.
type TokenStream interface {
	Next() token.Token
	Diagnostics() []diag.Diagnostic
}

type parser struct {
	ts       TokenStream
	tok, nxt token.Token // current token and one-token lookahead
	name     string
	diags    []diag.Diagnostic
}

// Parse parses one source file directly (no macro/conditional expansion).
func Parse(file *token.File) (*ast.File, []diag.Diagnostic) {
	return ParseStream(file.Name(), scanner.New(file))
}

// ParseStream parses tokens from ts (typically scanner → expander). The
// returned File is always non-nil; a non-empty diagnostic slice means it
// may be incomplete.
func ParseStream(name string, ts TokenStream) (*ast.File, []diag.Diagnostic) {
	p := &parser{ts: ts, name: name}
	p.tok = ts.Next()
	p.nxt = ts.Next()

	f := &ast.File{Name: name}
	for p.tok.Kind != token.EOF {
		if p.tok.Kind == token.Newline {
			p.advance()
			continue
		}
		f.Stmts = append(f.Stmts, p.parseLine()...)
	}

	// Merge upstream (lexical/expansion) diagnostics; source order.
	all := append(p.diags, p.ts.Diagnostics()...)
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Pos.Offset < all[j].Pos.Offset
	})
	return f, all
}

func (p *parser) advance() {
	p.tok = p.nxt
	p.nxt = p.ts.Next()
}

// sliceStream replays a fixed token slice (then EOF forever), so the
// expression parser can be reused on captured tokens (e.g. an .if
// condition) without a scanner.
type sliceStream struct {
	toks []token.Token
	i    int
}

func (s *sliceStream) Next() token.Token {
	if s.i < len(s.toks) {
		t := s.toks[s.i]
		s.i++
		return t
	}
	return token.Token{Kind: token.EOF}
}
func (s *sliceStream) Diagnostics() []diag.Diagnostic { return nil }

// ParseExprTokens parses a single expression from a token slice. Used by
// the expander to evaluate .if conditions (DESIGN §16 Phase 2).
func ParseExprTokens(toks []token.Token) (ast.Expr, []diag.Diagnostic) {
	p := &parser{ts: &sliceStream{toks: toks}}
	p.tok = p.ts.Next()
	p.nxt = p.ts.Next()
	e := p.parseExpr(precBoolOr)
	if e != nil && !p.atLineEnd() {
		p.errf(p.tok.Pos, "unexpected token after expression")
	}
	return e, p.diags
}

func (p *parser) errf(pos token.Pos, msg string) {
	p.diags = append(p.diags, diag.Diagnostic{
		Severity: diag.SeverityError, Pos: pos, Message: msg,
	})
}

// recoverLine skips to the end of the current line so the next line can
// parse cleanly. It does not consume the newline (the top loop does).
func (p *parser) recoverLine() {
	for p.tok.Kind != token.Newline && p.tok.Kind != token.EOF {
		p.advance()
	}
}

func (p *parser) atLineEnd() bool {
	return p.tok.Kind == token.Newline || p.tok.Kind == token.EOF
}

// expect consumes a token of kind k, reporting an error if it is missing.
func (p *parser) expect(k token.Kind, what string) (token.Pos, bool) {
	if p.tok.Kind == k {
		pos := p.tok.Pos
		p.advance()
		return pos, true
	}
	p.errf(p.tok.Pos, "expected "+what)
	return p.tok.Pos, false
}

// parseLine parses one source line, which may carry several leading
// labels before a terminating statement (e.g. `loop: LDA x`).
func (p *parser) parseLine() []ast.Stmt {
	var stmts []ast.Stmt
	for {
		switch {
		case p.tok.Kind == token.Ident && p.nxt.Kind == token.Colon:
			stmts = append(stmts, &ast.LabelDecl{
				Kind: ast.GlobalLabel, Name: p.tok.Lit, NamePos: p.tok.Pos,
			})
			p.advance() // ident
			p.advance() // ':'
			continue

		case p.tok.Kind == token.At:
			at := p.tok.Pos
			p.advance()
			name := ""
			if p.tok.Kind == token.Ident {
				name = p.tok.Lit
				p.advance()
			} else {
				p.errf(at, "expected name after '@'")
			}
			p.expect(token.Colon, "':' after cheap-local label")
			stmts = append(stmts, &ast.LabelDecl{
				Kind: ast.CheapLabel, Name: name, NamePos: at,
			})
			continue

		case p.tok.Kind == token.Colon:
			stmts = append(stmts, &ast.LabelDecl{
				Kind: ast.UnnamedLabel, NamePos: p.tok.Pos,
			})
			p.advance()
			continue

		case p.tok.Kind == token.Ident && p.nxt.Kind == token.Eq:
			if s := p.parseAssign(); s != nil {
				stmts = append(stmts, s)
			}
		case p.tok.Kind == token.Ident:
			if s := p.parseInstruction(); s != nil {
				stmts = append(stmts, s)
			}
		case p.tok.Kind == token.DotIdent:
			if s := p.parseDirective(); s != nil {
				stmts = append(stmts, s)
			}
		case p.atLineEnd():
			// trailing label-only line
		case p.tok.Kind == token.Illegal:
			// scanner already diagnosed; just resynchronize.
			p.recoverLine()
		default:
			p.errf(p.tok.Pos, "unexpected token "+p.tok.Kind.String())
			p.recoverLine()
		}
		break
	}

	if !p.atLineEnd() {
		p.errf(p.tok.Pos, "expected end of line")
		p.recoverLine()
	}
	return stmts
}

func (p *parser) parseAssign() ast.Stmt {
	name, namePos := p.tok.Lit, p.tok.Pos
	p.advance() // ident
	p.advance() // '='
	val := p.parseExpr(precBoolOr)
	if val == nil {
		p.recoverLine()
		return nil
	}
	return &ast.Assign{Name: name, NamePos: namePos, Value: val}
}

func (p *parser) parseInstruction() ast.Stmt {
	mn, mnPos := p.tok.Lit, p.tok.Pos
	p.advance()
	op, ok := p.parseOperand()
	if !ok {
		p.recoverLine()
		return nil
	}
	return &ast.Instruction{Mnemonic: mn, MnemonicPos: mnPos, Operand: op}
}

func (p *parser) parseDirective() ast.Stmt {
	name, namePos := p.tok.Lit, p.tok.Pos
	p.advance()
	d := &ast.Directive{Name: name, NamePos: namePos}
	if p.atLineEnd() {
		return d
	}
	for {
		e := p.parseExpr(precBoolOr)
		if e == nil {
			p.recoverLine()
			return nil
		}
		d.Args = append(d.Args, e)
		if p.tok.Kind != token.Comma {
			break
		}
		p.advance() // ','
	}
	return d
}

// ---- operands -------------------------------------------------------------

func (p *parser) parseIndex() (ast.Index, bool) {
	if p.tok.Kind != token.Comma {
		return ast.NoIndex, true
	}
	p.advance() // ','
	if p.tok.Kind != token.Ident {
		p.errf(p.tok.Pos, "expected X or Y after ','")
		return ast.NoIndex, false
	}
	reg := lower(p.tok.Lit)
	p.advance()
	switch reg {
	case "x":
		return ast.IndexX, true
	case "y":
		return ast.IndexY, true
	}
	p.errf(p.tok.Pos, "index register must be X or Y")
	return ast.NoIndex, false
}

func (p *parser) parseOperand() (ast.Operand, bool) {
	// Implied.
	if p.atLineEnd() {
		return ast.Operand{Form: ast.NoOperand}, true
	}

	// Accumulator: a lone A (case-insensitive) at end of line.
	if p.tok.Kind == token.Ident && lower(p.tok.Lit) == "a" &&
		(p.nxt.Kind == token.Newline || p.nxt.Kind == token.EOF) {
		p.advance()
		return ast.Operand{Form: ast.AccOperand}, true
	}

	// Immediate.
	if p.tok.Kind == token.Hash {
		p.advance()
		e := p.parseExpr(precBoolOr)
		if e == nil {
			return ast.Operand{}, false
		}
		return ast.Operand{Form: ast.ImmOperand, Expr: e}, true
	}

	// Indirect family. A leading '(' at operand start is the indirect
	// addressing group; parenthesized-expression-as-operand is a known
	// ca65-compat corner deferred to the differential suite (§6.5).
	if p.tok.Kind == token.LParen {
		return p.parseIndirectOperand()
	}

	// Optional ca65 force prefix: z: (zero page) / a: (absolute).
	force := ast.NoForce
	if p.tok.Kind == token.Ident && p.nxt.Kind == token.Colon {
		switch lower(p.tok.Lit) {
		case "z":
			force = ast.ForceZP
		case "a":
			force = ast.ForceAbs
		}
		if force != ast.NoForce {
			p.advance() // z|a
			p.advance() // ':'
		}
	}

	e := p.parseExpr(precBoolOr)
	if e == nil {
		return ast.Operand{}, false
	}
	idx, ok := p.parseIndex()
	if !ok {
		return ast.Operand{}, false
	}
	return ast.Operand{Form: ast.MemOperand, Expr: e, Index: idx, Force: force}, true
}

func (p *parser) parseIndirectOperand() (ast.Operand, bool) {
	p.advance() // '('
	e := p.parseExpr(precBoolOr)
	if e == nil {
		return ast.Operand{}, false
	}
	switch p.tok.Kind {
	case token.Comma: // (expr,X)
		p.advance()
		if p.tok.Kind != token.Ident || lower(p.tok.Lit) != "x" {
			p.errf(p.tok.Pos, "expected X in (operand,X)")
			return ast.Operand{}, false
		}
		p.advance()
		if _, ok := p.expect(token.RParen, "')'"); !ok {
			return ast.Operand{}, false
		}
		return ast.Operand{Form: ast.IndXOperand, Expr: e}, true

	case token.RParen:
		p.advance()
		if p.tok.Kind == token.Comma { // (expr),Y
			p.advance()
			if p.tok.Kind != token.Ident || lower(p.tok.Lit) != "y" {
				p.errf(p.tok.Pos, "expected Y in (operand),Y")
				return ast.Operand{}, false
			}
			p.advance()
			return ast.Operand{Form: ast.IndYOperand, Expr: e}, true
		}
		return ast.Operand{Form: ast.IndOperand, Expr: e}, true
	}
	p.errf(p.tok.Pos, "malformed indirect operand")
	return ast.Operand{}, false
}

// ---- expressions (Pratt) --------------------------------------------------

func (p *parser) parseExpr(minPrec int) ast.Expr {
	left := p.parseUnary()
	if left == nil {
		return nil
	}
	for {
		op, prec, ok := binOp(p.tok)
		if !ok || prec < minPrec {
			return left
		}
		opPos := p.tok.Pos
		p.advance()
		right := p.parseExpr(prec + 1) // left-associative
		if right == nil {
			return nil
		}
		left = &ast.Binary{Op: op, OpPos: opPos, X: left, Y: right}
	}
}

func (p *parser) parseUnary() ast.Expr {
	switch p.tok.Kind {
	case token.Plus, token.Minus, token.Tilde, token.Bang,
		token.Lt, token.Gt, token.Caret:
		op, opPos := p.tok.Kind, p.tok.Pos
		p.advance()
		x := p.parseUnary()
		if x == nil {
			return nil
		}
		return &ast.Unary{Op: op, OpPos: opPos, X: x}
	}
	return p.parsePrimary()
}

// scopedIdent continues parsing a `::`-qualified path. parts holds the
// components seen so far (at least one); global marks a leading `::`.
func (p *parser) scopedIdent(parts []string, global bool, pos token.Pos) ast.Expr {
	for p.tok.Kind == token.ColonColon {
		p.advance()
		if p.tok.Kind != token.Ident {
			p.errf(p.tok.Pos, "expected name after '::'")
			break
		}
		parts = append(parts, p.tok.Lit)
		p.advance()
	}
	n := len(parts)
	return &ast.Ident{
		Name:    parts[n-1],
		Scopes:  parts[:n-1],
		Global:  global,
		NamePos: pos,
	}
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.tok
	switch t.Kind {
	case token.Int:
		p.advance()
		return &ast.IntLit{Lit: t.Lit, LitPos: t.Pos}
	case token.Char:
		p.advance()
		var b byte
		if len(t.Lit) > 0 {
			b = t.Lit[0]
		}
		return &ast.CharLit{Val: b, ValPos: t.Pos}
	case token.String:
		p.advance()
		return &ast.StrLit{Val: t.Lit, ValPos: t.Pos}
	case token.Ident:
		p.advance()
		return p.scopedIdent([]string{t.Lit}, false, t.Pos)
	case token.ColonColon:
		p.advance()
		if p.tok.Kind != token.Ident {
			p.errf(t.Pos, "expected name after '::'")
			return &ast.Ident{Global: true, NamePos: t.Pos}
		}
		first := p.tok.Lit
		p.advance()
		return p.scopedIdent([]string{first}, true, t.Pos)
	case token.At:
		p.advance()
		if p.tok.Kind != token.Ident {
			p.errf(t.Pos, "expected name after '@'")
			return &ast.Ident{Cheap: true, NamePos: t.Pos}
		}
		name := p.tok.Lit
		p.advance()
		return &ast.Ident{Cheap: true, Name: name, NamePos: t.Pos}
	case token.Star:
		p.advance()
		return &ast.CurPC{StarPos: t.Pos}
	case token.UnnamedRef:
		p.advance()
		fwd := len(t.Lit) > 0 && t.Lit[0] == '+'
		return &ast.UnnamedRef{Forward: fwd, Count: len(t.Lit), RefPos: t.Pos}
	case token.LParen:
		p.advance()
		e := p.parseExpr(precBoolOr)
		if e == nil {
			return nil
		}
		if _, ok := p.expect(token.RParen, "')'"); !ok {
			return nil
		}
		return &ast.Paren{LParen: t.Pos, X: e}
	}
	p.errf(t.Pos, "expected expression")
	return nil
}
