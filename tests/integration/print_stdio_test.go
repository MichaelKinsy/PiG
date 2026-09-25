//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrintOutputIgnoresSuccessfulStderr(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	fixture := `package main
import("fmt";"os")
func main(){fmt.Fprintln(os.Stdout,"answer");fmt.Fprintln(os.Stderr,"fixture diagnostic")}
`
	if err := os.WriteFile(source, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "stdio-fixture.exe")
	if output, err := exec.Command("go", "build", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	sys := deterministicPrintSystem{name: "stdio fixture", bin: binary, cwd: root}
	if got := runDeterministicPrint(t, sys); got != "answer" {
		t.Fatalf("stdout = %q, want answer", got)
	}
}
