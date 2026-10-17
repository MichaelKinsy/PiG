package codemode

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The Worker host takes a QuickJS call by index into its own list, so the two lists are one contract.
func TestQJSExportsMatchTheWorkerHost(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(testenv.ModuleRoot(t), "durable", "core", "host", "cf", "src", "codemode", "quickjs.ts"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)QJS_EXPORTS = \[(.*?)\] as const`).FindSubmatch(source)
	if block == nil {
		t.Fatal("quickjs.ts has no QJS_EXPORTS list")
	}
	var host []string
	for _, m := range regexp.MustCompile(`"([a-z_0-9]+)"`).FindAllSubmatch(block[1], -1) {
		host = append(host, string(m[1]))
	}
	if !slices.Equal(host, qjsExports[:]) {
		t.Fatalf("the Worker host lists %v, the Go module %v", host, qjsExports)
	}
}
