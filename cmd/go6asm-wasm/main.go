//go:build js && wasm

// Command go6asm-wasm exposes the assembler to JavaScript (DESIGN §10).
// It is the ONLY file in the project that imports syscall/js, behind the
// js,wasm build tag (the layering rule in §4). No filesystem or fetch is
// used: the host passes source in by string/map and gets bytes back, so
// the wasm binary stays small and the security model trivial (§10.2).
package main

import (
	"fmt"
	"syscall/js"

	"github.com/carledwards/go6asm/analyze"
	"github.com/carledwards/go6asm/asm"
	"github.com/carledwards/go6asm/codegen"
	"github.com/carledwards/go6asm/debug"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/disasm"
	"github.com/carledwards/go6asm/target"
)

func main() {
	js.Global().Set("go6asm", js.ValueOf(map[string]any{
		"assemble": js.FuncOf(assemble),
	}))
	select {} // keep the Go runtime alive for callbacks
}

// assemble(opts) -> result. opts: { source?, files?, entry?, origin?,
// defines?, listing?, target?, config?, obj?, simple? }. With
// target/config the unit is assembled and linked (object/link path);
// `simple:true` selects Layer-0 inference (auto load address +
// synthesized vectors + the target's symbol pack, DESIGN §11) and
// requires a Layer-0 target ("sim-tui" or "raw"). obj also returns the
// serialized .o6. The result always carries the canonical symbol table
// plus the VICE-label and symbol-JSON sidecars (DESIGN §9.3, §10.2).
func assemble(_ js.Value, args []js.Value) any {
	if len(args) != 1 {
		return errResult("go6asm.assemble expects one options object")
	}
	o := args[0]

	in := asm.Input{Listing: boolField(o, "listing")}

	if files := o.Get("files"); files.Type() == js.TypeObject {
		keys := js.Global().Get("Object").Call("keys", files)
		for i := 0; i < keys.Length(); i++ {
			name := keys.Index(i).String()
			in.Files = append(in.Files, asm.SourceFile{
				Name: name, Content: []byte(files.Get(name).String()),
			})
		}
	}
	if src := o.Get("source"); src.Type() == js.TypeString {
		name := stringField(o, "entry", "main.s")
		in.Files = append(in.Files, asm.SourceFile{Name: name, Content: []byte(src.String())})
		in.Entry = name
	}
	if e := stringField(o, "entry", ""); e != "" {
		in.Entry = e
	}
	if org := o.Get("origin"); org.Type() == js.TypeNumber {
		in.Origin = uint16(org.Int())
	}
	if d := o.Get("defines"); d.Type() == js.TypeObject {
		in.Defines = map[string]int64{}
		keys := js.Global().Get("Object").Call("keys", d)
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			in.Defines[k] = int64(d.Get(k).Int())
		}
	}
	in.Target = stringField(o, "target", "")
	if c := o.Get("config"); c.Type() == js.TypeString {
		in.ConfigSrc = []byte(c.String())
	}
	in.EmitObject = boolField(o, "obj")
	in.Layer0 = boolField(o, "simple") // Layer-0 inference (DESIGN §11)

	res := asm.Assemble(in)

	bytes := js.Global().Get("Uint8Array").New(len(res.Image))
	js.CopyBytesToJS(bytes, res.Image)

	ds := make([]debug.Symbol, 0, len(res.Symbols))
	syms := make([]any, 0, len(res.Symbols))
	for _, s := range res.Symbols {
		kind := "label"
		if s.Kind == codegen.ConstSym {
			kind = "const"
		}
		ds = append(ds, debug.Symbol{Name: s.Name, Addr: s.Addr, Kind: kind})
		syms = append(syms, map[string]any{
			"name": s.Name, "addr": int(s.Addr), "kind": kind,
		})
	}
	symJSON, _ := debug.SymbolJSON(ds)

	comments := map[string]any{}
	for pc, c := range res.Comments {
		comments[fmt.Sprintf("0x%04X", pc)] = c
	}

	// Static analysis + reachability-aware disassembly (DESIGN §16).
	ar := analyzeResult(res, in.Target)
	findings := make([]any, 0, len(ar.Diags))
	for _, d := range ar.SortedDiags() {
		findings = append(findings, map[string]any{
			"severity": d.Severity.String(),
			"code":     d.Code,
			"message":  d.Message,
		})
	}
	dsyms := make([]disasm.Sym, 0, len(res.Symbols))
	for _, s := range res.Symbols {
		dsyms = append(dsyms, disasm.Sym{Name: s.Name, Addr: s.Addr})
	}
	dis := disasm.Render(disasm.Disassemble(res.Image, disasm.Options{
		Origin: res.Origin, Symbols: dsyms, Comments: res.Comments,
		CodeStarts: ar.CodeStarts,
	}))

	out := map[string]any{
		"success":  res.Ok(),
		"bytes":    bytes,
		"origin":   int(res.Origin),
		"symbols":  syms,
		"comments": comments,
		"listing":  res.Listing,
		"lbl":      debug.VICELabels(ds),
		"sym":      string(symJSON),
		"disasm":   dis,
		"analysis": findings,
		"errors":   diagsToJS(res.Errors),
		"warnings": diagsToJS(res.Warnings),
	}
	if in.EmitObject {
		obj := js.Global().Get("Uint8Array").New(len(res.Object))
		js.CopyBytesToJS(obj, res.Object)
		out["object"] = obj
	}
	return js.ValueOf(out)
}

