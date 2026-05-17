// The C64 differential: go6asm's object/link path vs ld65, both driven
// by the *shipped* memmap c64.cfg (the same bytes go6asm embeds), and a
// check that prg.Wrap produces the Commodore load-address header. Same
// gating/golden discipline as TestLD65Differential.
package compat

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/carledwards/go6asm/asm"
	"github.com/carledwards/go6asm/prg"
)

func TestLD65C64Differential(t *testing.T) {
	const fx = "testdata/c64/demo.s"
	cfgPath, err := filepath.Abs("../memmap/configs/c64.cfg")
	if err != nil {
		t.Fatal(err)
	}
	cfgSrc, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(fx)
	if err != nil {
		t.Fatal(err)
	}

	r := asm.Assemble(asm.Input{
		Entry:     "demo.s",
		Files:     []asm.SourceFile{{Name: "demo.s", Content: src}},
		ConfigSrc: cfgSrc,
	})
	if !r.Ok() {
		t.Fatalf("go6asm c64 link errors: %v", r.Errors)
	}
	if r.Origin != 0x0801 {
		t.Errorf("origin $%04X, want $0801", r.Origin)
	}

	golden := "testdata/c64/demo.bin"
	if cl65, missing := exec.LookPath("cl65"); missing == nil {
		want := ld65(t, cl65, cfgPath, src)
		if os.Getenv("GO6ASM_UPDATE") == "1" {
			if err := os.WriteFile(golden, want, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(r.Image, want) {
			t.Fatalf("byte mismatch vs ld65\n go6asm: % X\n ld65:   % X", r.Image, want)
		}
	} else {
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Skip("cl65 unavailable and no golden")
		}
		if !bytes.Equal(r.Image, want) {
			t.Fatalf("byte mismatch vs golden\n go6asm: % X\n golden: % X", r.Image, want)
		}
	}

	// PRG container: $0801 little-endian header then the image.
	p := prg.Wrap(r.Origin, r.Image)
	if p[0] != 0x01 || p[1] != 0x08 || !bytes.Equal(p[2:], r.Image) {
		t.Errorf("prg header/body wrong: % X", p[:4])
	}
}
