package format

import "testing"

func TestCanonicalGrid(t *testing.T) {
	in := "; a counter\n" +
		"   LDX #$00\n" +
		"loop:STX ViaBase;show it\n" +
		"  INX\n" +
		"   JMP   loop      ; again\n"
	want := "; a counter\n" +
		"        LDX #$00\n" +
		"loop:   STX ViaBase         ; show it\n" +
		"        INX\n" +
		"        JMP loop            ; again\n"
	if got := Source(in); got != want {
		t.Fatalf("grid mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestIdempotent(t *testing.T) {
	cases := []string{
		"; banner\nloop:   STX ViaBase         ; show\n        JMP loop\n",
		"LDA #$2A\ndone: JMP done ; park\n",
		"   .byte $01,$03,$07,$0F\n",
		"label:\n        NOP\n",
		"        lda #';'            ; semicolon char literal\n",
		"\n\n\n   STA  $10\n\n\n",
		"*= $E000\n        weird ::: line\n", // passthrough cases
		"; only a comment\n",
	}
	for _, c := range cases {
		one := Source(c)
		two := Source(one)
		if one != two {
			t.Fatalf("not idempotent for %q:\n once=%q\n twice=%q", c, one, two)
		}
	}
}

func TestCommentDelimiterIsLiteralAware(t *testing.T) {
	// ';' inside a char/string literal must NOT start a comment.
	got := Source("        LDA #';'\n        .byte \"a;b\",$00   ; data\n")
	want := "        LDA #';'\n" +
		"        .byte \"a;b\",$00     ; data\n"
	if got != want {
		t.Fatalf("literal-aware split wrong:\n got %q\nwant %q", got, want)
	}
}

func TestConservativePassthrough(t *testing.T) {
	// A line we don't confidently understand is emitted verbatim
	// (right-trimmed) so the formatter can never corrupt a program.
	got := Source("*= $E000     \n")
	if got != "*= $E000\n" {
		t.Fatalf("passthrough altered the line: %q", got)
	}
}

func TestCommentsAndBlanks(t *testing.T) {
	// Full-line comment kept; leading blanks dropped; blank runs
	// collapse to one; exactly one trailing newline.
	got := Source("\n\n; header\n\n\n        NOP\n\n\n")
	want := "; header\n\n        NOP\n"
	if got != want {
		t.Fatalf("comments/blanks:\n got %q\nwant %q", got, want)
	}
}

func TestLabelOnlyLine(t *testing.T) {
	got := Source("done:\n   JMP done\n")
	want := "done:\n        JMP done\n"
	if got != want {
		t.Fatalf("label-only:\n got %q\nwant %q", got, want)
	}
}
