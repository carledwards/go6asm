// Command go6asm is the assembler CLI: a thin host around asm.Assemble.
// The library never reads the filesystem (DESIGN §4); the CLI loads files
// from disk, passes them in by bytes, and writes the image back out.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/carledwards/go6asm/analyze"
	"github.com/carledwards/go6asm/asm"
	"github.com/carledwards/go6asm/codegen"
	"github.com/carledwards/go6asm/debug"
	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/disasm"
	"github.com/carledwards/go6asm/prg"
	"github.com/carledwards/go6asm/target"
)

type defineFlags map[string]int64

func (d defineFlags) String() string { return "" }
func (d defineFlags) Set(s string) error {
	name, val, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("expected name=value, got %q", s)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(val), 0, 64)
	if err != nil {
		return fmt.Errorf("define %s: %v", name, err)
	}
	d[strings.TrimSpace(name)] = n
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fl := flag.NewFlagSet("go6asm", flag.ContinueOnError)
	out := fl.String("o", "", "output file (default: <input>.bin, or .o6 with -obj)")
	lst := fl.String("l", "", "write a listing file")
	org := fl.String("org", "", "origin/load address, e.g. $E000 or 0xE000 (default: 0 / .org)")
	target := fl.String("t", "", "link with a built-in memory map: flat-64k | sim-tui")
	cfgPath := fl.String("C", "", "link with an ld65-style config file")
	emitObj := fl.Bool("obj", false, "emit a relocatable .o6 object instead of a linked image")
	lblPath := fl.String("lbl", "", "write a VICE monitor label file (.lbl)")
	symPath := fl.String("sym", "", "write the canonical symbol JSON (.sym)")
	simple := fl.Bool("simple", false, "Layer-0 mode: infer load address + vectors (DESIGN §11)")
	explain := fl.Bool("explain", false, "print the inferences made (implies -simple)")
	disAsm := fl.Bool("disasm", false, "print an annotated disassembly (symbols + source comments)")
	analyzeF := fl.Bool("analyze", false, "static analysis: reachable BRK/illegal opcodes, invalid memory access")
	prgOut := fl.Bool("prg", false, "write a Commodore .prg (2-byte load-address header)")
	defs := defineFlags{}
	fl.Var(defs, "D", "predefine a constant: -D NAME=VALUE (repeatable)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	srcs := fl.Args()
	if len(srcs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: go6asm [-o out] [-t target | -C cfg] [-obj] [-org $ADDR] [-D N=V] file.s [more.s|data.bin ...]")
		return 2
	}

	var in asm.Input
	in.Entry = srcs[0]
	in.Defines = defs
	in.Listing = *lst != ""
	in.Target = *target
	in.EmitObject = *emitObj
	in.Layer0 = *simple || *explain
	for _, p := range srcs {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6asm: %v\n", err)
			return 1
		}
		in.Files = append(in.Files, asm.SourceFile{Name: p, Content: b})
	}
	if *cfgPath != "" {
		b, err := os.ReadFile(*cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6asm: %v\n", err)
			return 1
		}
		in.ConfigSrc = b
	}
	if *org != "" {
		v, err := strconv.ParseUint(normHex(*org), 0, 16)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6asm: bad -org %q: %v\n", *org, err)
			return 2
		}
		in.Origin = uint16(v)
	}

	res := asm.Assemble(in)

	for _, d := range res.Errors {
		fmt.Fprint(os.Stderr, d.Render())
	}
	for _, d := range res.Warnings {
		fmt.Fprint(os.Stderr, d.Render())
	}
	if *explain && len(res.Infos) > 0 {
		fmt.Fprintf(os.Stderr, "Inferences I made for you (DESIGN §11):\n")
		for _, d := range res.Infos {
			fmt.Fprintf(os.Stderr, "  [%s] %s\n", d.Code, d.Message)
		}
	}
	fmt.Fprintf(os.Stderr, "%s: %d error(s), %d warning(s)\n",
		in.Entry, len(res.Errors), len(res.Warnings))
	if !res.Ok() {
		return 1
	}

	if rc := writeDebugInfo(res.Symbols, *lblPath, *symPath); rc != 0 {
		return rc
	}

	var codeStarts map[uint16]bool
	if *analyzeF || *disAsm {
		ar := runAnalysis(res, *target, *simple || *explain)
		codeStarts = ar.CodeStarts
		if *analyzeF {
			for _, d := range ar.SortedDiags() {
				fmt.Fprint(os.Stderr, d.Render())
			}
			fmt.Fprintf(os.Stderr, "%s: %d analysis finding(s)\n",
				in.Entry, countFindings(ar))
		}
	}

	if *disAsm {
		syms := make([]disasm.Sym, 0, len(res.Symbols))
		for _, s := range res.Symbols {
			syms = append(syms, disasm.Sym{Name: s.Name, Addr: s.Addr})
		}
		lines := disasm.Disassemble(res.Image, disasm.Options{
			Origin: res.Origin, Symbols: syms, Comments: res.Comments,
			CodeStarts: codeStarts,
		})
		fmt.Print(disasm.Render(lines))
	}

	base := filepath.Base(in.Entry)
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	if *emitObj {
		dst := *out
		if dst == "" {
			dst = stem + ".o6"
		}
		if err := os.WriteFile(dst, res.Object, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "go6asm: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s: %d bytes (relocatable object)\n", dst, len(res.Object))
		return 0
	}

	image, ext := res.Image, ".bin"
	if *prgOut {
		image, ext = prg.Wrap(res.Origin, res.Image), ".prg"
	}
	dst := *out
	if dst == "" {
		dst = stem + ext
	}
	if err := os.WriteFile(dst, image, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "go6asm: %v\n", err)
		return 1
	}
	if *lst != "" {
		if err := os.WriteFile(*lst, []byte(res.Listing), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "go6asm: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "%s: %d bytes, origin $%04X\n", dst, len(res.Image), res.Origin)
	return 0
}

// writeDebugInfo emits the optional VICE label and symbol-JSON sidecars
// (DESIGN §9.3) from go6asm's resolved symbols.
func writeDebugInfo(syms []codegen.Symbol, lblPath, symPath string) int {
	if lblPath == "" && symPath == "" {
		return 0
	}
	ds := make([]debug.Symbol, 0, len(syms))
	for _, s := range syms {
		kind := "label"
		if s.Kind == codegen.ConstSym {
			kind = "const"
		}
		ds = append(ds, debug.Symbol{Name: s.Name, Addr: s.Addr, Kind: kind})
	}
	if lblPath != "" {
		if err := os.WriteFile(lblPath, []byte(debug.VICELabels(ds)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "go6asm: %v\n", err)
			return 1
		}
	}
	if symPath != "" {
		j, err := debug.SymbolJSON(ds)
		if err == nil {
			err = os.WriteFile(symPath, j, 0o644)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6asm: %v\n", err)
			return 1
		}
	}
	return 0
}

// runAnalysis derives entry points (program start, exported labels,
// and the reset/NMI/IRQ vectors when the target has them) plus the
// device memory map, then runs the static analyzer.
func runAnalysis(res asm.Result, targetName string, layer0 bool) analyze.Result {
	origin := int(res.Origin)
	end := origin + len(res.Image)
	inImage := func(a int) bool { return a >= origin && a < end }

	// Entry points are the hardware vectors + program start, NOT every
	// label: seeding internal/data labels would decode data tables as
	// code (false findings). Reachability finds the rest from here.
	entries := []uint16{res.Origin}

	var regions []analyze.Region
	name := targetName
	if name == "" && layer0 {
		name = "sim-tui" // Layer-0 default (target.Builtin's default)
	}
	if tgt, ok := target.Builtin(name); ok && name != "" {
		for _, r := range tgt.Regions {
			regions = append(regions, analyze.Region{
				Lo: r.Lo, Hi: r.Hi, ReadOnly: r.ReadOnly, Name: r.Name,
			})
		}
		if tgt.HasVectors {
			v := int(tgt.VectorAddr) // NMI@+0, RESET@+2, IRQ@+4
			for _, off := range []int{0, 2, 4} {
				if inImage(v+off) && inImage(v+off+1) {
					lo := res.Image[v+off-origin]
					hi := res.Image[v+off+1-origin]
					a := uint16(lo) | uint16(hi)<<8
					if a != 0 { // $0000 = Layer-0 "no handler" sentinel
						entries = append(entries, a)
					}
				}
			}
		}
	}
	return analyze.Analyze(res.Image, analyze.Options{
		Origin: res.Origin, Entries: entries, Regions: regions,
	})
}

func countFindings(r analyze.Result) int {
	n := 0
	for _, d := range r.Diags {
		if d.Severity == diag.SeverityWarning || d.Severity == diag.SeverityError {
			n++
		}
	}
	return n
}

// normHex lets -org accept ca65-style "$E000" as well as "0xE000"/"57344".
func normHex(s string) string {
	if strings.HasPrefix(s, "$") {
		return "0x" + s[1:]
	}
	return s
}
