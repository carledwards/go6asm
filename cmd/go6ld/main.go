// Command go6ld links relocatable .o6 objects into a flat image, using a
// built-in (-t) or file (-C) ld65-style memory map. Like the assembler,
// the link library never touches the filesystem (DESIGN §4, §9); this CLI
// loads the objects and config and writes the image back out.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/carledwards/go6asm/debug"
	"github.com/carledwards/go6asm/link"
	"github.com/carledwards/go6asm/memmap"
	"github.com/carledwards/go6asm/object"
	"github.com/carledwards/go6asm/prg"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fl := flag.NewFlagSet("go6ld", flag.ContinueOnError)
	out := fl.String("o", "", "output image (default: <first object>.bin)")
	target := fl.String("t", "flat-64k", "built-in memory map: flat-64k | sim-tui | c64")
	cfgPath := fl.String("C", "", "ld65-style config file (overrides -t)")
	prgOut := fl.Bool("prg", false, "write a Commodore .prg (2-byte load-address header)")
	mapPath := fl.String("m", "", "write a human-readable placement map")
	lblPath := fl.String("L", "", "write a VICE monitor label file (.lbl)")
	symPath := fl.String("S", "", "write the canonical symbol JSON (.sym)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	objs := fl.Args()
	if len(objs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: go6ld [-o out.bin] [-t target | -C cfg] [-m map] [-L lbl] [-S sym] a.o6 [b.o6 ...]")
		return 2
	}

	cfg, rc := loadConfig(*cfgPath, *target)
	if rc != 0 {
		return rc
	}

	var files []*object.File
	for _, p := range objs {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6ld: %v\n", err)
			return 1
		}
		f, err := object.Read(b)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6ld: %s: %v\n", p, err)
			return 1
		}
		files = append(files, f)
	}

	res := link.Link(files, cfg)
	for _, d := range res.Diags {
		fmt.Fprint(os.Stderr, d.Render())
	}
	if res.HasErrors() {
		fmt.Fprintf(os.Stderr, "go6ld: link failed\n")
		return 1
	}

	image, ext := res.Image, ".bin"
	if *prgOut {
		image, ext = prg.Wrap(res.Origin, res.Image), ".prg"
	}
	dst := *out
	if dst == "" {
		base := filepath.Base(objs[0])
		dst = strings.TrimSuffix(base, filepath.Ext(base)) + ext
	}
	if err := os.WriteFile(dst, image, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "go6ld: %v\n", err)
		return 1
	}
	if *mapPath != "" {
		if err := os.WriteFile(*mapPath, []byte(renderMap(res)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "go6ld: %v\n", err)
			return 1
		}
	}
	if *lblPath != "" {
		if err := os.WriteFile(*lblPath, []byte(debug.VICELabels(dbgSyms(res))), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "go6ld: %v\n", err)
			return 1
		}
	}
	if *symPath != "" {
		j, err := debug.SymbolJSON(dbgSyms(res))
		if err == nil {
			err = os.WriteFile(*symPath, j, 0o644)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6ld: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "%s: %d bytes, origin $%04X\n", dst, len(res.Image), res.Origin)
	return 0
}

// dbgSyms maps the linker's resolved symbols to the debug-info model.
func dbgSyms(res link.Result) []debug.Symbol {
	out := make([]debug.Symbol, 0, len(res.Symbols))
	for _, s := range res.Symbols {
		kind := "label"
		if s.Kind == link.SymKindConst {
			kind = "const"
		}
		out = append(out, debug.Symbol{
			Name: s.Name, Addr: uint16(s.Value), Kind: kind,
		})
	}
	return out
}

func loadConfig(cfgPath, target string) (*memmap.Config, int) {
	if cfgPath != "" {
		b, err := os.ReadFile(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6ld: %v\n", err)
			return nil, 1
		}
		c, err := memmap.Parse(b)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go6ld: %v\n", err)
			return nil, 1
		}
		return c, 0
	}
	c, err := memmap.Builtin(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go6ld: %v (built-ins: flat-64k, sim-tui)\n", err)
		return nil, 2
	}
	return c, 0
}

// renderMap is go6ld's .map output (DESIGN §9.3): where each segment
// instance landed, then the resolved symbol table.
func renderMap(r link.Result) string {
	var b strings.Builder
	b.WriteString("Segments\n--------\n")
	b.WriteString("Name        Start   Size  File\n")
	for _, p := range r.Placements {
		fmt.Fprintf(&b, "%-10s  $%04X  %5d  %d\n", p.Segment, p.Addr, p.Size, p.File)
	}
	b.WriteString("\nSymbols\n-------\n")
	for _, s := range r.Symbols {
		kind := "label"
		if s.Kind == link.SymKindConst {
			kind = "const"
		}
		fmt.Fprintf(&b, "$%04X  %-5s  %s\n", uint16(s.Value), kind, s.Name)
	}
	return b.String()
}
