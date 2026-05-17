package target

import "testing"

func TestBuiltins(t *testing.T) {
	if _, ok := Builtin("bogus"); ok {
		t.Error("bogus target should not resolve")
	}
	def, ok := Builtin("") // empty ⇒ sim-tui
	if !ok || def.Name != "sim-tui" {
		t.Fatalf(`Builtin("") = %v, want sim-tui`, def)
	}
	st, _ := Builtin("sim-tui")
	if st.CodeOrigin != 0xE000 || st.VectorAddr != 0xFFFA || !st.HasVectors {
		t.Errorf("sim-tui = %+v", st)
	}
	if st.Symbols["RegFrame"] != 0xA802 || st.Symbols["ViaIFR"] != 0xB00D {
		t.Errorf("sim-tui symbol pack wrong: %v", st.Symbols)
	}
	raw, _ := Builtin("raw")
	if raw.CodeOrigin != 0x0200 || raw.HasVectors {
		t.Errorf("raw = %+v", raw)
	}

	lcm, ok := Builtin("lcm-32")
	if !ok || lcm.CodeOrigin != 0xE000 || lcm.VectorAddr != 0xFFFA || !lcm.HasVectors {
		t.Fatalf("lcm-32 = %+v", lcm)
	}
	if lcm.Symbols["VIA1_DDRA"] != 0x6003 || lcm.Symbols["VIA2_ORA"] != 0x8001 {
		t.Errorf("lcm-32 symbol pack wrong: %v", lcm.Symbols)
	}
	var romRO bool
	for _, r := range lcm.Regions {
		if r.Name == "ROM" && r.ReadOnly && r.Lo == 0xE000 && r.Hi == 0xFFFF {
			romRO = true
		}
	}
	if !romRO {
		t.Errorf("lcm-32 ROM region missing/!readonly: %+v", lcm.Regions)
	}
}
