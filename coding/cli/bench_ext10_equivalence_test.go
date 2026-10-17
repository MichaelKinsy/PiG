// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

// The overhead bench's ten extensions (bench/evals/fixtures/extensions/bench-ext-NN.ts) have Go SDK ports in bench/evals/fixtures/extensions-go: ten conventional
// factories (packed cell, or fused into a Piglet Binary) and ten exact standalones (one isolated process each). A bench arm may only compare realizations of the
// same extensions, so each realization must register exactly what the TypeScript original registers: name, tools with label and description, commands, handlers.
// This runs all three sets through the real `pig install --validate-only` path (source resolution, build, process start, registration over the wire).
func TestBenchExt10GoPortsRegisterWhatTheTypeScriptOriginalsRegister(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fixtures := filepath.Join("..", "..", "bench", "evals", "fixtures")
	var ts, factories, standalones []string
	for i := 1; i <= 10; i++ {
		ts = append(ts, filepath.Join(fixtures, "extensions", fmt.Sprintf("bench-ext-%02d.ts", i)))
		factories = append(factories, filepath.Join(fixtures, "extensions-go", "factory", fmt.Sprintf("bench-ext-%02d", i)))
		standalones = append(standalones, filepath.Join(fixtures, "extensions-go", "standalone", fmt.Sprintf("bench-ext-%02d", i)))
	}
	type registration struct {
		Name           string           `json:"name"`
		Valid          bool             `json:"valid"`
		Registered     bool             `json:"registered"`
		Tools          []string         `json:"tools"`
		Commands       []string         `json:"commands"`
		Handlers       []string         `json:"handlers"`
		ToolDetails    []map[string]any `json:"toolDetails"`
		CommandDetails []map[string]any `json:"commandDetails"`
	}
	type report struct {
		Valid      bool           `json:"valid"`
		Extensions []registration `json:"extensions"`
		Placement  struct {
			Cells []struct {
				Action     string   `json:"action"`
				Language   string   `json:"language"`
				Extensions []string `json:"extensions"`
			} `json:"cells"`
		} `json:"placement"`
	}
	validate := func(sources []string) report {
		t.Helper()
		stdout, stderr, code := captureStdoutStderr(t, func() int {
			return runPackageCommand(append([]string{"install", "--validate-only", "--json"}, sources...))
		})
		if code != 0 {
			t.Fatalf("validate %v: code %d\nstdout=%s\nstderr=%s", sources[0], code, stdout, stderr)
		}
		var r report
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatalf("parse report: %v\n%s", err, stdout)
		}
		if !r.Valid || len(r.Extensions) != 10 {
			t.Fatalf("validate %v: valid=%v extensions=%d, want true/10", sources[0], r.Valid, len(r.Extensions))
		}
		return r
	}
	original := validate(ts)
	for _, set := range []struct {
		name    string
		sources []string
		cells   int
		action  string
	}{{"packed factories", factories, 1, "pack"}, {"isolated standalones", standalones, 10, "isolate"}} {
		t.Run(set.name, func(t *testing.T) {
			got := validate(set.sources)
			for i, want := range original.Extensions {
				if !reflect.DeepEqual(got.Extensions[i], want) {
					t.Errorf("extension %d registers\n  %+v\nthe TypeScript original registers\n  %+v", i+1, got.Extensions[i], want)
				}
			}
			if len(got.Placement.Cells) != set.cells {
				t.Fatalf("placement has %d cells, want %d: %+v", len(got.Placement.Cells), set.cells, got.Placement.Cells)
			}
			for _, cell := range got.Placement.Cells {
				if cell.Action != set.action || cell.Language != "go" {
					t.Errorf("cell %+v, want a Go cell with action %s", cell, set.action)
				}
			}
		})
	}
}
