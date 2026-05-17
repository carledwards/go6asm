// Object emission (DESIGN §8, §9). GenerateObject is a parallel driver to
// Generate: instead of one absolute image it produces a relocatable
// object.File — named segments at origin 0, a symbol table with
// export/import attributes, and relocations for every cross-segment or
// imported reference. The flat Generate path is untouched.
//
// v1 object scope (the rest is deferred, see DESIGN §9 resolution note):
// a flat symbol namespace (no .proc/.scope/.org inside an object), the
// directive set .segment/.export[zp]/.import[zp]/.global plus the data
// directives, and relocations of the form  symbol [+|- const]  including
// the <lo / >hi byte selectors. Anything more complex than that in a
// relocatable position is a diagnostic, not a silent miscompile.
package codegen

import (
	"strings"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/expr"
	"github.com/carledwards/go6asm/isa"
	"github.com/carledwards/go6asm/object"
	"github.com/carledwards/go6asm/sema"
	"github.com/carledwards/go6asm/token"
)

// ObjectOptions configures GenerateObject.
type ObjectOptions struct {
	Defines        map[string]int64 // -D constants
	DefaultSegment string           // active segment before any .segment ("" ⇒ "CODE")
}

// ObjectResult is the relocatable object plus diagnostics.
// ObjComment is one source comment, addressed segment-relative so the
// linker can remap it to a final PC once segments are placed.
type ObjComment struct {
	Seg  int    // index into File.Segments
	Off  uint32 // offset within that segment
	Text string
}

type segOff struct {
	seg int
	off uint32
}

type ObjectResult struct {
	File     *object.File
	Comments []ObjComment // per-PC source comments (segment-relative)
	Diags    []diag.Diagnostic
}

// HasErrors reports whether any diagnostic is an error.
func (r ObjectResult) HasErrors() bool {
	for _, d := range r.Diags {
		if d.Severity == diag.SeverityError {
			return true
		}
	}
	return false
}

type oseg struct {
	name string
	buf  []byte
	off  int // current write offset == len(buf) in pass 2
}

// lsym is an in-unit label: which segment and the offset within it.
type lsym struct {
	seg int
	off int32
}

type ogen struct {
	opt   ObjectOptions
	diags []diag.Diagnostic

	segs   []*oseg
	segIdx map[string]int
	cur    int // active segment index

	root  *scope // reused for constant (`=`) resolution
	cheap map[ckey]expr.Value

	labels  map[string]lsym // global label name → placement
	exports map[string]bool
	imports map[string]bool
	globals map[string]bool // .global: export-if-defined-else-import

	assigns  []*ast.Assign
	pending  []pendingReloc
	comments map[segOff]string // (seg,off) → source comment, pass 2
	inPass2  bool

	// During emitInstr, `*` must be the instruction's start address
	// (ca65 semantics), not the live segment offset (which is already
	// past the opcode). curPCSet gates this override; directives keep
	// using the live offset (`.word *` = the word's own address).
	curPC    int32
	curPCSet bool
}

// pcNow is the value of `*`: the instruction-start override while
// emitting an instruction, else the live segment offset.
func (o *ogen) pcNow() int32 {
	if o.curPCSet {
		return o.curPC
	}
	return int32(o.seg().off)
}

func (o *ogen) errf(pos token.Pos, code, msg, hint string) {
	o.diags = append(o.diags, diag.Diagnostic{
		Severity: diag.SeverityError, Pos: pos, Message: msg, Hint: hint, Code: code,
	})
}

// GenerateObject assembles f into a relocatable object file.
func GenerateObject(f *ast.File, opt ObjectOptions) ObjectResult {
	def := opt.DefaultSegment
	if def == "" {
		def = "CODE"
	}
	o := &ogen{
		opt:     opt,
		segIdx:  map[string]int{},
		root:    newScope("", nil),
		cheap:   map[ckey]expr.Value{},
		labels:  map[string]lsym{},
		exports:  map[string]bool{},
		imports:  map[string]bool{},
		globals:  map[string]bool{},
		comments: map[segOff]string{},
	}
	o.segmentByName(def) // index 0, always present

	for name, n := range opt.Defines {
		if v, ok := expr.FromInt64(n); ok {
			o.root.syms[name] = &gsym{val: v, kind: ConstSym, defined: true}
		}
	}

	o.pass1(f)
	o.resolveConsts()
	o.inPass2 = true
	o.pass2(f)

	if o.hasErrors() {
		return ObjectResult{Diags: o.diags}
	}
	res := ObjectResult{File: o.buildFile(), Diags: o.diags}
	for k, text := range o.comments {
		res.Comments = append(res.Comments, ObjComment{Seg: k.seg, Off: k.off, Text: text})
	}
	return res
}

