package prg

import (
	"bytes"
	"testing"
)

func TestWrapRoundTrip(t *testing.T) {
	img := []byte{0xA9, 0x01, 0x8D, 0x20, 0xD0, 0x60}
	p := Wrap(0x0801, img)

	want := append([]byte{0x01, 0x08}, img...) // $0801 little-endian
	if !bytes.Equal(p, want) {
		t.Fatalf("Wrap = % X, want % X", p, want)
	}
	addr, ok := LoadAddr(p)
	if !ok || addr != 0x0801 {
		t.Errorf("LoadAddr = $%04X (%v), want $0801", addr, ok)
	}
	if !bytes.Equal(p[2:], img) {
		t.Errorf("body = % X, want % X", p[2:], img)
	}
}

func TestWrapEmpty(t *testing.T) {
	p := Wrap(0xE000, nil)
	if !bytes.Equal(p, []byte{0x00, 0xE0}) {
		t.Errorf("empty PRG = % X, want 00 E0", p)
	}
}

func TestLoadAddrShort(t *testing.T) {
	if _, ok := LoadAddr([]byte{0x01}); ok {
		t.Error("LoadAddr on 1 byte should fail")
	}
}
