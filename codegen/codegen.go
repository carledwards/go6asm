// Package codegen is the two-pass layout-and-emit driver (DESIGN §7.3,
// §7.4). Pass 1 assigns addresses and fixes each instruction's addressing
// mode (unknown/forward references default to absolute, ca65-parity).
// Pass 2 evaluates every expression against the full symbol table and
// emits bytes, reusing pass 1's mode verbatim — so the passes can never
// disagree (no "phase error") and output is deterministic.
//
// Phase 1 scope: direct binary only — no segments/objects/relocations.
// Directives: .byte .word .res .org .incbin .asciiz. .include is resolved
// by the assembler front-end (asm), not here. wasm-clean (DESIGN §4).
package codegen

import (
	"fmt"
	"strings"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/expr"
	"github.com/carledwards/go6asm/isa"
	"github.com/carledwards/go6asm/sema"
	"github.com/carledwards/go6asm/token"
)

// SymKind classifies a resolved symbol.
type SymKind int

const (
	LabelSym SymKind = iota
	ConstSym
)

// Symbol is go6asm's own resolved-symbol model; consumers (e.g.
// go6sim) adapt to it (DESIGN §10.4).
type Symbol struct {
	Name string
	Addr uint16
	Kind SymKind
	Pos  token.Pos
}

// Binaries resolves a .incbin name to bytes. nil ⇒ .incbin unsupported.
type Binaries interface {
	ReadFile(name string) ([]byte, bool)
}

// Options configures a run.
type Options struct {
	Origin  uint16           // starting PC when no .org precedes the first byte
	Incbin  Binaries         // optional .incbin resolver
	Defines map[string]int64 // predefined constants (CLI -D name=value)
	Listing bool             // produce Result.Listing
}

// Result is the assembled image plus symbols and diagnostics.
type Result struct {
	Image   []byte
	Origin  uint16
	Symbols []Symbol
	// Comments maps the start address of a byte-emitting statement to
	// the human comment on (or directly above) its source line — feeds
	// disassembly-annotation views (DESIGN §10.2, §12).
	Comments map[uint16]string
	Listing  string // populated when Options.Listing is set
	Diags    []diag.Diagnostic
}

type gsym struct {
	val     expr.Value
	kind    SymKind
	pos     token.Pos
	defined bool
}

// scope is one lexical scope (.proc/.scope; the root is the global
// scope). Names resolve current→parent→…→root; `::` starts at root;
// `A::b` descends named children (DESIGN §7.3, §16 Phase 2).
type scope struct {
	name   string
	parent *scope
	syms   map[string]*gsym
	kids   map[string]*scope
}

func newScope(name string, parent *scope) *scope {
	return &scope{name: name, parent: parent,
		syms: map[string]*gsym{}, kids: map[string]*scope{}}
}

// child returns the named child scope, creating it on first use so pass 1
// builds the tree and pass 2 navigates the same nodes.
func (s *scope) child(name string) *scope {
	c := s.kids[name]
	if c == nil {
		c = newScope(name, s)
		s.kids[name] = c
	}
	return c
}

func (s *scope) path() string {
	if s.parent == nil {
		return ""
	}
	if p := s.parent.path(); p != "" {
		return p + "::" + s.name
	}
	return s.name
}

type ckey struct {
	scope  string
	region int
	name   string
}

type assignRef struct {
	a  *ast.Assign
	sc *scope
}

type gen struct {
	opt   Options
	diags []diag.Diagnostic

	root    *scope
	cur     *scope // scope open at the current walk position
	cheap   map[ckey]expr.Value
	unnamed []int
	assigns []assignRef
	seen    map[string]bool // pass-2 duplicate-label detection

	// walk state; the Resolver reads these
	pc          int
	region      int
	unnamedSeen int
	recordEval  bool // gate speculative pass-1 eval diagnostics
	inPass2     bool
	listRows    []lstRow
	comments    map[int]string // start PC → source comment (pass 2)
}

// lstRow is one listing line: where the statement is in source, the PC it
// assembled at, and the bytes it produced.
type lstRow struct {
	pos   token.Pos
	pc    int
	bytes []byte
}

// resolver binds sema.Eval to the current walk position.
type resolver gen

func (g *gen) r() sema.Resolver { return (*resolver)(g) }