func (o *ogen) hasErrors() bool {
	for _, d := range o.diags {
		if d.Severity == diag.SeverityError {
			return true
		}
	}
	return false
}

func (o *ogen) segmentByName(name string) int {
	if i, ok := o.segIdx[name]; ok {
		return i
	}
	i := len(o.segs)
	o.segs = append(o.segs, &oseg{name: name})
	o.segIdx[name] = i
	return i
}

func (o *ogen) seg() *oseg { return o.segs[o.cur] }

// ---- resolver: constants resolve for real; in-unit labels and imports
// report "unknown" so selectEncoding defaults them to absolute (ca65
// parity) and reloc classification — not the numeric value — drives
// emission.

type oresolver ogen

func (o *ogen) r() sema.Resolver { return (*oresolver)(o) }

func (rv *oresolver) Lookup(ref sema.SymRef) (expr.Value, bool) {
	if len(ref.Scopes) != 0 || ref.Cheap || ref.Global {
		return 0, false // scoping unsupported in object v1
	}
	if y := rv.root.syms[ref.Name]; y != nil && y.defined && y.kind == ConstSym {
		return y.val, true
	}
	return 0, false
}
func (rv *oresolver) CurrentPC() expr.Value                   { return expr.Value((*ogen)(rv).pcNow()) }
func (rv *oresolver) UnnamedRef(bool, int) (expr.Value, bool) { return 0, false }

// constVal evaluates e as a pure constant. A difference of same-segment
// labels (segment bases cancel) is a constant too, so this defers to the
// relocation algebra rather than rejecting anything with a label in it.
func (o *ogen) constVal(e ast.Expr) (expr.Value, bool) {
	if e == nil {
		return 0, false
	}
	if !o.hasRelBase(e) {
		v, ok, _ := sema.Eval(e, o.r())
		return v, ok
	}
	rc, isReloc, ok := o.classify(e)
	if ok && !isReloc {
		return expr.Value(rc.addend), true
	}
	return 0, false
}

// ---- pass 1: place labels, size everything ---------------------------------

func (o *ogen) pass1(f *ast.File) {
	o.cur = 0
	for i := range o.segs {
		o.segs[i].off = 0
	}
	for _, st := range f.Stmts {
		switch s := st.(type) {
		case *ast.LabelDecl:
			o.declLabel(s)
		case *ast.Assign:
			o.assigns = append(o.assigns, s)
			y := o.root.syms[s.Name]
			if y == nil {
				y = &gsym{kind: ConstSym}
				o.root.syms[s.Name] = y
			}
			// Evaluate inline (as the flat path does) so a constant
			// defined before its use is known during pass-1 sizing —
			// otherwise `LDA X` mis-sizes as absolute here and as
			// zero-page in pass 2. resolveConsts still runs after for
			// forward / label-relative cases.
			if !y.defined {
				if v, ok := o.constVal(s.Value); ok {
					y.val, y.defined = v, true
				}
			}
		case *ast.Instruction:
			o.seg().off += o.sizeInstr(s)
		case *ast.Directive:
			o.sizeDirective(s)
		}
	}
}

func (o *ogen) declLabel(s *ast.LabelDecl) {
	if s.Kind != ast.GlobalLabel {
		o.errf(s.NamePos, "E0023",
			"cheap/unnamed labels are not supported in object mode v1",
			"use named labels in code assembled to .o6")
		return
	}
	if o.inPass2 {
		return
	}
	if _, dup := o.labels[s.Name]; dup {
		o.errf(s.NamePos, "E0002", "label "+s.Name+" redefined", "")
		return
	}
	o.labels[s.Name] = lsym{seg: o.cur, off: int32(o.seg().off)}
}

