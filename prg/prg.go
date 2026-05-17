// Package prg wraps a linked image in the Commodore PRG container: a
// two-byte little-endian load address followed by the raw bytes. This
// is what a C64 (and most Commodore machines, and the Ultimate
// hardware) expects from LOAD / a PRG runner.
//
// Leaf package (stdlib only) and wasm-clean (DESIGN §4): pure byte
// transform, no I/O.
package prg

// Wrap returns loadAddr (little-endian) followed by image — the
// Commodore .prg layout. An empty image still yields the 2-byte header
// (a valid, do-nothing PRG).
func Wrap(loadAddr uint16, image []byte) []byte {
	out := make([]byte, 0, len(image)+2)
	out = append(out, byte(loadAddr), byte(loadAddr>>8))
	return append(out, image...)
}

// LoadAddr reads the 2-byte header back. ok is false if b is too short
// to be a PRG.
func LoadAddr(b []byte) (addr uint16, ok bool) {
	if len(b) < 2 {
		return 0, false
	}
	return uint16(b[0]) | uint16(b[1])<<8, true
}
