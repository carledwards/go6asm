// Package debug emits the linker's debug-info sidecars (DESIGN §9.3):
// a VICE label file and go6asm's canonical symbol JSON. It is a leaf
// package (stdlib only) and wasm-clean (DESIGN §4): it formats a symbol
// slice into bytes/strings; the caller owns any file I/O.
//
// go6asm owns the canonical symbol model (DESIGN §10.4); the JSON shape
// here is that model. Consumers — e.g. 6502-sim-tui — adapt to it, not
// the reverse.
package debug

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Symbol is one resolved symbol. Addr is its absolute address (for a
// constant, its value truncated to 16 bits — VICE/monitors are 16-bit).
type Symbol struct {
	Name string
	Addr uint16
	Kind string // "label" | "const"
}

func sorted(syms []Symbol) []Symbol {
	out := append([]Symbol(nil), syms...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr != out[j].Addr {
			return out[i].Addr < out[j].Addr
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// VICELabels renders a VICE monitor label file: one `al C:$ADDR .name`
// line per symbol (the format `ld65 --dbgfile`/`da65` consumers expect),
// sorted by address for deterministic output.
func VICELabels(syms []Symbol) string {
	var b bytes.Buffer
	for _, s := range sorted(syms) {
		fmt.Fprintf(&b, "al C:$%04X .%s\n", s.Addr, s.Name)
	}
	return b.String()
}

// symFile is the canonical JSON envelope. The version lets consumers
// detect shape changes; symbols are sorted for stable diffs.
type symFile struct {
	Format  string    `json:"format"`
	Symbols []symEntry `json:"symbols"`
}

type symEntry struct {
	Name string `json:"name"`
	Addr uint16 `json:"addr"`
	Kind string `json:"kind"`
}

// SymbolJSON renders the canonical go6asm symbol table as JSON
// (DESIGN §9.3 out.sym, §10.4 canonical model).
func SymbolJSON(syms []Symbol) ([]byte, error) {
	f := symFile{Format: "go6asm-sym/1"}
	for _, s := range sorted(syms) {
		f.Symbols = append(f.Symbols, symEntry{Name: s.Name, Addr: s.Addr, Kind: s.Kind})
	}
	if f.Symbols == nil {
		f.Symbols = []symEntry{}
	}
	return json.MarshalIndent(f, "", "  ")
}
