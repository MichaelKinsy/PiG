package ai

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestTestFauxLimitsMatchParityOracle derives the expected limits from the
// test-faux model the upstream side of the parity harness registers, the
// behavior pig's test-faux stands in for (GUARD-03). Upstream's own faux.ts
// provider (maxTokens ?? 16384) is a different provider.
func TestTestFauxLimitsMatchParityOracle(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "parity", "testdata", "test-faux-provider.ts"))
	if err != nil {
		t.Fatal(err)
	}
	field := func(name string) int {
		match := regexp.MustCompile(name + `:\s*(\d+),`).FindSubmatch(source)
		if match == nil {
			t.Fatalf("%s not found in the parity oracle", name)
		}
		value, _ := strconv.Atoi(string(match[1]))
		return value
	}
	if got, want := TestFauxContextWindow, field("contextWindow"); got != want {
		t.Errorf("TestFauxContextWindow = %d, oracle %d", got, want)
	}
	if got, want := TestFauxMaxTokens, field("maxTokens"); got != want {
		t.Errorf("TestFauxMaxTokens = %d, oracle %d", got, want)
	}
}
