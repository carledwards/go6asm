package analyze

import (
	"strings"
	"testing"

	"github.com/carledwards/go6asm/diag"
)

func codes(r Result) map[string]bool {
	m := map[string]bool{}
	for _, d := range r.Diags {
		m[d.Code] = true
	}
	return m
}

// A clean loop: LDX #0 / INX / BNE loop / RTS — no findings.
func TestCleanProgram(t *testing.T) {
	img := []byte{0xA2, 0x00, 0xE8, 0xD0, 0xFD, 0x60}
	r := Analyze(img, Options{Origin: 0x0200, Entries: []uint16{0x0200}})
	if r.HasFindings() {
		t.Fatalf("unexpected findings: %v", r.Diags)
	}
	for _, pc := range []uint16{0x0200, 0x0202, 0x0203, 0x0205} {
		if !r.CodeStarts[pc] {
			t.Errorf("$%04X not marked code", pc)
		}
	}
}

// Fall-through into a BRK is flagged; the trailing data byte after a
// JMP is never decoded (unreachable), so no illegal-opcode noise.
func TestReachableBRK(t *testing.T) {
	// $0200 LDA #$01 ; $0202 BRK ; $0203 $FF (data, unreachable)
	img := []byte{0xA9, 0x01, 0x00, 0xFF}
	r := Analyze(img, Options{Origin: 0x0200, Entries: []uint16{0x0200}})
	if !codes(r)[codeReachBRK] {
		t.Errorf("expected reachable-BRK, got %v", r.Diags)
	}
	if r.CodeStarts[0x0203] {
		t.Error("$0203 (data past BRK) must not be code")
	}
}

func TestReachableIllegalOpcode(t *testing.T) {
	// $0200 NOP ; $0201 $02 (illegal, KIL on NMOS) reached by fallthrough
	img := []byte{0xEA, 0x02}
	r := Analyze(img, Options{Origin: 0x0200, Entries: []uint16{0x0200}})
	if !codes(r)[codeIllegal] {
		t.Errorf("expected illegal-opcode, got %v", r.Diags)
	}
}

func TestIndirectJumpBoundary(t *testing.T) {
	// JMP ($1234)
	img := []byte{0x6C, 0x34, 0x12}
	r := Analyze(img, Options{Origin: 0x0200, Entries: []uint16{0x0200}})
	cs := codes(r)
	if !cs[codeIndirect] {
		t.Errorf("expected indirect-jump note, got %v", r.Diags)
	}
	if r.HasFindings() {
		t.Errorf("indirect boundary is a Note, not a finding: %v", r.Diags)
	}
}

func TestDeviceAwareMemoryChecks(t *testing.T) {
	// $E000 STA $E010 (write into ROM)  -> A0004
	// $E003 LDA $7000 (unmapped)        -> A0005
	// $E006 RTS
	img := []byte{0x8D, 0x10, 0xE0, 0xAD, 0x00, 0x70, 0x60}
	regions := []Region{
		{Lo: 0x0000, Hi: 0x1FFF, Name: "RAM"},
		{Lo: 0xE000, Hi: 0xFFFF, ReadOnly: true, Name: "ROM"},
	}
	r := Analyze(img, Options{
		Origin: 0xE000, Entries: []uint16{0xE000}, Regions: regions,
	})
	cs := codes(r)
	if !cs[codeWriteROM] {
		t.Errorf("expected write-to-ROM, got %v", r.Diags)
	}
	if !cs[codeUnmapped] {
		t.Errorf("expected unmapped-access, got %v", r.Diags)
	}
	// Without regions, no memory diagnostics at all.
	r2 := Analyze(img, Options{Origin: 0xE000, Entries: []uint16{0xE000}})
	if codes(r2)[codeWriteROM] || codes(r2)[codeUnmapped] {
		t.Errorf("no regions ⇒ no memory checks, got %v", r2.Diags)
	}
}

func TestLeaveImageNote(t *testing.T) {
	// JMP $1234 — target outside the image.
	img := []byte{0x4C, 0x34, 0x12}
	r := Analyze(img, Options{Origin: 0x0200, Entries: []uint16{0x0200}})
	found := false
	for _, d := range r.Diags {
		if d.Code == codeLeaveImage && d.Severity == diag.SeverityNote {
			found = true
		}
	}
	if !found {
		t.Errorf("expected leave-image note, got %v", r.Diags)
	}
}

func TestRenderableMessages(t *testing.T) {
	img := []byte{0x00}
	r := Analyze(img, Options{Origin: 0x0200, Entries: []uint16{0x0200}})
	if len(r.Diags) == 0 || !strings.Contains(r.Diags[0].Message, "$0200") {
		t.Errorf("message should carry the address: %v", r.Diags)
	}
}
