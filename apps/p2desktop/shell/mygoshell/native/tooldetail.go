package native

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/activity"
	"github.com/egoist/mygo/ui"
)

// pig additive (D481): a call's detail, as the web's sheet shows it (plugins/coding-tools/activity.tsx): a command and its output as
// a terminal, a read's numbered lines, a write's content or an edit's diff; any other tool's input as JSON, then its output.

func argText(s activity.Step, key string) string {
	if s.Tool.Args == nil {
		return ""
	}
	v, _ := s.Tool.Args.Lookup(key).(string)
	return v
}

func (a *App) toolDetail(c *ui.Context, s activity.Step) {
	t := c.Theme()
	failed := s.Status == activity.StepError
	ui.Column(c).Gap(10).Padding(6, 4, 10).Children(func() {
		switch s.Tool.Name {
		case "bash":
			section(c, "Input", func() { terminal(c, "$ "+argText(s, "command"), false) })
			section(c, "Output", func() { output(c, s.Tool.Output, failed) })
		case "read":
			section(c, "Input", func() { filePath(c, argText(s, "path")) })
			section(c, "Output", func() {
				switch {
				case len(s.Tool.Images) > 0:
					ui.Text(c, "Image").FontSize(12).TextColor(t.TextMuted)
				case failed:
					output(c, s.Tool.Output, true)
				default:
					readLines(c, s.Tool.Output)
				}
			})
		case "write", "edit":
			section(c, "Input", func() { filePath(c, argText(s, "path")) })
			section(c, "Output", func() {
				diff := ""
				if d, ok := s.Tool.Details.(*jsvalue.Object); ok {
					diff, _ = d.Lookup("diff").(string)
				}
				switch {
				case failed:
					output(c, s.Tool.Output, true)
				case diff != "":
					diffBlock(c, diff)
				default:
					terminal(c, strings.TrimRight(firstNonEmpty(argText(s, "content"), s.Tool.Output), "\n"), false)
				}
			})
		default:
			section(c, "Input", func() { terminal(c, argsJSON(s), false) })
			section(c, "Output", func() { output(c, s.Tool.Output, failed) })
		}
	})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func argsJSON(s activity.Step) string {
	if s.Tool.Args == nil {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for i, k := range s.Tool.Args.Keys {
		v, _ := strconv.Unquote(fmt.Sprintf("%q", fmt.Sprint(s.Tool.Args.Lookup(k))))
		sep := ","
		if i == len(s.Tool.Args.Keys)-1 {
			sep = ""
		}
		fmt.Fprintf(&b, "  %q: %q%s\n", k, clip(v, 400), sep)
	}
	b.WriteString("}")
	return b.String()
}

func section(c *ui.Context, label string, body func()) {
	ui.Column(c.Key("section/" + label)).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(c.Theme().TextMuted)
		body()
	})
}

func filePath(c *ui.Context, path string) {
	ui.Text(c, path).FontSize(12).Font("monospace").Selectable().SingleLine().Ellipsis("…")
}

// terminal is a block of monospaced text on the surface colour.
func terminal(c *ui.Context, text string, failed bool) {
	t := c.Theme()
	colour := t.Text
	if failed {
		colour = t.Danger
	}
	ui.Column(c).Padding(8, 10).Radius(6).Background(t.Surface).BorderWidth(1).BorderColor(t.Border).Children(func() {
		ui.Text(c, text).FontSize(12).Font("monospace").TextColor(colour).Selectable()
	})
}

func output(c *ui.Context, text string, failed bool) {
	text = strings.TrimRight(text, " \t\r\n")
	if text == "" {
		ui.Text(c, "No output").FontSize(12).TextColor(c.Theme().TextMuted)
		return
	}
	terminal(c, clip(text, 20000), failed)
}

var numbered = regexp.MustCompile(`^\s*(\d+)\t(.*)$`)

// readLines is a read's output ("40\tcode" per line) as the file's lines, numbered from where the read started.
func readLines(c *ui.Context, out string) {
	t := c.Theme()
	lines := strings.Split(strings.TrimRight(out, " \t\r\n"), "\n")
	ui.Column(c).Padding(8, 10).Radius(6).Background(t.Surface).BorderWidth(1).BorderColor(t.Border).Children(func() {
		for i, line := range lines {
			if i >= 400 {
				ui.Text(c, fmt.Sprintf("… %d more lines", len(lines)-i)).FontSize(12).TextColor(t.TextMuted)
				return
			}
			n, text := strconv.Itoa(i+1), line
			if m := numbered.FindStringSubmatch(line); m != nil {
				n, text = m[1], m[2]
			}
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, n).FontSize(12).Font("monospace").TextColor(t.TextMuted).Width(36)
				ui.Text(c, text).FontSize(12).Font("monospace").Selectable()
			})
		}
	})
}

// diffBlock is an edit's diff ("+12 new", "-12 old", " 12 same"): additions and removals on tinted lines.
func diffBlock(c *ui.Context, diff string) {
	t := c.Theme()
	ui.Column(c).Padding(8, 0).Radius(6).Background(t.Surface).BorderWidth(1).BorderColor(t.Border).Children(func() {
		for i, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
			if i >= 400 {
				return
			}
			prefix, rest := "", line
			if line != "" {
				prefix, rest = line[:1], line[1:]
			}
			row := ui.Row(c).Gap(8).Padding(0, 10)
			switch prefix {
			case "+":
				row.Background(t.Success.Alpha(0.16))
			case "-":
				row.Background(t.Danger.Alpha(0.16))
			}
			row.Children(func() {
				ui.Text(c, prefix).FontSize(12).Font("monospace").TextColor(t.TextMuted).Width(10)
				ui.Text(c, strings.TrimLeft(rest, " ")).FontSize(12).Font("monospace").Selectable()
			})
		}
	})
}
