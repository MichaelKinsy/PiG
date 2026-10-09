package codingagent

// Ports packages/coding-agent/src/extensions/codemode/renderer.ts (codemodeRenderers).

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/tui"
)

// CodemodeRenderers draw the codemode tool's card: the call shows the script; the result lists the nested calls with
// their status and the cost of its model calls, followed by the script output without the "Script completed" header.
// The codemode tool definition carries them, as upstream's definition spreads codemodeRenderers; they are not built-in
// renderers keyed by tool name (upstream createAllToolRenderers has no codemode entry).
//
// Ports packages/coding-agent/src/extensions/codemode/renderer.ts (codemodeRenderers).
var CodemodeRenderers = ToolRenderers{RenderCall: codemodeRenderCall, RenderResult: codemodeRenderResult}

const (
	codemodeCodePreviewLines = 10
	codemodeCallPreviewCount = 8
	codemodeOutputPreview    = 5
	codemodeCollapsedArgs    = 80
)

// codemodeDetails are the details of a codemode tool result (package coding/extension/builtin/codemode ToolDetails).
var codemodeScriptHeader = lazyregexp.New(`^Script (completed|failed)\nWall time [\d.]+ seconds\nOutput:\n$`)

type codemodeDetails struct {
	Calls []struct {
		Name       string   `json:"name"`
		Args       string   `json:"args"`
		Status     string   `json:"status"`
		DurationMs *float64 `json:"durationMs"`
		Error      string   `json:"error"`
		Cost       *float64 `json:"cost"`
	} `json:"calls"`
	FullOutputPath string `json:"fullOutputPath"`
}

func codemodeFg(token, text string) string { return tui.ActiveTheme().Fg(token, text) }

// codemodeExpandKeyHint is upstream's keyHint("app.tools.expand", "to expand"): the key the user bound.
func codemodeExpandKeyHint() string {
	return tui.KeyHint(tui.AppKeyText("app.tools.expand", "ctrl+o"), "to expand")
}

// codemodeExpandHint is upstream's `... (N more <noun>, <key> to expand)`.
func codemodeExpandHint(hidden int, noun string) string {
	return codemodeFg("muted", fmt.Sprintf("... (%d more %s,", hidden, noun)) + " " + codemodeExpandKeyHint() + codemodeFg("muted", ")")
}

func codemodeDuration(ms *float64) string {
	if ms == nil {
		return ""
	}
	if *ms < 1000 {
		return fmt.Sprintf("%sms", jsstring.ToFixed(*ms, 0))
	}
	return jsstring.ToFixed(*ms/1000, 1) + "s"
}

// codemodeCost is cents for larger amounts, two significant digits for the fractions of a cent classifier calls cost.
func codemodeCost(cost float64) string {
	if cost >= 0.01 {
		return "$" + jsstring.ToFixed(cost, 2)
	}
	return "$" + jsstring.ToPrecision(cost, 2)
}

// codemodeContainer is upstream's `(context.lastComponent as Container | undefined) ?? new Container()`, cleared.
func codemodeContainer(last extension.Component) *tui.Container {
	component, ok := last.(*tui.Container)
	if !ok || component == nil {
		component = tui.NewContainer()
	}
	component.Clear()
	return component
}

func codemodeTextComponent(text string) *tui.Text { return tui.NewPaddedText(text, 0, 0, nil) }

// codemodeCodeArg is upstream's str(args?.code): the string, "" when absent or null, and not ok for another type.
func codemodeCodeArg(args json.RawMessage) (string, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil {
		return "", true
	}
	raw, present := fields["code"]
	if !present || string(raw) == "null" {
		return "", true
	}
	var code string
	if json.Unmarshal(raw, &code) != nil {
		return "", false
	}
	return code, true
}

func codemodeRenderCall(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	// The code includes the `// @options:` line, so options show as part of the script.
	code, valid := codemodeCodeArg(args)
	title := codemodeFg("toolTitle", "\x1b[1mcodemode\x1b[22m")
	component := codemodeContainer(context.LastComponent)
	if !valid {
		component.Add(codemodeTextComponent(title + " " + codemodeFg("error", "[invalid arg]")))
		return component
	}
	component.Add(codemodeTextComponent(title))
	if code != "" {
		highlighted := strings.Join(tui.HighlightCode(replaceTabs(jsstring.TrimEnd(strings.ReplaceAll(code, "\r", ""))), "javascript"), "\n")
		if context.Expanded {
			component.Add(codemodeTextComponent(highlighted))
		} else {
			component.Add(tui.NewVisualLinePreview(tui.VisualLinePreviewOptions{
				Text:           highlighted,
				MaxVisualLines: codemodeCodePreviewLines,
				Keep:           tui.VisualKeepStart,
				FormatHint:     func(hidden int) string { return codemodeExpandHint(hidden, "lines") },
			}))
		}
	}
	return component
}

