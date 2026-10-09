package evals

// pi: packages/evals/src/plan.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type planProbe struct {
	Op    string               `json:"op"`
	Value any                  `json:"value"`
	Cases []DiscoveredEvalCase `json:"cases"`
	Model string               `json:"model"`
	Runs  int                  `json:"runs"`
}

type planOutcome struct {
	Ok  any    `json:"ok,omitempty"`
	Err string `json:"err,omitempty"`
}

// planProbes cover names that split into one, two or three parts around " > ", parts that are empty or only JavaScript whitespace (including
// U+00A0, U+FEFF and U+2028 but not U+200B, U+180E or U+0085), repeated identities, items that are not records or lack a string name or file, and model
// identities with and without a slash at either end, each over several run counts.
func planProbes() []planProbe {
	random := rand.New(rand.NewSource(20260929))
	pieces := []string{"Set", "case", "", " ", "\u00a0", "\ufeff", "\u2028", "\u200b", "\u180e", "\t", "\n", "a > b", ">", " >", "> ", "é", " > ", "  >  ", "x"}
	name := func() string {
		var parts []string
		for range 1 + random.Intn(3) {
			var b strings.Builder
			for range random.Intn(3) {
				b.WriteString(pieces[random.Intn(len(pieces))])
			}
			parts = append(parts, b.String())
		}
		sep := " > "
		if random.Intn(8) == 0 {
			sep = ">"
		}
		return strings.Join(parts, sep)
	}
	odd := []any{nil, 1.0, "text", true, []any{}, []any{"name"}, map[string]any{}, map[string]any{"name": "A > B"}, map[string]any{"file": "f"}, map[string]any{"name": 1.0, "file": "f"}, map[string]any{"name": "A > B", "file": nil}}
	item := func() any {
		if random.Intn(6) == 0 {
			return odd[random.Intn(len(odd))]
		}
		return map[string]any{"name": name(), "file": []string{"a.eval.ts", "", "dir/b.eval.ts"}[random.Intn(3)]}
	}
	var probes []planProbe
	for range 600 {
		if random.Intn(10) == 0 {
			probes = append(probes, planProbe{Op: "parse", Value: odd[random.Intn(len(odd))]})
			continue
		}
		items := make([]any, random.Intn(4))
		for i := range items {
			items[i] = item()
		}
		if len(items) >= 2 && random.Intn(4) == 0 {
			items[1] = items[0]
			if record, ok := items[0].(map[string]any); ok && random.Intn(2) == 0 {
				items[1] = map[string]any{"name": record["name"], "file": "other.eval.ts"} // the same identity in another file is still a duplicate
			}
		}
		probes = append(probes, planProbe{Op: "parse", Value: items})
	}
	// Each part alone with only a character the two trims disagree on: JavaScript trims U+FEFF and keeps U+0085, Go's TrimSpace does the reverse.
	for _, name := range []string{"Set > \ufeff", "\ufeff > case", "Set > \u0085", "\u0085 > case", "Set > \ufeff\u00a0", "\u0085\u2028 > case"} {
		probes = append(probes, planProbe{Op: "parse", Value: []any{map[string]any{"name": name, "file": "a.eval.ts"}}})
	}
	models := []string{"anthropic/claude", "a/b", "openrouter/x/y", "/m", "m/", "m", "", "/", "//", "p/ "}
	for range 400 {
		cases := make([]DiscoveredEvalCase, random.Intn(4))
		for i := range cases {
			cases[i] = DiscoveredEvalCase{File: "f", FullName: "S > c", EvalSet: "S", CaseID: string(rune('a' + i))}
		}
		probes = append(probes, planProbe{Op: "plan", Cases: cases, Model: models[random.Intn(len(models))], Runs: random.Intn(8) - 2})
	}
	return probes
}

func runPlanProbe(probe planProbe) planOutcome {
	var result any
	var err error
	if probe.Op == "parse" {
		result, err = ParseDiscoveredCases(probe.Value)
	} else {
		result, err = CreateTaskPlan(probe.Cases, probe.Model, probe.Runs)
	}
	if err != nil {
		return planOutcome{Err: "TypeError: " + err.Error()}
	}
	raw, _ := json.Marshal(result)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	return planOutcome{Ok: decoded}
}

// plan.ts runs in Node from the pinned source against the same probes: every parsed case list, every task order (odd repetitions without_docs
// first, even with_docs first), and every validation error message must match Pi's.
func TestPlanMatchesPiOnSeededProbes(t *testing.T) {
	probes := planProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "evals", "src", "plan.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/plan.mjs", source)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []planOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	accepted, differing := 0, 0
	for i, probe := range probes {
		got := runPlanProbe(probe)
		if expected[i].Err == "" {
			accepted++
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if differing++; differing <= 5 {
				raw, _ := json.Marshal(probe)
				t.Errorf("probe %d %s differs from Pi:\n  Pig %+v\n  Pi  %+v", i, raw, got, expected[i])
			}
		}
	}
	if differing > 5 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if accepted < len(probes)/5 || accepted > len(probes)*4/5 {
		t.Errorf("probes are one-sided: Pi accepted %d of %d", accepted, len(probes))
	}
}