// analyzeResult derives entry points (program start, exported labels,
// and the target's reset/NMI/IRQ vectors) plus the device memory map,
// then runs the static analyzer (mirrors cmd/go6asm's runAnalysis).
func analyzeResult(res asm.Result, targetName string) analyze.Result {
	origin := int(res.Origin)
	end := origin + len(res.Image)
	inImage := func(a int) bool { return a >= origin && a < end }

	// Hardware vectors + program start only — not every label (a data
	// label seeded as code would decode tables as instructions).
	entries := []uint16{res.Origin}
	var regions []analyze.Region
	if targetName != "" {
		if tgt, ok := target.Builtin(targetName); ok {
			for _, r := range tgt.Regions {
				regions = append(regions, analyze.Region{
					Lo: r.Lo, Hi: r.Hi, ReadOnly: r.ReadOnly, Name: r.Name,
				})
			}
			if tgt.HasVectors {
				v := int(tgt.VectorAddr)
				for _, off := range []int{0, 2, 4} {
					if inImage(v+off) && inImage(v+off+1) {
						a := uint16(res.Image[v+off-origin]) |
							uint16(res.Image[v+off+1-origin])<<8
						if a != 0 {
							entries = append(entries, a)
						}
					}
				}
			}
		}
	}
	return analyze.Analyze(res.Image, analyze.Options{
		Origin: res.Origin, Entries: entries, Regions: regions,
	})
}

func diagsToJS(ds []diag.Diagnostic) []any {
	out := make([]any, 0, len(ds))
	for _, d := range ds {
		p := d.Pos.Resolve()
		out = append(out, map[string]any{
			"message":  d.Message,
			"code":     d.Code,
			"hint":     d.Hint,
			"file":     p.Filename,
			"line":     p.Line,
			"col":      p.Col,
			"rendered": d.Render(),
		})
	}
	return out
}

func boolField(o js.Value, k string) bool {
	v := o.Get(k)
	return v.Type() == js.TypeBoolean && v.Bool()
}

func stringField(o js.Value, k, def string) string {
	if v := o.Get(k); v.Type() == js.TypeString {
		return v.String()
	}
	return def
}

func errResult(msg string) any {
	return js.ValueOf(map[string]any{
		"success": false,
		"errors":  []any{map[string]any{"message": msg}},
	})
}
