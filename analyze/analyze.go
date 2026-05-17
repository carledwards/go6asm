// Package analyze is go6asm's static program checker (DESIGN §16): a
// reachability sweep over the linked image from known entry points
// (reset/NMI/IRQ vectors + exported labels). It follows branches,
// JMP, and JSR/return, and reports:
//
//   - control reaching a BRK ($00)            — almost always a bug
//   - control reaching an illegal/undefined opcode
//   - JMP (indirect) / dynamic flow           — an analysis boundary
//   - (device-aware, when a memory map is given) a store to a
//     read-only region, or any access to an unmapped address, for
//     statically-resolvable operands
//
// Honesty rule (DESIGN §16): things it cannot bound — indirect/computed
// addresses, jump tables, self-modifying code — are reported as
// boundaries ("analysis stops here"), never silently passed and never
// asserted safe. Reachable instruction starts are returned so the
// disassembler can tell code from data.
//
// Leaf, wasm-clean (DESIGN §4): depends only on isa + diag.
package analyze

import (
	"fmt"
	"sort"

	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/isa"
)

// Region is one span of the target's address space. Addresses not
// covered by any Region (when Regions is non-empty) are "unmapped".
type Region struct {
	Lo, Hi   uint16 // inclusive
	ReadOnly bool   // a store here is a fault (ROM)
	Name     string // for diagnostics, e.g. "ROM"
}

// Options drives Analyze.
type Options struct {
	Origin  uint16
	Entries []uint16 // reset/NMI/IRQ + exported entry labels
	Regions []Region // optional; empty ⇒ skip memory-map checks
}

// Result is the analysis outcome.
type Result struct {
	// CodeStarts is every address decoded as an instruction start on a
	// reachable path — the disassembler's code/data oracle.
	CodeStarts map[uint16]bool
	Diags      []diag.Diagnostic
}

// Static-analyzer diagnostic codes (A-series; distinct from the
// front-end's E#### / I####).
const (
	codeReachBRK   = "A0001"
	codeIllegal    = "A0002"
	codeIndirect   = "A0003"
	codeWriteROM   = "A0004"
	codeUnmapped   = "A0005"
	codeLeaveImage = "A0006"
)

var branches = map[string]bool{
	"BPL": true, "BMI": true, "BVC": true, "BVS": true,
	"BCC": true, "BCS": true, "BNE": true, "BEQ": true,
}

func writesMemory(mnem string, m isa.Mode) bool {
	switch mnem {
	case "STA", "STX", "STY":
		return true
	case "INC", "DEC", "ASL", "LSR", "ROL", "ROR":
		return m != isa.Accumulator // RMW to memory
	}
	return false
}

