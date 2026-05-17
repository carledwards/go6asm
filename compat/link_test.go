// The ld65 link differential (DESIGN §9, Phase-3 capstone). For every
// testdata/link/*.s fixture it assembles+links with go6asm's object/link
// path and with the reference cc65 toolchain (ca65+ld65 via cl65), using
// the *same* memory-map config, and requires byte-identical output.
//
// This is stricter than TestCA65Differential, which only exercises the
// flat codegen path: here the bytes flow source → .o6 → linker, so
// segment placement and every relocation kind are on the line. Gating
// and golden fallback match the ca65 suite.
package compat

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carledwards/go6asm/asm"
)

func TestLD65Differential(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/link/*.s")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no link fixtures found: %v", err)
	}
	cfgPath, err := filepath.Abs("testdata/link/link.cfg")
	if err != nil {
		t.Fatal(err)
	}
	cfgSrc, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cl65, missing := exec.LookPath("cl65")
	if missing != nil {
		t.Log("cl65 not found; comparing go6asm against checked-in goldens only")
	}
	update := os.Getenv("GO6ASM_UPDATE") == "1"

	for _, fx := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fx), ".s")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(fx)
			if err != nil {
				t.Fatal(err)
			}

			r := asm.Assemble(asm.Input{
				Entry:     name + ".s",
				Files:     []asm.SourceFile{{Name: name + ".s", Content: src}},
				ConfigSrc: cfgSrc,
			})
			if !r.Ok() {
				t.Fatalf("go6asm object/link errors: %v", r.Errors)
			}
			got := r.Image
			golden := strings.TrimSuffix(fx, ".s") + ".bin"

			if missing == nil {
				want := ld65(t, cl65, cfgPath, src)
				if update {
					if err := os.WriteFile(golden, want, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("byte mismatch vs ld65\n go6asm: % X\n ld65:   % X", got, want)
				}
				return
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Skipf("cl65 unavailable and no golden %s", golden)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("byte mismatch vs golden\n go6asm: % X\n golden: % X", got, want)
			}
		})
	}
}

// ld65 assembles+links src with the reference toolchain using the shared
// config (cl65 drives ca65 then ld65).
func ld65(t *testing.T, cl65, cfg string, src []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.s")
	out := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(in, src, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cl65, "-t", "none", "--cpu", "6502", "-C", cfg, "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cl65 failed: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
