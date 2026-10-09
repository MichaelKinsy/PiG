package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type statusOracleOp struct {
	Width      int     `json:"width"`
	Text       *string `json:"text"`
	Invalidate bool    `json:"invalidate"`
}

type statusOracleProbe struct {
	Kind      string             `json:"kind"`
	Widths    []int              `json:"widths,omitempty"`
	Texts     []string           `json:"texts,omitempty"`
	PaddingX  int                `json:"paddingX"`
	PaddingY  int                `json:"paddingY"`
	Ops       []statusOracleOp   `json:"ops,omitempty"`
	Message   string             `json:"message"`
	Indicator map[string]any     `json:"indicator"`
	Steps     []loaderRenderStep `json:"steps,omitempty"`
}

type statusOracleBorders struct {
	Border  string `json:"border"`
	Spinner string `json:"spinner"`
}

func statusOracleProbes() []statusOracleProbe {
	str := func(s string) *string { return &s }
	widths := []int{-3, 0, 1, 2, 5, 17, 80}
	probes := []statusOracleProbe{{Kind: "border", Widths: widths}, {Kind: "idle", Widths: widths[1:]}}
	for _, padding := range [][2]int{{1, 1}, {0, 0}, {2, 0}, {0, 2}, {3, 1}} {
		for _, texts := range [][]string{{"a b c"}, {""}, {"a rather long text that wraps in narrow widths"}, {"日本語 text 🙂"}, {"line one\nline two"}, {"\x1b[31mred\x1b[0m styled"}} {
			ops := []statusOracleOp{{Width: 10}, {Width: 10, Text: str("changed without invalidate")}, {Width: 20}, {Width: 10, Invalidate: true}, {Width: 3}, {Width: 40, Text: str(""), Invalidate: true}, {Width: 1, Text: str("tail"), Invalidate: true}}
			probes = append(probes, statusOracleProbe{Kind: "themed", Texts: texts, PaddingX: padding[0], PaddingY: padding[1], Ops: ops})
		}
	}
	indicators := []map[string]any{nil, {"frames": []string{}}, {"frames": []string{"x"}}, {"frames": []string{"⠋", "⠙", "⠹"}}, {"frames": []string{"🚀", "🌕"}}, {"frames": []string{"\x1b[31m●\x1b[0m", "\x1b[32m●\x1b[0m"}}}
	steps := []loaderRenderStep{{nil, 0}, {nil, 1}, {nil, 2}, {str("changed message"), 0}, {str(""), 1}, {str("日本語のメッセージが長い"), 1}, {str("a\nb"), 0}}
	for _, message := range []string{"Working", "", "a very long working message that will not fit in a narrow border", "日本語のメッセージ", "\x1b[1mbold\x1b[0m text"} {
		for _, indicator := range indicators {
			probes = append(probes, statusOracleProbe{Kind: "status", Message: message, Indicator: indicator, Steps: steps, Widths: []int{1, 2, 3, 8, 13, 40, 120}})
		}
	}
	return probes
}

// renderStatusProbe runs probe through Pig's components and returns the value Pi's oracle returns for it.
func renderStatusProbe(probe statusOracleProbe) any {
	tag := func(name string) func(string) string {
		return func(text string) string { return "<" + name + ">" + text + "</" + name + ">" }
	}
	switch probe.Kind {
	case "border":
		border := NewDynamicBorder(tag("b"))
		out := [][]string{}
		for _, width := range probe.Widths {
			out = append(out, border.Render(width))
		}
		return out
	case "idle":
		out := [][]string{}
		for _, width := range probe.Widths {
			out = append(out, nonNil((&IdleStatus{}).Render(width)))
		}
		return out
	case "themed":
		state := probe.Texts[0]
		text := NewThemedText(func() string { return state }, probe.PaddingX, probe.PaddingY)
		out := [][]string{}
		for _, op := range probe.Ops {
			if op.Text != nil {
				state = *op.Text
			}
			if op.Invalidate {
				text.Invalidate()
			}
			out = append(out, nonNil(text.Render(op.Width)))
		}
		return out
	default:
		var options *LoaderIndicatorOptions
		if probe.Indicator != nil {
			options = &LoaderIndicatorOptions{}
			if frames, ok := probe.Indicator["frames"].([]string); ok {
				options.Frames = frames
			}
		}
		indicator := &StatusIndicator{Kind: "working", Loader: NewLoader(nil, tag("sp"), tag("msg"), probe.Message, options)}
		out := [][]statusOracleBorders{}
		for _, step := range probe.Steps {
			if step.Message != nil {
				indicator.SetMessage(*step.Message)
			}
			for range step.Ticks {
				indicator.Tick()
			}
			row := []statusOracleBorders{}
			for _, width := range probe.Widths {
				row = append(row, statusOracleBorders{Border: indicator.RenderInBorder(width), Spinner: indicator.RenderSpinnerInBorder(width)})
			}
			out = append(out, row)
		}
		return out
	}
}

// TestStatusProbeDump prints the corpus for the Pi side of the status-components parity scenario.
func TestStatusProbeDump(t *testing.T) {
	line, err := json.Marshal(statusOracleProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("status-probes:%s\n", line)
}

// TestStatusComponentsParity prints Pig's results for the corpus, one JSON line per probe, for the status-components parity scenario.
func TestStatusComponentsParity(t *testing.T) {
	for _, probe := range statusOracleProbes() {
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false) // JSON.stringify writes < and > as they are
		if err := encoder.Encode(renderStatusProbe(probe)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("status-observation:%s", line.String())
	}
}

// IdleStatus throws a RangeError for a negative width, so its widths start at 0. DynamicBorder.render, ThemedText.render, IdleStatus.render and StatusIndicator.renderInBorder/renderSpinnerInBorder (components/dynamic-border.ts, themed-text.ts,
// status-indicator.ts) against pinned Pi: the rule at every width including 0 and below, the stale-until-invalidated text with its paddings, the two blank idle rows,
// and the loader line inside an editor border (frames, messages, widths down to 1).
func TestStatusComponentsMatchPi(t *testing.T) {
	probes := statusOracleProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/status_components.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
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
	failures := 0
	for i, probe := range probes {
		got, err := json.Marshal(renderStatusProbe(probe))
		if err != nil {
			t.Fatal(err)
		}
		var want, have any
		_ = json.Unmarshal(expected[i], &want)
		_ = json.Unmarshal(got, &have)
		if !reflect.DeepEqual(have, want) {
			if failures++; failures <= 5 {
				spec, _ := json.Marshal(probe)
				t.Errorf("%s\n  Pig %s\n  Pi  %s", spec, got, expected[i])
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
