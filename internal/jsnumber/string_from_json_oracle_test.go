package jsnumber

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// pi: String(JSON.parse(raw)) of the Node that runs the pinned Pi, over JSON values of every kind, nested arrays included.
func TestStringFromJSONMatchesNode(t *testing.T) {
	raws := []string{`null`, `true`, `false`, `0`, `-0`, `5`, `1.5`, `1e21`, `1e-7`, `123456789012345678901234567890`, `""`, `"x"`, `"a\u00e9"`, `{}`, `{"a":1}`, `[]`, `[1]`, `[1,"a",null,true]`,
		`[[1,2],[3,[4]]]`, `[{}]`, `[1e21,-0]`, ` 7 `, `[ ]`}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `let s="";process.stdin.setEncoding("utf8");process.stdin.on("data",c=>s+=c).on("end",()=>process.stdout.write(JSON.stringify(JSON.parse(s).map(r=>String(JSON.parse(r))))))`)
	input, err := json.Marshal(raws)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("node is required: ", err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, raw := range raws {
		if got := StringFromJSON([]byte(raw)); got != want[i] {
			t.Errorf("StringFromJSON(%s) = %q, Node %q", raw, got, want[i])
		}
	}
	if got := StringFromJSON(nil); got != "undefined" {
		t.Errorf("StringFromJSON(nil) = %q, want undefined", got)
	}
}
