package isa

//go:generate go run gen/gen.go -o nmos6502_table.go

// NMOS6502 is the v1 target: official NMOS 6502 opcodes only. Illegal /
// undocumented opcodes are deliberately excluded (DESIGN §6.3); they
// arrive later behind --allow-illegal.
//
// nmos6502Table is generated from ca65 by `go generate` (see gen/), so
// the encodings are verified against the byte-identical contract (§6.5)
// rather than hand-transcribed.
var NMOS6502 = newCPU("6502", nmos6502Table)

func e(m Mnemonic, a Mode, op byte) Encoding {
	return Encoding{Mnemonic: m, Mode: a, Opcode: op} // Bytes filled by newCPU
}