func (o *ogen) sizeInstr(s *ast.Instruction) int {
	v, known := o.constVal(s.Operand.Expr)
	enc, _, ok, _ := (&gen{}).selectEncoding(s, v, known)
	if !ok || enc.Bytes == 0 {
		return 1
	}
	return enc.Bytes
}

func (o *ogen) resolveConsts() {
	for it := 0; it <= len(o.assigns)+1; it++ {
		progress := false
		for _, a := range o.assigns {
			y := o.root.syms[a.Name]
			if y == nil || y.kind != ConstSym {
				continue
			}
			if v, ok := o.constVal(a.Value); ok && (!y.defined || y.val != v) {
				y.val, y.defined, progress = v, true, true
			}
		}
		if !progress {
			break
		}
	}
}

// ---- pass 2: emit bytes + relocations --------------------------------------

func (o *ogen) pass2(f *ast.File) {
	o.cur = 0
	for i := range o.segs {
		o.segs[i].buf = o.segs[i].buf[:0]
		o.segs[i].off = 0
	}
	for _, st := range f.Stmts {
		seg, off := o.cur, int32(o.seg().off)
		switch s := st.(type) {
		case *ast.LabelDecl:
			o.declLabel(s)
		case *ast.Assign:
			// finalized by resolveConsts
		case *ast.Instruction:
			o.emitInstr(s)
		case *ast.Directive:
			o.emitDirective(s)
		}
		// Capture the source comment for any statement that emitted
		// bytes in the same segment (so it survives object→link into
		// the disassembly, DESIGN §16 annotated disassembler).
		if o.cur == seg && int32(o.seg().off) > off {
			o.captureComment(stmtPos(st), seg, uint32(off))
		}
	}
}

// captureComment mirrors the flat path's rule (codegen.go): a trailing
// `; …` on the statement's line, else the nearest comment-only line
// directly above it.
func (o *ogen) captureComment(pos token.Pos, seg int, off uint32) {
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
		o.comments[segOff{seg, off}] = c
	}
}

func (o *ogen) put(b byte) {
	s := o.seg()
	s.buf = append(s.buf, b)
	s.off++
}

func (o *ogen) emitInstr(s *ast.Instruction) {
	// `*` in this instruction's operand = the instruction's start
	// address (ca65), even though o.put advances the live offset.
	o.curPC, o.curPCSet = int32(o.seg().off), true
	defer func() { o.curPCSet = false }()

	v, known := o.constVal(s.Operand.Expr)
	enc, rel, ok, dg := (&gen{}).selectEncoding(s, v, known)
	if !ok {
		if dg != nil {
			o.diags = append(o.diags, *dg)
		}
		return
	}
	o.put(enc.Opcode)
	switch enc.Mode {
	case isa.Implied, isa.Accumulator:
		return
	}

	if rel { // relative branch: same-segment offset math, no reloc
		o.emitBranch(s)
		return
	}

	rc, isRel, ok := o.classify(s.Operand.Expr)
	if !ok {
		o.errf(s.Operand.Expr.Pos(), "E0026",
			"relocatable expression too complex for the object format",
			"only  symbol [+|- constant]  (with optional <lo / >hi) is representable")
		return
	}
	switch enc.Mode {
	case isa.Immediate, isa.ZeroPage, isa.ZeroPageX, isa.ZeroPageY,
		isa.IndexedIndirect, isa.IndirectIndexed:
		if isRel {
			if rc.rt == object.RelWord {
				o.errf(s.Operand.Expr.Pos(), "E0010",
					"a relocatable address needs <lo / >hi in a one-byte operand", "")
				return
			}
			o.reloc(rc, 1)
			o.put(byte(rc.addend))
			return
		}
		if !v.FitsByte() {
			o.errf(s.Operand.Expr.Pos(), "E0010",
				"operand out of range for "+enc.Mode.String()+": "+v.String(), "")
			return
		}
		o.put(v.U8())
	case isa.Absolute, isa.AbsoluteX, isa.AbsoluteY, isa.Indirect:
		if isRel {
			o.reloc(rc, 2)
			o.put(byte(rc.addend))
			o.put(byte(rc.addend >> 8))
			return
		}
		w := v.U16()
		o.put(byte(w))
		o.put(byte(w >> 8))
	}
}

