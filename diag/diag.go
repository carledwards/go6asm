// Package diag is the cross-cutting diagnostic type shared by every
// phase (scanner, parser, sema, codegen) and its ca65-style caret
// renderer (DESIGN §14).
//
// The standard library localizes errors in go/scanner (scanner.ErrorList).
// go6asm deliberately uses one richer, shared Diagnostic instead: better
// diagnostics than any existing 6502 assembler is an explicit project
// goal (§14), and every phase emits the same shape, so a single package
// owns it. Leaf-ish: depends only on token. wasm-clean (DESIGN §4).
package diag

import (
	"fmt"
	"strings"

	"github.com/carledwards/go6asm/token"
)

// Severity classifies a Diagnostic. SeverityInfo is non-blocking and
// silent by default (DESIGN §11.5): it records inferences, not problems.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityNote
	SeverityInfo
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityNote:
		return "note"
	case SeverityInfo:
		return "info"
	default:
		return "unknown"
	}
}

// Diagnostic is one message (DESIGN §14).
type Diagnostic struct {
	Severity Severity
	Pos      token.Pos
	EndPos   token.Pos // optional; same-file range for caret width
	Message  string
	Hint     string // optional "did you mean ..."
	Code     string // optional stable code, e.g. "E0014"
}

// Render produces the multi-line caret block from DESIGN §14:
//
//	error[E0014]: branch out of range
//	  --> game.s:42:5
//	   |
//	42 |     BEQ done
//	   |     ^^^^^^^^
//	   |
//	   = hint: use BNE + JMP for an unconditional long branch
func (d Diagnostic) Render() string {
	var b strings.Builder
	if d.Code != "" {
		fmt.Fprintf(&b, "%s[%s]: %s\n", d.Severity, d.Code, d.Message)
	} else {
		fmt.Fprintf(&b, "%s: %s\n", d.Severity, d.Message)
	}
	if !d.Pos.IsValid() {
		return b.String()
	}

	pos := d.Pos.Resolve()
	fmt.Fprintf(&b, "  --> %s\n", pos.String())

	gutter := fmt.Sprintf("%d", pos.Line)
	pad := strings.Repeat(" ", len(gutter))
	line := d.Pos.File.LineText(pos.Line)

	caretLen := 1
	if d.EndPos.IsValid() && d.EndPos.File == d.Pos.File {
		if n := d.EndPos.Offset - d.Pos.Offset; n > caretLen {
			caretLen = n
		}
	}

	fmt.Fprintf(&b, "%s |\n", pad)
	fmt.Fprintf(&b, "%s | %s\n", gutter, line)
	fmt.Fprintf(&b, "%s | %s%s\n", pad,
		strings.Repeat(" ", pos.Col-1), strings.Repeat("^", caretLen))
	fmt.Fprintf(&b, "%s |\n", pad)
	if d.Hint != "" {
		fmt.Fprintf(&b, "%s = hint: %s\n", pad, d.Hint)
	}
	return b.String()
}
