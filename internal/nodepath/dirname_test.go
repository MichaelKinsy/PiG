package nodepath

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"testing"
)

// TestPosixDirnameMatchesNode compares PosixDirname with Node's path.posix.dirname over the posix inputs of testdata/join_cases.json and the root,
// doubled-slash and trailing-slash forms.
func TestPosixDirnameMatchesNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	data, err := os.ReadFile("testdata/join_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []joinCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	inputs := []string{"", "/", "//", "///", "/a", "//a", "///a", "a", "a/", "a//", "/a/", "/a/b", "/a/b/", "//a/b", "a/b/c", "./a", "../a", "~/a", ".", "..", "/h", "~"}
	for _, c := range file.Cases {
		if c.Flavor == "posix" {
			inputs = append(inputs, c.Args...)
		}
	}
	slices.Sort(inputs)
	inputs = slices.Compact(inputs)
	encoded, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `const path = require("path"); let s = ""; process.stdin.on("data", (d) => (s += d)).on("end", () => process.stdout.write(JSON.stringify(JSON.parse(s).map((p) => path.posix.dirname(p)))));`)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = stdin.Write(encoded)
		_ = stdin.Close()
	}()
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(inputs) {
		t.Fatalf("node returned %d results for %d inputs", len(want), len(inputs))
	}
	for i, input := range inputs {
		if got := PosixDirname(input); got != want[i] {
			t.Errorf("PosixDirname(%q) = %q, Node gives %q", input, got, want[i])
		}
	}
}