func (o *ogen) emitBranch(s *ast.Instruction) {
	id, ok := bareIdent(s.Operand.Expr)
	if !ok {
		o.errf(s.Operand.Expr.Pos(), "E0014",
			"branch target must be a label in object mode", "")
		o.put(0)
		return
	}
	l, ok := o.labels[id.Name]
	if !ok {
		o.errf(id.NamePos, "E0014",
			"branch to undefined or external label "+id.Name,
			"branches cannot cross object boundaries")
		o.put(0)
		return
	}
	if l.seg != o.cur {
		o.errf(id.NamePos, "E0014",
			"branch target "+id.Name+" is in a different segment", "")
		o.put(0)
		return
	}
	// disp from the byte after the operand to the target offset.
	disp := int(l.off) - (o.seg().off + 1)
	if disp < -128 || disp > 127 {
		o.errf(s.MnemonicPos, "E0014", "branch out of range",
			"target is too far; use the inverse branch + JMP")
		o.put(0)
		return
	}
	o.put(byte(int8(disp)))
}

// ---- relocation classification --------------------------------------------

type relocClass struct {
	sym    int // index into the object symbol table (filled at build time)
	name   string
	imp    bool // imported symbol vs in-unit anchor
	seg    int  // in-unit: anchor segment
	addend int32
	rt     object.RelType
}

// relTerm identifies a relocation base: an in-unit segment (by index) or
// an imported symbol (by name).
type relTerm struct {
	imp  bool
	seg  int
	name string
}

// relAcc accumulates a relocatable expression as Σ coeff·base + addend
// (ca65's relocatable-expression algebra). rt is the <lo/>hi selector.
type relAcc struct {
	terms  map[relTerm]int
	addend int32
	rt     object.RelType
}

// hasRelBase reports whether e mentions any in-unit label, import, or *
// — i.e. anything whose final value the linker, not the assembler, fixes.
func (o *ogen) hasRelBase(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.Paren:
		return o.hasRelBase(n.X)
	case *ast.Unary:
		return o.hasRelBase(n.X)
	case *ast.Binary:
		return o.hasRelBase(n.X) || o.hasRelBase(n.Y)
	case *ast.CurPC:
		return true
	case *ast.Ident:
		if _, ok := o.labels[n.Name]; ok {
			return true
		}
		return o.isImport(n.Name)
	}
	return false
}

// analyze walks e, folding pure-constant subtrees into a.addend and
// relocatable bases into a.terms with signed coefficients. sign carries
// ±1 through subtraction. ok is false if a base flows into an operation
// that cannot be represented as a relocation (multiply, bitwise, …) or
// the expression references an unresolved symbol.
func (o *ogen) analyze(e ast.Expr, sign int32, a *relAcc) bool {
	if !o.hasRelBase(e) { // pure-constant subtree
		v, ok, _ := sema.Eval(e, o.r())
		if !ok {
			return false
		}
		a.addend += sign * int32(v)
		return true
	}
	switch n := e.(type) {
	case *ast.Paren:
		return o.analyze(n.X, sign, a)
	case *ast.Unary:
		switch n.Op {
		case token.Plus:
			return o.analyze(n.X, sign, a)
		case token.Minus:
			return o.analyze(n.X, -sign, a)
		case token.Lt: // < low byte
			a.rt = object.RelByteLo
			return o.analyze(n.X, sign, a)
		case token.Gt: // > high byte
			a.rt = object.RelByteHi
			return o.analyze(n.X, sign, a)
		}
		return false // ~ / ! over a relocatable base: not representable
	case *ast.Binary:
		switch n.Op {
		case token.Plus:
			return o.analyze(n.X, sign, a) && o.analyze(n.Y, sign, a)
		case token.Minus:
			return o.analyze(n.X, sign, a) && o.analyze(n.Y, -sign, a)
		}
		return false // *, /, &, |, ^, <<, >> with a base: not representable
	case *ast.Ident:
		if l, ok := o.labels[n.Name]; ok {
			a.terms[relTerm{seg: l.seg, name: o.segs[l.seg].name}] += int(sign)
			a.addend += sign * l.off
			return true
		}
		if o.isImport(n.Name) {
			a.terms[relTerm{imp: true, name: n.Name}] += int(sign)
			return true
		}
		return false
	case *ast.CurPC:
		a.terms[relTerm{seg: o.cur, name: o.segs[o.cur].name}] += int(sign)
		a.addend += sign * o.pcNow()
		return true
	}
	return false
}

