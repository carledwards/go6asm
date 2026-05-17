package klaus

import (
	"os"
	"strconv"
	"testing"

	"github.com/beevik/go6502/cpu"
)

// TestKlausFunctional runs Klaus Dormann's 6502 functional test ROM on
// the neutral beevik/go6502 emulator and requires it to reach the
// success trap.
//
// The ROM is GPLv3 and is NEVER vendored into this MIT repo. Supply it
// out-of-band:
//
//	GO6ASM_KLAUS_BIN=/path/to/6502_functional_test.bin make functest
//
// Without it the test skips (CI provides the path). The standard 2020
// build loads at $0000, enters at $0400, and parks in `JMP *` at $3469
// on success; any other self-loop is a failed sub-test trap. Override
// with GO6ASM_KLAUS_ENTRY / GO6ASM_KLAUS_SUCCESS if a custom build.
func TestKlausFunctional(t *testing.T) {
	binPath := os.Getenv("GO6ASM_KLAUS_BIN")
	if binPath == "" {
		t.Skip("set GO6ASM_KLAUS_BIN to the Klaus 6502_functional_test.bin (GPLv3; not vendored)")
	}
	img, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("reading Klaus binary: %v", err)
	}

	entry := envAddr(t, "GO6ASM_KLAUS_ENTRY", 0x0400)
	success := envAddr(t, "GO6ASM_KLAUS_SUCCESS", 0x3469)

	mem := cpu.NewFlatMemory()
	mem.StoreBytes(0x0000, img)
	c := cpu.NewCPU(cpu.NMOS, mem)
	c.SetPC(entry)

	const maxSteps = 200_000_000 // Klaus is ~30M instructions
	for range maxSteps {
		pc := c.Reg.PC
		c.Step()
		if c.Reg.PC == pc {
			if pc == success {
				return // all sub-tests passed
			}
			t.Fatalf("Klaus trapped at $%04X (failed sub-test); success is $%04X",
				pc, success)
		}
	}
	t.Fatalf("Klaus did not terminate within %d steps (PC=$%04X)", maxSteps, c.Reg.PC)
}

func envAddr(t *testing.T, key string, def uint16) uint16 {
	t.Helper()
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 0, 16)
	if err != nil {
		t.Fatalf("bad %s=%q: %v", key, s, err)
	}
	return uint16(v)
}
