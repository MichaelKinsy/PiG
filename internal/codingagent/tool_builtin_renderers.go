package codingagent

// Ports packages/coding-agent/src/core/tools/renderers/index.ts
// withBuiltInRenderers with the renderCall and renderResult pairs of the
// built-in tools, so an extension's override of a built-in tool that defines
// one renderer draws the built-in renderer in the other half.

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	builtInPreviewLines   = 10          // upstream: packages/coding-agent/src/core/tools/renderers/read.ts:formatReadResult
	shellElapsedTickEvery = time.Second // upstream: packages/coding-agent/src/core/tools/renderers/bash.ts:createShellRenderers
	builtInRenderStateKey = "\x00pig-builtin-render-state"
)

// withBuiltInRenderers is upstream withBuiltInRenderers: a definition of a
// built-in tool name that lacks renderCall or renderResult gets the built-in
// renderer for it.
func withBuiltInRenderers(name string, definition extension.ToolDefinition) extension.ToolDefinition {
	call, result := builtInToolRenderers(name)
	if call == nil {
		return definition
	}
	if definition.RenderCall == nil {
		definition.RenderCall = call
	}
	if definition.RenderResult == nil {
		definition.RenderResult = result
	}
	return definition
}

// linesComponent renders the rows a function produces for a width.
type linesComponent struct{ render func(width int) []string }

func (c linesComponent) Render(width int) []string { return c.render(width) }
func (linesComponent) Invalidate()                 {}

// builtInRenderState is the part of a card's renderer state the built-in
// renderers keep. The shell ticker and the edit preview update it off the UI
// loop, so it has its own lock.
type builtInRenderState struct {
	mu        sync.Mutex
	startedAt time.Time
	endedAt   time.Time
	stopTick  chan struct{}

	editArgsKey  string
	editPreview  *tools.EditsDiffPreview
	editPending  bool
	editSettledE bool
	// editCall reports that the built-in edit renderCall drew this card, so
	// its box holds the preview (upstream state.callComponent).
	editCall bool
}

func builtInState(context extension.ToolRenderContext) *builtInRenderState {
	state, ok := context.State.(map[string]any)
	if !ok || state == nil {
		return &builtInRenderState{}
	}
	if existing, ok := state[builtInRenderStateKey].(*builtInRenderState); ok {
		return existing
	}
	created := &builtInRenderState{}
	state[builtInRenderStateKey] = created
	return created
}

func builtInToolRenderers(name string) (extension.ToolRenderCallFunc, extension.ToolRenderResultFunc) {
	switch name {
	case "bash", "powershell":
		prompt, _ := tui.ShellToolPrompt(name)
		return shellRenderCall(prompt), shellRenderResult
	case "read":
		return readRenderCall, readRenderResult
	case "write":
		return writeRenderCall, writeRenderResult
	case "edit":
		return editRenderCall, editRenderResult
	case "grep":
		return headerRenderCall(func(args json.RawMessage, _ string) string { return tui.FormatGrepHeader(args) }), listRenderResult("grep")
	case "find":
		return headerRenderCall(func(args json.RawMessage, _ string) string { return tui.FormatFindHeader(args) }), listRenderResult("find")
	case "ls":
		return headerRenderCall(tui.FormatLsHeader), listRenderResult("ls")
	}
	return nil, nil
}

func headerRenderCall(header func(json.RawMessage, string) string) extension.ToolRenderCallFunc {
	return func(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		return tui.NewPaddedText(header(args, context.Cwd), 0, 0, nil)
	}
}

func renderResultValue(result extension.AgentToolResult) agent.AgentToolResult {
	switch value := result.(type) {
	case agent.AgentToolResult:
		return value
	case *agent.AgentToolResult:
		if value != nil {
			return *value
		}
	}
	return agent.AgentToolResult{}
}

func stringArg(args any, keys ...string) (string, bool) {
	var fields map[string]any
	switch value := args.(type) {
	case json.RawMessage:
		_ = json.Unmarshal(value, &fields)
	case map[string]any:
		fields = value
	}
	for _, key := range keys {
		value, present := fields[key]
		if !present || value == nil {
			continue
		}
		text, ok := value.(string)
		return text, ok
	}
	return "", true
}

// shellRenderCall is upstream createShellRenderers renderCall: it records
// when the command started executing.
func shellRenderCall(prompt string) extension.ToolRenderCallFunc {
	return func(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		state := builtInState(context)
		state.mu.Lock()
		if context.ExecutionStarted && state.startedAt.IsZero() {
			state.startedAt = time.Now()
			state.endedAt = time.Time{}
		}
		state.mu.Unlock()
		return tui.NewPaddedText(tui.FormatShellHeader(args, prompt), 0, 0, nil)
	}
}

