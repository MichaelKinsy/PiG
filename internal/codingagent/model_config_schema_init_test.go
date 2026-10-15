package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// codingagentInitBytesBudget and codingagentInitAllocsBudget bound this package's initializers, measured through the runtime's init accounting (GODEBUG=inittrace=1) of a program that links only this package, so test files and other packages do not count. At a5df22bd3a the package spent 61,336 bytes in 722 allocations: 34,960 bytes in 290 allocations built the models.json schema, which only a models.json load reads, and 14,832 bytes in 32 allocations built the Mermaid block parser, which only Markdown text reaching the Mermaid transform reads. Both are built on first use now: 11,544 bytes in 400 allocations. Raise a budget only with a profile that shows the new init work is needed before main.
const (
	codingagentInitBytesBudget  = 12_500
	codingagentInitAllocsBudget = 412
)

func TestCodingagentPackageInitBudget(t *testing.T) {
	bytes, allocs := packageInitWork(t, "github.com/MichaelKinsy/PiG/internal/codingagent")
	t.Logf("internal/codingagent init: %d bytes, %d allocs", bytes, allocs)
	if bytes > codingagentInitBytesBudget || allocs > codingagentInitAllocsBudget {
		t.Fatalf("internal/codingagent package init uses %d bytes in %d allocations, budget %d bytes in %d allocations", bytes, allocs, codingagentInitBytesBudget, codingagentInitAllocsBudget)
	}
}

// packageInitWork builds a program that only links importPath, runs it with GODEBUG=inittrace=1 and returns the bytes and allocations importPath's own initializers report. The program's source is an overlay, so the test writes nothing into the module tree.
func packageInitWork(t *testing.T, importPath string) (int, int) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n\nimport _ \""+importPath+"\"\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(root, "internal", "codingagent", "initprobe", "main.go"): source}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "initprobe.exe")
	build := exec.CommandContext(testbudget.Context(t), "go", "build", "-overlay="+overlayPath, "-o", binary, "./internal/codingagent/initprobe")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	run := exec.CommandContext(testbudget.Context(t), binary)
	run.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	var stderr bytes.Buffer
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("init probe: %v\n%s", err, stderr.String())
	}
	match := regexp.MustCompile(`(?m)^init ` + regexp.QuoteMeta(importPath) + ` @[\d.]+ ms, [\d.]+ ms clock, (\d+) bytes, (\d+) allocs$`).FindStringSubmatch(stderr.String())
	if match == nil {
		t.Fatalf("no init line for %s in:\n%s", importPath, stderr.String())
	}
	initBytes, _ := strconv.Atoi(match[1])
	initAllocs, _ := strconv.Atoi(match[2])
	return initBytes, initAllocs
}
