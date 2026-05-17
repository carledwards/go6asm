// Package asm is the top-level library entry point (DESIGN §7.5). It
// wires scanner → parser → codegen, flattens .include directives from
// in-memory source, and resolves .incbin from the same in-memory files.
//
// It never touches the filesystem — source arrives as bytes — so it is
// wasm-clean and behaves identically on native and js/wasm (DESIGN §4,
// §10.2). The host (CLI, web editor) is responsible for loading files.
//
// Phase 1 scope: direct binary only. Object/Listing/Comments fields from
// the design's Result land in their later phases.
package asm

import (
	"bytes"
	"strings"

	"github.com/carledwards/go6asm/ast"
	"github.com/carledwards/go6asm/codegen"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/expand"
	"github.com/carledwards/go6asm/link"
	"github.com/carledwards/go6asm/memmap"
	"github.com/carledwards/go6asm/object"
	"github.com/carledwards/go6asm/parser"
	"github.com/carledwards/go6asm/scanner"
	"github.com/carledwards/go6asm/token"
)

// SourceFile is one named in-memory file (source or binary for .incbin).
type SourceFile struct {
	Name    string
	Content []byte
}

// Input is an assembly request. No filesystem is consulted; .include and
// .incbin resolve against Files by name.
type Input struct {
	Files        []SourceFile
	Entry        string           // defaults to the sole file, else "main.s"
	Origin       uint16           // start PC when no .org precedes the first byte
	Defines      map[string]int64 // -D name=value
	AllowIllegal bool             // accepted; undocumented NMOS ops are post-v1
	Listing      bool             // produce Result.Listing

	// Object/link mode (DESIGN §8, §9). When Target or ConfigSrc is set
	// the unit is assembled to a relocatable object and linked; the flat
	// path (both empty) is unchanged. Target "" or "raw" ⇒ flat.
	Target     string // built-in memmap config name (e.g. "sim-tui", "flat-64k")
	ConfigSrc  []byte // explicit ld65-style cfg; overrides Target
	EmitObject bool   // also populate Result.Object with the serialized .o6

	// Layer0 enables progressive-disclosure inference (DESIGN §11): pick
	// the load address from the target, auto-generate the NMI/RESET/IRQ
	// vector block, and inject the target's platform symbol pack. Off ⇒
	// today's explicit behavior (protects the byte-identical contract).
	Layer0 bool
}

// Result is the assembled output (DESIGN §7.5, Phase-1 subset).
type Result struct {
	Image    []byte
	Origin   uint16
	Symbols  []codegen.Symbol
	Comments map[uint16]string // PC → source comment (flat path; DESIGN §10.2)
	Listing  string
	Object   []byte // serialized .o6, when Input.EmitObject is set
	Errors   []diag.Diagnostic
	Warnings []diag.Diagnostic
	// Infos records every Layer-0 inference (DESIGN §11.5): non-blocking,
	// silent by default, surfaced by --explain.
	Infos []diag.Diagnostic
}

// Ok reports whether assembly produced no errors.
func (r Result) Ok() bool { return len(r.Errors) == 0 }

// Assemble runs the full pipeline.
func Assemble(in Input) Result {
	files := make(map[string][]byte, len(in.Files))
	for _, f := range in.Files {
		files[f.Name] = f.Content
	}

	entry := in.Entry
	if entry == "" {
		if len(in.Files) == 1 {
			entry = in.Files[0].Name
		} else {
			entry = "main.s"
		}
	}

	fs := token.NewFileSet()
	var diags []diag.Diagnostic
	stmts := flatten(entry, files, fs, map[string]bool{}, &diags, token.NoPos, in.Defines)

	prog := &ast.File{Name: entry, Stmts: stmts}

	var res Result
	if in.Layer0 {
		diags = append(diags, assembleLayer0(prog, in, &res)...)
	} else if in.objectMode() {
		diags = append(diags, assembleObject(prog, in, &res)...)
	} else {
		cg := codegen.Generate(prog, codegen.Options{
			Origin:  in.Origin,
			Incbin:  binResolver(files),
			Defines: in.Defines,
			Listing: in.Listing,
		})
		diags = append(diags, cg.Diags...)
		res.Image, res.Origin, res.Symbols = cg.Image, cg.Origin, cg.Symbols
		res.Comments = cg.Comments
		res.Listing = cg.Listing
	}

	for _, d := range diags {
		switch d.Severity {
		case diag.SeverityError:
			res.Errors = append(res.Errors, d)
		case diag.SeverityWarning:
			res.Warnings = append(res.Warnings, d)
		case diag.SeverityInfo:
			res.Infos = append(res.Infos, d)
		}
	}
	return res
}

func (in Input) objectMode() bool {
	return in.EmitObject || len(in.ConfigSrc) > 0 ||
		(in.Target != "" && in.Target != "raw")
}

