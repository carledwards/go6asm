// Package expr defines the canonical assembled-expression value type
// (DESIGN §6.4 "Expression value model"): 32-bit signed, ca65-parity,
// wraparound on overflow. Leaf package, no I/O, wasm-clean (DESIGN §4).
//
// Go's int32 arithmetic already wraps two's-complement on overflow, which
// is exactly ca65's behavior, so the wrap is free. Relational and logical
// operators yield 0/1. Division/modulo by zero is reported via ok=false,
// never a panic — the caller turns that into a diagnostic.
package expr

import "fmt"

// Value is one expression result: 32-bit signed, ca65-parity.
type Value int32

// FromInt64 narrows a CLI `-D name=value` (or any int64) into the 32-bit
// model. ok is false if v does not fit a signed OR unsigned 32-bit range,
// so a wildly out-of-range define is rejected rather than silently mangled.
func FromInt64(v int64) (Value, bool) {
	if v < -(1<<31) || v > (1<<32)-1 {
		return 0, false
	}
	return Value(int32(v)), true
}

// IsTrue reports ca65 truthiness: any non-zero value is true.
func (a Value) IsTrue() bool { return a != 0 }

func b2v(b bool) Value {
	if b {
		return 1
	}
	return 0
}

// Arithmetic. Overflow wraps mod 2^32 (Go int32 semantics == ca65).
func (a Value) Add(b Value) Value { return a + b }
func (a Value) Sub(b Value) Value { return a - b }
func (a Value) Mul(b Value) Value { return a * b }
func (a Value) Neg() Value        { return -a }

// Div is signed integer division. ok is false on divide-by-zero.
func (a Value) Div(b Value) (Value, bool) {
	if b == 0 {
		return 0, false
	}
	return a / b, true
}

// Mod is signed remainder (ca65 `.mod`). ok is false on modulo-by-zero.
func (a Value) Mod(b Value) (Value, bool) {
	if b == 0 {
		return 0, false
	}
	return a % b, true
}

// Bitwise.
func (a Value) And(b Value) Value { return a & b }
func (a Value) Or(b Value) Value  { return a | b }
func (a Value) Xor(b Value) Value { return a ^ b }
func (a Value) Cpl() Value        { return ^a } // ca65 unary ~

// Shl/Shr take the shift count from b. A negative or >=32 count yields 0
// (Shl) or a full sign-fill (Shr); exact ca65 corner behavior for large
// counts is pinned against the compat suite (§6.5).
func (a Value) Shl(b Value) Value {
	if b < 0 || b >= 32 {
		return 0
	}
	return a << uint(b)
}

func (a Value) Shr(b Value) Value {
	if b < 0 || b >= 32 {
		if a < 0 {
			return -1
		}
		return 0
	}
	return a >> uint(b) // arithmetic: signed model
}

// Relational (signed) and logical operators yield 0 or 1.
func (a Value) Eq(b Value) Value { return b2v(a == b) }
func (a Value) Ne(b Value) Value { return b2v(a != b) }
func (a Value) Lt(b Value) Value { return b2v(a < b) }
func (a Value) Le(b Value) Value { return b2v(a <= b) }
func (a Value) Gt(b Value) Value { return b2v(a > b) }
func (a Value) Ge(b Value) Value { return b2v(a >= b) }

func (a Value) LogAnd(b Value) Value { return b2v(a != 0 && b != 0) }
func (a Value) LogOr(b Value) Value  { return b2v(a != 0 || b != 0) }
func (a Value) LogNot() Value        { return b2v(a == 0) } // ca65 unary !

// Byte extraction (DESIGN §6.4): `<` low, `>` high, `^` bank (0 for 6502).
func (a Value) LoByte() Value   { return a & 0xFF }
func (a Value) HiByte() Value   { return (a >> 8) & 0xFF }
func (a Value) BankByte() Value { return (a >> 16) & 0xFF }

// FitsByte reports whether a is emittable by .byte: ca65 accepts both an
// unsigned byte and a signed byte, i.e. -128..255.
func (a Value) FitsByte() bool { return a >= -128 && a <= 255 }

// FitsWord reports whether a is emittable by .word: -32768..65535.
func (a Value) FitsWord() bool { return a >= -32768 && a <= 65535 }

// U8 returns the low 8 bits as a byte (caller should have checked FitsByte).
func (a Value) U8() byte { return byte(a & 0xFF) }

// U16 returns the low 16 bits (caller should have checked FitsWord).
func (a Value) U16() uint16 { return uint16(a & 0xFFFF) }

// RelBranch computes a relative-branch operand: the signed offset from the
// instruction at pc to target, measured from the byte after the 2-byte
// branch (pc+2). ok is false if the offset is out of -128..127 range —
// the source of diagnostic E0014 (DESIGN §14).
func RelBranch(pc, target Value) (int8, bool) {
	off := int64(target) - (int64(pc) + 2)
	if off < -128 || off > 127 {
		return 0, false
	}
	return int8(off), true
}

// String renders a value for diagnostics as decimal plus hex.
func (a Value) String() string {
	return fmt.Sprintf("%d ($%X)", int32(a), uint32(a))
}