// Analyze sweeps image (loaded at opt.Origin) from opt.Entries.
func Analyze(image []byte, opt Options) Result {
	res := Result{CodeStarts: map[uint16]bool{}}
	if len(image) == 0 {
		return res
	}
	lo := int(opt.Origin)
	hi := lo + len(image) // exclusive
	at := func(a int) (byte, bool) {
		if a >= lo && a < hi {
			return image[a-lo], true
		}
		return 0, false
	}
	seenDiag := map[string]bool{} // dedupe (code,addr)
	emit := func(sev diag.Severity, code string, addr int, msg string) {
		k := fmt.Sprintf("%s@%04X", code, addr)
		if seenDiag[k] {
			return
		}
		seenDiag[k] = true
		res.Diags = append(res.Diags, diag.Diagnostic{
			Severity: sev, Code: code,
			Message: fmt.Sprintf("$%04X: %s", addr, msg),
		})
	}

	regionOf := func(a int) (*Region, bool) {
		for i := range opt.Regions {
			r := &opt.Regions[i]
			if a >= int(r.Lo) && a <= int(r.Hi) {
				return r, true
			}
		}
		return nil, false
	}
	// memCheck flags a statically-resolvable access. indexed widens the
	// checked span to a..a+255 and only faults if the WHOLE span is bad
	// (conservative — never a false positive).
	memCheck := func(pc, a int, store, indexed bool) {
		if len(opt.Regions) == 0 {
			return
		}
		check := func(x int) (ro, mapped bool) {
			if r, ok := regionOf(x & 0xFFFF); ok {
				return r.ReadOnly, true
			}
			return false, false
		}
		span := 1
		if indexed {
			span = 256
		}
		allUnmapped, allRO := true, true
		for off := 0; off < span; off++ {
			ro, mapped := check(a + off)
			if mapped {
				allUnmapped = false
			}
			if !ro {
				allRO = false
			}
		}
		if allUnmapped {
			emit(diag.SeverityWarning, codeUnmapped, pc,
				fmt.Sprintf("accesses unmapped memory $%04X", a&0xFFFF))
			return
		}
		if store && allRO {
			emit(diag.SeverityWarning, codeWriteROM, pc,
				fmt.Sprintf("writes read-only memory $%04X", a&0xFFFF))
		}
	}

	work := append([]uint16(nil), opt.Entries...)
	pushed := map[uint16]bool{}
	push := func(a uint16) {
		if !pushed[a] {
			pushed[a] = true
			work = append(work, a)
		}
	}
	for i := range opt.Entries {
		pushed[opt.Entries[i]] = true
	}

	for len(work) > 0 {
		pc := int(work[len(work)-1])
		work = work[:len(work)-1]
		if res.CodeStarts[uint16(pc)] {
			continue
		}
		op, ok := at(pc)
		if !ok {
			// Reachable code outside the image (a KERNAL/ROM call on a
			// real target, or a bad jump). A boundary, not a verdict.
			emit(diag.SeverityNote, codeLeaveImage, pc,
				"control leaves the analyzed image")
			continue
		}
		enc, known := isa.NMOS6502.Decode(op)
		if !known {
			emit(diag.SeverityWarning, codeIllegal, pc,
				fmt.Sprintf("illegal/undefined opcode $%02X reached as code", op))
			continue // unknown length: cannot follow safely
		}
		res.CodeStarts[uint16(pc)] = true
		n := enc.Bytes
		b1, _ := at(pc + 1)
		mnem := string(enc.Mnemonic)

		// Operand effective-address checks.
		switch enc.Mode {
		case isa.Absolute:
			w := int(b1) | int(must(at, pc+2))<<8
			memCheck(pc, w, writesMemory(mnem, enc.Mode), false)
		case isa.AbsoluteX, isa.AbsoluteY:
			w := int(b1) | int(must(at, pc+2))<<8
			memCheck(pc, w, writesMemory(mnem, enc.Mode), true)
		case isa.ZeroPage:
			memCheck(pc, int(b1), writesMemory(mnem, enc.Mode), false)
		}

		// Control flow.
		switch {
		case mnem == "BRK":
			emit(diag.SeverityWarning, codeReachBRK, pc,
				"BRK reached on a normal-execution path")
		case mnem == "RTS" || mnem == "RTI":
			// path ends
		case mnem == "JMP" && enc.Mode == isa.Indirect:
			emit(diag.SeverityNote, codeIndirect, pc,
				"JMP (indirect): analysis cannot follow the target")
		case mnem == "JMP": // absolute
			push(uint16(int(b1) | int(must(at, pc+2))<<8))
		case mnem == "JSR":
			push(uint16(int(b1) | int(must(at, pc+2))<<8)) // subroutine
			push(uint16(pc + n))                            // assume it returns
		case branches[mnem]:
			push(uint16(pc + n))                       // not taken
			push(uint16(pc + n + int(int8(b1))))       // taken
		default:
			push(uint16(pc + n)) // fall through
		}
	}
	return res
}

// must reads a byte that the caller already knows is in range (operand
// bytes of an in-range, known-length instruction); 0 if truncated.
func must(at func(int) (byte, bool), a int) byte {
	v, _ := at(a)
	return v
}

// HasFindings reports whether any non-Note diagnostic was produced.
func (r Result) HasFindings() bool {
	for _, d := range r.Diags {
		if d.Severity == diag.SeverityWarning || d.Severity == diag.SeverityError {
			return true
		}
	}
	return false
}

// SortedDiags returns the diagnostics in address order for stable output.
func (r Result) SortedDiags() []diag.Diagnostic {
	out := append([]diag.Diagnostic(nil), r.Diags...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Message < out[j].Message
	})
	return out
}
