package asm

import (
	"fmt"
	"strings"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/sema"
	"github.com/carledwards/go6asm/target"
	"github.com/carledwards/go6asm/token"
)

// assembleLayer0 implements the §11 "Light" inference set: pick the load
// address from the target, auto-generate the NMI/RESET/IRQ vector block,
// and inject the target's platform symbol pack. It rewrites the program
// into an explicit object/link build and delegates to assembleObject, so
// there is one code path producing bytes (no parser hacks — DESIGN
// §11.8). Every inference is recorded as a SeverityInfo diagnostic.
func assembleLayer0(prog *ast.File, in Input, res *Result) []diag.Diagnostic {
	var diags []diag.Diagnostic
	info := func(code, msg string) {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.SeverityInfo, Code: code, Message: msg,
		})
	}
	fail := func(code, msg, hint string) []diag.Diagnostic {
		return append(diags, diag.Diagnostic{
			Severity: diag.SeverityError, Code: code, Message: msg, Hint: hint,
		})
	}

	// Scan for Layer-1 directives and the inference-off triggers
	// (DESIGN §11.3). .target/.load/.org are consumed here; any user
	// .segment means "I'm driving" — Layer 2, which Layer-0 mode can't
	// infer a config for.
	var (
		kept       []ast.Stmt
		tgtName    = in.Target
		loadSet    bool
		loadAddr   uint16
		loadReason string
	)
	for _, st := range prog.Stmts {
		d, ok := st.(*ast.Directive)
		if !ok {
			kept = append(kept, st)
			continue
		}
		switch strings.ToLower(d.Name) {
		case ".target":
			if n, ok := bareName(d); ok {
				tgtName = n
			} else {
				return fail("E0027", ".target needs a name", "e.g. .target sim-tui")
			}
		case ".load", ".org":
			v, ok := dirAddr(d)
			if !ok {
				return fail("E0027", d.Name+" needs an address", "e.g. .load $0600")
			}
			loadSet, loadAddr, loadReason = true, v, d.Name+" directive"
		case ".segment":
			return fail("E0028",
				"this source uses .segment — that is Layer 2 (you are driving)",
				"assemble it explicitly with -t/-C instead of Layer-0 inference")
		default:
			kept = append(kept, st)
		}
	}

	tgt, ok := target.Builtin(tgtName)
	if !ok {
		return fail("E0025", "unknown target "+quote(tgtName),
			"built-in targets: "+strings.Join(target.Names(), ", "))
	}
	info("I0001", fmt.Sprintf("target %q selected", tgt.Name))

	if !loadSet {
		loadAddr, loadReason = tgt.CodeOrigin, "target default code region"
	}
	info("I0002", fmt.Sprintf("load address $%04X (%s)", loadAddr, loadReason))

	// Platform symbol pack: target constants seed the define set; the
	// user's -D and source `=` still win (merged so user keys override).
	defs := map[string]int64{}
	for k, v := range tgt.Symbols {
		defs[k] = v
	}
	for k, v := range in.Defines {
		defs[k] = v
	}

	// Synthesize the vector block unless the target has none (raw).
	stmts := kept
	if tgt.HasVectors {
		stmts = append([]ast.Stmt{labelDecl("__go6_reset")}, stmts...)
		stmts = append(stmts,
			directive(".segment", strLit("VECTORS")),
			directive(".word", intLit("0"), identExpr("__go6_reset"), intLit("0")),
		)
		info("I0003", fmt.Sprintf(
			"vectors auto-generated at $%04X (NMI→$0000, RESET→$%04X, IRQ→$0000)",
			tgt.VectorAddr, loadAddr))
	} else {
		info("I0003", "no vector block (raw target)")
	}

	cfg := layer0Config(loadAddr, tgt)

	oin := in
	oin.Layer0 = false
	oin.Defines = defs
	oin.ConfigSrc = []byte(cfg)
	oin.Target = ""
	return append(diags, assembleObject(&ast.File{Name: prog.Name, Stmts: stmts}, oin, res)...)
}

// layer0Config builds the ld65-style cfg the inference links through: a
// single flat region, CODE pinned at the load address, and (for a
// vectored target) a VECTORS segment at the target's vector base.
func layer0Config(load uint16, t *target.Target) string {
	var b strings.Builder
	b.WriteString("MEMORY { MAIN: start=$0000, size=$10000, type=rw, fill=yes, fillval=$00; }\n")
	b.WriteString("SEGMENTS {\n")
	fmt.Fprintf(&b, "  CODE: load=MAIN, type=rw, start=$%04X;\n", load)
	if t.HasVectors {
		fmt.Fprintf(&b, "  VECTORS: load=MAIN, type=rw, start=$%04X;\n", t.VectorAddr)
	}
	b.WriteString("}\n")
	return b.String()
}

// ---- tiny AST builders for synthesized statements (token.NoPos) -----------

func labelDecl(name string) *ast.LabelDecl {
	return &ast.LabelDecl{Kind: ast.GlobalLabel, Name: name, NamePos: token.NoPos}
}
func directive(name string, args ...ast.Expr) *ast.Directive {
	return &ast.Directive{Name: name, NamePos: token.NoPos, Args: args}
}
func strLit(v string) *ast.StrLit   { return &ast.StrLit{Val: v} }
func intLit(lit string) *ast.IntLit { return &ast.IntLit{Lit: lit} }
func identExpr(n string) *ast.Ident { return &ast.Ident{Name: n} }

// bareName extracts a single identifier argument (.target sim-tui).
func bareName(d *ast.Directive) (string, bool) {
	if len(d.Args) == 1 {
		if id, ok := d.Args[0].(*ast.Ident); ok && len(id.Scopes) == 0 {
			return id.Name, true
		}
		if s, ok := d.Args[0].(*ast.StrLit); ok {
			return s.Val, true
		}
	}
	return "", false
}

// dirAddr evaluates a single numeric-literal argument (.load $0600).
func dirAddr(d *ast.Directive) (uint16, bool) {
	if len(d.Args) != 1 {
		return 0, false
	}
	lit, ok := d.Args[0].(*ast.IntLit)
	if !ok {
		return 0, false
	}
	v, _, ok := sema.ParseIntLit(lit.Lit)
	if !ok {
		return 0, false
	}
	return uint16(v), true
}

func quote(s string) string { return `"` + s + `"` }
