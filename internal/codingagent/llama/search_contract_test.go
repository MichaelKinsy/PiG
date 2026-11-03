package llama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func contractCatalog(n int) []searchOracleModel {
	catalog := make([]searchOracleModel, n)
	for i := range catalog {
		catalog[i] = searchOracleModel{ID: fmt.Sprintf("owner/gemma-%02d-GGUF", i), Downloads: float64(1000 * (i + 1))}
	}
	return catalog
}

// Directed scenarios for HuggingFaceSearch.handleInput (extensions/llama/ui.ts) against pinned Pi, on the virtual clock the oracle uses: a key that leaves the trimmed
// query as it is (a cursor key, a trailing space) keeps the pending 500 ms debounce, so the search starts when the first key's timer fires; Page Up and Page Down move the
// selection by ten results and clamp, over a list longer than a page, and on an empty list do nothing; Up and Down wrap at both ends of a short list.
func TestLlamaSearchDirectedKeysMatchPi(t *testing.T) {
	const (
		left, right, pageUp, pageDown, up, down, enter = "\x1b[D", "\x1b[C", "\x1b[5~", "\x1b[6~", "\x1b[A", "\x1b[B", "\r"
	)
	keys := func(values ...string) []searchOracleOp {
		ops := make([]searchOracleOp, len(values))
		for i, value := range values {
			ops[i] = searchOracleOp{Key: value}
		}
		return ops
	}
	wait := func(ms int) searchOracleOp { return searchOracleOp{Wait: ms} }
	join := func(parts ...[]searchOracleOp) []searchOracleOp {
		var all []searchOracleOp
		for _, part := range parts {
			all = append(all, part...)
		}
		return all
	}
	scenario := func(delay, limit, catalog int, ops []searchOracleOp) searchOracleScenario {
		return searchOracleScenario{Widths: []int{14, 40, 80}, Delay: delay, Limit: limit, Catalog: contractCatalog(catalog), Ops: ops}
	}
	scenarios := []searchOracleScenario{
		// The cursor key after "ge" changes nothing: the debounce of the first key still fires at 500 ms.
		scenario(0, 8, 20, join(keys("g", "e"), []searchOracleOp{wait(300)}, keys(left), []searchOracleOp{wait(250), wait(2000)})),
		scenario(0, 8, 20, join(keys("g", "e"), []searchOracleOp{wait(300)}, keys(right, left), []searchOracleOp{wait(199), wait(1), wait(2000)})),
		// A trailing space leaves the trimmed query as it is.
		scenario(0, 8, 20, join(keys("g", "e"), []searchOracleOp{wait(300)}, keys(" "), []searchOracleOp{wait(250), wait(2000)})),
		// Page keys over thirty results: down two pages, up one, clamped at both ends, then Enter takes the selection.
		scenario(0, 60, 30, join(keys("g", "e", "m"), []searchOracleOp{wait(2000)}, keys(pageDown, pageDown, pageDown, pageDown, pageUp, pageUp, pageUp, pageUp, pageUp, pageDown, enter))),
		scenario(0, 60, 30, join(keys("g", "e", "m"), []searchOracleOp{wait(2000)}, keys(down, down, pageDown, up, pageUp, pageUp, up, up, down))),
		// Page keys on an empty list (a query under two characters) and on a one-result list.
		scenario(0, 60, 30, keys(pageDown, pageUp, up, down, "g", pageDown, pageUp)),
		scenario(0, 1, 30, join(keys("g", "e", "m"), []searchOracleOp{wait(2000)}, keys(pageDown, pageUp, up, down, down))),
		// Up and Down wrap over a short list.
		scenario(0, 3, 30, join(keys("g", "e", "m"), []searchOracleOp{wait(2000)}, keys(up, up, down, down, down, down))),
	}
	useOracleTheme(t, "dark")
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/llama_search.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []searchOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, s := range scenarios {
		got := runSearchOracleWithPig(t, s)
		if !reflect.DeepEqual(got.Frames, expected[i].Frames) || !reflect.DeepEqual(got.Settled, expected[i].Settled) || !reflect.DeepEqual(got.Calls, expected[i].Calls) {
			ops, _ := json.Marshal(s.Ops)
			t.Errorf("scenario %d differs from Pi: calls Pig %q Pi %q, settled Pig %v Pi %v\n  ops %s", i, got.Calls, expected[i].Calls, got.Settled, expected[i].Settled, ops)
		}
	}
}