func (rv *resolver) Lookup(ref sema.SymRef) (expr.Value, bool) {
	if ref.Cheap {
		v, ok := rv.cheap[ckey{rv.cur.path(), rv.region, ref.Name}]
		return v, ok
	}
	start := rv.cur
	if ref.Global {
		start = rv.root
	}
	if len(ref.Scopes) == 0 {
		for s := start; s != nil; s = s.parent {
			if y := s.syms[ref.Name]; y != nil && y.defined {
				return y.val, true
			}
		}
		return 0, false
	}
	// Qualified path: find the base scope, then descend named children.
	base := rv.root
	if !ref.Global {
		base = nil
		for s := start; s != nil; s = s.parent {
			if _, ok := s.kids[ref.Scopes[0]]; ok {
				base = s
				break
			}
		}
		if base == nil {
			return 0, false
		}
	}
	cur := base
	for _, nm := range ref.Scopes {
		if cur = cur.kids[nm]; cur == nil {
			return 0, false
		}
	}
	if y := cur.syms[ref.Name]; y != nil && y.defined {
		return y.val, true
	}
	return 0, false
}

func (rv *resolver) CurrentPC() expr.Value { return expr.Value(rv.pc) }
func (rv *resolver) UnnamedRef(fwd bool, count int) (expr.Value, bool) {
	pos := rv.unnamedSeen - count
	if fwd {
		pos = rv.unnamedSeen + count - 1
	}
	if pos < 0 || pos >= len(rv.unnamed) {
		return 0, false
	}
	return expr.Value(rv.unnamed[pos]), true
}

func (g *gen) errf(pos token.Pos, code, msg, hint string) {
	g.diags = append(g.diags, diag.Diagnostic{
		Severity: diag.SeverityError, Pos: pos, Message: msg, Hint: hint, Code: code,
	})
}

// eval evaluates e at the current position. Pass-1 evals are speculative
// (recordEval false): the value is only a sizing hint and pass 2 produces
// the authoritative diagnostics.
func (g *gen) eval(e ast.Expr) (expr.Value, bool) {
	if e == nil {
		return 0, false
	}
	v, ok, ds := sema.Eval(e, g.r())
	if g.recordEval {
		g.diags = append(g.diags, ds...)
	}
	return v, ok
}

// Generate assembles f into a flat image.
func Generate(f *ast.File, opt Options) Result {
	root := newScope("", nil)
	g := &gen{
		opt:      opt,
		root:     root,
		cur:      root,
		cheap:    map[ckey]expr.Value{},
		seen:     map[string]bool{},
		comments: map[int]string{},
	}
	g.seedDefines()
	plans := g.pass1(f)
	g.resolveConsts()
	img := g.pass2(f, plans)
	res := Result{
		Image:   img.bytes(),
		Origin:  img.origin,
		Symbols: g.symbols(),
		Diags:   g.diags,
	}
	if len(g.comments) > 0 {
		res.Comments = make(map[uint16]string, len(g.comments))
		for pc, c := range g.comments {
			if pc >= 0 && pc <= 0xFFFF {
				res.Comments[uint16(pc)] = c
			}
		}
	}
	if opt.Listing {
		res.Listing = g.renderListing()
	}
	return res
}

// instrPlan is pass 1's decision for one instruction, reused in pass 2.
type instrPlan struct {
	enc isa.Encoding
	rel bool             // relative branch: operand is a target address
	dg  *diag.Diagnostic // deferred selection error, emitted in pass 2
}

// seedDefines installs CLI -D constants before pass 1 so source can
// reference them. An out-of-range value is reported, not silently mangled.
func (g *gen) seedDefines() {
	for name, n := range g.opt.Defines {
		v, ok := expr.FromInt64(n)
		if !ok {
			g.errf(token.NoPos, "E0017",
				"define "+name+" does not fit the 32-bit value model", "")
			continue
		}
		g.root.syms[name] = &gsym{val: v, kind: ConstSym, pos: token.NoPos, defined: true}
	}
}

func (g *gen) resetWalk() {
	g.pc, g.region, g.unnamedSeen = int(g.opt.Origin), 0, 0
	g.cur = g.root
}

