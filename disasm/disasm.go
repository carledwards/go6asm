// Package disasm renders an annotated 6502 disassembly of a linked
// image. It is the teaching surface (DESIGN §16): every line carries
// the bytes, the decoded instruction with symbol names substituted for
// addresses, and — crucially — the *original source comment* that was
// compiled alongside it, threaded back in from Result.Comments. The
// author's own words ("cross-segment ref -> relocation") survive into
// the dump; that is the point.
//
// v1 is a linear sweep from the origin: bytes that aren't a valid
// opcode render as `.byte`. Distinguishing code from data (so a data
// table isn't mis-decoded as instructions) is the static-analyzer's
// reachability job (DESIGN §16) and is layered on later.
//
// Leaf package: stdlib + isa only, wasm-clean (DESIGN §4).
package disasm

import (
	"fmt"
	"sort"
	"strings"

	"github.com/carledwards/go6asm/isa"
)

// Sym is one address→name binding (labels and exported constants).
type Sym struct {
	Name string
	Addr uint16
}

// Options drives a disassembly.
type Options struct {
	Origin   uint16
	Symbols  []Sym
	Comments map[uint16]string // PC → source comment
	// CodeStarts, when non-nil, is the static analyzer's set of
	// reachable instruction starts: addresses not in it render as
	// `.byte` data instead of being mis-decoded. nil ⇒ linear sweep.
	CodeStarts map[uint16]bool
}

// Line is one disassembled unit.
type Line struct {
	Addr    uint16
	Bytes   []byte
	Label   string // a symbol sitting exactly on Addr (printed "name:")
	Text    string // "LDA msg,X" / ".byte $7E"
	Comment string // threaded source comment, if any
}

// Disassemble linearly decodes image (loaded at opt.Origin).
func Disassemble(image []byte, opt Options) []Line {
	byAddr := map[uint16]string{}
	for _, s := range opt.Symbols {
		if _, dup := byAddr[s.Addr]; !dup {
			byAddr[s.Addr] = s.Name
		}
	}
	lo := opt.Origin
	hi := opt.Origin + uint16(len(image)) // may wrap; only used for label gating

	var out []Line
	for i := 0; i < len(image); {
		pc := opt.Origin + uint16(i)
		var ln Line
		ln.Addr = pc
		// A symbol exactly on this address, and within the image, is a
		// code/data label here — print it as a "name:" header line.
		if name, ok := byAddr[pc]; ok && inRange(pc, lo, hi) {
			ln.Label = name
		}
		ln.Comment = opt.Comments[pc]

		op := image[i]
		// With an analyzer code map, only decode at reachable
		// instruction starts; everything else is data.
		if opt.CodeStarts != nil && !opt.CodeStarts[pc] {
			ln.Bytes = []byte{op}
			ln.Text = fmt.Sprintf(".byte $%02X", op)
			out = append(out, ln)
			i++
			continue
		}
		enc, ok := isa.NMOS6502.Decode(op)
		n := 1
		if ok {
			n = enc.Bytes
		}
		if !ok || i+n > len(image) {
			ln.Bytes = []byte{op}
			ln.Text = fmt.Sprintf(".byte $%02X", op)
			out = append(out, ln)
			i++
			continue
		}
		ln.Bytes = append([]byte(nil), image[i:i+n]...)
		ln.Text = format(enc, pc, ln.Bytes, byAddr)
		out = append(out, ln)
		i += n
	}
	return out
}

func inRange(a, lo, hi uint16) bool {
	if lo <= hi {
		return a >= lo && a < hi
	}
	return a >= lo || a < hi // origin+len wrapped past $FFFF
}

// format renders the operand for enc using symbol names where an
// address has one. operand bytes follow the opcode in b.
func format(enc isa.Encoding, pc uint16, b []byte, sym map[uint16]string) string {
	m := string(enc.Mnemonic)
	name := func(a uint16, hexFmt string) string {
		if n, ok := sym[a]; ok {
			return n
		}
		return fmt.Sprintf(hexFmt, a)
	}
	switch enc.Mode {
	case isa.Implied:
		return m
	case isa.Accumulator:
		return m + " A"
	case isa.Immediate:
		return fmt.Sprintf("%s #$%02X", m, b[1])
	case isa.ZeroPage:
		return fmt.Sprintf("%s %s", m, name(uint16(b[1]), "$%02X"))
	case isa.ZeroPageX:
		return fmt.Sprintf("%s %s,X", m, name(uint16(b[1]), "$%02X"))
	case isa.ZeroPageY:
		return fmt.Sprintf("%s %s,Y", m, name(uint16(b[1]), "$%02X"))
	case isa.Absolute:
		return fmt.Sprintf("%s %s", m, name(word(b), "$%04X"))
	case isa.AbsoluteX:
		return fmt.Sprintf("%s %s,X", m, name(word(b), "$%04X"))
	case isa.AbsoluteY:
		return fmt.Sprintf("%s %s,Y", m, name(word(b), "$%04X"))
	case isa.Indirect:
		return fmt.Sprintf("%s (%s)", m, name(word(b), "$%04X"))
	case isa.IndexedIndirect:
		return fmt.Sprintf("%s (%s,X)", m, name(uint16(b[1]), "$%02X"))
	case isa.IndirectIndexed:
		return fmt.Sprintf("%s (%s),Y", m, name(uint16(b[1]), "$%02X"))
	case isa.Relative:
		target := pc + 2 + uint16(int8(b[1]))
		return fmt.Sprintf("%s %s", m, name(target, "$%04X"))
	}
	return m
}

func word(b []byte) uint16 { return uint16(b[1]) | uint16(b[2])<<8 }

// Render produces the columnar text dump:
//
//	$E000  A9 01        LDA #$01          ; clear the VIC screen
func Render(lines []Line) string {
	var sb strings.Builder
	for _, ln := range lines {
		if ln.Label != "" {
			fmt.Fprintf(&sb, "%s:\n", ln.Label)
		}
		var hex strings.Builder
		for i, by := range ln.Bytes {
			if i > 0 {
				hex.WriteByte(' ')
			}
			fmt.Fprintf(&hex, "%02X", by)
		}
		line := fmt.Sprintf("$%04X  %-11s  %s", ln.Addr, hex.String(), ln.Text)
		if ln.Comment != "" {
			line = fmt.Sprintf("%-44s ; %s", line, ln.Comment)
		}
		sb.WriteString(strings.TrimRight(line, " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// SymsFromPairs is a small helper for callers holding name/addr pairs
// in a deterministic order (sorted by address, then name).
func SymsFromPairs(m map[string]uint16) []Sym {
	out := make([]Sym, 0, len(m))
	for n, a := range m {
		out = append(out, Sym{Name: n, Addr: a})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr != out[j].Addr {
			return out[i].Addr < out[j].Addr
		}
		return out[i].Name < out[j].Name
	})
	return out
}
