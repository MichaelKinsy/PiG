package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A sample line carries the process CPU time of the open and of each measured turn, as durable/interop/bench.mjs's
// does, so bench/k8s's durable-native profile can gate on CPU time and report cpuWarm and cpuCold for both targets.
func TestSampleReportsTheProcessCPUOfTheOpenAndEachTurn(t *testing.T) {
	dir := t.TempDir()
	if err := seed([]string{"-dir", dir, "-sizes", "50"}); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	sampleErr := sample([]string{"-fixture", filepath.Join(dir, "go-50.sqlite"), "-turns", "50", "-sample", "2"})
	os.Stdout = stdout
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil || sampleErr != nil {
		t.Fatalf("sample: %v %v", sampleErr, err)
	}
	var line struct {
		Turn    []float64 `json:"turn"`
		OpenCPU *float64  `json:"openCpu"`
		TurnCPU []float64 `json:"turnCpu"`
	}
	if err := json.Unmarshal(out, &line); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if line.OpenCPU == nil || *line.OpenCPU <= 0 || len(line.Turn) != measuredTurns || len(line.TurnCPU) != len(line.Turn) {
		t.Fatalf("sample line: %s", out)
	}
	for i, ms := range line.TurnCPU {
		if ms <= 0 {
			t.Errorf("turn %d: CPU %v ms: %s", i, ms, out)
		}
	}
}