// classify reduces e to exactly one of: a pure constant (isReloc=false,
// value in rc.addend), a single +1·base relocation (isReloc=true), or an
// unrepresentable relocatable expression (ok=false). Cancelled bases —
// e.g. a same-segment label difference — net to a constant.
func (o *ogen) classify(e ast.Expr) (rc relocClass, isReloc, ok bool) {
	a := relAcc{terms: map[relTerm]int{}, rt: object.RelWord}
	if !o.analyze(e, 1, &a) {
		return relocClass{}, false, false
	}
	var live []relTerm
	for k, c := range a.terms {
		if c != 0 {
			live = append(live, k)
		}
	}
	rc.addend, rc.rt = a.addend, a.rt
	switch {
	case len(live) == 0:
		return rc, false, true
	case len(live) == 1 && a.terms[live[0]] == 1:
		k := live[0]
		rc.imp, rc.seg, rc.name = k.imp, k.seg, k.name
		return rc, true, true
	default: // 2·base, two distinct segments, base−2·base, …
		return relocClass{}, false, false
	}
}

func (o *ogen) reloc(rc relocClass, size uint8) {
	s := o.seg()
	r := object.Reloc{
		Offset: uint32(s.off),
		Type:   rc.rt,
		Size:   size,
		Addend: rc.addend,
	}
	// SymIndex is bound in buildFile via this marker map.
	o.pending = append(o.pending, pendingReloc{seg: o.cur, r: r, rc: rc})
}

type pendingReloc struct {
	seg int
	r   object.Reloc
	rc  relocClass
}

func (o *ogen) isImport(name string) bool {
	if o.imports[name] {
		return true
	}
	if o.globals[name] {
		_, defined := o.labels[name]
		return !defined
	}
	return false
}

func bareIdent(e ast.Expr) (*ast.Ident, bool) {
	if p, ok := e.(*ast.Paren); ok {
		return bareIdent(p.X)
	}
	id, ok := e.(*ast.Ident)
	return id, ok
}

// ---- directives ------------------------------------------------------------

func (o *ogen) sizeDirective(s *ast.Directive) {
	switch strings.ToLower(s.Name) {
	case ".segment":
		o.switchSegment(s)
	case ".byte", ".byt":
		for _, a := range s.Args {
			if sl, ok := a.(*ast.StrLit); ok {
				o.seg().off += len(sl.Val)
			} else {
				o.seg().off++
			}
		}
	case ".word", ".addr", ".dbyt":
		o.seg().off += 2 * len(s.Args)
	case ".asciiz":
		if sl, ok := single(s); ok {
			o.seg().off += len(sl.Val) + 1
		}
	case ".res":
		if len(s.Args) >= 1 {
			if n, ok := o.constVal(s.Args[0]); ok && n >= 0 {
				o.seg().off += int(n)
			}
		}
	case ".export", ".exportzp", ".import", ".importzp", ".global",
		".globalzp", ".assert", ".proc", ".scope", ".endproc", ".endscope":
		// no bytes
	}
}

func (o *ogen) switchSegment(s *ast.Directive) {
	if len(s.Args) < 1 {
		o.errf(s.NamePos, "E0006", ".segment requires a name", "")
		return
	}
	sl, ok := s.Args[0].(*ast.StrLit)
	if !ok {
		o.errf(s.NamePos, "E0006", ".segment name must be a quoted string", "")
		return
	}
	o.cur = o.segmentByName(sl.Val)
}

func (o *ogen) names(s *ast.Directive) []string {
	var out []string
	for _, a := range s.Args {
		if id, ok := a.(*ast.Ident); ok {
			out = append(out, id.Name)
		} else {
			o.errf(a.Pos(), "E0006", s.Name+" takes symbol names", "")
		}
	}
	return out
}

