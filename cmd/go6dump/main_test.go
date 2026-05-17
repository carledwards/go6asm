package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/carledwards/go6asm/object"
)

func TestGo6dumpReadsObject(t *testing.T) {
	dir := t.TempDir()
	o6 := filepath.Join(dir, "x.o6")
	f := &object.File{
		Segments: []object.Segment{{
			Name: "CODE", Relocatable: true, Bytes: []byte{0xEA, 0xEA},
			Relocs: []object.Reloc{
				{Offset: 0, Type: object.RelWord, Size: 2, SymIndex: 0},
			},
		}},
		Symbols: []object.Symbol{
			{Name: "\x01seg:CODE", SegIndex: 0, Flags: object.SymLocal | object.SymLabel},
		},
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o6, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := run([]string{"-x", o6}); rc != 0 {
		t.Fatalf("go6dump exit %d", rc)
	}
}

func TestGo6dumpRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.o6")
	if err := os.WriteFile(bad, []byte("not an object"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := run([]string{bad}); rc == 0 {
		t.Fatal("expected non-zero exit for a non-object file")
	}
}

func TestGo6dumpDispName(t *testing.T) {
	if got := dispName("\x01seg:RODATA"); got != "<segment RODATA>" {
		t.Errorf("dispName = %q", got)
	}
	if got := dispName("reset"); got != "reset" {
		t.Errorf("dispName passthrough = %q", got)
	}
}
