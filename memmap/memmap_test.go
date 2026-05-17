package memmap

import "testing"

func TestParseBasic(t *testing.T) {
	cfg, err := Parse([]byte(`
# a comment
MEMORY {
    RAM: start=$0000, size=$2000, type=rw;
    ROM: start=$E000, size=$2000, type=ro, fill=yes, fillval=$FF;
}
SEGMENTS {
    CODE:    load=ROM, type=ro;
    VECTORS: load=ROM, type=ro, start=$FFFA;
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ram, ok := cfg.Area("RAM")
	if !ok || ram.Start != 0x0000 || ram.Size != 0x2000 || ram.ReadOnly {
		t.Errorf("RAM = %+v", ram)
	}
	rom, _ := cfg.Area("ROM")
	if !rom.ReadOnly || !rom.Fill || rom.FillVal != 0xFF {
		t.Errorf("ROM = %+v", rom)
	}
	if len(cfg.Segments) != 2 || cfg.Segments[0].Name != "CODE" ||
		cfg.Segments[0].Load != "ROM" || cfg.Segments[0].Start != -1 {
		t.Errorf("CODE seg = %+v", cfg.Segments[0])
	}
	if cfg.Segments[1].Start != 0xFFFA {
		t.Errorf("VECTORS start = $%X, want $FFFA", cfg.Segments[1].Start)
	}
}

func TestBuiltins(t *testing.T) {
	for _, name := range []string{"flat-64k", "sim-tui", "c64", "lcm-32"} {
		cfg, err := Builtin(name)
		if err != nil {
			t.Fatalf("Builtin(%q): %v", name, err)
		}
		if len(cfg.Memory) == 0 || len(cfg.Segments) == 0 {
			t.Errorf("%s: empty config", name)
		}
		for _, s := range cfg.Segments {
			if _, ok := cfg.Area(s.Load); !ok {
				t.Errorf("%s: segment %s loads into unknown area %q", name, s.Name, s.Load)
			}
		}
	}
	if _, err := Builtin("nope"); err == nil {
		t.Error("expected error for unknown built-in")
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"MEMORY { RAM: start=$0 ",            // unterminated
		"MEMORY { RAM: bogus=$0; }",          // unknown attribute
		"MEMORY { RAM: start=zzz, size=1; }", // bad number
		"SEGMENTS { C: load=X type=ro; }",    // missing comma/semicolon
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%q: expected a parse error", src)
		}
	}
}

func TestNumberForms(t *testing.T) {
	cfg, err := Parse([]byte("MEMORY { A: start=$10, size=4096; B: start=0x20, size=%100; }"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory[0].Start != 0x10 || cfg.Memory[0].Size != 4096 {
		t.Errorf("A = %+v", cfg.Memory[0])
	}
	if cfg.Memory[1].Start != 0x20 || cfg.Memory[1].Size != 4 {
		t.Errorf("B = %+v", cfg.Memory[1])
	}
}
