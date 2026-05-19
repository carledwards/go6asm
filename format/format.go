// Package format is go6asm's canonical source pretty-printer — the
// `gofmt` of this assembler.
//
// go6asm's scanner intentionally discards comments (they carry no
// assembly meaning), so a comment-preserving AST round-trip is not
// possible without threading comments through the whole pipeline.
// Assembly is line-oriented (ca65: one statement per line), so this
// formatter works lexically per line instead: it splits each line at
// the real comment delimiter (string/char-literal aware), normalizes
// the code field onto a fixed grid, and realigns the trailing comment
// to a column. Full-line comments and single blank lines are kept;
// runs of blanks collapse to one.
//
// It is deliberately *conservative*: any line it cannot confidently
// classify is emitted verbatim (right-trimmed). The formatter therefore
// never changes what the program assembles to — worst case it leaves a
// line alone. Idempotent: Source(Source(x)) == Source(x).
//
// stdlib-only → wasm-clean (DESIGN §4).
package format

import "strings"

// Canonical columns (0-based). 6502 mnemonics are 3 chars, so a 3-char
// mnemonic at MnemonicCol puts its operand at MnemonicCol+4.
const (
	MnemonicCol = 8
	CommentCol  = 28
)

// Source returns src reformatted onto the canonical grid. Total and
// idempotent; never returns an error (unparseable lines pass through).
func Source(src string) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	pendingBlank := false

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			pendingBlank = len(out) > 0 // ignore leading blanks; collapse runs
			continue
		}
		if pendingBlank {
			out = append(out, "")
			pendingBlank = false
		}

		code, comment := splitComment(line)
		code = strings.TrimSpace(code)

		if code == "" { // a full-line comment
			out = append(out, comment)
			continue
		}
		label, mnem, operand, ok := parseCode(code)
		if !ok {
			out = append(out, line) // conservative: leave it exactly as-is
			continue
		}
		out = append(out, render(label, mnem, operand, comment))
	}
	return strings.Join(out, "\n") + "\n"
}

// splitComment splits a line into its code part and a normalized
// comment ("" if none). A ';' inside a "..." or '...' literal is not a
// comment. The returned comment is "; " + trimmed text (decorative
// banners are preserved as their own text after the single space).
func splitComment(line string) (code, comment string) {
	inStr, inChar := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inStr:
			if c == '"' {
				inStr = false
			}
		case inChar:
			if c == '\'' {
				inChar = false
			}
		case c == '"':
			inStr = true
		case c == '\'':
			inChar = true
		case c == ';':
			text := strings.TrimRight(strings.TrimLeft(line[i+1:], " \t;"), " \t")
			if text == "" {
				return line[:i], ";"
			}
			return line[:i], "; " + text
		}
	}
	return line, ""
}

// parseCode pulls an optional `label:` then a mnemonic/directive and the
// rest as the operand. ok=false means "I'm not confident" → caller
// passes the line through untouched.
func parseCode(code string) (label, mnem, operand string, ok bool) {
	rest := code
	if i := labelEnd(rest); i > 0 {
		label = rest[:i] // includes the trailing ':'
		rest = strings.TrimLeft(rest[i:], " \t")
	}
	if rest == "" {
		return label, "", "", label != "" // label-only line
	}
	sp := strings.IndexAny(rest, " \t")
	if sp < 0 {
		mnem = rest
	} else {
		mnem = rest[:sp]
		operand = strings.TrimSpace(rest[sp+1:])
	}
	// A mnemonic/directive is letters (or a leading '.'); anything else
	// (e.g. `*=`, macro invocations with odd syntax) → pass through.
	if !looksLikeMnemonic(mnem) {
		return "", "", "", false
	}
	return label, mnem, operand, true
}

// labelEnd returns the index just past a leading "ident:" (incl. ':'),
// or 0 if the line doesn't start with a label. Accepts ca65 identifier
// chars plus a leading '@' (cheap locals).
func labelEnd(s string) int {
	if s == "" {
		return 0
	}
	i := 0
	if s[0] == '@' {
		i = 1
	}
	if i >= len(s) || !isIdentStart(s[i]) {
		return 0
	}
	for i < len(s) && isIdentPart(s[i]) {
		i++
	}
	if i < len(s) && s[i] == ':' {
		return i + 1
	}
	return 0
}

func looksLikeMnemonic(m string) bool {
	if m == "" {
		return false
	}
	if m[0] == '.' { // directive, e.g. .byte
		m = m[1:]
	}
	if m == "" || !isAlpha(m[0]) {
		return false
	}
	for i := 0; i < len(m); i++ {
		if !isAlpha(m[i]) && !isDigit(m[i]) {
			return false
		}
	}
	return true
}

func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return isAlpha(c) || c == '_' }
func isIdentPart(c byte) bool  { return isAlpha(c) || isDigit(c) || c == '_' }

// render lays the parsed pieces on the grid.
func render(label, mnem, operand, comment string) string {
	var b strings.Builder

	if mnem == "" { // label-only line
		b.WriteString(label)
	} else {
		if label != "" {
			b.WriteString(label)
			if len(label) < MnemonicCol {
				b.WriteString(strings.Repeat(" ", MnemonicCol-len(label)))
			} else {
				b.WriteByte(' ')
			}
		} else {
			b.WriteString(strings.Repeat(" ", MnemonicCol))
		}
		b.WriteString(mnem)
		if operand != "" {
			b.WriteByte(' ')
			b.WriteString(operand)
		}
	}

	if comment == "" {
		return strings.TrimRight(b.String(), " ")
	}
	n := b.Len()
	if n < CommentCol {
		b.WriteString(strings.Repeat(" ", CommentCol-n))
	} else {
		b.WriteByte(' ')
	}
	b.WriteString(comment)
	return b.String()
}
