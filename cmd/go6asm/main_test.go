package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGo6asmFlatWithDebugInfo(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.s")
	bin := filepath.Join(dir, "p.bin")
	lbl := filepath.Join(dir, "p.lbl")
	sym := filepath.Join(dir, "p.sym")
	if err := os.WriteFile(src,
		[]byte(".org $0300\nstart:\n\tLDA val\n\tRTS\nval:\n\t.byte $99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := run([]string{"-o", bin, "-lbl", lbl, "-sym", sym, src})
	if rc != 0 {
		t.Fatalf("go6asm exit %d", rc)
	}

	img, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	// $0300 LDA val (3)  $0303 RTS (1)  $0304 .byte $99  ⇒ val=$0304
	want := []byte{0xAD, 0x04, 0x03, 0x60, 0x99}
	if string(img) != string(want) {
		t.Fatalf("image % X, want % X", img, want)
	}

	lb, err := os.ReadFile(lbl)
	if err != nil {
		t.Fatal(err)
	}
	// Flat mode exposes every symbol (not just exports), sorted by addr.
	wantLbl := "al C:$0300 .start\nal C:$0304 .val\n"
	if string(lb) != wantLbl {
		t.Errorf("lbl =\n%q\nwant\n%q", lb, wantLbl)
	}

	sb, err := os.ReadFile(sym)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sb), `"go6asm-sym/1"`) ||
		!strings.Contains(string(sb), `"val"`) {
		t.Errorf("sym json missing expected content:\n%s", sb)
	}
}

func TestGo6asmNoArgs(t *testing.T) {
	if rc := run(nil); rc != 2 {
		t.Errorf("exit %d, want 2", rc)
	}
}

func TestGo6asmSimpleExplain(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.s")
	bin := filepath.Join(dir, "hello.bin")
	if err := os.WriteFile(src, []byte("\tLDA #$42\n\tRTS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := run([]string{"-explain", "-o", bin, src}); rc != 0 {
		t.Fatalf("go6asm -explain exit %d", rc)
	}
	img, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	// sim-tui Layer-0: $2000 ROM, code at $E000, RESET ($FFFC) → $E000.
	if len(img) != 0x2000 || img[0] != 0xA9 || img[1] != 0x42 {
		t.Fatalf("image len %d head % X", len(img), img[:3])
	}
	if img[0x1FFC] != 0x00 || img[0x1FFD] != 0xE0 {
		t.Errorf("RESET vector = %02X %02X, want 00 E0", img[0x1FFC], img[0x1FFD])
	}
}
