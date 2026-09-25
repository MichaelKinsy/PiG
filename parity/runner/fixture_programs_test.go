//go:build parity

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestParityFixtureProgramsCompile type-checks every Go program under
// parity/testdata with go vet, which compiles without linking. `go build ./...`
// skips testdata directories, so the only other thing that compiles these
// programs is the scenario that `go run`s one. Without this guard an ai API
// refactor rots them silently, and the scenario then fails on a compile error
// instead of on behavior.
func TestParityFixtureProgramsCompile(t *testing.T) {
	testdata := filepath.Join("..", "testdata")
	entries, err := os.ReadDir(testdata)
	if err != nil {
		t.Fatal(err)
	}
	var programs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sources, err := filepath.Glob(filepath.Join(testdata, entry.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(sources) > 0 {
			programs = append(programs, "./parity/testdata/"+entry.Name())
		}
	}
	if len(programs) == 0 {
		t.Fatal("no Go programs found under parity/testdata; the guard no longer sees the fixtures")
	}
	args := append([]string{"vet"}, programs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
}