func (g *gen) pass1(f *ast.File) []instrPlan {
	g.resetWalk()
	g.recordEval = false
	var plans []instrPlan
	for _, st := range f.Stmts {
		switch s := st.(type) {
		case *ast.LabelDecl:
			g.declLabel(s)
		case *ast.Assign:
			g.assigns = append(g.assigns, assignRef{s, g.cur})
			if _, exists := g.cur.syms[s.Name]; !exists {
				v, ok := g.eval(s.Value)
				g.cur.syms[s.Name] = &gsym{
					val: v, kind: ConstSym, pos: s.NamePos, defined: ok,
				}
			}
		case *ast.Instruction:
			p := g.planInstruction(s)
			plans = append(plans, p)
			g.pc += p.enc.Bytes
		case *ast.Directive:
			if !g.scopeDirective(s) {
				g.pass1Directive(s)
			}
		}
	}
	return plans
}

// declName declares a global label in the current scope. Pass 1 records
// the address (first definition wins); pass 2 reports redefinitions.
func (g *gen) declName(name string, pos token.Pos) {
	if g.inPass2 {
		key := g.cur.path() + "\x00" + name
		if g.seen[key] {
			g.errf(pos, "E0002", "label "+name+" redefined", "")
		}
		g.seen[key] = true
	} else if _, ok := g.cur.syms[name]; !ok {
		g.cur.syms[name] = &gsym{
			val: expr.Value(g.pc), kind: LabelSym, pos: pos, defined: true,
		}
	}
	g.region++
}

func (g *gen) declLabel(s *ast.LabelDecl) {
	switch s.Kind {
	case ast.GlobalLabel:
		g.declName(s.Name, s.NamePos)
	case ast.CheapLabel:
		g.cheap[ckey{g.cur.path(), g.region, s.Name}] = expr.Value(g.pc)
	case ast.UnnamedLabel:
		g.unnamed = append(g.unnamed, g.pc)
		g.unnamedSeen++
	}
}

// scopeDirective handles .proc/.scope/.endproc/.endscope (no bytes) in
// both passes, mutating the scope tree and walk position. It returns true
// if s was a scope directive.
func (g *gen) scopeDirective(s *ast.Directive) bool {
	switch strings.ToLower(s.Name) {
	case ".proc":
		name := directiveName(s)
		if name == "" {
			g.errf(s.NamePos, "E0006", ".proc requires a name", "")
			return true
		}
		g.declName(name, s.NamePos) // .proc defines a label
		g.cur = g.cur.child(name)
	case ".scope":
		name := directiveName(s)
		if name == "" {
			g.errf(s.NamePos, "E0006", ".scope requires a name", "")
			return true
		}
		g.region++
		g.cur = g.cur.child(name)
	case ".endproc", ".endscope":
		if g.cur.parent == nil {
			g.errf(s.NamePos, "E0007", s.Name+" without an open scope", "")
			return true
		}
		g.cur = g.cur.parent
		g.region++
	default:
		return false
	}
	return true
}

// directiveName extracts a bare identifier argument (.proc NAME).
func directiveName(s *ast.Directive) string {
	if len(s.Args) == 1 {
		if id, ok := s.Args[0].(*ast.Ident); ok && len(id.Scopes) == 0 {
			return id.Name
		}
	}
	return ""
}

// resolveConsts re-evaluates `=` constants to a fixed point so a constant
// that references a label (whose address is only known after pass 1) still
// resolves. Speculative: diagnostics are not recorded here.
func (g *gen) resolveConsts() {
	g.recordEval = false
	for it := 0; it <= len(g.assigns)+1; it++ {
		progress := false
		for _, ar := range g.assigns {
			s := ar.sc.syms[ar.a.Name]
			if s == nil || s.kind != ConstSym {
				continue
			}
			g.cur = ar.sc // evaluate in the scope of the definition
			if v, ok := g.eval(ar.a.Value); ok && (!s.defined || s.val != v) {
				s.val, s.defined, progress = v, true, true
			}
		}
		if !progress {
			break
		}
	}
	g.cur = g.root
}

