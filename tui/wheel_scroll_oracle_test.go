package tui

// pi: packages/tui/src/wheel-scroll.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type wheelOp struct {
	Set *any `json:"set,omitempty"`
	Dir int  `json:"dir,omitempty"`
	// Now is never omitted: a missing now would reach Pi as undefined, a NaN gap, where Go reads 0.
	Now float64 `json:"now"`
}

type wheelScript struct {
	Lines      any       `json:"lines"`
	Accelerate bool      `json:"accelerate"`
	Ops        []wheelOp `json:"ops"`
}

func wheelLines(v any) WheelScrollLines {
	switch x := v.(type) {
	case string:
		switch x {
		case "auto":
			return WheelScrollLines{Auto: true}
		case "NaN":
			return WheelScrollLines{Lines: math.NaN()}
		case "Infinity":
			return WheelScrollLines{Lines: math.Inf(1)}
		case "-Infinity":
			return WheelScrollLines{Lines: math.Inf(-1)}
		}
	case float64:
		return WheelScrollLines{Lines: x}
	}
	panic(fmt.Sprintf("bad lines %#v", v))
}

// wheelScripts feed gesture sequences with gaps around every threshold (under 5 ms, 5 to 200 ms, over 200 ms, equal to them), direction changes,
// fractional carries, and every lines setting: auto, integers, fractions, below 1, large (up to the int32 range Go returns), NaN and infinities,
// switched mid-script. A setting above MaxInt32 is left out: Go's Next returns an
// int and clamps there, where Pi returns the number. Pi's product path cannot reach it, since settings-manager.ts:1414 getFullscreenWheelScrollLines
// clamps the setting to 1..100.
func wheelScripts() []wheelScript {
	random := rand.New(rand.NewSource(20260933))
	settings := []any{"auto", "auto", "auto", 1.0, 3.0, 2.7, 0.2, 0.0, -4.0, 1e9, 2e9, "NaN", "Infinity", "-Infinity"}
	gaps := []float64{0, 1, 4, 4.99, 5, 6, 10, 20, 25, 33.3, 50, 99, 100, 150, 199, 200, 200.5, 201, 500, 3000}
	var scripts []wheelScript
	for range 1500 {
		script := wheelScript{Lines: settings[random.Intn(len(settings))], Accelerate: random.Intn(5) != 0}
		now := float64(random.Intn(1000))
		dir := 1
		for range 5 + random.Intn(40) {
			switch r := random.Intn(12); {
			case r == 0:
				v := settings[random.Intn(len(settings))]
				script.Ops = append(script.Ops, wheelOp{Set: &v})
				continue
			case r < 3:
				dir = -dir
			}
			now += gaps[random.Intn(len(gaps))]
			script.Ops = append(script.Ops, wheelOp{Dir: dir, Now: now})
		}
		scripts = append(scripts, script)
	}
	return scripts
}

// wheel-scroll.ts runs in Node from the pinned source against the same scripts: every line count the accelerator returns, through gesture gaps, burst
// gaps, direction changes, carried fractions and lines settings (including NaN and infinities) changed in the middle of a gesture, must match Pi's.
func TestWheelScrollAcceleratorMatchesPiOnSeededScripts(t *testing.T) {
	scripts := wheelScripts()
	payload, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "tui", "src", "wheel-scroll.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/wheel_scroll.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]int
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(scripts) {
		t.Fatalf("Pi answered %d of %d scripts", len(expected), len(scripts))
	}
	multi, differing := 0, 0
	for i, script := range scripts {
		accelerator := NewWheelScrollAccelerator(wheelLines(script.Lines), script.Accelerate)
		got := []int{}
		for _, op := range script.Ops {
			if op.Set != nil {
				accelerator.SetLines(wheelLines(*op.Set))
				continue
			}
			n := accelerator.Next(op.Dir, op.Now)
			if n > 1 {
				multi++
			}
			got = append(got, n)
		}
		want := expected[i]
		if want == nil {
			want = []int{}
		}
		if !reflect.DeepEqual(got, want) {
			if differing++; differing <= 5 {
				raw, _ := json.Marshal(script)
				t.Errorf("script %d %s\n  Pig %v\n  Pi  %v", i, raw, got, want)
			}
		}
	}
	if differing > 5 {
		t.Errorf("%d of %d scripts differ from Pi", differing, len(scripts))
	}
	if multi < 1000 {
		t.Errorf("scripts barely accelerate: %d multi-line events", multi)
	}
}
