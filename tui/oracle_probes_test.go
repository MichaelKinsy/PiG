package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// oracleProcesses bounds the node processes one oracle comparison runs at once. oracleProbesPerProcess is the fewest probes worth a process of their own: a node process loads the pinned Pi before it renders anything.
var oracleProcesses = max(1, min(8, runtime.NumCPU()))

const oracleProbesPerProcess = 32

// runPiOracle runs the script of testdata over the probes in one node process and returns the one result per probe it prints.
func runPiOracle[R any](ctx context.Context, script string, probes any) ([]R, error) {
	input, err := json.Marshal(probes)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "node", "testdata/"+script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Pi oracle %s: %w\n%s", script, err, &stderr)
	}
	var results []R
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("Pi oracle %s: %w", script, err)
	}
	return results, nil
}

// piOracleProbes is runPiOracle for the oracle scripts that answer each probe on its own (no probe reads what an earlier one did, and the script's globals are set again by each probe): it splits the probes into runs of consecutive probes, one node process each and at most oracleProcesses at once, and returns the results in probe order.
// The split changes nothing the oracle computes. It divides the wall time of the large sweeps, which Pi's JavaScript, not the Go comparison, dominates, and it touches no Go state, so it is safe in any test.
func piOracleProbes[P, R any](t *testing.T, script string, probes []P) []R {
	t.Helper()
	parts := max(1, min(oracleProcesses, len(probes)/oracleProbesPerProcess))
	size := (len(probes) + parts - 1) / parts
	results := make([][]R, parts)
	errs := make([]error, parts)
	var wg sync.WaitGroup
	for i := range parts {
		chunk := probes[min(len(probes), i*size):min(len(probes), (i+1)*size)]
		wg.Go(func() {
			results[i], errs[i] = runPiOracle[R](t.Context(), script, chunk)
			if errs[i] == nil && len(results[i]) != len(chunk) {
				errs[i] = fmt.Errorf("Pi oracle %s returned %d results for %d probes", script, len(results[i]), len(chunk))
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	return slices.Concat(results...)
}
