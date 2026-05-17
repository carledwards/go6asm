package token

// Kind is a lexical token class for the v1 ca65 subset (DESIGN §6.1, §6.4).
type Kind int

const (
	EOF Kind = iota
	Illegal
	Newline // statement separator; ca65 is one statement per line

	Ident  // case-sensitive identifier
	Int    // numeric literal; Lit holds raw source ($1A, %1010, 0x1A, 0b1, 42)
	String // double-quoted; Lit holds the decoded value (escapes resolved)
	Char   // 'A'; Lit holds the single decoded byte as a string

	DotIdent // .byte, .mod, .cpu, … — parser disambiguates directive vs word-op
	At       // @  (cheap-local label sigil)

	Colon      // :
	ColonColon // ::  (global-scope reference)
	UnnamedRef // :+ :++ :- :-- ; Lit is the sign run, e.g. "++" or "-"

	Comma  // ,
	Hash   // #
	LParen // (
	RParen // )

	Eq    // =
	NotEq // <>
	Lt    // <   (also unary low-byte; parser decides by position)
	Gt    // >   (also unary high-byte)
	Le    // <=
	Ge    // >=
	Shl   // <<
	Shr   // >>

	Plus  // +
	Minus // -
	Star  // *   (also current-PC pseudovar)
	Slash // /

	Amp   // &
	Pipe  // |
	Caret // ^   (also unary bank-byte, 0 for 6502)
	Tilde // ~
	Bang  // !

	AndAnd // &&
	OrOr   // ||

	// Word-only operators: ca65 has no symbolic form for these, so the
	// scanner never emits them — the parser synthesizes them when it sees
	// the corresponding DotIdent (.mod, .xor) in an infix position.
	Mod     // .mod   (modulo; ca65's % is a binary-literal prefix, not mod)
	BoolXor // .xor   (boolean xor)
)

var kindNames = map[Kind]string{
	EOF: "EOF", Illegal: "Illegal", Newline: "Newline",
	Ident: "Ident", Int: "Int", String: "String", Char: "Char",
	DotIdent: "DotIdent", At: "@",
	Colon: ":", ColonColon: "::", UnnamedRef: "UnnamedRef",
	Comma: ",", Hash: "#", LParen: "(", RParen: ")",
	Eq: "=", NotEq: "<>", Lt: "<", Gt: ">", Le: "<=", Ge: ">=",
	Shl: "<<", Shr: ">>",
	Plus: "+", Minus: "-", Star: "*", Slash: "/",
	Amp: "&", Pipe: "|", Caret: "^", Tilde: "~", Bang: "!",
	AndAnd: "&&", OrOr: "||",
	Mod: ".mod", BoolXor: ".xor",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "Kind(?)"
}

// Token is one lexeme. Pos is the start of the token.
type Token struct {
	Kind Kind
	Lit  string
	Pos  Pos
}
