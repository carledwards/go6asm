// Package memmap parses the linker memory-map config (DESIGN §9.1), a
// strict subset of ca65's ld65 syntax: the MEMORY and SEGMENTS blocks.
//
// Leaf package (stdlib + embed only), wasm-clean (DESIGN §4): configs
// arrive as bytes; built-ins are embedded, not read from disk.
package memmap

import (
	"embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed configs/*.cfg
var builtinFS embed.FS

// MemArea is one MEMORY region.
type MemArea struct {
	Name     string
	Start    int // first address
	Size     int // bytes
	ReadOnly bool
	Fill     bool
	FillVal  byte
}

// SegSpec places a named segment into a memory area.
type SegSpec struct {
	Name  string
	Load  string // MEMORY area name
	Type  string // ro | rw | bss | zp
	Start int    // explicit address; -1 = pack sequentially
}

// Config is a parsed memory map.
type Config struct {
	Memory   []MemArea
	Segments []SegSpec
}

// Area returns the named memory area.
func (c *Config) Area(name string) (*MemArea, bool) {
	for i := range c.Memory {
		if c.Memory[i].Name == name {
			return &c.Memory[i], true
		}
	}
	return nil, false
}

// Builtin returns an embedded config. Names: flat-64k, sim-tui.
func Builtin(name string) (*Config, error) {
	b, err := builtinFS.ReadFile("configs/" + name + ".cfg")
	if err != nil {
		return nil, fmt.Errorf("memmap: no built-in config %q", name)
	}
	return Parse(b)
}

// Parse parses a memory-map config.
func Parse(src []byte) (*Config, error) {
	toks, err := lexCfg(src)
	if err != nil {
		return nil, err
	}
	p := &cfgParser{toks: toks}
	cfg := &Config{}
	for !p.eof() {
		block := p.ident()
		p.expect("{")
		switch strings.ToUpper(block) {
		case "MEMORY":
			for !p.at("}") && !p.eof() {
				cfg.Memory = append(cfg.Memory, p.memArea())
			}
		case "SEGMENTS":
			for !p.at("}") && !p.eof() {
				cfg.Segments = append(cfg.Segments, p.segSpec())
			}
		default:
			// SYMBOLS/FEATURES/FILES: skip the block (post-v1, §9.1).
			depth := 1
			for depth > 0 && !p.eof() {
				switch p.next() {
				case "{":
					depth++
				case "}":
					depth--
				}
			}
			if p.err == nil {
				continue
			}
		}
		p.expect("}")
	}
	if p.err != nil {
		return nil, p.err
	}
	return cfg, nil
}

func (p *cfgParser) memArea() MemArea {
	m := MemArea{}
	m.Name = p.ident()
	p.expect(":")
	for {
		k := strings.ToLower(p.ident())
		p.expect("=")
		v := p.next()
		switch k {
		case "start":
			m.Start = num(p, v)
		case "size":
			m.Size = num(p, v)
		case "type":
			m.ReadOnly = strings.EqualFold(v, "ro")
		case "fill":
			m.Fill = strings.EqualFold(v, "yes")
		case "fillval":
			m.FillVal = byte(num(p, v))
		case "file", "define":
			// accepted, ignored in v1
		default:
			p.fail("unknown MEMORY attribute %q", k)
		}
		if p.at(",") {
			p.next()
			continue
		}
		break
	}
	p.expect(";")
	return m
}

func (p *cfgParser) segSpec() SegSpec {
	s := SegSpec{Start: -1}
	s.Name = p.ident()
	p.expect(":")
	for {
		k := strings.ToLower(p.ident())
		p.expect("=")
		v := p.next()
		switch k {
		case "load":
			s.Load = v
		case "type":
			s.Type = strings.ToLower(v)
		case "start":
			s.Start = num(p, v)
		case "run", "define", "optional", "align":
			// accepted, ignored in v1
		default:
			p.fail("unknown SEGMENTS attribute %q", k)
		}
		if p.at(",") {
			p.next()
			continue
		}
		break
	}
	p.expect(";")
	return s
}

// ---- tiny cfg lexer/parser ------------------------------------------------

func lexCfg(src []byte) ([]string, error) {
	var toks []string
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '{' || c == '}' || c == ':' || c == ';' || c == ',' || c == '=':
			toks = append(toks, string(c))
			i++
		default:
			j := i
			for j < len(src) && !isCfgSep(src[j]) {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("memmap: unexpected byte %q", c)
			}
			toks = append(toks, string(src[i:j]))
			i = j
		}
	}
	return toks, nil
}

func isCfgSep(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '{', '}', ':', ';', ',', '=', '#':
		return true
	}
	return false
}

type cfgParser struct {
	toks []string
	i    int
	err  error
}

func (p *cfgParser) eof() bool { return p.i >= len(p.toks) || p.err != nil }

func (p *cfgParser) next() string {
	if p.i >= len(p.toks) {
		p.fail("unexpected end of config")
		return ""
	}
	t := p.toks[p.i]
	p.i++
	return t
}

func (p *cfgParser) at(t string) bool {
	return p.i < len(p.toks) && p.toks[p.i] == t
}

func (p *cfgParser) ident() string {
	t := p.next()
	if t == "" || strings.ContainsAny(t, "{}:;,=") {
		p.fail("expected a name, got %q", t)
	}
	return t
}

func (p *cfgParser) expect(t string) {
	if got := p.next(); got != t && p.err == nil {
		p.fail("expected %q, got %q", t, got)
	}
}

func (p *cfgParser) fail(format string, a ...any) {
	if p.err == nil {
		p.err = fmt.Errorf("memmap: "+format, a...)
	}
}

func num(p *cfgParser, s string) int {
	v := s
	switch {
	case strings.HasPrefix(v, "$"):
		v = "0x" + v[1:]
	case strings.HasPrefix(v, "%"):
		n, err := strconv.ParseInt(v[1:], 2, 64)
		if err != nil {
			p.fail("bad binary number %q", s)
		}
		return int(n)
	}
	n, err := strconv.ParseInt(v, 0, 64)
	if err != nil {
		p.fail("bad number %q", s)
		return 0
	}
	return int(n)
}
