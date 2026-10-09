package jsstring

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
	"unicode/utf8"
)

// ToLower against Node's String.prototype.toLowerCase over every code point, and over strings that put a capital sigma in word-final and
// word-internal contexts with case-ignorable characters around it.
func TestToLowerMatchesNode(t *testing.T) {
	var inputs []string
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		inputs = append(inputs, string(r))
	}
	atoms := []string{"Σ", "σ", "ς", "A", "a", "1", " ", "'", ".", "\u0301", "\u00ad", "·", "İ", "ǅ", "Ⅰ", "ʰ", "_", "-", "Ω", "ß"}
	var build func(prefix string, depth int)
	build = func(prefix string, depth int) {
		if depth == 0 {
			if utf8.ValidString(prefix) {
				inputs = append(inputs, prefix)
			}
			return
		}
		for _, atom := range atoms {
			build(prefix+atom, depth-1)
		}
	}
	for depth := 1; depth <= 4; depth++ {
		build("", depth)
	}
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `let s="";process.stdin.setEncoding("utf8");process.stdin.on("data",c=>s+=c).on("end",()=>process.stdout.write(JSON.stringify(JSON.parse(s).map(x=>x.toLowerCase()))))`)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, text := range inputs {
		if got := ToLower(text); got != want[i] {
			if failures++; failures <= 10 {
				t.Errorf("ToLower(%q) = %q, Node %q", text, got, want[i])
			}
		}
	}
	if failures > 10 {
		t.Errorf("%d of %d inputs differ", failures, len(inputs))
	}
}