// shellRenderResult is upstream createShellRenderers renderResult: the output
// preview, warnings, and the Elapsed footer that invalidates the card every
// second while the command runs, then the Took footer.
func shellRenderResult(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	state := builtInState(context)
	state.mu.Lock()
	if !state.startedAt.IsZero() && options.IsPartial && state.stopTick == nil {
		stop := make(chan struct{})
		state.stopTick = stop
		invalidate := context.Invalidate
		go func() {
			ticker := time.NewTicker(shellElapsedTickEvery)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					if invalidate != nil {
						invalidate()
					}
				}
			}
		}()
	}
	if !options.IsPartial || context.IsError {
		if state.endedAt.IsZero() {
			state.endedAt = time.Now()
		}
		if state.stopTick != nil {
			close(state.stopTick)
			state.stopTick = nil
		}
	}
	startedAt, endedAt := state.startedAt, state.endedAt
	state.mu.Unlock()

	value := renderResultValue(result)
	details := shellDetailsFrom(value.Details)
	footer := ""
	if !startedAt.IsZero() {
		label := "Took"
		if options.IsPartial {
			label = "Elapsed"
		}
		end := endedAt
		if end.IsZero() {
			end = time.Now()
		}
		footer = themeFg(tui.ActiveTheme().Muted, label+" "+tui.FormatToolDuration(end.Sub(startedAt)))
	}
	return linesComponent{render: func(width int) []string {
		lines := shellResultLines(value.Content, details, options.IsPartial, options.Expanded, width)
		if footer != "" {
			lines = append(lines, tui.NewPaddedText("\n"+footer, 0, 0, nil).Render(width)...)
		}
		return lines
	}}
}

func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}

func moreLinesHint(text string) string {
	theme := tui.ActiveTheme()
	return themeFg(theme.Muted, text) + " " + expandKeyHint() + themeFg(theme.Muted, ")")
}

// readRenderCall is upstream read.ts renderCall: the compact label for a
// skill, docs or context file while collapsed, else the path and range.
func readRenderCall(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	if !context.Expanded {
		if header := tui.FormatCompactReadHeader(args, context.Cwd); header != "" {
			return tui.NewPaddedText(header, 0, 0, nil)
		}
	}
	return tui.NewPaddedText(tui.FormatReadHeader(args, context.Cwd), 0, 0, nil)
}

// readRenderResult is upstream read.ts formatReadResult: nothing while
// collapsed unless the read failed; otherwise the output, highlighted by the
// file's language, the first ten lines while collapsed, and the truncation
// warning.
func readRenderResult(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	if !options.Expanded && !context.IsError {
		return tui.NewPaddedText("", 0, 0, nil)
	}
	theme := tui.ActiveTheme()
	value := renderResultValue(result)
	rawPath, _ := stringArg(context.Args, "file_path", "path")
	output := shellTextOutput(value.Content)
	lang := ""
	if !context.IsError && rawPath != "" {
		lang = tui.LanguageFromPath(rawPath)
	}
	var rendered []string
	if lang != "" {
		rendered = tui.HighlightCode(replaceTabs(output), lang)
	} else {
		rendered = strings.Split(output, "\n")
	}
	lines := trimTrailingEmptyLines(rendered)
	display := lines
	if !options.Expanded && len(lines) > builtInPreviewLines {
		display = lines[:builtInPreviewLines]
	}
	styled := make([]string, len(display))
	for i, line := range display {
		if lang != "" {
			styled[i] = replaceTabs(line)
		} else {
			styled[i] = themeFg(theme.ToolOutput, replaceTabs(line))
		}
	}
	text := "\n" + strings.Join(styled, "\n")
	if remaining := len(lines) - len(display); remaining > 0 {
		text += moreLinesHint("\n... (" + strconv.Itoa(remaining) + " more lines,")
	}
	if truncation := readTruncation(value.Details); truncation != nil && truncation.Truncated {
		maxBytes := truncation.MaxBytes
		if maxBytes == 0 {
			maxBytes = tools.DefaultMaxBytesUpstream
		}
		maxLines := truncation.MaxLines
		if maxLines == 0 {
			maxLines = tools.DefaultMaxLinesUpstream
		}
		var warning string
		switch {
		case truncation.FirstLineExceedsLimit:
			warning = "[First line exceeds " + tools.FormatSize(maxBytes) + " limit]"
		case truncation.TruncatedBy == "lines":
			warning = "[Truncated: showing " + strconv.Itoa(truncation.OutputLines) + " of " + strconv.Itoa(truncation.TotalLines) + " lines (" + strconv.Itoa(maxLines) + " line limit)]"
		default:
			warning = "[Truncated: " + strconv.Itoa(truncation.OutputLines) + " lines shown (" + tools.FormatSize(maxBytes) + " limit)]"
		}
		text += "\n" + themeFg(theme.Warning, warning)
	}
	return tui.NewPaddedText(text, 0, 0, nil)
}