func codemodeRenderResult(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	value := renderResultValue(result)
	var details codemodeDetails
	if raw, err := json.Marshal(value.Details); err == nil {
		_ = json.Unmarshal(raw, &details)
	}
	component := codemodeContainer(context.LastComponent)
	if calls := details.Calls; len(calls) > 0 {
		shown := calls
		if !options.Expanded && len(calls) > codemodeCallPreviewCount {
			shown = calls[len(calls)-codemodeCallPreviewCount:]
		}
		lines := make([]string, 0, len(shown))
		if len(shown) < len(calls) {
			lines = append(lines, codemodeFg("muted", fmt.Sprintf("... (%d earlier calls,", len(calls)-len(shown)))+" "+codemodeExpandKeyHint()+codemodeFg("muted", ")"))
		}
		for _, call := range shown {
			icon := ""
			switch call.Status {
			case "running":
				icon = codemodeFg("warning", "…")
			case "ok":
				icon = codemodeFg("success", "✓")
			case "error":
				icon = codemodeFg("error", "✗")
			case "cancelled":
				icon = codemodeFg("muted", "⊘")
			}
			args := call.Args
			if !options.Expanded && jsstring.Length(args) > codemodeCollapsedArgs {
				args = jsstring.Slice(args, 0, codemodeCollapsedArgs-3) + "..."
			}
			line := icon + " " + codemodeFg("toolTitle", call.Name)
			if args != "" {
				line += " " + codemodeFg("muted", args)
			}
			if duration := codemodeDuration(call.DurationMs); duration != "" {
				line += " " + codemodeFg("dim", duration)
			}
			if call.Cost != nil && *call.Cost != 0 {
				line += " " + codemodeFg("dim", codemodeCost(*call.Cost))
			}
			if options.Expanded && call.Error != "" {
				line += "\n    " + codemodeFg("error", strings.ReplaceAll(call.Error, "\n", "\n    "))
			}
			lines = append(lines, line)
		}
		// Collapsed rows hide earlier calls, so the total covers every call.
		var total float64
		priced := 0
		for _, call := range calls {
			if call.Cost != nil && *call.Cost != 0 {
				priced++
				total += *call.Cost
			}
		}
		if priced > 1 {
			lines = append(lines, codemodeFg("muted", "Model calls: "+codemodeCost(total)))
		}
		component.Add(tui.NewSpacer(1))
		component.Add(codemodeTextComponent(strings.Join(lines, "\n")))
	}

	// Drop the "Script completed\nWall time ... \nOutput:\n" header. Rejected input (invalid options) has no header.
	content := value.Content
	if len(content) > 0 {
		if first, ok := content[0].(ai.TextContent); ok && codemodeScriptHeader.MatchString(first.Text) {
			content = content[1:]
		}
	}
	output := ""
	if !options.IsPartial {
		output = jsstring.Trim(codemodeTextOutput(content, context.ShowImages))
	}
	if output != "" {
		token := "toolOutput"
		if context.IsError {
			token = "error"
		}
		lines := strings.Split(replaceTabs(output), "\n")
		for i, line := range lines {
			lines[i] = codemodeFg(token, line)
		}
		styled := strings.Join(lines, "\n")
		component.Add(tui.NewSpacer(1))
		if options.Expanded {
			component.Add(codemodeTextComponent(styled))
		} else {
			// Limit wrapped lines, not logical ones: script output is often one long JSON line.
			component.Add(tui.NewVisualLinePreview(tui.VisualLinePreviewOptions{
				Text:           styled,
				MaxVisualLines: codemodeOutputPreview,
				Keep:           tui.VisualKeepStart,
				FormatHint:     func(hidden int) string { return codemodeExpandHint(hidden, "lines") },
			}))
			// The collapsed preview hides the truncation notice at the end, so name the file here.
			if details.FullOutputPath != "" {
				component.Add(codemodeTextComponent(codemodeFg("muted", "Full output: "+details.FullOutputPath)))
			}
		}
	}
	return component
}

// codemodeTextOutput is upstream's getTextOutput (core/tools/render-utils.ts): the text blocks without ANSI escapes,
// binary garbage or carriage returns, joined by newlines, then one fallback line per image when the terminal cannot
// show images or images are hidden.
func codemodeTextOutput(content []ai.ToolResultMessageContent, showImages bool) string {
	var texts []string
	var images []ai.ImageContent
	for _, block := range content {
		switch b := block.(type) {
		case ai.TextContent:
			texts = append(texts, strings.ReplaceAll(tools.SanitizeBinaryOutput(string(tools.StripANSI([]byte(b.Text)))), "\r", ""))
		case ai.ImageContent:
			images = append(images, b)
		}
	}
	output := strings.Join(texts, "\n")
	if len(images) > 0 && (tui.GetCapabilities().Images == "" || !showImages) {
		indicators := make([]string, len(images))
		for i, image := range images {
			mimeType := image.MimeType
			if mimeType == "" {
				mimeType = "image/unknown"
			}
			var dimensions *tui.ImageDimensions
			if image.Data != "" && image.MimeType != "" {
				dimensions = tui.GetImageDimensions(image.Data, image.MimeType)
			}
			indicators[i] = tui.ImageFallback(mimeType, dimensions, "")
		}
		if output != "" {
			output += "\n"
		}
		output += strings.Join(indicators, "\n")
	}
	return output
}
