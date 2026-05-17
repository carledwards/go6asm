// Command go6dump inspects a relocatable .o6 object: its segments,
// symbol table, and relocations (DESIGN §8). Read-only; useful for
// debugging the assembler/linker and for the ca65/ld65 differential.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/carledwards/go6asm/object"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fl := flag.NewFlagSet("go6dump", flag.ContinueOnError)
	hex := fl.Bool("x", false, "also hex-dump each segment's bytes")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: go6dump [-x] file.o6")
		return 2
	}
	b, err := os.ReadFile(fl.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "go6dump: %v\n", err)
		return 1
	}
	f, err := object.Read(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go6dump: %v\n", err)
		return 1
	}

	fmt.Printf("%s: %d segment(s), %d symbol(s)\n",
		fl.Arg(0), len(f.Segments), len(f.Symbols))

	for i := range f.Segments {
		s := &f.Segments[i]
		kind := "absolute"
		if s.Relocatable {
			kind = "reloc"
		}
		fmt.Printf("\n[%d] segment %q  %s  %d bytes  %d reloc(s)\n",
			i, s.Name, kind, len(s.Bytes), len(s.Relocs))
		if !s.Relocatable {
			fmt.Printf("    origin $%04X\n", s.Origin)
		}
		for _, r := range s.Relocs {
			fmt.Printf("    +%04X  %-8s sym#%d  addend %+d\n",
				r.Offset, relName(r.Type), r.SymIndex, r.Addend)
		}
		if *hex {
			hexDump(s.Bytes)
		}
	}

	if len(f.Symbols) > 0 {
		fmt.Printf("\nSymbols\n")
		for i, s := range f.Symbols {
			fmt.Printf("  #%d  %-20s value $%04X  seg %d  [%s]\n",
				i, dispName(s.Name), uint16(s.Value), s.SegIndex, symFlags(s.Flags))
		}
	}
	return 0
}

// dispName makes internal per-segment anchor symbols (named "\x01seg:NAME"
// by codegen) human-readable in the dump.
func dispName(n string) string {
	if rest, ok := strings.CutPrefix(n, "\x01seg:"); ok {
		return "<segment " + rest + ">"
	}
	return n
}

func relName(t object.RelType) string {
	switch t {
	case object.RelByteLo:
		return "byte-lo"
	case object.RelByteHi:
		return "byte-hi"
	case object.RelWord:
		return "word"
	case object.RelBranch:
		return "branch"
	}
	return fmt.Sprintf("rel%d", t)
}

func symFlags(f object.SymFlags) string {
	var p []string
	if f&object.SymExported != 0 {
		p = append(p, "export")
	}
	if f&object.SymImported != 0 {
		p = append(p, "import")
	}
	if f&object.SymLabel != 0 {
		p = append(p, "label")
	}
	if f&object.SymLocal != 0 {
		p = append(p, "local")
	}
	if len(p) == 0 {
		return "-"
	}
	return strings.Join(p, "|")
}

func hexDump(b []byte) {
	for i := 0; i < len(b); i += 16 {
		end := min(i+16, len(b))
		fmt.Printf("    %04X  ", i)
		for j := i; j < end; j++ {
			fmt.Printf("%02X ", b[j])
		}
		fmt.Println()
	}
}
