package jsstring

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

// ToUpper against Node's String.prototype.toUpperCase over every code point, and over strings that join special-casing characters.
func TestToUpperMatchesNode(t *testing.T) {
	var inputs []string
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		inputs = append(inputs, string(r))
	}
	inputs = append(inputs, "straße", "ŉ", "ǰx", "ﬃ", "ᾀa", "ΐ", "İstanbul", "mixed ß case ΣΑΣ")
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `let s="";process.stdin.setEncoding("utf8");process.stdin.on("data",c=>s+=c).on("end",()=>process.stdout.write(JSON.stringify(JSON.parse(s).map(x=>x.toUpperCase()))))`)
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
		if got := ToUpper(text); got != want[i] {
			if failures++; failures <= 10 {
				t.Errorf("ToUpper(%q) = %q, Node %q", text, got, want[i])
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d inputs differ from Node", failures, len(inputs))
	}
}
