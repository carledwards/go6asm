package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/carledwards/go6asm/object"
)

// writeObj builds a tiny .o6: a CODE segment with one word reloc against
// its own segment anchor (addend 4 ⇒ resolves to base+4).
func writeObj(t *testing.T, path string) {
	t.Helper()
	f := &object.File{
		Segments: []object.Segment{{
			Name:        "CODE",
			Relocatable: true,
			Bytes:       []byte{0x4C, 0x00, 0x00, 0xEA, 0xEA},
			Relocs: []object.Reloc{
				{Offset: 1, Type: object.RelWord, Size: 2, SymIndex: 0, Addend: 4},
			},
		}},
		Symbols: []object.Symbol{
			{Name: "anchor", Value: 0, SegIndex: 0, Flags: object.SymLocal | object.SymLabel},
		},
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGo6ldLinksFlat64k(t *testing.T) {
	dir := t.TempDir()
	o6 := filepath.Join(dir, "a.o6")
	bin := filepath.Join(dir, "a.bin")
	mp := filepath.Join(dir, "a.map")
	writeObj(t, o6)

	if rc := run([]string{"-t", "flat-64k", "-o", bin, "-m", mp, o6}); rc != 0 {
		t.Fatalf("go6ld exit %d", rc)
	}
	img, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	// flat-64k pins CODE at $0200; reloc addend 4 ⇒ $0204.
	if len(img) != 5 || img[1] != 0x04 || img[2] != 0x02 {
		t.Fatalf("image % X (want JMP $0204 …)", img)
	}
	if _, err := os.Stat(mp); err != nil {
		t.Errorf("map not written: %v", err)
	}
}

func TestGo6ldUnknownTarget(t *testing.T) {
	dir := t.TempDir()
	o6 := filepath.Join(dir, "a.o6")
	writeObj(t, o6)
	if rc := run([]string{"-t", "bogus", o6}); rc == 0 {
		t.Fatal("expected non-zero exit for unknown target")
	}
}

func TestGo6ldNoArgs(t *testing.T) {
	if rc := run(nil); rc != 2 {
		t.Errorf("exit %d, want 2", rc)
	}
}
