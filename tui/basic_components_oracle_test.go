package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type basicComponentSpec struct {
	Kind     string               `json:"kind"`
	Text     string               `json:"text"`
	PaddingX int                  `json:"paddingX"`
	PaddingY int                  `json:"paddingY"`
	Lines    int                  `json:"lines"`
	Bg       bool                 `json:"bg,omitempty"`
	Children []basicComponentSpec `json:"children,omitempty"`
}

type basicComponentProbe struct {
	Spec  basicComponentSpec `json:"spec"`
	Width int                `json:"width"`
}

type basicComponentResult struct {
	First  []string `json:"first"`
	Second []string `json:"second"`
	Third  []string `json:"third"`
}

func (spec basicComponentSpec) build() Component {
	tag := func(text string) string { return "<bg>" + text + "</bg>" }
	switch spec.Kind {
	case "text":
		if spec.Bg {
			return NewPaddedText(spec.Text, spec.PaddingX, spec.PaddingY, tag)
		}
		return NewPaddedText(spec.Text, spec.PaddingX, spec.PaddingY, nil)
	case "truncated":
		return NewTruncatedText(spec.Text, spec.PaddingX, spec.PaddingY)
	case "spacer":
		return NewSpacer(spec.Lines)
	case "box":
		box := NewBox()
		box.PaddingX, box.PaddingY = spec.PaddingX, spec.PaddingY
		if spec.Bg {
			box.BgFn = tag
		}
		for _, child := range spec.Children {
			box.AddChild(child.build())
		}
		return box
	}
	panic("unknown kind " + spec.Kind)
}

// Text, TruncatedText, Spacer and Box against the pinned pi-tui (components/text.ts, truncated-text.ts, spacer.ts, box.ts): the same rows at every width, on
// the first render, the cached second render and the render after invalidate.
// Pi source: packages/tui/src/components/{text,truncated-text,spacer,box}.ts
func TestBasicComponentsRenderLikePi(t *testing.T) {
	contents := []string{
		"", " ", "hello", "hello world, this is a sentence that wraps over several rows at small widths",
		"line one\nline two\n\nline four", "trailing spaces   \n   leading spaces", "tab\tseparated\tcolumns here",
		"日本語のテキストと한국어와中文混合ｈａｌｆ ｶﾀｶﾅ", "👨‍👩‍👧‍👦🏳️‍🌈🇺🇸1️⃣⚠️❤️‍🔥👍🏽 emoji run",
		"\x1b[1;31mbold red that wraps across lines\x1b[0m and plain tail", "\x1b]8;;https://example.com/a/long/path\x1b\\link text\x1b]8;;\x1b\\ after",
		"averyveryverylongunbrokenwordthatmustbebrokenacrossmanyrowsatsmallwidths", "Z̴̢̛̗͓͖̹a̷͚͎l̶̰g̵̝o क्ष स्त्र กำ \u200b\u200d",
		"a\r\nb\rc", "  ", "\x1b[7mreverse\x1b[0m \x1b[48;5;12mbg\x1b[49m end",
	}
	widths := []int{1, 2, 3, 5, 8, 12, 20, 40, 80}
	pads := [][2]int{{0, 0}, {1, 1}, {2, 0}, {0, 2}, {3, 1}}
	var probes []basicComponentProbe
	for _, content := range contents {
		for _, pad := range pads {
			for _, width := range widths {
				for _, bg := range []bool{false, true} {
					probes = append(probes, basicComponentProbe{basicComponentSpec{Kind: "text", Text: content, PaddingX: pad[0], PaddingY: pad[1], Bg: bg}, width})
				}
				// D66: when the padding leaves no content cell, Pig clamps the horizontal padding where Pi emits a row wider than the viewport.
				if pad[0] <= (width-1)/2 {
					probes = append(probes, basicComponentProbe{basicComponentSpec{Kind: "truncated", Text: content, PaddingX: pad[0], PaddingY: pad[1]}, width})
				}
				probes = append(probes, basicComponentProbe{basicComponentSpec{Kind: "box", PaddingX: pad[0], PaddingY: pad[1], Bg: true, Children: []basicComponentSpec{
					{Kind: "text", Text: content, PaddingX: 0, PaddingY: 0}, {Kind: "spacer", Lines: 1}, {Kind: "truncated", Text: content},
				}}, width})
			}
		}
	}
	for _, lines := range []int{0, 1, 3} {
		probes = append(probes, basicComponentProbe{basicComponentSpec{Kind: "spacer", Lines: lines}, 20})
	}
	probes = append(probes, basicComponentProbe{basicComponentSpec{Kind: "box", PaddingX: 1, PaddingY: 1}, 10}, basicComponentProbe{basicComponentSpec{Kind: "box", PaddingX: 2, PaddingY: 0, Bg: true}, 10})
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/basic_components.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []basicComponentResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(expected), len(probes))
	}
	failures := 0
	for i, probe := range probes {
		component := probe.Spec.build()
		got := basicComponentResult{First: component.Render(probe.Width), Second: component.Render(probe.Width)}
		component.Invalidate()
		got.Third = component.Render(probe.Width)
		if !reflect.DeepEqual(nonNil(got.First), nonNil(expected[i].First)) || !reflect.DeepEqual(nonNil(got.Second), nonNil(expected[i].Second)) || !reflect.DeepEqual(nonNil(got.Third), nonNil(expected[i].Third)) {
			failures++
			if failures <= 5 {
				spec, _ := json.Marshal(probe)
				t.Errorf("probe %d %s\n pi: %q\n go: %q", i, strings.TrimSpace(string(spec)), expected[i].First, got.First)
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

func nonNil(lines []string) []string {
	if lines == nil {
		return []string{}
	}
	return lines
}
