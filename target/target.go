// Package target bundles a platform's Layer-0 authoring defaults
// (DESIGN §11): where code loads, where the NMI/RESET/IRQ vector block
// goes, and a symbol pack of the platform's well-known register names so
// Layer-0 source can write STA RegFrame instead of STA $A802.
//
// Leaf package (stdlib only) and wasm-clean (DESIGN §4): it is pure data
// + lookups; the assembler turns it into inference.
package target

// Target describes one platform's Layer-0 inference inputs.
type Target struct {
	Name string
	// CodeOrigin is the default load address when the source has no
	// .org/.load/.segment (DESIGN §11.2).
	CodeOrigin uint16
	// VectorAddr is the base of the 6-byte NMI/RESET/IRQ block
	// (NMI @ +0, RESET @ +2, IRQ @ +4 — the standard 6502 layout).
	VectorAddr uint16
	// HasVectors is false for "raw" (no vector block synthesized).
	HasVectors bool
	// Symbols is the platform register/command name pack, injected as
	// predefined constants (user -D and source `=` still win).
	Symbols map[string]int64
	// Regions is the device address map for the static analyzer's
	// device-aware checks (write-to-ROM, unmapped access). Addresses
	// in no region are "unmapped". Empty ⇒ no device check (e.g. raw).
	Regions []Region
}

// Region is one span of a target's address space.
type Region struct {
	Lo, Hi   uint16 // inclusive
	ReadOnly bool   // a store here is a fault (ROM)
	Name     string
}

// simTUI mirrors 6502-sim-tui's documented memory map: ROM at $E000,
// the 6502 vector block at $FFFA, and the VIC/VIA register names its
// demos use (internal/demos/demos.go).
var simTUI = &Target{
	Name:       "sim-tui",
	CodeOrigin: 0xE000,
	VectorAddr: 0xFFFA,
	HasVectors: true,
	Symbols: map[string]int64{
		// VIC controller registers.
		"RegCmd": 0xA800, "RegPause": 0xA801, "RegFrame": 0xA802,
		"RegRectX": 0xA803, "RegRectY": 0xA804, "RegRectW": 0xA805,
		"RegRectH": 0xA806, "RegGfxColor": 0xA807, "RegMode": 0xA808,
		// VIC commands.
		"CmdClear": 0x01, "CmdShiftUp": 0x02, "CmdRotLeft": 0x07,
		"CmdGfxClear": 0x20, "CmdGfxRectFill": 0x23, "CmdGfxFillCircle": 0x26,
		// Plane bases.
		"ColorBase": 0xA000, "CharBase": 0xA400,
		// 6522 VIA #1 (timer pacing).
		"ViaBase": 0xB000, "ViaT1C_L": 0xB004, "ViaT1C_H": 0xB005,
		"ViaT1L_L": 0xB006, "ViaT1L_H": 0xB007, "ViaACR": 0xB00B,
		"ViaIFR": 0xB00D, "ViaIER": 0xB00E, "ViaT1Bit": 0x40,
	},
	// 6502-sim-tui memory map (internal/demos/demos.go header):
	// RAM $0000-$1FFF, VIC planes/regs + VIA $A000-$DFFF, ROM
	// $E000-$FFFF. $2000-$9FFF is unmapped — a store there is a bug.
	Regions: []Region{
		{Lo: 0x0000, Hi: 0x1FFF, Name: "RAM"},
		{Lo: 0xA000, Hi: 0xDFFF, Name: "VIC/VIA"},
		{Lo: 0xE000, Hi: 0xFFFF, ReadOnly: true, Name: "ROM"},
	},
}

// lcm32 is carledwards/lcm-32 — a bare-metal 6502 board: 8 KB RAM,
// two W65C22 VIAs (LEDs on the ports), 8 KB ROM, vectors $FFFA. Map
// from carledwards/6502-simulator (examples/lcm32.ts). No display.
var lcm32 = &Target{
	Name:       "lcm-32",
	CodeOrigin: 0xE000,
	VectorAddr: 0xFFFA,
	HasVectors: true,
	Symbols: map[string]int64{
		// VIA bases.
		"VIA1": 0x6000, "VIA2": 0x8000,
		// W65C22 #1 @ $6000 (standard register order).
		"VIA1_ORB": 0x6000, "VIA1_ORA": 0x6001, "VIA1_DDRB": 0x6002,
		"VIA1_DDRA": 0x6003, "VIA1_T1CL": 0x6004, "VIA1_T1CH": 0x6005,
		"VIA1_T1LL": 0x6006, "VIA1_T1LH": 0x6007, "VIA1_T2CL": 0x6008,
		"VIA1_T2CH": 0x6009, "VIA1_SR": 0x600A, "VIA1_ACR": 0x600B,
		"VIA1_PCR": 0x600C, "VIA1_IFR": 0x600D, "VIA1_IER": 0x600E,
		"VIA1_ORAH": 0x600F,
		// W65C22 #2 @ $8000.
		"VIA2_ORB": 0x8000, "VIA2_ORA": 0x8001, "VIA2_DDRB": 0x8002,
		"VIA2_DDRA": 0x8003, "VIA2_T1CL": 0x8004, "VIA2_T1CH": 0x8005,
		"VIA2_T1LL": 0x8006, "VIA2_T1LH": 0x8007, "VIA2_T2CL": 0x8008,
		"VIA2_T2CH": 0x8009, "VIA2_SR": 0x800A, "VIA2_ACR": 0x800B,
		"VIA2_PCR": 0x800C, "VIA2_IFR": 0x800D, "VIA2_IER": 0x800E,
		"VIA2_ORAH": 0x800F,
	},
	// LCM-32 map: RAM $0000-$1FFF, VIA1 $6000-$600F, VIA2 $8000-$800F,
	// ROM $E000-$FFFF. Everything else is unmapped.
	Regions: []Region{
		{Lo: 0x0000, Hi: 0x1FFF, Name: "RAM"},
		{Lo: 0x6000, Hi: 0x600F, Name: "VIA1"},
		{Lo: 0x8000, Hi: 0x800F, Name: "VIA2"},
		{Lo: 0xE000, Hi: 0xFFFF, ReadOnly: true, Name: "ROM"},
	},
}

// raw is "no platform": a flat image, no synthesized vectors.
var raw = &Target{
	Name:       "raw",
	CodeOrigin: 0x0200,
	HasVectors: false,
}

var builtins = map[string]*Target{
	"sim-tui": simTUI,
	"lcm-32":  lcm32,
	"raw":     raw,
}

// Builtin returns the named target. The default (empty name) is sim-tui:
// Layer 0 assumes a platform so "just instructions" boots somewhere
// (DESIGN §11.2). Ask for "raw" to opt out.
func Builtin(name string) (*Target, bool) {
	if name == "" {
		name = "sim-tui"
	}
	t, ok := builtins[name]
	return t, ok
}

// Names lists the built-in target names (for diagnostics/help).
func Names() []string { return []string{"raw", "sim-tui", "lcm-32"} }