func readTruncation(details any) *tools.TruncationResult {
	switch value := details.(type) {
	case *tools.ReadDetails:
		if value != nil {
			return value.Truncation
		}
		return nil
	case map[string]any:
		raw, ok := value["truncation"]
		if !ok || raw == nil {
			return nil
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var truncation tools.TruncationResult
		if json.Unmarshal(encoded, &truncation) != nil {
			return nil
		}
		return &truncation
	}
	return nil
}

// listRenderResult is upstream grep.ts, find.ts and ls.ts renderResult.
func listRenderResult(name string) extension.ToolRenderResultFunc {
	return func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, _ extension.ToolRenderContext) extension.Component {
		value := renderResultValue(result)
		body := makeListBodyRenderer(name, value.Content, value.Details)
		return linesComponent{render: func(width int) []string {
			lines := body(width, options.Expanded)
			if len(lines) == 0 {
				return nil
			}
			return append([]string{""}, lines...)
		}}
	}
}

// writeRenderCall is upstream write.ts renderCall: the path, then the content
// being written, highlighted by the file's language, ten lines while
// collapsed.
func writeRenderCall(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	theme := tui.ActiveTheme()
	text := tui.FormatWriteHeader(args, context.Cwd)
	rawPath, _ := stringArg(args, "file_path", "path")
	content, ok := stringArg(args, "content")
	switch {
	case !ok:
		text += "\n\n" + themeFg(theme.Error, "[invalid content arg - expected string]")
	case content != "":
		normalized := strings.ReplaceAll(content, "\r", "")
		lang := ""
		if rawPath != "" {
			lang = tui.LanguageFromPath(rawPath)
		}
		var rendered []string
		if lang != "" {
			rendered = tui.HighlightCode(replaceTabs(normalized), lang)
		} else {
			rendered = strings.Split(normalized, "\n")
		}
		lines := trimTrailingEmptyLines(rendered)
		display := lines
		if !context.Expanded && len(lines) > builtInPreviewLines {
			display = lines[:builtInPreviewLines]
		}
		styled := make([]string, len(display))
		for i, line := range display {
			if lang != "" {
				styled[i] = line
			} else {
				styled[i] = themeFg(theme.ToolOutput, replaceTabs(line))
			}
		}
		text += "\n\n" + strings.Join(styled, "\n")
		if remaining := len(lines) - len(display); remaining > 0 {
			text += moreLinesHint("\n... (" + strconv.Itoa(remaining) + " more lines, " + strconv.Itoa(len(lines)) + " total,")
		}
	}
	return tui.NewPaddedText(text, 0, 0, nil)
}

// writeRenderResult is upstream write.ts renderResult: the error text of a
// failed write, else nothing.
func writeRenderResult(result extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	output := renderResultValue(result).Content
	if !context.IsError || output == "" {
		return tui.NewPaddedText("", 0, 0, nil)
	}
	return tui.NewPaddedText("\n"+themeFg(tui.ActiveTheme().Error, output), 0, 0, nil)
}

// editPreviewInput is upstream getRenderablePreviewInput.
func editPreviewInput(args any) (string, []tools.EditReplacement, bool) {
	var input struct {
		Path     *string `json:"path"`
		FilePath *string `json:"file_path"`
		Edits    []struct {
			OldText *string `json:"oldText"`
			NewText *string `json:"newText"`
		} `json:"edits"`
		OldText *string `json:"oldText"`
		NewText *string `json:"newText"`
	}
	switch value := args.(type) {
	case json.RawMessage:
		if json.Unmarshal(value, &input) != nil {
			return "", nil, false
		}
	default:
		return "", nil, false
	}
	path := ""
	switch {
	case input.Path != nil:
		path = *input.Path
	case input.FilePath != nil:
		path = *input.FilePath
	}
	if path == "" {
		return "", nil, false
	}
	if len(input.Edits) > 0 {
		edits := make([]tools.EditReplacement, 0, len(input.Edits))
		for _, edit := range input.Edits {
			if edit.OldText == nil || edit.NewText == nil {
				edits = nil
				break
			}
			edits = append(edits, tools.EditReplacement{OldText: *edit.OldText, NewText: *edit.NewText})
		}
		if edits != nil {
			return path, edits, true
		}
	}
	if input.OldText != nil && input.NewText != nil {
		return path, []tools.EditReplacement{{OldText: *input.OldText, NewText: *input.NewText}}, true
	}
	return "", nil, false
}

func editArgsKey(path string, edits []tools.EditReplacement) string {
	type edit struct {
		OldText string `json:"oldText"`
		NewText string `json:"newText"`
	}
	list := make([]edit, len(edits))
	for i, e := range edits {
		list[i] = edit(e)
	}
	encoded, _ := json.Marshal(struct {
		Path  string `json:"path"`
		Edits []edit `json:"edits"`
	}{path, list})
	return string(encoded)
}

