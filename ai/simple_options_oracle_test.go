package ai

// pi: packages/ai/src/api/simple-options.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type simpleOptionsProbe struct {
	Op       string         `json:"op"`
	Level    string         `json:"level,omitempty"`
	Custom   map[string]int `json:"custom"` // nil is null, which the driver reads as undefined; an empty map stays {}
	Base     *int           `json:"base,omitempty"`
	ModelMax int            `json:"modelMax,omitempty"`
	Ceiling  int            `json:"ceiling,omitempty"`
	Budget   int            `json:"budget,omitempty"`
}

func customBudgetsOf(custom map[string]int) *ThinkingBudgets {
	if custom == nil {
		return nil
	}
	get := func(name string) *int {
		if value, ok := custom[name]; ok {
			return &value
		}
		return nil
	}
	budgets := NewThinkingBudgets(get("minimal"), get("low"), get("medium"), get("high"))
	if budgets == nil {
		return &ThinkingBudgets{}
	}
	return budgets
}

// Pi's simple-options.ts runs in Node on the same budgets: configured budgets (zero and negative included, and partial sets) win over the defaults, xhigh and max read the high budget, and the answer-room clamp and the
// max-token adjustment follow.
func TestSimpleOptionsMatchPi(t *testing.T) {
	random := rand.New(rand.NewSource(20260957))
	levels := []string{"minimal", "low", "medium", "high", "xhigh", "max"}
	values := []int{0, 0, -1, -2048, 1, 500, 1024, 2048, 8192, 16384, 40000}
	var probes []simpleOptionsProbe
	for range 6000 {
		var custom map[string]int
		if random.Intn(4) != 0 {
			custom = map[string]int{}
			for _, name := range levels[:4] {
				if random.Intn(2) == 0 {
					custom[name] = values[random.Intn(len(values))]
				}
			}
		}
		level := levels[random.Intn(len(levels))]
		var base *int
		if random.Intn(3) != 0 {
			base = new(values[random.Intn(len(values))] + random.Intn(3000))
		}
		modelMax := []int{1, 1024, 2048, 4096, 8192, 16000, 64000, 128000}[random.Intn(8)]
		probes = append(probes,
			simpleOptionsProbe{Op: "budget", Level: level, Custom: custom},
			simpleOptionsProbe{Op: "adjust", Level: level, Custom: custom, Base: base, ModelMax: modelMax},
			simpleOptionsProbe{Op: "room", Budget: values[random.Intn(len(values))], Ceiling: modelMax - random.Intn(3)*1000},
			simpleOptionsProbe{Op: "clamp", Level: level})
	}
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api", "simple-options.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/simple_options.mjs", src)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	differing := 0
	for i, probe := range probes {
		var got, want any
		switch probe.Op {
		case "budget":
			got = ThinkingBudgetForLevel(probe.Level, customBudgetsOf(probe.Custom))
			var n int
			if err := json.Unmarshal(expected[i], &n); err != nil {
				t.Fatalf("budget %v: %v (%s)", probe, err, expected[i])
			}
			want = n
		case "adjust":
			maxTokens, budget := AdjustMaxTokensForThinking(probe.Base, probe.ModelMax, probe.Level, customBudgetsOf(probe.Custom))
			got = map[string]int{"maxTokens": maxTokens, "thinkingBudget": budget}
			var m map[string]int
			if err := json.Unmarshal(expected[i], &m); err != nil {
				t.Fatalf("adjust %v: %v (%s)", probe, err, expected[i])
			}
			want = m
		case "room":
			got = ClampThinkingBudgetToAnswerRoom(probe.Budget, probe.Ceiling)
			var n int
			if err := json.Unmarshal(expected[i], &n); err != nil {
				t.Fatal(err)
			}
			want = n
		default:
			got = string(ClampReasoning(ModelThinkingLevel(probe.Level)))
			var text string
			if err := json.Unmarshal(expected[i], &text); err != nil {
				t.Fatal(err)
			}
			want = text
		}
		if !reflect.DeepEqual(got, want) {
			if differing++; differing <= 10 {
				t.Errorf("%s level=%s custom=%v base=%v modelMax=%d: Pig %v, Pi %v", probe.Op, probe.Level, probe.Custom, probe.Base, probe.ModelMax, got, want)
			}
		}
	}
	if differing > 10 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
}
