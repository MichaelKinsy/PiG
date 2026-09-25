package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Upstream renderers/bash.ts formatDuration (0.87.1) switched from a bare
// `${(ms / 1000).toFixed(1)}s` to minutes and hours past one minute.
func TestFormatToolDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0.0s"},
		{1999 * time.Microsecond, "0.0s"}, // Date.now() differences are whole milliseconds
		{250 * time.Millisecond, "0.3s"},  // toFixed rounds an exact tie up; Go %.1f gives 0.2
		{1150 * time.Millisecond, "1.1s"}, // 1.15 is stored below the tie
		{3200 * time.Millisecond, "3.2s"},
		{59999 * time.Millisecond, "60.0s"},
		{60 * time.Second, "1m 0s"},
		{61500 * time.Millisecond, "1m 1s"},
		{3599999 * time.Millisecond, "59m 59s"},
		{time.Hour, "1h 0m 0s"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1h 2m 3s"},
		{25*time.Hour + 59*time.Minute + 59*time.Second, "25h 59m 59s"},
	}
	for _, tc := range cases {
		if got := FormatToolDuration(tc.in); got != tc.want {
			t.Errorf("FormatToolDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJSNumberString(t *testing.T) {
	cases := map[float64]string{
		1.5:     "1.5",
		10:      "10",
		0.1:     "0.1",
		-3:      "-3",
		1e-7:    "1e-7",
		1.5e-7:  "1.5e-7",
		1e21:    "1e+21",
		2147483: "2147483",
	}
	for in, want := range cases {
		if got := JSNumberString(in); got != want {
			t.Errorf("JSNumberString(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatShellHeader(t *testing.T) {
	th := ActiveTheme()
	title := func(s string) string { return th.ToolTitle + "\x1b[1m" + s + SGRBoldDimReset + SGRFgReset }
	muted := func(s string) string { return th.Muted + s + SGRFgReset }
	cases := []struct {
		name, prompt, args, want string
	}{
		{"bash command", "$", `{"command":"ls -la"}`, title("$ ls -la")},
		{"powershell command", "PS>", `{"command":"Get-ChildItem"}`, title("PS> Get-ChildItem")},
		{"fractional timeout", "$", `{"command":"sleep 2","timeout":1.5}`, title("$ sleep 2") + muted(" (timeout 1.5s)")},
		{"zero timeout is falsy", "$", `{"command":"x","timeout":0}`, title("$ x")},
		{"string timeout", "PS>", `{"command":"x","timeout":"5"}`, title("PS> x") + muted(" (timeout 5s)")},
		{"missing command", "$", `{}`, title("$ " + th.ToolOutput + "..." + SGRFgReset)},
		{"null command", "PS>", `{"command":null}`, title("PS> " + th.ToolOutput + "..." + SGRFgReset)},
		{"non-string command", "$", `{"command":42}`, title("$ " + th.Error + "[invalid arg]" + SGRFgReset)},
		{"partial args", "PS>", ``, title("PS> " + th.ToolOutput + "..." + SGRFgReset)},
	}
	for _, tc := range cases {
		if got := FormatShellHeader(json.RawMessage(tc.args), tc.prompt); got != tc.want {
			t.Errorf("%s: FormatShellHeader = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// createAllToolRenderers maps powershell to createShellRenderers("PS>").
func TestBuiltinToolHeaderUsesShellPrompts(t *testing.T) {
	args := json.RawMessage(`{"command":"echo hi","timeout":3}`)
	if got, want := HeaderForTool("powershell", args, ""), FormatShellHeader(args, "PS>"); got != want {
		t.Errorf("powershell header = %q, want %q", got, want)
	}
	if got, want := HeaderForTool("bash", args, ""), FormatShellHeader(args, "$"); got != want {
		t.Errorf("bash header = %q, want %q", got, want)
	}
	for _, name := range []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"} {
		if !HasBuiltInToolRenderers(name) {
			t.Errorf("HasBuiltInToolRenderers(%q) = false", name)
		}
	}
	if HasBuiltInToolRenderers("custom_tool") {
		t.Error("HasBuiltInToolRenderers(custom_tool) = true")
	}
}

// Upstream renders the running footer as Text("\n" + muted("Elapsed …")):
// Text owns the leading separator, wrapping, and row padding at the card's
// inner width. bash and powershell share this path through createShellRenderers.
func TestToolExecutionComponent_ShellElapsedFooterRows(t *testing.T) {
	const width = 10
	for _, tool := range []struct {
		name   string
		header string
	}{
		{name: "bash", header: "$ x"},
		{name: "powershell", header: "PS> x"},
	} {
		for _, branch := range []struct {
			name string
			set  func(*ToolExecutionComponent)
			want []string
		}{
			{
				name: "no output",
				set:  func(*ToolExecutionComponent) {},
				want: []string{"", "", tool.header, "", "Elapsed", "1m 1s", ""},
			},
			{
				name: "renderer output",
				set: func(c *ToolExecutionComponent) {
					c.BodyRenderer = func(int, bool) []string { return []string{"out"} }
					c.SetStreaming("out")
				},
				want: []string{"", "", tool.header, "", "out", "", "Elapsed", "1m 1s", ""},
			},
			{
				name: "collapsed plain output",
				set:  func(c *ToolExecutionComponent) { c.SetStreaming("out") },
				want: []string{"", "", tool.header, "", "out", "", "Elapsed", "1m 1s", ""},
			},
		} {
			t.Run(tool.name+"/"+branch.name, func(t *testing.T) {
				c := NewToolExecutionComponent(tool.name, tool.header)
				c.MarkExecutionStarted()
				c.StartedAt = time.Now().Add(-61 * time.Second)
				branch.set(c)

				rows := c.Render(width)
				visible := make([]string, len(rows))
				for i, row := range rows {
					if got := widthx.VisibleWidth(row); got > width {
						t.Errorf("row %d width = %d, want <= %d: %q", i, got, width, widthx.StripAnsi(row))
					}
					visible[i] = strings.TrimSpace(widthx.StripAnsi(row))
				}
				if strings.Join(visible, "|") != strings.Join(branch.want, "|") {
					t.Fatalf("rows = %q, want %q", visible, branch.want)
				}

				// The card's inner width is eight cells. Source-executing
				// upstream Text at that width produces these exact rows.
				c.StartedAt = time.Now().Add(-61 * time.Second)
				footer := c.runningElapsedRows(width - 2)
				gotFooter := make([]string, len(footer))
				for i, row := range footer {
					gotFooter[i] = widthx.StripAnsi(row)
				}
				wantFooter := []string{"        ", "Elapsed ", "1m 1s   "}
				if strings.Join(gotFooter, "|") != strings.Join(wantFooter, "|") {
					t.Fatalf("footer rows = %q, want upstream Text rows %q", gotFooter, wantFooter)
				}
			})
		}
	}
}