func (g *gen) pass2(f *ast.File, plans []instrPlan) *image {
	g.resetWalk()
	g.recordEval = true
	g.inPass2 = true
	img := newImage()
	pi := 0
	for _, st := range f.Stmts {
		p0 := g.pc
		switch s := st.(type) {
		case *ast.LabelDecl:
			switch s.Kind {
			case ast.GlobalLabel:
				g.declName(s.Name, s.NamePos) // pass-2: dup detection
			case ast.UnnamedLabel:
				g.unnamedSeen++
			}
		case *ast.Assign:
			// values are finalized by resolveConsts; nothing to emit
		case *ast.Instruction:
			g.emitInstruction(s, plans[pi], img)
			pi++
		case *ast.Directive:
			if !g.scopeDirective(s) {
				g.emitDirective(s, img)
			}
		}
		if g.pc > p0 {
			g.captureComment(stmtPos(st), p0)
		}
		if g.opt.Listing {
			g.recordListRow(stmtPos(st), p0, img)
		}
	}
	return img
}

func stmtPos(st ast.Stmt) token.Pos { return st.Pos() }

// captureComment records the human comment for the byte-emitting
// statement starting at pc: a trailing `; …` on its own source line,
// or, failing that, the nearest comment-only line directly above it
// (blank lines skipped). Mirrors the scanner's string/char rules so a
// ';' inside "a;b" or 'a' is not mistaken for a comment.
func (g *gen) captureComment(pos token.Pos, pc int) {
	if !pos.IsValid() {
		return
	}
	rp := pos.Resolve()
	c := lineTrailingComment(pos.File.LineText(rp.Line))
	if c == "" {
		for k := rp.Line - 1; k >= 1; k-- {
			up := pos.File.LineText(k)
			if strings.TrimSpace(up) == "" {
				continue
			}
			if t, ok := commentOnly(up); ok {
				c = t
			}
			break
		}
	}
	if c != "" {
		g.comments[pc] = c
	}
}

// lineTrailingComment returns the trimmed text after the first ';' that
// is not inside a string or char literal, or "" if the line has none.
func lineTrailingComment(line string) string {
	inStr, inChr := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case inChr:
			if c == '\\' {
				i++
			} else if c == '\'' {
				inChr = false
			}
		case c == '"':
			inStr = true
		case c == '\'':
			inChr = true
		case c == ';':
			return strings.TrimSpace(line[i+1:])
		}
	}
	return ""
}

// commentOnly reports whether line is a whole-line comment and returns
// its trimmed text.
func commentOnly(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, ";") {
		return strings.TrimSpace(t[1:]), true
	}
	return "", false
}

// recordListRow captures the bytes a statement produced (cap a few so a
// big .res stays readable).
func (g *gen) recordListRow(pos token.Pos, p0 int, img *image) {
	row := lstRow{pos: pos, pc: p0}
	for a := p0; a < g.pc && a-p0 < 8; a++ {
		if b, ok := img.cells[a]; ok {
			row.bytes = append(row.bytes, b)
		}
	}
	g.listRows = append(g.listRows, row)
}

// ---- instruction encoding -------------------------------------------------

func (g *gen) planInstruction(s *ast.Instruction) instrPlan {
	v, known := g.eval(s.Operand.Expr)
	enc, rel, ok, dg := g.selectEncoding(s, v, known)
	if !ok || enc.Bytes == 0 {
		enc.Bytes = 1 // keep the walk advancing after an error
	}
	return instrPlan{enc: enc, rel: rel, dg: dg}
}

func (g *gen) emitInstruction(s *ast.Instruction, p instrPlan, img *image) {
	at := g.pc
	g.pc += p.enc.Bytes
	if p.dg != nil {
		g.diags = append(g.diags, *p.dg)
		return
	}
	img.put(at, p.enc.Opcode)
	switch p.enc.Mode {
	case isa.Implied, isa.Accumulator:
		return
	}
	// ca65 semantics: `*` in an operand is the address of THIS
	// instruction, not the byte after it. Evaluate with the PC wound
	// back to the instruction start, then restore.
	g.pc = at
	v, ok := g.eval(s.Operand.Expr)
	g.pc = at + p.enc.Bytes
	if !ok {
		return
	}
	switch p.enc.Mode {
	case isa.Immediate, isa.ZeroPage, isa.ZeroPageX, isa.ZeroPageY,
		isa.IndexedIndirect, isa.IndirectIndexed:
		if !v.FitsByte() {
			g.errf(s.Operand.Expr.Pos(), "E0010",
				"operand out of range for "+p.enc.Mode.String()+": "+v.String(), "")
			return
		}
		img.put(at+1, v.U8())
	case isa.Absolute, isa.AbsoluteX, isa.AbsoluteY, isa.Indirect:
		if !v.FitsWord() {
			g.errf(s.Operand.Expr.Pos(), "E0010",
				"operand out of range for absolute addressing: "+v.String(), "")
			return
		}
		w := v.U16()
		img.put(at+1, byte(w))
		img.put(at+2, byte(w>>8))
	case isa.Relative:
		off, inRange := expr.RelBranch(expr.Value(at), v)
		if !inRange {
			g.errf(s.MnemonicPos, "E0014", "branch out of range",
				"target is too far; use the inverse branch + JMP")
			return
		}
		img.put(at+1, byte(off))
	}
}

