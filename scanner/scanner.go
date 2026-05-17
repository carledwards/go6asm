// Package scanner is the hand-written tokenizer for the v1 ca65 subset
// (DESIGN §6.1, §7.1). Like the standard library's go/scanner it streams
// tokens (token.Token), resolves line/column lazily via package token,
// and recovers from bad input by emitting an Illegal token and continuing
// so the parser can report multiple errors per pass.
//
// Depends only on token and diag. wasm-clean (DESIGN §4).
package scanner

import (
	"fmt"

	"github.com/carledwards/go6asm/diag"
	"github.com/carledwards/go6asm/token"
)

// Scanner tokenizes one source file.
type Scanner struct {
	file  *token.File
	src   []byte
	off   int // offset of the next byte to read
	diags []diag.Diagnostic
}

// New returns a Scanner over file.
func New(file *token.File) *Scanner {
	return &Scanner{file: file, src: file.Content()}
}

// Diagnostics returns the diagnostics collected so far (lexical errors).
func (s *Scanner) Diagnostics() []diag.Diagnostic { return s.diags }

func (s *Scanner) pos(off int) token.Pos { return token.Pos{File: s.file, Offset: off} }

func (s *Scanner) errf(off int, format string, a ...any) {
	s.diags = append(s.diags, diag.Diagnostic{
		Severity: diag.SeverityError,
		Pos:      s.pos(off),
		Message:  fmt.Sprintf(format, a...),
	})
}

// at returns the byte at offset i, or 0 at/after EOF.
func (s *Scanner) at(i int) byte {
	if i < 0 || i >= len(s.src) {
		return 0
	}
	return s.src[i]
}

func (s *Scanner) cur() byte  { return s.at(s.off) }
func (s *Scanner) peek() byte { return s.at(s.off + 1) }

// Next returns the next token. It returns an EOF token indefinitely once
// the input is exhausted.
func (s *Scanner) Next() token.Token {
	s.skipTrivia()

	start := s.off
	if s.off >= len(s.src) {
		return token.Token{Kind: token.EOF, Pos: s.pos(start)}
	}

	c := s.cur()
	switch {
	case c == '\n':
		s.off++
		return token.Token{Kind: token.Newline, Pos: s.pos(start)}

	case isIdentStart(c):
		return s.scanIdent()

	case isDigit(c) || c == '$' || c == '%':
		return s.scanNumber()

	case c == '\'':
		return s.scanChar()

	case c == '"':
		return s.scanString()

	case c == '.':
		if isIdentStart(s.peek()) {
			s.off++ // consume '.'
			t := s.scanIdent()
			t.Kind = token.DotIdent
			t.Lit = "." + t.Lit
			t.Pos = s.pos(start)
			return t
		}
		s.off++
		s.errf(start, "unexpected %q", ".")
		return token.Token{Kind: token.Illegal, Lit: ".", Pos: s.pos(start)}
	}

	return s.scanOperator(start)
}

// skipTrivia consumes spaces, tabs, carriage returns, and ';' comments.
// Newlines are significant and are NOT skipped here.
func (s *Scanner) skipTrivia() {
	for s.off < len(s.src) {
		switch s.cur() {
		case ' ', '\t', '\r':
			s.off++
		case ';':
			for s.off < len(s.src) && s.cur() != '\n' {
				s.off++
			}
		default:
			return
		}
	}
}

func (s *Scanner) scanIdent() token.Token {
	start := s.off
	for s.off < len(s.src) && isIdentPart(s.cur()) {
		s.off++
	}
	return token.Token{Kind: token.Ident, Lit: string(s.src[start:s.off]), Pos: s.pos(start)}
}

// scanNumber consumes a numeric literal and stores the raw source text
// (including any prefix). Decoding to a value is the parser's job; the
// lexer only validates enough to delimit the token. Underscores in
// numbers are not supported in v1 (DESIGN §6.1).
func (s *Scanner) scanNumber() token.Token {
	start := s.off
	c := s.cur()

	switch {
	case c == '$':
		s.off++
		if !isHex(s.cur()) {
			s.errf(start, "$ must be followed by a hex digit")
			return token.Token{Kind: token.Illegal, Lit: "$", Pos: s.pos(start)}
		}
		for isHex(s.cur()) {
			s.off++
		}
	case c == '%':
		s.off++
		if s.cur() != '0' && s.cur() != '1' {
			s.errf(start, "%% must be followed by a binary digit")
			return token.Token{Kind: token.Illegal, Lit: "%", Pos: s.pos(start)}
		}
		for s.cur() == '0' || s.cur() == '1' {
			s.off++
		}
	case c == '0' && (s.peek() == 'x' || s.peek() == 'X'):
		s.off += 2
		if !isHex(s.cur()) {
			s.errf(start, "0x must be followed by a hex digit")
			return token.Token{Kind: token.Illegal, Lit: string(s.src[start:s.off]), Pos: s.pos(start)}
		}
		for isHex(s.cur()) {
			s.off++
		}
	case c == '0' && (s.peek() == 'b' || s.peek() == 'B'):
		s.off += 2
		if s.cur() != '0' && s.cur() != '1' {
			s.errf(start, "0b must be followed by a binary digit")
			return token.Token{Kind: token.Illegal, Lit: string(s.src[start:s.off]), Pos: s.pos(start)}
		}
		for s.cur() == '0' || s.cur() == '1' {
			s.off++
		}
	default: // decimal
		for isDigit(s.cur()) {
			s.off++
		}
	}
	return token.Token{Kind: token.Int, Lit: string(s.src[start:s.off]), Pos: s.pos(start)}
}

