package expand

import (
	"testing"

	"github.com/carledwards/go6asm/scanner"
	"github.com/carledwards/go6asm/token"
)

// Brick 0: the expander is a faithful identity over the token stream and
// propagates upstream diagnostics.
func TestIdentityPassthrough(t *testing.T) {
	src := "loop:\n\tLDA msg,X\n\tBNE loop ; ok\n\t`bad\n"
	fs := token.NewFileSet()

	direct, dd := scanner.ScanAll(fs.AddFile("a.s", []byte(src)))

	ex := New(scanner.New(fs.AddFile("b.s", []byte(src))), Options{})
	var got []token.Token
	for {
		tk := ex.Next()
		got = append(got, tk)
		if tk.Kind == token.EOF {
			break
		}
	}

	if len(got) != len(direct) {
		t.Fatalf("token count %d, want %d", len(got), len(direct))
	}
	for i := range got {
		if got[i].Kind != direct[i].Kind || got[i].Lit != direct[i].Lit {
			t.Errorf("tok %d = (%s %q), want (%s %q)",
				i, got[i].Kind, got[i].Lit, direct[i].Kind, direct[i].Lit)
		}
	}
	if len(ex.Diagnostics()) != len(dd) || len(dd) == 0 {
		t.Errorf("diagnostics not propagated: got %d, want %d", len(ex.Diagnostics()), len(dd))
	}
}