func (o *ogen) emitDirective(s *ast.Directive) {
	switch strings.ToLower(s.Name) {
	case ".segment":
		o.switchSegment(s)
	case ".export", ".exportzp":
		for _, n := range o.names(s) {
			o.exports[n] = true
		}
	case ".import", ".importzp":
		for _, n := range o.names(s) {
			o.imports[n] = true
		}
	case ".global", ".globalzp":
		for _, n := range o.names(s) {
			o.globals[n] = true
		}
	case ".byte", ".byt":
		o.emitByteList(s)
	case ".word", ".addr", ".dbyt":
		o.emitWordList(s)
	case ".asciiz":
		sl, ok := single(s)
		if !ok {
			o.errf(s.NamePos, "E0012", ".asciiz takes one string literal", "")
			return
		}
		for i := 0; i < len(sl.Val); i++ {
			o.put(sl.Val[i])
		}
		o.put(0)
	case ".res":
		o.emitRes(s)
	case ".assert":
		o.emitObjAssert(s)
	case ".proc", ".scope", ".endproc", ".endscope":
		o.errf(s.NamePos, "E0023",
			s.Name+" is not supported in object mode v1",
			"object files use a flat symbol namespace")
	case ".org":
		o.errf(s.NamePos, "E0023",
			".org has no meaning in a relocatable object",
			"place the segment with a linker config instead")
	case ".include":
		o.errf(s.NamePos, "E0016",
			".include is resolved by the assembler front-end, not codegen", "")
	default:
		o.errf(s.NamePos, "E0009",
			"directive "+s.Name+" not supported in object mode v1", "see DESIGN §9")
	}
}

func (o *ogen) emitByteList(s *ast.Directive) {
	for _, a := range s.Args {
		if sl, ok := a.(*ast.StrLit); ok {
			for i := 0; i < len(sl.Val); i++ {
				o.put(sl.Val[i])
			}
			continue
		}
		if v, ok := o.constVal(a); ok {
			if !v.FitsByte() {
				o.errf(a.Pos(), "E0010", "value out of range for .byte: "+v.String(), "")
			}
			o.put(v.U8())
			continue
		}
		rc, isRel, ok := o.classify(a)
		if !ok {
			o.errf(a.Pos(), "E0026",
				"relocatable expression too complex for the object format", "")
			o.put(0)
			continue
		}
		if !isRel || rc.rt == object.RelWord {
			o.errf(a.Pos(), "E0010",
				"a relocatable .byte value needs a <lo / >hi selector", "")
			o.put(0)
			continue
		}
		o.reloc(rc, 1)
		o.put(byte(rc.addend))
	}
}

func (o *ogen) emitWordList(s *ast.Directive) {
	for _, a := range s.Args {
		if _, ok := a.(*ast.StrLit); ok {
			o.errf(a.Pos(), "E0011", ".word does not take a string argument", "")
			o.put(0)
			o.put(0)
			continue
		}
		if v, ok := o.constVal(a); ok {
			if !v.FitsWord() {
				o.errf(a.Pos(), "E0010", "value out of range for .word: "+v.String(), "")
			}
			w := v.U16()
			o.put(byte(w))
			o.put(byte(w >> 8))
			continue
		}
		rc, isRel, ok := o.classify(a)
		if !ok || !isRel {
			o.errf(a.Pos(), "E0026",
				"relocatable .word value too complex for the object format", "")
			o.put(0)
			o.put(0)
			continue
		}
		o.reloc(rc, 2)
		o.put(byte(rc.addend))
		o.put(byte(rc.addend >> 8))
	}
}

func (o *ogen) emitRes(s *ast.Directive) {
	if len(s.Args) < 1 {
		o.errf(s.NamePos, "E0013", ".res needs a count", "")
		return
	}
	n, okN := o.constVal(s.Args[0])
	var fill expr.Value
	if len(s.Args) >= 2 {
		fill, _ = o.constVal(s.Args[1])
	}
	if !okN || n < 0 {
		o.errf(s.NamePos, "E0013", ".res count must be a non-negative constant", "")
		return
	}
	for i := 0; i < int(n); i++ {
		o.put(fill.U8())
	}
}

func (o *ogen) emitObjAssert(s *ast.Directive) {
	if len(s.Args) < 2 {
		o.errf(s.NamePos, "E0018", ".assert needs a condition and an action", "")
		return
	}
	v, ok := o.constVal(s.Args[0])
	if !ok || v != 0 {
		return // unresolved or holds
	}
	sev := diag.SeverityError
	if id, isID := s.Args[1].(*ast.Ident); isID {
		a := strings.ToLower(id.Name)
		if a == "warning" || a == "ldwarning" {
			sev = diag.SeverityWarning
		}
	}
	msg := ".assert failed"
	if len(s.Args) >= 3 {
		if sl, isStr := s.Args[2].(*ast.StrLit); isStr {
			msg = sl.Val
		}
	}
	o.diags = append(o.diags, diag.Diagnostic{
		Severity: sev, Pos: s.NamePos, Code: "E0019", Message: msg,
	})
}

