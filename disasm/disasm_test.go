package disasm

import (
	"strings"
	"testing"
)

func TestDisassembleWithSymbolsAndComments(t *testing.T) {
	// $0200 LDA msg,X      ; load char
	// $0203 BEQ msg        (operand 3: $0205+3 = $0208 = msg)
	// $0205 JMP start      ; loop forever
	// $0208 msg: data byte $7E
	img := []byte{
		0xBD, 0x08, 0x02, // LDA $0208,X
		0xF0, 0x03, // BEQ -> $0208
		0x4C, 0x00, 0x02, // JMP $0200
		0x7E, // data
	}
	lines := Disassemble(img, Options{
		Origin:  0x0200,
		Symbols: []Sym{{"start", 0x0200}, {"msg", 0x0208}},
		Comments: map[uint16]string{
			0x0200: "load char",
			0x0205: "loop forever",
		},
	})

	if lines[0].Label != "start" {
		t.Errorf("line0 label = %q, want start", lines[0].Label)
	}
	if lines[0].Text != "LDA msg,X" {
		t.Errorf("line0 text = %q, want LDA msg,X", lines[0].Text)
	}
	if lines[0].Comment != "load char" {
		t.Errorf("line0 comment = %q", lines[0].Comment)
	}
	if lines[1].Text != "BEQ msg" {
		t.Errorf("line1 text = %q, want BEQ msg", lines[1].Text)
	}
	if lines[2].Text != "JMP start" || lines[2].Comment != "loop forever" {
		t.Errorf("line2 = %q / %q", lines[2].Text, lines[2].Comment)
	}
	// $0208 is the 'msg' data byte: not reached by linear decode as a
	// clean instruction boundary here ($7E = ROR abs needs 2 operand
	// bytes that run off the image → renders as .byte).
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last.Text, ".byte $7E") {
		t.Errorf("last line = %q, want .byte $7E", last.Text)
	}

	out := Render(lines)
	if !strings.Contains(out, "start:\n") ||
		!strings.Contains(out, "LDA msg,X") ||
		!strings.Contains(out, "; load char") {
		t.Errorf("render missing expected content:\n%s", out)
	}
}

func TestDisassembleIllegalOpcode(t *testing.T) {
	lines := Disassemble([]byte{0x02, 0xEA}, Options{Origin: 0x0200})
	if lines[0].Text != ".byte $02" {
		t.Errorf("illegal $02 -> %q, want .byte $02", lines[0].Text)
	}
	if lines[1].Text != "NOP" {
		t.Errorf("line1 = %q, want NOP", lines[1].Text)
	}
}
