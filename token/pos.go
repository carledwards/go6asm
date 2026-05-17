// Package token defines source positions, an in-memory file set, and the
// lexical token vocabulary for the v1 ca65 subset. It mirrors the role of
// the standard library's go/token: positions + token kinds in one leaf
// package (DESIGN §6.1, §7.1).
//
// It holds file contents in memory and never opens a path itself, so it
// compiles and runs unchanged under GOOS=js GOARCH=wasm (DESIGN §4).
package token

import (
	"fmt"
	"sort"
)

// FileSet owns the source files for one assembly run.
type FileSet struct {
	files []*File
}

// NewFileSet returns an empty FileSet.
func NewFileSet() *FileSet { return &FileSet{} }

// AddFile registers a file's contents and returns a handle. name is used
// only for diagnostics; it is never opened.
func (fs *FileSet) AddFile(name string, content []byte) *File {
	f := &File{name: name, content: content}
	fs.files = append(fs.files, f)
	return f
}

// Files returns the registered files in insertion order.
func (fs *FileSet) Files() []*File { return fs.files }

// File is one in-memory source file.
type File struct {
	name    string
	content []byte
	lines   []int // byte offset of each line start; lines[0] == 0
}

// Name returns the file's diagnostic name.
func (f *File) Name() string { return f.name }

// Content returns the file's bytes.
func (f *File) Content() []byte { return f.content }

func (f *File) ensureLines() {
	if f.lines != nil {
		return
	}
	f.lines = []int{0}
	for i, b := range f.content {
		if b == '\n' {
			f.lines = append(f.lines, i+1)
		}
	}
}

// LineText returns the text of 1-based line n, without its line ending.
func (f *File) LineText(n int) string {
	f.ensureLines()
	if n < 1 || n > len(f.lines) {
		return ""
	}
	start := f.lines[n-1]
	end := len(f.content)
	if n < len(f.lines) {
		end = f.lines[n] - 1 // drop '\n'
	}
	if end > start && f.content[end-1] == '\r' {
		end-- // drop '\r' from CRLF
	}
	return string(f.content[start:end])
}

// Pos is a compact source position: a file plus a byte offset. Line and
// column are resolved lazily so the hot path stays cheap (DESIGN §7.1).
type Pos struct {
	File   *File
	Offset int
}

// NoPos is the zero, invalid position.
var NoPos = Pos{}

// IsValid reports whether p refers to a file.
func (p Pos) IsValid() bool { return p.File != nil }

// Position is a resolved, human-facing location. Line and Col are 1-based;
// Col counts bytes (sufficient for ASCII 6502 source).
type Position struct {
	Filename string
	Line     int
	Col      int
	Offset   int
}

func (p Position) String() string {
	if p.Filename == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Col)
}

// Resolve turns a Pos into a Position. An invalid Pos resolves to the zero
// Position.
func (p Pos) Resolve() Position {
	if p.File == nil {
		return Position{}
	}
	f := p.File
	f.ensureLines()
	off := min(max(p.Offset, 0), len(f.content))
	// Index of the largest line start <= off.
	i := max(sort.Search(len(f.lines), func(i int) bool { return f.lines[i] > off })-1, 0)
	return Position{
		Filename: f.name,
		Line:     i + 1,
		Col:      off - f.lines[i] + 1,
		Offset:   off,
	}
}
