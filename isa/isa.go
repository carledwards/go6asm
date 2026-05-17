// Package isa holds CPU instruction tables: mnemonics, addressing modes,
// and opcode encodings.
//
// It is a leaf package — pure data and lookups, no I/O — and obeys the
// core-wasm invariant (DESIGN §4, §6.3). NMOS 6502 is the only v1 target;
// 65C02/65816 are pluggable later behind the same CPU type.
package isa

// Mnemonic is a canonical uppercase instruction name, e.g. "LDA".
// Mnemonics are case-insensitive on input but canonical uppercase in
// listings (DESIGN §6.3).
type Mnemonic string

// Mode is a 6502 addressing mode.
type Mode uint8

const (
	Implied         Mode = iota // RTS
	Accumulator                 // ASL A
	Immediate                   // LDA #$10
	ZeroPage                    // LDA $10
	ZeroPageX                   // LDA $10,X
	ZeroPageY                   // LDX $10,Y
	Absolute                    // LDA $1000
	AbsoluteX                   // LDA $1000,X
	AbsoluteY                   // LDA $1000,Y
	Indirect                    // JMP ($1000)
	IndexedIndirect             // LDA ($10,X)
	IndirectIndexed             // LDA ($10),Y
	Relative                    // BEQ label
)

var modeNames = [...]string{
	Implied:         "implied",
	Accumulator:     "accumulator",
	Immediate:       "immediate",
	ZeroPage:        "zeropage",
	ZeroPageX:       "zeropage,X",
	ZeroPageY:       "zeropage,Y",
	Absolute:        "absolute",
	AbsoluteX:       "absolute,X",
	AbsoluteY:       "absolute,Y",
	Indirect:        "(indirect)",
	IndexedIndirect: "(indirect,X)",
	IndirectIndexed: "(indirect),Y",
	Relative:        "relative",
}

func (m Mode) String() string {
	if int(m) < len(modeNames) {
		return modeNames[m]
	}
	return "unknown"
}

// OperandBytes is the number of operand bytes a mode carries (0, 1, or 2),
// excluding the opcode byte.
func (m Mode) OperandBytes() int {
	switch m {
	case Implied, Accumulator:
		return 0
	case Absolute, AbsoluteX, AbsoluteY, Indirect:
		return 2
	default: // Immediate, ZeroPage*, (ind*), Relative
		return 1
	}
}

// Encoding is one legal (Mnemonic, Mode) → opcode entry. Bytes is the
// total instruction length including the opcode (1..3).
type Encoding struct {
	Mnemonic Mnemonic
	Mode     Mode
	Opcode   byte
	Bytes    int
}

type encKey struct {
	m Mnemonic
	a Mode
}

// CPU is an instruction set. Look up by (mnemonic, mode) for encoding, or
// by opcode for decoding.
type CPU struct {
	Name      string
	byKey     map[encKey]Encoding
	byOpcode  [256]*Encoding
	mnemonics map[Mnemonic]struct{}
}

// newCPU builds a CPU from an encoding table. It panics on a duplicate
// (mnemonic, mode) or opcode, since the tables are compile-time constants
// and a clash is a programming error, not user input.
func newCPU(name string, encs []Encoding) *CPU {
	c := &CPU{
		Name:      name,
		byKey:     make(map[encKey]Encoding, len(encs)),
		mnemonics: make(map[Mnemonic]struct{}),
	}
	for _, e := range encs {
		if e.Bytes == 0 {
			e.Bytes = 1 + e.Mode.OperandBytes()
		}
		k := encKey{e.Mnemonic, e.Mode}
		if _, dup := c.byKey[k]; dup {
			panic("isa: duplicate encoding for " + string(e.Mnemonic) + " " + e.Mode.String())
		}
		if c.byOpcode[e.Opcode] != nil {
			panic("isa: duplicate opcode in " + name)
		}
		c.byKey[k] = e
		ec := e
		c.byOpcode[e.Opcode] = &ec
		c.mnemonics[e.Mnemonic] = struct{}{}
	}
	return c
}

// Lookup returns the encoding for a mnemonic in a given mode. ok is false
// if that combination is not a legal instruction.
func (c *CPU) Lookup(m Mnemonic, a Mode) (Encoding, bool) {
	e, ok := c.byKey[encKey{m, a}]
	return e, ok
}

// Decode returns the encoding for an opcode byte. ok is false for an
// undefined opcode (illegal/undocumented NMOS ops are not in the v1 table).
func (c *CPU) Decode(op byte) (Encoding, bool) {
	if e := c.byOpcode[op]; e != nil {
		return *e, true
	}
	return Encoding{}, false
}

// HasMnemonic reports whether m exists in any addressing mode.
func (c *CPU) HasMnemonic(m Mnemonic) bool {
	_, ok := c.mnemonics[m]
	return ok
}
