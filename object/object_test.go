package object

import (
	"bytes"
	"reflect"
	"testing"
)

func sample() *File {
	return &File{
		Segments: []Segment{
			{
				Name: "CODE", Relocatable: true,
				Bytes: []byte{0xA9, 0x00, 0x4C, 0x00, 0x00},
				Relocs: []Reloc{
					{Offset: 3, Type: RelWord, Size: 2, SymIndex: 1, Addend: 0},
				},
			},
			{
				Name: "VECTORS", Origin: 0xFFFA, HasLoadAddr: true, LoadAddr: 0xFFFA,
				Bytes: []byte{0, 0, 0, 4, 0, 0},
			},
		},
		Symbols: []Symbol{
			{Name: "reset", Value: 0, SegIndex: 0, Flags: SymExported | SymLabel, Pos: 42},
			{Name: "extern_sub", Value: 0, SegIndex: -1, Flags: SymImported},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	in := sample()
	var buf bytes.Buffer
	if err := in.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := string(buf.Bytes()[:4]); got != "GO6O" {
		t.Errorf("magic = %q, want GO6O", got)
	}

	out, err := Read(buf.Bytes())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch\n in: %+v\nout: %+v", in, out)
	}
}

func TestRelocatableVsAbsolute(t *testing.T) {
	out := mustRT(t, sample())
	if !out.Segments[0].Relocatable {
		t.Error("CODE must be relocatable")
	}
	if out.Segments[1].Relocatable || out.Segments[1].Origin != 0xFFFA {
		t.Errorf("VECTORS must be absolute @ $FFFA, got %+v", out.Segments[1])
	}
	if out.Symbols[1].SegIndex != -1 || out.Symbols[1].Flags&SymImported == 0 {
		t.Errorf("extern_sub must be an absolute import, got %+v", out.Symbols[1])
	}
}

func TestBadMagic(t *testing.T) {
	if _, err := Read([]byte("NOPExxxxxxxxxxxx")); err == nil {
		t.Error("expected error for bad magic")
	}
	if _, err := Read([]byte{1, 2}); err == nil {
		t.Error("expected error for short input")
	}
}

func TestUnknownSectionIgnored(t *testing.T) {
	// Forward-compat: a future section kind must not break older readers.
	in := sample()
	var buf bytes.Buffer
	if err := in.Write(&buf); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// Flip the strings section kind to an unknown value... no: instead
	// just confirm a clean file still reads, then corrupt a copy's first
	// section kind to 99 and ensure Read tolerates it (segment dropped).
	b2 := append([]byte(nil), b...)
	b2[16] = 99 // first section-table entry's kind low byte
	if _, err := Read(b2); err != nil {
		t.Errorf("unknown section kind must be ignored, got %v", err)
	}
}

func mustRT(t *testing.T, f *File) *File {
	t.Helper()
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	g, err := Read(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return g
}
