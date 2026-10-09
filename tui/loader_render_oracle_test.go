package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type loaderRenderStep struct {
	Message *string `json:"message"`
	Ticks   int     `json:"ticks"`
}

type loaderRenderProbe struct {
	Message   string             `json:"message"`
	Indicator map[string]any     `json:"indicator"`
	Steps     []loaderRenderStep `json:"steps"`
	Widths    []int              `json:"widths"`
}

// Loader.render against the pinned pi-tui (components/loader.ts): the spinner frame styled by spinnerColorFn (or verbatim for an extension indicator), the message
// styled by messageColorFn, one blank row above, wrapped at width minus the padding, after frame advances and message changes.
// Pi source: packages/tui/src/components/loader.ts
func TestLoaderRenderMatchesPi(t *testing.T) {
	str := func(s string) *string { return &s }
	messages := []string{"Working...", "", "日本語のメッセージが長くて折り返される場合", "\x1b[1mbold\x1b[0m message with a very long tail that has to wrap in narrow widths", "a message\nwith a line break"}
	indicators := []map[string]any{
		nil,
		{"frames": []string{}},
		{"frames": []string{"x"}},
		{"frames": []string{"⠋", "⠙", "⠹", "⠸"}, "intervalMs": 100},
		{"frames": []string{"🚀", "🌕"}},
		{"frames": []string{"\x1b[31m●\x1b[0m", "\x1b[32m●\x1b[0m"}},
		{"intervalMs": 50},
	}
	steps := []loaderRenderStep{{nil, 0}, {nil, 1}, {nil, 2}, {str("changed message"), 0}, {str(""), 3}, {str("last"), 1}}
	var probes []loaderRenderProbe
	for _, message := range messages {
		for _, indicator := range indicators {
			probes = append(probes, loaderRenderProbe{Message: message, Indicator: indicator, Steps: steps, Widths: []int{1, 3, 4, 10, 30, 80}})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/loader_render.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][][][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	tag := func(name string) func(string) string {
		return func(text string) string { return "<" + name + ">" + text + "</" + name + ">" }
	}
	failures := 0
	for i, probe := range probes {
		var indicator *LoaderIndicatorOptions
		if probe.Indicator != nil {
			indicator = &LoaderIndicatorOptions{}
			if frames, ok := probe.Indicator["frames"].([]string); ok {
				indicator.Frames = frames
			}
			if ms, ok := probe.Indicator["intervalMs"].(int); ok {
				indicator.IntervalMs = float64(ms)
			}
		}
		loader := NewLoader(nil, tag("sp"), tag("msg"), probe.Message, indicator)
		for s, step := range probe.Steps {
			if step.Message != nil {
				loader.SetMessage(*step.Message)
			}
			for range step.Ticks {
				loader.Tick()
			}
			for w, width := range probe.Widths {
				got := loader.Render(width)
				if !reflect.DeepEqual(nonNil(got), nonNil(expected[i][s][w])) {
					failures++
					if failures <= 5 {
						spec, _ := json.Marshal(probe)
						t.Errorf("probe %d step %d width %d %s\n pi: %q\n go: %q", i, s, width, spec, expected[i][s][w], got)
					}
				}
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d renders differ from Pi", failures)
	}
}