// scanChar reads a character literal: a single (possibly escaped) byte
// between single quotes. Its numeric value is computed by the parser.
func (s *Scanner) scanChar() token.Token {
	start := s.off
	s.off++ // opening '
	if s.cur() == '\'' || s.cur() == '\n' || s.off >= len(s.src) {
		s.errf(start, "empty or unterminated character literal")
		return token.Token{Kind: token.Illegal, Pos: s.pos(start)}
	}
	b, ok := s.scanByteValue(start)
	if !ok {
		return token.Token{Kind: token.Illegal, Pos: s.pos(start)}
	}
	if s.cur() != '\'' {
		s.errf(start, "character literal must be a single character")
		s.recoverLine()
		return token.Token{Kind: token.Illegal, Pos: s.pos(start)}
	}
	s.off++ // closing '
	return token.Token{Kind: token.Char, Lit: string([]byte{b}), Pos: s.pos(start)}
}

// scanString reads a double-quoted string with \n \t \\ \" \xNN escapes
// (DESIGN §6.1) and stores the decoded value.
func (s *Scanner) scanString() token.Token {
	start := s.off
	s.off++ // opening "
	var buf []byte
	for {
		if s.off >= len(s.src) || s.cur() == '\n' {
			s.errf(start, "unterminated string literal")
			return token.Token{Kind: token.Illegal, Pos: s.pos(start)}
		}
		if s.cur() == '"' {
			s.off++ // closing "
			return token.Token{Kind: token.String, Lit: string(buf), Pos: s.pos(start)}
		}
		b, ok := s.scanByteValue(start)
		if !ok {
			s.recoverLine()
			return token.Token{Kind: token.Illegal, Pos: s.pos(start)}
		}
		buf = append(buf, b)
	}
}

// scanByteValue reads one byte of a string/char body, resolving an escape
// if the current byte is a backslash. It returns ok=false (after emitting
// a diagnostic) on a bad escape; the caller decides how to recover.
func (s *Scanner) scanByteValue(start int) (byte, bool) {
	if s.cur() != '\\' {
		b := s.cur()
		s.off++
		return b, true
	}
	s.off++ // consume backslash
	e := s.cur()
	s.off++
	switch e {
	case 'n':
		return '\n', true
	case 't':
		return '\t', true
	case '\\':
		return '\\', true
	case '"':
		return '"', true
	case '\'':
		return '\'', true
	case 'x':
		hi, lo := s.cur(), s.at(s.off+1)
		if !isHex(hi) || !isHex(lo) {
			s.errf(start, `\x requires two hex digits`)
			return 0, false
		}
		s.off += 2
		return hexVal(hi)<<4 | hexVal(lo), true
	default:
		s.errf(start, "unknown escape %q", "\\"+string(e))
		return 0, false
	}
}

// recoverLine advances to (but not past) the next newline so the parser
// can resynchronize on a statement boundary.
func (s *Scanner) recoverLine() {
	for s.off < len(s.src) && s.cur() != '\n' {
		s.off++
	}
}

func (s *Scanner) scanOperator(start int) token.Token {
	tok := func(k token.Kind, n int) token.Token {
		s.off += n
		return token.Token{Kind: k, Lit: k.String(), Pos: s.pos(start)}
	}
	c := s.cur()
	switch c {
	case ',':
		return tok(token.Comma, 1)
	case '#':
		return tok(token.Hash, 1)
	case '(':
		return tok(token.LParen, 1)
	case ')':
		return tok(token.RParen, 1)
	case '=':
		return tok(token.Eq, 1)
	case '+':
		return tok(token.Plus, 1)
	case '-':
		return tok(token.Minus, 1)
	case '*':
		return tok(token.Star, 1)
	case '/':
		return tok(token.Slash, 1)
	case '~':
		return tok(token.Tilde, 1)
	case '!':
		return tok(token.Bang, 1)
	case '^':
		return tok(token.Caret, 1)
	case '@':
		return tok(token.At, 1)
	case '&':
		if s.peek() == '&' {
			return tok(token.AndAnd, 2)
		}
		return tok(token.Amp, 1)
	case '|':
		if s.peek() == '|' {
			return tok(token.OrOr, 2)
		}
		return tok(token.Pipe, 1)
	case '<':
		switch s.peek() {
		case '<':
			return tok(token.Shl, 2)
		case '=':
			return tok(token.Le, 2)
		case '>':
			return tok(token.NotEq, 2)
		}
		return tok(token.Lt, 1)
	case '>':
		switch s.peek() {
		case '>':
			return tok(token.Shr, 2)
		case '=':
			return tok(token.Ge, 2)
		}
		return tok(token.Gt, 1)
	case ':':
		switch {
		case s.peek() == ':':
			return tok(token.ColonColon, 2)
		case s.peek() == '+' || s.peek() == '-':
			sign := s.peek()
			s.off++ // consume ':'
			from := s.off
			for s.cur() == sign {
				s.off++
			}
			return token.Token{Kind: token.UnnamedRef, Lit: string(s.src[from:s.off]), Pos: s.pos(start)}
		}
		return tok(token.Colon, 1)
	}

	s.off++
	s.errf(start, "unexpected character %q", string(c))
	return token.Token{Kind: token.Illegal, Lit: string(c), Pos: s.pos(start)}
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}

// ScanAll tokenizes the whole file (test/diagnostic convenience). The
// trailing EOF is included as the last token.
func ScanAll(file *token.File) ([]token.Token, []diag.Diagnostic) {
	s := New(file)
	var toks []token.Token
	for {
		t := s.Next()
		toks = append(toks, t)
		if t.Kind == token.EOF {
			return toks, s.Diagnostics()
		}
	}
}