// selectEncoding picks the isa encoding. known says whether val is
// resolved; an unknown memory operand defaults to absolute (ca65-parity).
// A non-nil diagnostic means no legal mode exists.
func (g *gen) selectEncoding(s *ast.Instruction, val expr.Value, known bool) (isa.Encoding, bool, bool, *diag.Diagnostic) {
	bad := func(code, msg, hint string) (isa.Encoding, bool, bool, *diag.Diagnostic) {
		return isa.Encoding{}, false, false,
			&diag.Diagnostic{Severity: diag.SeverityError, Pos: s.MnemonicPos,
				Code: code, Message: msg, Hint: hint}
	}
	M := isa.Mnemonic(strings.ToUpper(s.Mnemonic))
	if !isa.NMOS6502.HasMnemonic(M) {
		return bad("E0001", "unknown instruction "+string(M),
			"not in the v1 NMOS table (full table lands with the ca65 differential suite)")
	}
	op := s.Operand
	pick := func(m isa.Mode) (isa.Encoding, bool) { return isa.NMOS6502.Lookup(M, m) }

	switch op.Form {
	case ast.NoOperand:
		if e, ok := pick(isa.Implied); ok {
			return e, false, true, nil
		}
		if e, ok := pick(isa.Accumulator); ok {
			return e, false, true, nil
		}
		return bad("E0003", string(M)+" requires an operand", "")
	case ast.AccOperand:
		if e, ok := pick(isa.Accumulator); ok {
			return e, false, true, nil
		}
		return bad("E0003", "accumulator addressing not available for "+string(M), "")
	case ast.ImmOperand:
		if e, ok := pick(isa.Immediate); ok {
			return e, false, true, nil
		}
		return bad("E0003", "immediate addressing not available for "+string(M), "")
	case ast.IndOperand:
		if e, ok := pick(isa.Indirect); ok {
			return e, false, true, nil
		}
		return bad("E0003", "indirect addressing not available for "+string(M), "")
	case ast.IndXOperand:
		if e, ok := pick(isa.IndexedIndirect); ok {
			return e, false, true, nil
		}
		return bad("E0003", "(indirect,X) not available for "+string(M), "")
	case ast.IndYOperand:
		if e, ok := pick(isa.IndirectIndexed); ok {
			return e, false, true, nil
		}
		return bad("E0003", "(indirect),Y not available for "+string(M), "")
	case ast.MemOperand:
		if op.Index == ast.NoIndex && op.Force == ast.NoForce {
			if e, ok := pick(isa.Relative); ok {
				return e, true, true, nil
			}
		}
		zpM, absM := isa.ZeroPage, isa.Absolute
		switch op.Index {
		case ast.IndexX:
			zpM, absM = isa.ZeroPageX, isa.AbsoluteX
		case ast.IndexY:
			zpM, absM = isa.ZeroPageY, isa.AbsoluteY
		}
		wantZP := false
		switch op.Force {
		case ast.ForceZP:
			wantZP = true
		case ast.ForceAbs:
			wantZP = false
		default:
			_, hasZP := pick(zpM)
			wantZP = known && val >= 0 && val <= 0xFF && hasZP
		}
		if wantZP {
			if e, ok := pick(zpM); ok {
				return e, false, true, nil
			}
			if op.Force == ast.ForceZP {
				return bad("E0003", "zero-page addressing not available for "+string(M), "")
			}
		}
		if e, ok := pick(absM); ok {
			return e, false, true, nil
		}
		if e, ok := pick(zpM); ok && op.Force != ast.ForceAbs {
			return e, false, true, nil
		}
		return bad("E0003", "addressing mode not available for "+string(M), "")
	}
	return bad("E0003", "malformed operand", "")
}

