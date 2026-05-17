package expr

import "testing"

func TestWraparound(t *testing.T) {
	// ca65-parity: overflow wraps mod 2^32.
	max := Value(0x7FFFFFFF)
	if got := max.Add(1); got != Value(-1<<31) {
		t.Errorf("0x7FFFFFFF + 1 = %d, want %d", int32(got), int32(-1<<31))
	}
	if got := Value(-1).Mul(Value(-1)); got != 1 {
		t.Errorf("-1 * -1 = %d, want 1", int32(got))
	}
}

func TestFromInt64(t *testing.T) {
	cases := []struct {
		in   int64
		want Value
		ok   bool
	}{
		{0, 0, true},
		{255, 255, true},
		{-128, -128, true},
		{0xFFFFFFFF, -1, true},     // unsigned 32-bit wraps to signed
		{1 << 32, 0, false},        // too big
		{-(1 << 31) - 1, 0, false}, // too negative
	}
	for _, c := range cases {
		got, ok := FromInt64(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("FromInt64(%d) = (%d, %v), want (%d, %v)",
				c.in, int32(got), ok, int32(c.want), c.ok)
		}
	}
}

func TestDivModByZero(t *testing.T) {
	if _, ok := Value(10).Div(0); ok {
		t.Error("Div by zero must report ok=false")
	}
	if _, ok := Value(10).Mod(0); ok {
		t.Error("Mod by zero must report ok=false")
	}
	if q, ok := Value(7).Div(2); !ok || q != 3 {
		t.Errorf("7/2 = (%d,%v), want (3,true)", int32(q), ok)
	}
	if r, ok := Value(7).Mod(3); !ok || r != 1 {
		t.Errorf("7%%3 = (%d,%v), want (1,true)", int32(r), ok)
	}
}

func TestRelationalAndLogical(t *testing.T) {
	if Value(3).Lt(5) != 1 || Value(5).Lt(3) != 0 {
		t.Error("Lt signed compare wrong")
	}
	if Value(-1).Lt(0) != 1 {
		t.Error("-1 < 0 must be true (signed)")
	}
	if Value(0).LogOr(0) != 0 || Value(0).LogOr(7) != 1 {
		t.Error("LogOr wrong")
	}
	if Value(5).LogNot() != 0 || Value(0).LogNot() != 1 {
		t.Error("LogNot wrong")
	}
}

func TestByteExtraction(t *testing.T) {
	v := Value(0x12ABCD)
	if v.LoByte() != 0xCD || v.HiByte() != 0xAB || v.BankByte() != 0x12 {
		t.Errorf("byte extraction wrong: lo=%X hi=%X bank=%X",
			int32(v.LoByte()), int32(v.HiByte()), int32(v.BankByte()))
	}
}

func TestFitChecks(t *testing.T) {
	if !Value(255).FitsByte() || !Value(-128).FitsByte() || Value(256).FitsByte() {
		t.Error("FitsByte boundary wrong")
	}
	if !Value(65535).FitsWord() || Value(65536).FitsWord() {
		t.Error("FitsWord boundary wrong")
	}
	if Value(0xBEEF).U16() != 0xBEEF || Value(0x1FF).U8() != 0xFF {
		t.Error("U8/U16 truncation wrong")
	}
}

func TestRelBranch(t *testing.T) {
	// Forward to the next instruction after a 2-byte branch: offset 0.
	if off, ok := RelBranch(0x1000, 0x1002); !ok || off != 0 {
		t.Errorf("RelBranch self+2 = (%d,%v), want (0,true)", off, ok)
	}
	if off, ok := RelBranch(0x1000, 0x1081); !ok || off != 127 {
		t.Errorf("max forward = (%d,%v), want (127,true)", off, ok)
	}
	if _, ok := RelBranch(0x1000, 0x1082); ok {
		t.Error("offset +128 must be out of range")
	}
	if off, ok := RelBranch(0x1000, 0x0F82); !ok || off != -128 {
		t.Errorf("max backward = (%d,%v), want (-128,true)", off, ok)
	}
	if _, ok := RelBranch(0x1000, 0x0F81); ok {
		t.Error("offset -129 must be out of range")
	}
}
