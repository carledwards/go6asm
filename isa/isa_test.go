package isa

import "testing"

func TestLookupKnownOpcodes(t *testing.T) {
	cases := []struct {
		m     Mnemonic
		a     Mode
		op    byte
		bytes int
	}{
		{"LDA", Immediate, 0xA9, 2},
		{"LDA", AbsoluteX, 0xBD, 3},
		{"LDX", ZeroPageY, 0xB6, 2},
		{"STA", IndexedIndirect, 0x81, 2},
		{"JMP", Indirect, 0x6C, 3},
		{"JSR", Absolute, 0x20, 3},
		{"RTS", Implied, 0x60, 1},
		{"BEQ", Relative, 0xF0, 2},
		{"BRK", Implied, 0x00, 1},
	}
	for _, c := range cases {
		got, ok := NMOS6502.Lookup(c.m, c.a)
		if !ok {
			t.Errorf("%s %s: not found", c.m, c.a)
			continue
		}
		if got.Opcode != c.op {
			t.Errorf("%s %s: opcode %#02x, want %#02x", c.m, c.a, got.Opcode, c.op)
		}
		if got.Bytes != c.bytes {
			t.Errorf("%s %s: bytes %d, want %d", c.m, c.a, got.Bytes, c.bytes)
		}
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	for _, enc := range nmos6502Table {
		got, ok := NMOS6502.Decode(enc.Opcode)
		if !ok {
			t.Errorf("opcode %#02x (%s) does not decode", enc.Opcode, enc.Mnemonic)
			continue
		}
		if got.Mnemonic != enc.Mnemonic || got.Mode != enc.Mode {
			t.Errorf("opcode %#02x decoded to %s %s, want %s %s",
				enc.Opcode, got.Mnemonic, got.Mode, enc.Mnemonic, enc.Mode)
		}
	}
}

func TestIllegalAndMissing(t *testing.T) {
	// 0x02 is a JAM/illegal opcode — must not be in the v1 table.
	if _, ok := NMOS6502.Decode(0x02); ok {
		t.Error("illegal opcode 0x02 must not decode in v1")
	}
	if _, ok := NMOS6502.Lookup("LDA", Implied); ok {
		t.Error("LDA has no implied mode")
	}
	if !NMOS6502.HasMnemonic("LDA") {
		t.Error("LDA must be a known mnemonic")
	}
	if NMOS6502.HasMnemonic("LAX") {
		t.Error("LAX is illegal; must not be known in v1")
	}
}

func TestOfficialOpcodeCount(t *testing.T) {
	// The official NMOS 6502 has exactly 151 documented opcodes. The
	// generated table is derived from ca65; this guards regenerations.
	if len(nmos6502Table) != 151 {
		t.Fatalf("nmos6502Table has %d opcodes, want 151", len(nmos6502Table))
	}
}

func TestModeOperandBytes(t *testing.T) {
	for m, want := range map[Mode]int{
		Implied:         0,
		Accumulator:     0,
		Immediate:       1,
		ZeroPageX:       1,
		IndirectIndexed: 1,
		Relative:        1,
		Absolute:        2,
		AbsoluteY:       2,
		Indirect:        2,
	} {
		if got := m.OperandBytes(); got != want {
			t.Errorf("%s.OperandBytes() = %d, want %d", m, got, want)
		}
	}
}