// ---- directives -----------------------------------------------------------

func (g *gen) constArg(e ast.Expr) (expr.Value, bool) {
	v, ok, _ := sema.Eval(e, g.r())
	return v, ok
}

func (g *gen) incbinBytes(s *ast.Directive) ([]byte, bool) {
	if g.opt.Incbin == nil || len(s.Args) != 1 {
		return nil, false
	}
	sl, ok := s.Args[0].(*ast.StrLit)
	if !ok {
		return nil, false
	}
	return g.opt.Incbin.ReadFile(sl.Val)
}

// pass1Directive advances pc exactly as emitDirective will, so the two
// passes agree on every address.
func (g *gen) pass1Directive(s *ast.Directive) {
	switch strings.ToLower(s.Name) {
	case ".byte":
		for _, a := range s.Args {
			if sl, ok := a.(*ast.StrLit); ok {
				g.pc += len(sl.Val)
			} else {
				g.pc++
			}
		}
	case ".word":
		g.pc += 2 * len(s.Args)
	case ".asciiz":
		if len(s.Args) == 1 {
			if sl, ok := s.Args[0].(*ast.StrLit); ok {
				g.pc += len(sl.Val) + 1
			}
		}
	case ".res":
		if len(s.Args) >= 1 {
			if n, ok := g.constArg(s.Args[0]); ok && n >= 0 {
				g.pc += int(n)
			}
		}
	case ".org":
		if len(s.Args) == 1 {
			if v, ok := g.constArg(s.Args[0]); ok {
				g.pc = int(v)
			}
		}
	case ".incbin":
		if b, ok := g.incbinBytes(s); ok {
			g.pc += len(b)
		}
	}
}

func (g *gen) emitDirective(s *ast.Directive, img *image) {
	switch strings.ToLower(s.Name) {
	case ".byte":
		for _, a := range s.Args {
			if sl, ok := a.(*ast.StrLit); ok {
				for i := 0; i < len(sl.Val); i++ {
					img.put(g.pc, sl.Val[i])
					g.pc++
				}
				continue
			}
			v, ok := g.eval(a)
			if ok && !v.FitsByte() {
				g.errf(a.Pos(), "E0010", "value out of range for .byte: "+v.String(), "")
			}
			img.put(g.pc, v.U8())
			g.pc++
		}
	case ".word":
		for _, a := range s.Args {
			if _, ok := a.(*ast.StrLit); ok {
				g.errf(a.Pos(), "E0011", ".word does not take a string argument", "")
				g.pc += 2
				continue
			}
			v, ok := g.eval(a)
			if ok && !v.FitsWord() {
				g.errf(a.Pos(), "E0010", "value out of range for .word: "+v.String(), "")
			}
			w := v.U16()
			img.put(g.pc, byte(w))
			img.put(g.pc+1, byte(w>>8))
			g.pc += 2
		}
	case ".asciiz":
		sl, ok := single(s)
		if !ok {
			g.errf(s.NamePos, "E0012", ".asciiz takes exactly one string literal", "")
			return
		}
		for i := 0; i < len(sl.Val); i++ {
			img.put(g.pc, sl.Val[i])
			g.pc++
		}
		img.put(g.pc, 0)
		g.pc++
	case ".res":
		if len(s.Args) < 1 {
			g.errf(s.NamePos, "E0013", ".res needs a count", "")
			return
		}
		n, okN := g.constArg(s.Args[0])
		var fill expr.Value
		if len(s.Args) >= 2 {
			fill, _ = g.eval(s.Args[1])
		}
		if !okN || n < 0 {
			g.errf(s.NamePos, "E0013", ".res count must be a non-negative constant", "")
			return
		}
		for i := 0; i < int(n); i++ {
			img.put(g.pc, fill.U8())
			g.pc++
		}
	case ".org":
		if len(s.Args) != 1 {
			g.errf(s.NamePos, "E0013", ".org takes one address", "")
			return
		}
		v, ok := g.constArg(s.Args[0])
		if !ok {
			g.errf(s.Args[0].Pos(), "E0013", ".org address must be a constant", "")
			return
		}
		g.pc = int(v)
	case ".incbin":
		b, ok := g.incbinBytes(s)
		if !ok {
			g.errf(s.NamePos, "E0015", ".incbin file unavailable",
				"the front-end must supply binary files")
			return
		}
		for _, by := range b {
			img.put(g.pc, by)
			g.pc++
		}
	case ".include":
		g.errf(s.NamePos, "E0016",
			".include is resolved by the assembler front-end, not codegen", "")
	case ".assert":
		g.emitAssert(s)
	default:
		g.errf(s.NamePos, "E0009",
			"directive "+s.Name+" not supported in v1", "see DESIGN §6.4")
	}
}

