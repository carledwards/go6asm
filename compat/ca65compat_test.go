// Package compat is the ca65 byte-identical differential suite (DESIGN
// §6.5). For every testdata/*.s fixture it assembles with go6asm and with
// ca65 (via cl65) and requires identical bytes.
//
// If cl65 is absent (local dev without cc65), it falls back to the
// checked-in golden .bin. `make refresh-ca65-fixtures` (GO6ASM_UPDATE=1)
// regenerates the goldens on a machine with ca65. This is the strictness
// contract: a change that breaks ca65 compatibility fails CI.
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

const rawCfg = "MEMORY { MAIN: start=$0000, size=$10000, file=%O; }\n" +
	"SEGMENTS { CODE: load=MAIN, type=rw; }\n"

func TestCA65Differential(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.s")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	cl65, ca65Avail := exec.LookPath("cl65")
	if ca65Avail != nil {
		t.Log("cl65 not found; comparing go6asm against checked-in goldens only")
	}
	update := os.Getenv("GO6ASM_UPDATE") == "1"

	cfgPath := filepath.Join(t.TempDir(), "raw.cfg")
	if err := os.WriteFile(cfgPath, []byte(rawCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, fx := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fx), ".s")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(fx)
			if err != nil {
				t.Fatal(err)
			}

			got := goasm(t, name, src)
			golden := strings.TrimSuffix(fx, ".s") + ".bin"

			if ca65Avail == nil {
				want := ca65(t, cl65, cfgPath, src)
				if update {
					if err := os.WriteFile(golden, want, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("byte mismatch vs ca65\n go6asm: % X\n ca65:   % X", got, want)
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

func goasm(t *testing.T, name string, src []byte) []byte {
	t.Helper()
	r := asm.Assemble(asm.Input{
		Entry: name + ".s",
		Files: []asm.SourceFile{{Name: name + ".s", Content: src}},
	})
	if !r.Ok() {
		t.Fatalf("go6asm errors: %v", r.Errors)
	}
	return r.Image
}

func ca65(t *testing.T, cl65, cfg string, src []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.s")
	out := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(in, src, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cl65, "-t", "none", "--cpu", "6502", "-C", cfg, "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ca65 failed: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
