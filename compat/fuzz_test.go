// Differential fuzzer (DESIGN §16, the root-cause answer to "we keep
// finding interaction bugs by hand"). It generates randomized but
// grammatically-valid ca65 programs — biased toward the constructs
// that have actually bitten us (`*`, symbol±const, label differences,
// zero-page/absolute sizing, `=`-constant operands) — assembles each
// with go6asm AND ca65, and requires byte-identical output.
//
// ca65 is the arbiter of validity: if cl65 rejects a sample we skip it
// (it's outside the agreed subset). If cl65 accepts it, go6asm MUST
// accept it and emit the same bytes; anything else is a real go6asm
// bug, reported with the seed + source so it reproduces and can become
// a regression fixture.
//
// Gated on cl65 like the rest of compat. `TestCA65Fuzz` runs a bounded
// batch in the normal suite; `FuzzAssemble` is the coverage-guided
// deep-run entry point (`go test -fuzz=FuzzAssemble ./compat/`).
package compat

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/carledwards/go6asm/asm"
)

// ---- program generator ----------------------------------------------------

type gen struct {
	r       *rand.Rand
	b       strings.Builder
	labels  []string // defined code labels
	consts  []string // defined `=` constants (all < $100 so usable as zp)
	nLabels int
	nConsts int
}

func (g *gen) pick(ss []string) string { return ss[g.r.Intn(len(ss))] }

// constExpr is a pure (non-relocatable) value — richer arithmetic is
// fine here because both assemblers compute the same 32-bit ca65 value.
func (g *gen) constExpr(max int) string {
	switch g.r.Intn(5) {
	case 0:
		return fmt.Sprintf("$%X", g.r.Intn(max+1))
	case 1:
		return strconv.Itoa(g.r.Intn(max + 1))
	case 2:
		return fmt.Sprintf("%%%b", g.r.Intn(256))
	case 3:
		if len(g.consts) > 0 {
			return g.pick(g.consts)
		}
		return strconv.Itoa(g.r.Intn(max + 1))
	default:
		a, b2 := g.r.Intn(max+1), g.r.Intn(64)
		op := []string{"+", "-", "|", "&", "^"}[g.r.Intn(5)]
		return fmt.Sprintf("(%d %s %d)", a, op, b2)
	}
}

// relExpr is what may appear in a relocatable (address) operand: only
// the forms go6asm and ca65 both represent — symbol [±const], <sym,
// >sym, *, *±k, labelA-labelB.
func (g *gen) relExpr() string {
	if len(g.labels) == 0 {
		return fmt.Sprintf("$%04X", g.r.Intn(0x10000))
	}
	l := g.pick(g.labels)
	switch g.r.Intn(7) {
	case 0:
		return l
	case 1:
		return fmt.Sprintf("%s+%d", l, g.r.Intn(32))
	case 2:
		return fmt.Sprintf("%s-%d", l, g.r.Intn(8))
	case 3:
		return "*"
	case 4:
		return fmt.Sprintf("*+%d", g.r.Intn(8)*0) // *+0 keeps it in range/simple
	case 5:
		return l + "-" + g.pick(g.labels) // same-segment label diff (a constant)
	default:
		return fmt.Sprintf("$%04X", g.r.Intn(0x10000))
	}
}

func (g *gen) byteExpr() string { // fits 8 bits
	switch g.r.Intn(4) {
	case 0:
		return "#" + g.constExpr(255)
	case 1:
		if len(g.labels) > 0 {
			return "#<" + g.pick(g.labels)
		}
		return "#" + g.constExpr(255)
	case 2:
		if len(g.labels) > 0 {
			return "#>" + g.pick(g.labels)
		}
		return "#" + g.constExpr(255)
	default:
		return "#<*"
	}
}

func (g *gen) emit(s string) { g.b.WriteString(s); g.b.WriteByte('\n') }

func (g *gen) stmt() {
	switch g.r.Intn(12) {
	case 0: // label
		n := fmt.Sprintf("L%d", g.nLabels)
		g.nLabels++
		g.labels = append(g.labels, n)
		g.emit(n + ":")
	case 1: // constant (< $100 so it doubles as a zp address/operand)
		n := fmt.Sprintf("C%d", g.nConsts)
		g.nConsts++
		g.emit(n + " = " + g.constExpr(255))
		g.consts = append(g.consts, n)
	case 2: // immediate
		g.emit("\t" + g.pick([]string{"LDA", "LDX", "LDY", "ADC", "SBC",
			"AND", "ORA", "EOR", "CMP", "CPX", "CPY"}) + " " + g.byteExpr())
	case 3: // zero page (constant < $100 or small literal)
		zp := g.constExpr(255)
		if len(g.consts) > 0 && g.r.Intn(2) == 0 {
			zp = g.pick(g.consts)
		}
		g.emit("\t" + g.pick([]string{"LDA", "STA", "LDX", "STX", "LDY",
			"STY", "INC", "DEC", "ASL", "LSR", "ROL", "ROR", "BIT"}) + " " + zp)
	case 4: // absolute (label/relexpr → exercises relocations)
		g.emit("\t" + g.pick([]string{"LDA", "STA", "ADC", "AND", "ORA",
			"EOR", "CMP", "INC", "DEC"}) + " a:" + g.relExpr())
	case 5: // absolute,X / ,Y
		g.emit("\t" + g.pick([]string{"LDA", "STA", "ADC", "CMP"}) +
			" a:" + g.relExpr() + g.pick([]string{",X", ",Y"}))
	case 6: // (zp,X) / (zp),Y
		zp := g.constExpr(255)
		if g.r.Intn(2) == 0 {
			g.emit("\tLDA (" + zp + ",X)")
		} else {
			g.emit("\tLDA (" + zp + "),Y")
		}
	case 7: // JMP abs / JMP (ind) / JSR
		switch g.r.Intn(3) {
		case 0:
			g.emit("\tJMP " + g.relExpr())
		case 1:
			g.emit("\tJMP (" + g.constExpr(0xFFFF) + ")")
		default:
			g.emit("\tJSR " + g.relExpr())
		}
	case 8: // branch to an existing label (ca65 arbitrates range)
		if len(g.labels) > 0 {
			g.emit("\t" + g.pick([]string{"BEQ", "BNE", "BCC", "BCS",
				"BPL", "BMI", "BVC", "BVS"}) + " " + g.pick(g.labels))
		} else {
			g.emit("\tNOP")
		}
	case 9: // implied
		g.emit("\t" + g.pick([]string{"NOP", "TAX", "TAY", "TXA", "TYA",
			"INX", "INY", "DEX", "DEY", "CLC", "SEC", "CLD", "SED", "SEI",
			"CLI", "PHA", "PLA", "RTS", "RTI", "ASL A", "LSR A"}))
	case 10: // .byte / .word
		if g.r.Intn(2) == 0 {
			g.emit("\t.byte " + g.constExpr(255) + ", " + g.constExpr(255))
		} else {
			g.emit("\t.word " + g.relExpr() + ", " + g.constExpr(0xFFFF))
		}
	default: // .res / .asciiz
		if g.r.Intn(2) == 0 {
			g.emit("\t.res " + strconv.Itoa(1+g.r.Intn(8)))
		} else {
			g.emit("\t.asciiz \"" + strings.Repeat("x", g.r.Intn(6)) + "\"")
		}
	}
}