// emitAssert implements `.assert cond, error|warning, "message"`
// (DESIGN §6.4). The condition is evaluated against the full symbol
// table; a false (zero) condition produces the requested diagnostic.
func (g *gen) emitAssert(s *ast.Directive) {
	if len(s.Args) < 2 {
		g.errf(s.NamePos, "E0018", ".assert needs a condition and an action", "")
		return
	}
	v, ok := g.eval(s.Args[0])
	if !ok {
		return // unresolved; eval already reported
	}
	action := "error"
	if id, isID := s.Args[1].(*ast.Ident); isID {
		action = strings.ToLower(id.Name)
	}
	msg := ".assert failed"
	if len(s.Args) >= 3 {
		if sl, isStr := s.Args[2].(*ast.StrLit); isStr {
			msg = sl.Val
		}
	}
	if v != 0 {
		return // assertion holds
	}
	sev := diag.SeverityError
	if action == "warning" || action == "ldwarning" {
		sev = diag.SeverityWarning
	}
	g.diags = append(g.diags, diag.Diagnostic{
		Severity: sev, Pos: s.NamePos, Code: "E0019", Message: msg,
	})
}

// renderListing produces the human-readable listing (DESIGN §7.4, §16
// Phase 2). The format is go6asm's own and is not part of the
// byte-identical contract.
func (g *gen) renderListing() string {
	var b strings.Builder
	b.WriteString("Addr  Code               Ln    Source\n")
	b.WriteString("----  -----------------  ----  ------\n")
	for _, r := range g.listRows {
		if !r.pos.IsValid() {
			continue
		}
		p := r.pos.Resolve()
		var code strings.Builder
		for i, by := range r.bytes {
			if i > 0 {
				code.WriteByte(' ')
			}
			fmt.Fprintf(&code, "%02X", by)
		}
		fmt.Fprintf(&b, "%04X  %-17s  %-4d  %s\n",
			uint16(r.pc), code.String(), p.Line, r.pos.File.LineText(p.Line))
	}
	return b.String()
}

func single(s *ast.Directive) (*ast.StrLit, bool) {
	if len(s.Args) != 1 {
		return nil, false
	}
	sl, ok := s.Args[0].(*ast.StrLit)
	return sl, ok
}

// ---- outputs --------------------------------------------------------------

func (g *gen) symbols() []Symbol {
	var out []Symbol
	var walk func(s *scope)
	walk = func(sc *scope) {
		prefix := sc.path()
		if prefix != "" {
			prefix += "::"
		}
		for name, y := range sc.syms {
			if !y.defined {
				continue
			}
			out = append(out, Symbol{
				Name: prefix + name, Addr: uint16(y.val), Kind: y.kind, Pos: y.pos,
			})
		}
		for _, k := range sc.kids {
			walk(k)
		}
	}
	walk(g.root)
	return out
}

// image is a sparse byte buffer keyed by absolute address; gaps from .org
// jumps are zero-filled on materialization.
type image struct {
	origin   uint16
	cells    map[int]byte
	lo, hi   int
	hasBytes bool
}

func newImage() *image { return &image{cells: map[int]byte{}} }

func (im *image) put(addr int, b byte) {
	if addr < 0 || addr > 0xFFFF {
		return
	}
	if !im.hasBytes || addr < im.lo {
		im.lo = addr
	}
	if !im.hasBytes || addr > im.hi {
		im.hi = addr
	}
	im.cells[addr] = b
	im.hasBytes = true
}

func (im *image) bytes() []byte {
	if !im.hasBytes {
		return nil
	}
	im.origin = uint16(im.lo)
	out := make([]byte, im.hi-im.lo+1)
	for a, b := range im.cells {
		out[a-im.lo] = b
	}
	return out
}
