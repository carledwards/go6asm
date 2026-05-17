package debug

import (
	"encoding/json"
	"strings"
	"testing"
)

var sample = []Symbol{
	{Name: "start", Addr: 0x0200, Kind: "label"},
	{Name: "MAGIC", Addr: 0x00FF, Kind: "const"},
	{Name: "reset", Addr: 0xFFFA, Kind: "label"},
}

func TestVICELabelsSortedByAddr(t *testing.T) {
	got := VICELabels(sample)
	want := "al C:$00FF .MAGIC\n" +
		"al C:$0200 .start\n" +
		"al C:$FFFA .reset\n"
	if got != want {
		t.Errorf("VICELabels:\n got:\n%s want:\n%s", got, want)
	}
}

func TestVICELabelsEmpty(t *testing.T) {
	if got := VICELabels(nil); got != "" {
		t.Errorf("empty = %q, want \"\"", got)
	}
}

func TestSymbolJSONShape(t *testing.T) {
	b, err := SymbolJSON(sample)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Format  string `json:"format"`
		Symbols []struct {
			Name string `json:"name"`
			Addr int    `json:"addr"`
			Kind string `json:"kind"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, b)
	}
	if f.Format != "go6asm-sym/1" {
		t.Errorf("format = %q", f.Format)
	}
	if len(f.Symbols) != 3 || f.Symbols[0].Name != "MAGIC" ||
		f.Symbols[0].Addr != 0x00FF || f.Symbols[2].Name != "reset" {
		t.Errorf("symbols = %+v", f.Symbols)
	}
}

func TestSymbolJSONEmptyIsArray(t *testing.T) {
	b, err := SymbolJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"symbols": []`) {
		t.Errorf("empty symbols should marshal as [], got:\n%s", b)
	}
}
