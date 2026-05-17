package diag

import (
	"strings"
	"testing"

	"github.com/carledwards/go6asm/token"
)

func TestSeverityString(t *testing.T) {
	for s, want := range map[Severity]string{
		SeverityError:   "error",
		SeverityWarning: "warning",
		SeverityNote:    "note",
		SeverityInfo:    "info",
	} {
		if got := s.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	fs := token.NewFileSet()
	src := "reset:\n    BEQ done\n"
	f := fs.AddFile("game.s", []byte(src))
	beq := strings.Index(src, "BEQ")

	d := Diagnostic{
		Severity: SeverityError,
		Code:     "E0014",
		Pos:      token.Pos{File: f, Offset: beq},
		EndPos:   token.Pos{File: f, Offset: beq + len("BEQ done")},
		Message:  "branch out of range",
		Hint:     "use BNE + JMP for an unconditional long branch",
	}
	out := d.Render()

	for _, want := range []string{
		"error[E0014]: branch out of range",
		"--> game.s:2:5",
		"2 |     BEQ done",
		"^^^^^^^^", // 8 carets over "BEQ done"
		"= hint: use BNE + JMP",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestRenderNoPos(t *testing.T) {
	out := Diagnostic{Severity: SeverityWarning, Message: "no location"}.Render()
	if out != "warning: no location\n" {
		t.Errorf("got %q", out)
	}
}
