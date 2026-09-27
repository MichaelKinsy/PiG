package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOmittedOutputWarnsWithoutChangingJSON(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "interfacerecommend")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build generator: %v\n%s", err, output)
	}
	input := filepath.Join(root, "input.json")
	if err := os.WriteFile(input, []byte(`{"upstreamVersion":"fixture","interfaces":[{"id":"fixture:Export","name":"Export"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var previous []byte
	for _, explicit := range []bool{false, true} {
		args := []string{"-inventory", input, "-go-inventory", input}
		if explicit {
			args = append(args, "-out", "-")
		}
		cmd := exec.CommandContext(t.Context(), binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		if err != nil {
			t.Fatalf("recommend: %v\n%s", err, stderr.String())
		}
		if !json.Valid(stdout) || !bytes.Contains(stdout, []byte("fixture:Export")) {
			t.Fatalf("recommendations are not intact JSON: %s", stdout)
		}
		if explicit {
			if stderr.Len() != 0 || !bytes.Equal(previous, stdout) {
				t.Fatalf("explicit stdout changed output or warned: %s", stderr.String())
			}
		} else {
			for _, want := range []string{"writing to stdout", "run: make generate", "make interface-recommendations-generate", "-out -"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("missing %q in notice: %s", want, stderr.String())
				}
			}
			previous = stdout
		}
	}
}
