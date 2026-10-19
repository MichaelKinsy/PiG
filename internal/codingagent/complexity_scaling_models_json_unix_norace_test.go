//go:build unix && !race

package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// processCPUOf is the CPU time the process uses while op runs, from the least of runs fresh operations. Other processes on a loaded host
// do not add to CPU time as they do to wall time, which a long run cannot dodge as a short one can.
func processCPUOf(t *testing.T, prepare func(n int) func(), n, runs int) time.Duration {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	best := time.Duration(1<<63 - 1)
	for range runs {
		op := prepare(n)
		runtime.GC()
		runtime.GC()
		start := processCPU(t)
		op()
		best = min(best, processCPU(t)-start)
	}
	return best
}

// A models.json provider composes its definitions over the provider's models one by one; looking each definition up in the list so far
// must not scan the list. Allocations are linear either way, so the gate compares the CPU time of composing n and 16n models.
func TestScalingModelsJSONProviderComposition(t *testing.T) {
	compose := func(n int) func() {
		var sb strings.Builder
		sb.WriteString(`{"providers":{"big":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"k","models":[`)
		for i := range n {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"id":"m-%d","name":"Model %d","contextWindow":128000,"maxTokens":8192}`, i, i)
		}
		sb.WriteString(`]}}}`)
		path := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		registry := NewModelRegistryWithModelsPath(path)
		if got := len(registry.GetProviderModelData("big")); got != n {
			t.Fatalf("provider big composes %d models, want %d", got, n)
		}
		return func() { registry.GetProviderModelData("big") }
	}
	// 16 times the models: a linear path takes 16-40 times the CPU (larger lists lose cache locality), a quadratic one over 200 times; the
	// bound sits between them with a wide margin each way, so one measurement decides.
	const small, large, bound = 1000, 16000, 100
	smallCPU := processCPUOf(t, compose, small, 5)
	largeCPU := processCPUOf(t, compose, large, 5)
	ratio := float64(largeCPU) / float64(smallCPU)
	t.Logf("composing %d models takes %s of CPU, %d models %s: ratio %.1f", small, smallCPU, large, largeCPU, ratio)
	testenv.TimingGate(t, ratio > bound, "composing a provider's models.json definitions took %.1fx the CPU time for %dx the models: the cost per definition grows with the models before it", ratio, large/small)
}