// assembleObject runs the relocatable path: source → object → (optional)
// link. It fills res.Object/Image/Origin/Symbols and returns diagnostics.
func assembleObject(prog *ast.File, in Input, res *Result) []diag.Diagnostic {
	or := codegen.GenerateObject(prog, codegen.ObjectOptions{Defines: in.Defines})
	diags := or.Diags
	if or.File == nil { // had errors; nothing to serialize or link
		return diags
	}

	if in.EmitObject {
		var buf bytes.Buffer
		if err := or.File.Write(&buf); err != nil {
			return append(diags, diag.Diagnostic{
				Severity: diag.SeverityError, Code: "E0024",
				Message: "serializing object: " + err.Error(),
			})
		}
		res.Object = buf.Bytes()
	}

	cfg, cerr := resolveConfig(in)
	if cerr != nil {
		return append(diags, *cerr)
	}
	if cfg == nil { // EmitObject only, no link requested
		return diags
	}

	lk := link.Link([]*object.File{or.File}, cfg)
	diags = append(diags, lk.Diags...)
	if lk.HasErrors() {
		return diags
	}
	res.Image, res.Origin = lk.Image, lk.Origin

	// Placement per segment index (single translation unit ⇒ file 0).
	pl := map[int]link.Placement{}
	for _, p := range lk.Placements {
		if p.File == 0 {
			pl[p.SegIdx] = p
		}
	}
	seen := map[string]bool{}
	// All labels (local + exported) at their final addresses, so the
	// disassembler can name every branch/JSR target — not just exports.
	for _, s := range or.File.Symbols {
		if s.Flags&object.SymLabel == 0 || s.SegIndex < 0 {
			continue
		}
		if len(s.Name) > 0 && s.Name[0] == '\x01' {
			continue // internal per-segment anchor
		}
		p, ok := pl[s.SegIndex]
		if !ok {
			continue
		}
		res.Symbols = append(res.Symbols, codegen.Symbol{
			Name: s.Name, Addr: uint16(int(p.Addr) + int(s.Value)), Kind: codegen.LabelSym,
		})
		seen[s.Name] = true
	}
	// Exported constants (not labels, not placed) from the linker.
	for _, s := range lk.Symbols {
		if s.Kind != link.SymKindConst || seen[s.Name] {
			continue
		}
		res.Symbols = append(res.Symbols, codegen.Symbol{
			Name: s.Name, Addr: uint16(s.Value), Kind: codegen.ConstSym,
		})
		seen[s.Name] = true
	}
	// Remap segment-relative source comments to final PCs.
	if len(or.Comments) > 0 {
		res.Comments = make(map[uint16]string, len(or.Comments))
		for _, c := range or.Comments {
			if p, ok := pl[c.Seg]; ok {
				res.Comments[uint16(int(p.Addr)+int(c.Off))] = c.Text
			}
		}
	}
	return diags
}

// resolveConfig picks the memory map: an explicit ConfigSrc wins, else a
// built-in Target, else nil (no link).
func resolveConfig(in Input) (*memmap.Config, *diag.Diagnostic) {
	switch {
	case len(in.ConfigSrc) > 0:
		c, err := memmap.Parse(in.ConfigSrc)
		if err != nil {
			return nil, &diag.Diagnostic{Severity: diag.SeverityError,
				Code: "E0025", Message: "linker config: " + err.Error()}
		}
		return c, nil
	case in.Target != "" && in.Target != "raw":
		c, err := memmap.Builtin(in.Target)
		if err != nil {
			return nil, &diag.Diagnostic{Severity: diag.SeverityError,
				Code: "E0025", Message: err.Error(),
				Hint: "built-in targets: flat-64k, sim-tui"}
		}
		return c, nil
	}
	return nil, nil
}

// flatten parses name and splices any .include targets in place,
// recursively. visited holds the files currently on the include stack so
// cycles are blocked while diamond includes are still allowed.
func flatten(name string, files map[string][]byte, fs *token.FileSet,
	visited map[string]bool, diags *[]diag.Diagnostic, ref token.Pos,
	defs map[string]int64) []ast.Stmt {

	content, ok := files[name]
	if !ok {
		*diags = append(*diags, diag.Diagnostic{
			Severity: diag.SeverityError, Pos: ref, Code: "E0020",
			Message: "cannot find included file " + name,
			Hint:    "pass it to the assembler (the core never reads the filesystem)",
		})
		return nil
	}
	if visited[name] {
		*diags = append(*diags, diag.Diagnostic{
			Severity: diag.SeverityError, Pos: ref, Code: "E0021",
			Message: "include cycle through " + name,
		})
		return nil
	}
	visited[name] = true
	defer delete(visited, name)

	ex := expand.New(scanner.New(fs.AddFile(name, content)),
		expand.Options{Defines: defs})
	f, d := parser.ParseStream(name, ex)
	*diags = append(*diags, d...)

	out := make([]ast.Stmt, 0, len(f.Stmts))
	for _, st := range f.Stmts {
		dir, isDir := st.(*ast.Directive)
		if !isDir || strings.ToLower(dir.Name) != ".include" {
			out = append(out, st)
			continue
		}
		inc, ok := singleStr(dir)
		if !ok {
			*diags = append(*diags, diag.Diagnostic{
				Severity: diag.SeverityError, Pos: dir.NamePos, Code: "E0022",
				Message: ".include requires a single string literal",
			})
			continue
		}
		out = append(out, flatten(inc.Val, files, fs, visited, diags, dir.NamePos, defs)...)
	}
	return out
}

func singleStr(d *ast.Directive) (*ast.StrLit, bool) {
	if len(d.Args) != 1 {
		return nil, false
	}
	sl, ok := d.Args[0].(*ast.StrLit)
	return sl, ok
}

// binResolver adapts the in-memory file map to codegen.Binaries (.incbin).
type binResolver map[string][]byte

func (b binResolver) ReadFile(name string) ([]byte, bool) {
	v, ok := b[name]
	return v, ok
}
