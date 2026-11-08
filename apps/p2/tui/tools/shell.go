package tools

// A shell command's card, for plugins whose tools run commands (`createShellRenderers(prompt)`), copied from coding-agent core/tools/renderers/bash.ts.
// Durable's shell result carries no `details` (the spill path is a diagnostic), so the truncation footer is not shown; the recorded duration comes from
// the result.
//
// pi: apps/p2/src/tui/tools/shell.ts

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/components/visualtruncate"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

const shellPreviewLines = 5

func formatDuration(ms float64) string {
	seconds := ms / 1000
	if seconds < 60 {
		return jsstring.ToFixed(seconds, 1) + "s"
	}
	totalSeconds := int(math.Floor(seconds))
	minutes := totalSeconds / 60
	remainder := totalSeconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, remainder)
	}
	return fmt.Sprintf("%dh %dm %ds", minutes/60, minutes%60, remainder)
}

func formatShellCall(args ToolArgs, prompt string, t *theme.Theme) string {
	command, commandOK := Str(args.Lookup("command"))
	timeoutSuffix := ""
	if timeout, ok := Num(args.Lookup("timeout")); ok && timeout != 0 {
		timeoutSuffix = t.Fg("muted", " (timeout "+jsnumber.String(timeout)+"s)")
	}
	var commandDisplay string
	switch {
	case !commandOK:
		commandDisplay = InvalidArgText(t)
	case command != "":
		commandDisplay = render.SafeText(command)
	default:
		commandDisplay = t.Fg("toolOutput", "...")
	}
	return t.Fg("toolTitle", t.Bold(prompt+" "+commandDisplay)) + timeoutSuffix
}

func rebuildShellResult(component *pitui.Container, result ToolResultView, options ToolRenderResultOptions, t *theme.Theme, context *ToolRenderContext, startedAt, endedAt *float64) {
	component.Clear()
	output := jsstring.Trim(GetTextOutput(&result))
	if output != "" {
		lines := strings.Split(output, "\n")
		for i, line := range lines {
			lines[i] = t.Fg("toolOutput", line)
		}
		styled := strings.Join(lines, "\n")
		if options.Expanded {
			component.AddChild(pitui.NewPaddedText("\n"+styled, 0, 0, nil))
		} else {
			component.AddChild(pitui.NewSpacer(1))
			component.AddChild(visualtruncate.NewVisualLinePreview(visualtruncate.VisualLinePreviewOptions{
				Text: styled, MaxVisualLines: shellPreviewLines, KeepStart: false,
				FormatHint: func(hidden int) string {
					return t.Fg("muted", fmt.Sprintf("... (%d earlier lines,", hidden)) + " " + render.KeyHint(context.UI, "app.tools.expand", "to expand") + t.Fg("muted", ")")
				},
			}))
		}
	}
	// A final result's recorded duration wins; the card's own clock is the fallback for live progress.
	if !options.IsPartial && context.DurationMs != nil {
		component.AddChild(pitui.NewPaddedText("\n"+t.Fg("muted", "Took "+formatDuration(*context.DurationMs)), 0, 0, nil))
	} else if startedAt != nil {
		label := "Took"
		if options.IsPartial {
			label = "Elapsed"
		}
		end := float64(time.Now().UnixMilli())
		if endedAt != nil {
			end = *endedAt
		}
		component.AddChild(pitui.NewPaddedText("\n"+t.Fg("muted", label+" "+formatDuration(end-*startedAt)), 0, 0, nil))
	}
}

func stateNumber(state map[string]any, key string) *float64 {
	if value, ok := state[key].(float64); ok {
		return &value
	}
	return nil
}

// CreateShellRenderers is the card of a tool that runs `args.command` (and an optional `args.timeout` in seconds): `$ command`, the output's last
// lines, how long it took. Shells differ only in the prompt they display (prompt).
func CreateShellRenderers(prompt string) *ToolRenderers {
	return &ToolRenderers{
		RenderCall: func(args ToolArgs, t *theme.Theme, context *ToolRenderContext) pitui.Component {
			state := context.State
			if context.ExecutionStarted && state["startedAt"] == nil {
				state["startedAt"] = float64(time.Now().UnixMilli())
				delete(state, "endedAt")
			}
			text, ok := context.LastComponent.(*pitui.Text)
			if !ok {
				text = pitui.NewPaddedText("", 0, 0, nil)
			}
			text.SetText(formatShellCall(args, prompt, t))
			return text
		},
		RenderResult: func(result ToolResultView, options ToolRenderResultOptions, t *theme.Theme, context *ToolRenderContext) pitui.Component {
			state := context.State
			// The card's own clock only for runs it watched: a stored result without a duration shows none.
			if options.IsPartial {
				state["watched"] = true
			} else if watched, _ := state["watched"].(bool); !watched {
				delete(state, "startedAt")
			}
			if state["startedAt"] != nil && options.IsPartial && state["interval"] == nil {
				state["interval"] = startTicker(time.Second, context.Invalidate)
			}
			if !options.IsPartial || context.IsError {
				if state["endedAt"] == nil {
					state["endedAt"] = float64(time.Now().UnixMilli())
				}
				if stop, ok := state["interval"].(func()); ok {
					stop()
					delete(state, "interval")
				}
			}
			component, ok := context.LastComponent.(*pitui.Container)
			if !ok {
				component = pitui.NewContainer()
			}
			rebuildShellResult(component, result, options, t, context, stateNumber(state, "startedAt"), stateNumber(state, "endedAt"))
			component.Invalidate()
			return component
		},
	}
}

// startTicker calls fn every period until the returned stop function is called.
func startTicker(period time.Duration, fn func()) (stop func()) {
	ticker := time.NewTicker(period)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				fn()
			case <-done:
				return
			}
		}
	}()
	var once bool
	return func() {
		if !once {
			once = true
			ticker.Stop()
			close(done)
		}
	}
}