// ---- assemble the object.File ---------------------------------------------

func (o *ogen) buildFile() *object.File {
	f := &object.File{}
	for _, sg := range o.segs {
		f.Segments = append(f.Segments, object.Segment{
			Name:        sg.name,
			Relocatable: true,
			Bytes:       append([]byte(nil), sg.buf...),
		})
	}

	symIdx := map[string]int{}            // export/import name → File.Symbols index
	anchorIdx := make([]int, len(o.segs)) // per-segment anchor symbol index
	for i := range anchorIdx {
		anchorIdx[i] = -1
	}

	addSym := func(s object.Symbol) int {
		f.Symbols = append(f.Symbols, s)
		return len(f.Symbols) - 1
	}
	// constVal reports a name that is a defined pure-constant `=`
	// assignment (resolveConsts has run). A constant whose value is
	// label-relative is not "pure" (constVal/classify rejected it) and
	// is intentionally not surfaced — it would be an address alias,
	// not a constant.
	constVal := func(name string) (int32, bool) {
		if y := o.root.syms[name]; y != nil && y.defined && y.kind == ConstSym {
			return int32(y.val), true
		}
		return 0, false
	}

	// Exported labels: real placement, importable by name.
	for name, l := range o.labels {
		flags := object.SymLabel
		if o.exports[name] || (o.globals[name]) {
			flags |= object.SymExported
		} else {
			flags |= object.SymLocal
		}
		symIdx[name] = addSym(object.Symbol{
			Name: name, Value: l.off, SegIndex: l.seg, Flags: flags,
		})
	}
	// Exported constants (DESIGN §16 4b): a `name = $XX` that is
	// .export/.exportzp/.global-marked surfaces as an absolute symbol
	// (SegIndex -1 ⇒ the linker classifies it SymKindConst). This is
	// ca65's visibility rule — an un-exported constant stays local —
	// and is what feeds a consumer's memory-label view (e.g. sim-tui
	// naming ZP variables X / COUNTER / BALL_*).
	for name := range o.exports {
		if _, isLabel := o.labels[name]; isLabel {
			continue
		}
		if v, ok := constVal(name); ok {
			symIdx[name] = addSym(object.Symbol{
				Name: name, Value: v, SegIndex: -1, Flags: object.SymExported,
			})
		}
	}
	// Imported externals.
	for name := range o.imports {
		if _, ok := o.labels[name]; ok {
			continue
		}
		if _, ok := symIdx[name]; ok {
			continue
		}
		symIdx[name] = addSym(object.Symbol{
			Name: name, SegIndex: -1, Flags: object.SymImported,
		})
	}
	for name := range o.globals {
		if _, defined := o.labels[name]; defined {
			continue
		}
		if _, ok := symIdx[name]; ok {
			continue
		}
		// .global of a defined constant ⇒ exported constant; of an
		// undefined name ⇒ an import (unchanged).
		if v, ok := constVal(name); ok {
			symIdx[name] = addSym(object.Symbol{
				Name: name, Value: v, SegIndex: -1, Flags: object.SymExported,
			})
			continue
		}
		symIdx[name] = addSym(object.Symbol{
			Name: name, SegIndex: -1, Flags: object.SymImported,
		})
	}

	anchor := func(seg int) int {
		if anchorIdx[seg] >= 0 {
			return anchorIdx[seg]
		}
		i := addSym(object.Symbol{
			Name:     "\x01seg:" + o.segs[seg].name,
			SegIndex: seg,
			Flags:    object.SymLocal | object.SymLabel,
		})
		anchorIdx[seg] = i
		return i
	}

	for _, p := range o.pending {
		r := p.r
		if p.rc.imp {
			r.SymIndex = symIdx[p.rc.name]
		} else {
			r.SymIndex = anchor(p.rc.seg)
		}
		f.Segments[p.seg].Relocs = append(f.Segments[p.seg].Relocs, r)
	}
	return f
}