func genProgram(seed int64) string {
	g := &gen{r: rand.New(rand.NewSource(seed))}
	n := 5 + g.r.Intn(25)
	for i := 0; i < n; i++ {
		g.stmt()
	}
	return g.b.String()
}

// ---- differential ---------------------------------------------------------

// tryCl65 assembles src with ca65/ld65; ok is false if cl65 rejects it
// (then the sample is outside the agreed subset and is skipped).
func tryCl65(t *testing.T, cl65, cfg, src string) (out []byte, ok bool) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "f.s")
	bin := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cl65, "-t", "none", "--cpu", "6502", "-C", cfg, "-o", bin, in)
	if _, err := cmd.CombinedOutput(); err != nil {
		return nil, false // ca65 rejects ⇒ not our concern
	}
	b, err := os.ReadFile(bin)
	if err != nil {
		return nil, false
	}
	return b, true
}

// check runs one differential sample. Returns false (with a fully
// reproducible report) on a real go6asm divergence.
func check(t *testing.T, cl65, cfg string, seed int64) (accepted bool, bug bool) {
	src := genProgram(seed)
	want, ok := tryCl65(t, cl65, cfg, src)
	if !ok {
		return false, false // ca65 didn't accept it; skip
	}
	r := asm.Assemble(asm.Input{
		Entry: "f.s",
		Files: []asm.SourceFile{{Name: "f.s", Content: []byte(src)}},
	})
	if !r.Ok() {
		t.Errorf("go6asm rejected a ca65-valid program (seed=%d)\n--- source ---\n%s\n--- errors ---\n%v",
			seed, src, r.Errors)
		return true, true
	}
	if !bytes.Equal(r.Image, want) {
		t.Errorf("byte mismatch vs ca65 (seed=%d)\n--- source ---\n%s\n go6asm: % X\n ca65:   % X",
			seed, src, r.Image, want)
		return true, true
	}
	return true, false
}

func TestCA65Fuzz(t *testing.T) {
	cl65, miss := exec.LookPath("cl65")
	if miss != nil {
		t.Skip("cl65 not found; differential fuzzer needs cc65")
	}
	cfg := filepath.Join(t.TempDir(), "raw.cfg")
	if err := os.WriteFile(cfg, []byte(rawCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	n := 200
	if v := os.Getenv("GO6ASM_FUZZ_N"); v != "" {
		if x, err := strconv.Atoi(v); err == nil {
			n = x
		}
	}
	base := int64(1) // deterministic by default → reproducible CI
	if v := os.Getenv("GO6ASM_FUZZ_SEED"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil {
			base = x
		}
	}
	t.Logf("differential fuzz: %d programs, base seed %d "+
		"(override GO6ASM_FUZZ_N / GO6ASM_FUZZ_SEED)", n, base)

	accepted := 0
	for i := range n {
		seed := base + int64(i)
		acc, bug := check(t, cl65, cfg, seed)
		if acc {
			accepted++
		}
		if bug {
			t.FailNow() // first divergence is enough; it's reproducible
		}
	}
	t.Logf("%d/%d programs were ca65-valid and matched go6asm byte-for-byte",
		accepted, n)
	if accepted == 0 {
		t.Fatal("generator produced no ca65-valid programs — generator bug")
	}
}

// FuzzAssemble lets libFuzzer's coverage guidance explore the generator
// space and persist a corpus: `go test -fuzz=FuzzAssemble ./compat/`.
func FuzzAssemble(f *testing.F) {
	cl65, miss := exec.LookPath("cl65")
	if miss != nil {
		f.Skip("cl65 not found; differential fuzzer needs cc65")
	}
	cfg := filepath.Join(f.TempDir(), "raw.cfg")
	if err := os.WriteFile(cfg, []byte(rawCfg), 0o644); err != nil {
		f.Fatal(err)
	}
	for _, s := range []int64{1, 2, 7, 42, 1337} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		check(t, cl65, cfg, seed)
	})
}
