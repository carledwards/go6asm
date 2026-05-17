// Package klaus is the execution oracle for go6asm (DESIGN §15, §13.4).
// It lives in its own module (own go.mod, local `replace` for go6asm) so
// the neutral third-party emulator dependency (beevik/go6502) never
// enters go6asm's or any consumer's module graph — the cycle-avoidance
// decision recorded in DESIGN §15.
//
// ca65 proves go6asm encodes bytes correctly (the compat suite). This
// proves the bytes actually *execute* correctly on an independent 6502,
// closing the dynamic-correctness half of the contract. Smoke first; the
// full Klaus functional ROM is added on top of this harness.
package klaus

import (
	"testing"

	"github.com/beevik/go6502/cpu"
	"github.com/carledwards/go6asm/asm"
)

// run assembles src with go6asm and executes it on beevik/go6502 until
// the program parks in a self-loop (JMP *) or maxSteps is hit.
func run(t *testing.T, src string, maxSteps int) (*cpu.CPU, *cpu.FlatMemory) {
	t.Helper()
	r := asm.Assemble(asm.Input{
		Entry: "t.s",
		Files: []asm.SourceFile{{Name: "t.s", Content: []byte(src)}},
	})
	if !r.Ok() {
		t.Fatalf("assembly failed: %v", r.Errors)
	}

	mem := cpu.NewFlatMemory()
	mem.StoreBytes(r.Origin, r.Image)
	c := cpu.NewCPU(cpu.NMOS, mem)
	c.SetPC(r.Origin)

	for range maxSteps {
		pc := c.Reg.PC
		c.Step()
		if c.Reg.PC == pc { // JMP-to-self success trap
			return c, mem
		}
	}
	t.Fatalf("program did not terminate within %d steps (PC=$%04X)", maxSteps, c.Reg.PC)
	return nil, nil
}

// TestSmoke exercises the full pipeline end to end — including Phase-2
// macro expansion and conditional assembly — then runs the result on an
// independent CPU and checks the computed state.
func TestSmoke(t *testing.T) {
	const src = `.org $0200
COUNT = 5

.macro ADD3
    CLC
    ADC #$03
.endmacro

reset:
    LDX #$00
    LDA #$00
loop:
    ADD3                ; macro: CLC / ADC #$03
    INX
    CPX #COUNT          ; '=' constant resolved in codegen
    BNE loop
.if 1
    STA $00             ; taken branch
.else
    STA $10             ; must not assemble
.endif
    LDX #$AA
    STX $01
done:
    JMP done
`
	c, mem := run(t, src, 100_000)

	if got := mem.LoadByte(0x0000); got != 15 {
		t.Errorf("$00 = %d, want 15 (5 iterations × 3, via macro)", got)
	}
	if got := mem.LoadByte(0x0001); got != 0xAA {
		t.Errorf("$01 = $%02X, want $AA", got)
	}
	if got := mem.LoadByte(0x0010); got != 0 {
		t.Errorf("$10 = $%02X, want 0 (.else branch must not have assembled)", got)
	}
	if c.Reg.X != 0xAA {
		t.Errorf("X = $%02X, want $AA", c.Reg.X)
	}
}