// editRenderCall is upstream edit.ts renderCall: a Box holding the path and,
// once the arguments are complete, the diff the edits would make, computed
// off the UI loop, in the success or error background. The box reads the
// card's state when it draws, so the result renderer's settled diff and
// error show in it as they do in upstream's shared call component.
func editRenderCall(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	state := builtInState(context)
	path, edits, ok := editPreviewInput(args)
	key := ""
	if ok {
		key = editArgsKey(path, edits)
	}
	state.mu.Lock()
	state.editCall = true
	if state.editArgsKey != key {
		state.editArgsKey = key
		state.editPreview = nil
		state.editPending = false
		state.editSettledE = false
	}
	if context.ArgsComplete && ok && state.editPreview == nil && !state.editPending {
		state.editPending = true
		cwd, invalidate := context.Cwd, context.Invalidate
		go func() {
			preview := tools.ComputeEditsDiff(path, edits, cwd)
			state.mu.Lock()
			current := state.editArgsKey == key
			if current {
				state.editPreview = &preview
				state.editPending = false
			}
			state.mu.Unlock()
			if current && invalidate != nil {
				invalidate()
			}
		}()
	}
	state.mu.Unlock()
	header := tui.FormatEditHeader(args, context.Cwd)
	return linesComponent{render: func(width int) []string {
		state.mu.Lock()
		preview, settledError := state.editPreview, state.editSettledE
		state.mu.Unlock()
		theme := tui.ActiveTheme()
		token := "toolPendingBg"
		switch {
		case preview != nil && preview.Error != "":
			token = "toolErrorBg"
		case preview != nil:
			token = "toolSuccessBg"
		case settledError:
			token = "toolErrorBg"
		}
		open := theme.Bg(token)
		box := tui.NewPaddedBox(1, 1, func(text string) string { return open + text + tui.SGRBgReset })
		box.AddChild(tui.NewPaddedText(header, 0, 0, nil))
		if preview != nil {
			box.AddChild(tui.NewSpacer(1))
			if preview.Error != "" {
				box.AddChild(tui.NewPaddedText(themeFg(theme.Error, preview.Error), 0, 0, nil))
			} else {
				diff := preview.Diff
				box.AddChild(linesComponent{render: func(width int) []string { return diffRows(diff, width) }})
			}
		}
		return box.Render(width)
	}}
}

// diffRows is upstream renderDiff drawn in a Text of the given width.
func diffRows(diff string, width int) []string {
	return strings.Split(strings.Join(renderDiffString(diff, width), "\n"), "\n")
}

// editResultDiff is the result's details.diff when it is a string.
func editResultDiff(details any) (string, bool) {
	switch value := details.(type) {
	case *tools.EditToolDetails:
		if value != nil {
			return value.Diff, true
		}
	case tools.EditToolDetails:
		return value.Diff, true
	case map[string]any:
		diff, ok := value["diff"].(string)
		return diff, ok
	}
	return "", false
}

// editRenderResult is upstream edit.ts renderResult: it settles the call
// box on the result's diff and error state, then shows the error text of a
// failed edit unless the preview already showed it, or the result's diff
// when the preview did not show it.
func editRenderResult(result extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	state := builtInState(context)
	value := renderResultValue(result)
	path, edits, ok := editPreviewInput(context.Args)
	key := ""
	if ok {
		key = editArgsKey(path, edits)
	}
	var preview *tools.EditsDiffPreview
	state.mu.Lock()
	if state.editCall {
		if !context.IsError {
			if diff, isString := editResultDiff(value.Details); isString {
				state.editPreview = &tools.EditsDiffPreview{Diff: diff}
				state.editArgsKey = key
				state.editPending = false
			}
		}
		state.editSettledE = context.IsError
		preview = state.editPreview
	}
	state.mu.Unlock()

	previewDiff, previewError := "", ""
	if preview != nil {
		previewDiff, previewError = preview.Diff, preview.Error
	}
	empty := linesComponent{render: func(int) []string { return nil }}
	if context.IsError {
		if value.Content == "" || value.Content == previewError {
			return empty
		}
		output := themeFg(tui.ActiveTheme().Error, value.Content)
		return linesComponent{render: func(width int) []string {
			return append([]string{""}, tui.NewPaddedText(output, 1, 0, nil).Render(width)...)
		}}
	}
	diff, _ := editResultDiff(value.Details)
	if diff == "" || diff == previewDiff {
		return empty
	}
	return linesComponent{render: func(width int) []string {
		rows := diffRows(diff, max(1, width-2))
		return append([]string{""}, tui.NewPaddedText(strings.Join(rows, "\n"), 1, 0, nil).Render(width)...)
	}}
}
