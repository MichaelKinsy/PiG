package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type userMessageRenderProbe struct {
	Theme  string `json:"theme"`
	Text   string `json:"text"`
	Pad    int    `json:"pad"`
	Pads   []int  `json:"pads"`
	Widths []int  `json:"widths"`
}

// UserMessageComponent.render (components/user-message.ts) against pinned Pi: the message as markdown on the user background with the ordered-list markers and
// backslash escapes kept, the vertical padding rows, the output padding (changed by setOutputPad), and the OSC 133 zone marks on the first and last row.
func TestUserMessageComponentRenderMatchesPi(t *testing.T) {
	texts := []string{"", "hello", "  \n\n  ", "plain text that is long enough to wrap in the narrower widths of this probe", "line one\nline two\n\nline four",
		"3. third\n4. fourth\n\n9) paren", "1. a\n1. b\n1. c", "escapes \\* \\_ \\# \\[x\\] \\\\ \\` and \\<b\\>", "**bold** *italic* `code` ~~strike~~ [link](https://x.y)",
		"# Heading\n\n> quote\n\n- item\n- item two\n\n```go\nfmt.Println(1)\n```", "| a | b |\n|---|---|\n| 1 | 2 |", "日本語のメッセージ 🙂 with wide cells", "trailing spaces   \nand a hard break  \nend",
		"\x1b[31mraw escape\x1b[0m text", "tab\there", "$x^2$ and \\(y\\)", "https://example.com/a/very/long/url/that/does/not/wrap/anywhere/at/all/in/narrow/widths"}
	var probes []userMessageRenderProbe
	for _, theme := range []string{"dark", "light"} {
		for _, text := range texts {
			for _, pad := range []int{0, 1, 3} {
				probes = append(probes, userMessageRenderProbe{Theme: theme, Text: text, Pad: pad, Pads: []int{2, 0, 1}, Widths: []int{1, 4, 12, 30, 80}})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/user_message_render.mjs", pigversion.UpstreamVersion)
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
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		component := NewUserMessageComponent(probe.Text, nil, probe.Pad, nil)
		check := func(frame int) {
			for w, width := range probe.Widths {
				got := component.Render(width)
				if !reflect.DeepEqual(nonNil(got), nonNil(expected[i][frame][w])) {
					if failures++; failures <= 4 {
						spec, _ := json.Marshal(probe)
						t.Errorf("frame %d width %d %s\n  Pig %q\n  Pi  %q", frame, width, spec, got, expected[i][frame][w])
					}
				}
			}
		}
		check(0)
		for p, pad := range probe.Pads {
			component.SetOutputPad(pad)
			check(p + 1)
		}
	}
	if failures > 4 {
		t.Errorf("%d renders differ from Pi", failures)
	}
}
