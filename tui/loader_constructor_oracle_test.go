package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type loaderConstructorProbe struct {
	Cancellable bool           `json:"cancellable"`
	Message     string         `json:"message"`
	Indicator   map[string]any `json:"indicator"`
}

// goIndicator reads the JSON form the oracle receives (undefined frames are an absent key) as the Go options.
func (p loaderConstructorProbe) goIndicator() *LoaderIndicatorOptions {
	if p.Indicator == nil {
		return nil
	}
	options := &LoaderIndicatorOptions{}
	if frames, ok := p.Indicator["frames"].([]string); ok {
		options.Frames = frames
	}
	if interval, ok := p.Indicator["intervalMs"].(float64); ok {
		options.IntervalMs = interval
	}
	return options
}

type loaderConstructorResult struct {
	Frames     []string   `json:"frames"`
	IntervalMs int        `json:"intervalMs"`
	Verbatim   bool       `json:"verbatim"`
	Rows       [][]string `json:"rows"`
}

// loader.ts:28-41 (and cancellable-loader.ts, which inherits it): the constructor's fifth argument is the indicator, applied through setIndicator.
// No indicator is the default spinner (not verbatim, 80ms); an indicator renders its frames verbatim, an empty frame list hides the indicator, and an interval that is not positive keeps the default.
func TestLoaderConstructorIndicatorMatchesPi(t *testing.T) {
	frames := func(f ...string) []string { return append([]string{}, f...) }
	var probes []loaderConstructorProbe
	for _, cancellable := range []bool{false, true} {
		for _, indicator := range []map[string]any{
			nil,
			{},
			{"frames": frames("a", "b", "c")},
			{"frames": frames("only")},
			{"frames": frames()},
			{"frames": frames("x", "y"), "intervalMs": 33.0},
			{"frames": frames("x", "y"), "intervalMs": -4.0},
			{"intervalMs": 120.0},
		} {
			probes = append(probes, loaderConstructorProbe{Cancellable: cancellable, Message: "Working...", Indicator: indicator})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/loader_constructor.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []loaderConstructorResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, probe := range probes {
		var loader *Loader
		if probe.Cancellable {
			loader = &NewCancellableLoader(nil, nil, nil, probe.Message, probe.goIndicator()).Loader
		} else {
			loader = NewLoader(nil, nil, nil, probe.Message, probe.goIndicator())
		}
		got := loaderConstructorResult{Frames: loader.Frames, IntervalMs: loader.IntervalMs, Verbatim: loader.IndicatorVerbatim}
		for _, width := range []int{30, 4} {
			got.Rows = append(got.Rows, loader.Render(width))
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("probe %d (cancellable=%v indicator=%+v): got %+v, Pi %+v", i, probe.Cancellable, probe.Indicator, got, want[i])
		}
	}
}
