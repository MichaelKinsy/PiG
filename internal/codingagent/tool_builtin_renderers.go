package codingagent

// Ports packages/coding-agent/src/core/tools/renderers/index.ts
// withBuiltInRenderers with the renderCall and renderResult pairs of the
// built-in tools, so an extension's override of a built-in tool that defines
// one renderer draws the built-in renderer in the other half.

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"maps"
	"math"
	"reflect"
	"slices"
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

// computeEditsDiff computes the edit call's preview. Upstream's preview is a
// promise continuation, so it never lands before the synchronous render that
// follows renderCall; a test replaces it to hold the preview until that render.
var computeEditsDiff = tools.ComputeEditsDiff

// toolTimerNow is the card clock shellRenderCall and shellRenderResult read, as renderers/bash.ts reads Date.now(). The
// tool-card oracle tests fix it, as their Pi probe fixes Date.now(), so both sides time the same elapsed span.
var toolTimerNow = time.Now

// linesComponent renders the rows a function produces for a width.
type linesComponent struct{ render func(width int) []string }

func (c linesComponent) Render(width int) []string { return c.render(width) }
func (linesComponent) Invalidate()                 {}

// builtInRenderState is the part of a card's renderer state the built-in
// renderers keep. The shell ticker and the edit preview update it off the UI
// loop, so it has its own lock.
type builtInRenderState struct {
	mu       sync.Mutex
	lifetime context.Context
	closed   bool
	tasks    sync.WaitGroup
	// spawn, when set, starts a task the card does not join itself: an interactive session runs its cards' workers in its own
	// background group, which its end cancels and joins. Unset, a task runs in tasks, which the card's Dispose joins.
	spawn     func(task func())
	startedAt time.Time
	endedAt   time.Time
	stopTick  chan struct{}
	// shellView* is the shell result view the last result render built, reused while its inputs are unchanged.
	shellViewKey   shellViewKey
	shellViewValue shellResultView
	shellViewBuilt bool

	// writeHighlight is the write card's highlighted lines, extended as its content streams in (upstream WriteCallRenderComponent.cache).
	writeHighlight *writeHighlightCache
	// writePlain is the write card's lines for a path without a language, extended as its content streams in.
	writePlain writePlainLines
	// writeWarmed is the length of the content last handed to the whole-file highlight warm-up (warmWholeHighlight), writeWarmOff
	// that the file's language cannot resume or a warm-up failed, so no warm-up is worth its cost, writeWarmRunning that the
	// card's warm-up worker is alive, writeWarmNext and writeWarmLang the newest content waiting for it, and writeWarmFailure the panic of a warm-up that the next frame raises.
	// writeWarmHeld and writeWarmHeldLang are the text, and its language, that the last warm-up left the units for the next one of
	// (empty when it left none), and writeWarmDone that the call is complete: both end the card's hold on them (releaseWarmUnits). All are guarded by mu.
	writeWarmed       int
	writeWarmOff      bool
	writeWarmRunning  bool
	writeWarmDone     bool
	writeWarmNext     string
	writeWarmLang     string
	writeWarmHeld     string
	writeWarmHeldLang string
	writeWarmFailure  any
	// highlighter is the row's highlighting of a complete write call's file, or of a codemode call's script (upstream
	// WriteCallRenderComponent.highlighter and CodemodeRenderState.scriptHighlighter). A complete write call redraws on
	// every update and expand toggle with the same file, and a codemode call on every progress update and timer tick with
	// the same script, so the row keeps the last highlighting until the code, the theme or the loaded grammars change. It
	// holds the only copy of a complete call's displayed source.
	highlighter tui.CodeHighlighter

	// editArgs is the decoded edit arguments the renderers last read; editArgsFor is the arguments the preview belongs to.
	editArgs     *editRenderArgs
	editArgsFor  *editRenderArgs
	editPreview  tools.EditDiffOutcome
	editPending  bool
	editSettledE bool
	// editCall reports that the built-in edit renderCall drew this card, so
	// its box holds the preview (upstream state.callComponent).
	editCall bool
}

// goTask runs task on a goroutine the card's owner joins. state.mu is held, so a card that ended starts no more.
func (state *builtInRenderState) goTask(task func()) {
	if state.spawn != nil {
		state.spawn(task)
		return
	}
	state.tasks.Go(task)
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
		return lsRenderCall, listRenderResult("ls")
	}
	return nil, nil
}

func headerRenderCall(header func(json.RawMessage, string) string) extension.ToolRenderCallFunc {
	return func(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		return tui.NewPaddedText(header(args, context.Cwd), 0, 0, nil)
	}
}

// lsRenderCall is upstream ls.ts renderCall. Its path goes through renderToolPath, which throws for a path whose hyperlink cannot be named.
func lsRenderCall(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	rawPath, _ := stringArg(args, "path")
	panicOnUnlinkablePath(rawPath, context.Cwd)
	return tui.NewPaddedText(tui.FormatLsHeader(args, context.Cwd), 0, 0, nil)
}

// panicOnUnlinkablePath is renderToolPath's throw (render-utils.ts linkPath): the call renderer throws for a path whose file URL cannot be named, and
// tool-execution.ts updateDisplay draws the call fallback instead, as runToolRenderer does when it recovers this panic.
func panicOnUnlinkablePath(rawPath, cwd string) {
	if err := tui.ToolPathLinkError(rawPath, cwd); err != nil {
		panic(err)
	}
}

func renderResultValue(result any) agent.AgentToolResult {
	value, _ := toolResultOf(result)
	return value
}

// retainedToolResult is a finished tool result as a tool card keeps it, without the structured content its tool derives from its content
// alone (tools.RebuildStructuredContent): a read card would otherwise hold the file text twice, once as JSON, for as long as the
// transcript shows it. Pi's toReadOutput returns the text string itself, so its card holds it once. toolResultOf gives the result back
// whole.
type retainedToolResult struct {
	result agent.AgentToolResult
	tool   string
}

// retainToolResult is the value a tool card keeps for a finished result of the tool named tool: a retainedToolResult when its
// structured content is the one the tool derives from its content, else the result.
func retainToolResult(tool string, result agent.AgentToolResult) any {
	if len(result.StructuredContent) > 0 {
		if rebuilt, ok := tools.RebuildStructuredContent(tool, result.Content); ok && bytes.Equal(rebuilt, result.StructuredContent) {
			result.StructuredContent = nil
			return retainedToolResult{result: result, tool: tool}
		}
	}
	return result
}

// toolResultOf reads the tool result a tool card holds (a value or a pointer); ok is false when the card has none.
func toolResultOf(result any) (agent.AgentToolResult, bool) {
	switch value := result.(type) {
	case retainedToolResult:
		whole := value.result
		whole.StructuredContent, _ = tools.RebuildStructuredContent(value.tool, whole.Content)
		return whole, true
	case agent.AgentToolResult:
		return value, true
	case *agent.AgentToolResult:
		if value != nil {
			return *value, true
		}
	}
	return agent.AgentToolResult{}, false
}

// decodeRenderArgs reads the arguments a tool card holds once, so a renderer that needs several of them does not decode the whole object for each.
func decodeRenderArgs(args any) map[string]any {
	var fields map[string]any
	switch value := args.(type) {
	case json.RawMessage:
		_ = json.Unmarshal(value, &fields)
	case map[string]any:
		fields = value
	}
	return fields
}

// callFields is the decoded arguments a renderCall draws. A card whose arguments are still streaming hands a renderCall
// that reads them decoded (readsCallFields) the object its streaming parser holds, in context.Args, and no text.
func callFields(args json.RawMessage, context extension.ToolRenderContext) map[string]any {
	if args == nil {
		if fields, ok := context.Args.(map[string]any); ok {
			return fields
		}
	}
	return decodeRenderArgs(args)
}

// fieldsRenderCall is the code of the built-in renderCall that reads a streaming call's arguments decoded: the write card,
// whose content argument grows with every delta.
var fieldsRenderCall = reflect.ValueOf(extension.ToolRenderCallFunc(writeRenderCall)).Pointer()

// readsCallFields reports whether render reads a streaming call's arguments through callFields, so a card need not
// encode them as text for each frame.
func readsCallFields(render extension.ToolRenderCallFunc) bool {
	return render != nil && reflect.ValueOf(render).Pointer() == fieldsRenderCall
}

// pathRenderArgs is the part of decoded arguments that holds the file path a header names, as JSON for tui.FormatEditHeader, which reads no other argument.
func pathRenderArgs(fields map[string]any) json.RawMessage {
	paths := make(map[string]any, 2)
	for _, key := range []string{"file_path", "path"} {
		if value, present := fields[key]; present {
			paths[key] = value
		}
	}
	//portlint:allow mapkeyorder tui.FormatWriteHeader decodes this object and reads only file_path and path by name, so member order is unobservable
	raw, _ := json.Marshal(paths)
	return raw
}

func stringArg(args any, keys ...string) (string, bool) {
	return stringField(decodeRenderArgs(args), keys...)
}

// stringField is stringArg for arguments that are already decoded.
func stringField(fields map[string]any, keys ...string) (string, bool) {
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
		startToolTimer(context)
		return tui.NewPaddedText(tui.FormatShellHeader(args, prompt), 0, 0, nil)
	}
}

// startToolTimer starts the card's clock once execution starts. Call it from renderCall.
//
// Ports packages/coding-agent/src/core/tools/render-utils.ts (startToolTimer).
func startToolTimer(context extension.ToolRenderContext) {
	state := builtInState(context)
	state.mu.Lock()
	if context.ExecutionStarted && state.startedAt.IsZero() {
		state.startedAt = toolTimerNow()
		state.endedAt = time.Time{}
	}
	state.mu.Unlock()
}

// toolTimerText is the card's timing line, from renderResult: `Elapsed 4.2s` while the result is partial, redrawn every
// second, and `Took 4.2s` once it is final. It reports false when the card has no start time and no recorded duration.
//
// Ports packages/coding-agent/src/core/tools/render-utils.ts (toolTimerText).
func toolTimerText(context extension.ToolRenderContext, isPartial bool) (string, bool) {
	state := builtInState(context)
	state.mu.Lock()
	if !state.closed && !state.startedAt.IsZero() && isPartial && state.stopTick == nil {
		stop := make(chan struct{})
		state.stopTick = stop
		var cancelled <-chan struct{}
		if state.lifetime != nil {
			cancelled = state.lifetime.Done()
		}
		invalidate := context.Invalidate
		state.tasks.Go(func() {
			ticker := time.NewTicker(shellElapsedTickEvery)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-cancelled:
					return
				case <-ticker.C:
					if invalidate != nil {
						invalidate()
					}
				}
			}
		})
	}
	if !isPartial || context.IsError {
		if state.endedAt.IsZero() {
			state.endedAt = toolTimerNow()
		}
		if state.stopTick != nil {
			close(state.stopTick)
			state.stopTick = nil
		}
	}
	startedAt, endedAt := state.startedAt, state.endedAt
	state.mu.Unlock()

	// A final result's recorded duration wins: it is monotonic and survives reloads. The card's own clock is the fallback for
	// live progress and for results stored without one.
	if !isPartial && context.DurationMs != nil {
		return "Took " + tui.FormatToolDuration(time.Duration(*context.DurationMs)*time.Millisecond), true
	}
	if startedAt.IsZero() {
		return "", false
	}
	label := "Took"
	if isPartial {
		label = "Elapsed"
	}
	end := endedAt
	if end.IsZero() {
		end = toolTimerNow()
	}
	return label + " " + tui.FormatToolDuration(end.Sub(startedAt)), true
}

// shellRenderResult is upstream createShellRenderers renderResult: the output preview, warnings, and the timing footer.
func shellRenderResult(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	state := builtInState(context)
	timer, hasTimer := toolTimerText(context, options.IsPartial)
	footer := ""
	if hasTimer {
		footer = themeFg(tui.ActiveTheme().Muted, timer)
	}
	value := renderResultValue(result)
	view := state.shellView(toolResultTextOutput(value, context.ShowImages), shellDetailsFrom(value.Details), options.IsPartial, options.Expanded)
	var footerText *tui.Text
	if footer != "" {
		footerText = tui.NewPaddedText("\n"+footer, 0, 0, nil)
	}
	cachedWidth, cached := -1, []string(nil)
	return linesComponent{render: func(width int) []string {
		if width != cachedWidth {
			cached = view.render(width)
			if footerText != nil {
				cached = append(cached, footerText.Render(width)...)
			}
			cachedWidth = width
		}
		return cached
	}}
}

// shellViewKey is what a shell card's result view is built from: the output, the details fields it reads, the card state,
// and the theme and expand key hint its rows show.
type shellViewKey struct {
	content                        string
	truncated                      bool
	truncatedBy                    string
	totalLines, outputLines, bytes int
	fullOutputPath                 string
	isPartial, expanded            bool
	theme                          *tui.Theme
	hint                           string
}

// shellView is the view of a shell result. The elapsed ticker reruns the result renderer every second while the command
// runs; an update whose output, details, state, theme and key hint are unchanged reuses the view instead of reading the
// output again.
func (state *builtInRenderState) shellView(content string, details shellResultDetails, isPartial, expanded bool) shellResultView {
	key := shellViewKey{content: content, fullOutputPath: details.FullOutputPath, isPartial: isPartial, expanded: expanded, theme: tui.ActiveTheme(), hint: expandKeyHint()}
	if tr := details.Truncation; tr != nil {
		key.truncated, key.truncatedBy, key.totalLines, key.outputLines, key.bytes = tr.Truncated, tr.TruncatedBy, tr.TotalLines, tr.OutputLines, tr.MaxBytes
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.shellViewBuilt && state.shellViewKey == key {
		return state.shellViewValue
	}
	state.shellViewKey, state.shellViewValue, state.shellViewBuilt = key, newShellResultView(content, details, isPartial, expanded), true
	return state.shellViewValue
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
	rawPath, _ := stringArg(args, "file_path", "path")
	panicOnUnlinkablePath(rawPath, context.Cwd)
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
	output := toolResultTextOutput(value, context.ShowImages)
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
	if truncation := readTruncation(value.Details); truncation != nil && truncation.truncated {
		var warning string
		switch {
		case truncation.firstLineExceedsLimit:
			warning = "[First line exceeds " + tools.FormatSize(truncation.maxBytes) + " limit]"
		case truncation.truncatedBy == "lines":
			warning = "[Truncated: showing " + truncation.outputLines + " of " + truncation.totalLines + " lines (" + truncation.maxLines + " line limit)]"
		default:
			warning = "[Truncated: " + truncation.outputLines + " lines shown (" + tools.FormatSize(truncation.maxBytes) + " limit)]"
		}
		text += "\n" + themeFg(theme.Warning, warning)
	}
	return tui.NewPaddedText(text, 0, 0, nil)
}

// truncationView is details.truncation as read.ts formatReadResult reads it. The counts are the text `${value}` makes of the member, so one a
// result does not carry reads "undefined"; the limits are `value ?? default`, so a limit of 0 is kept.
type truncationView struct {
	truncated, firstLineExceedsLimit bool
	truncatedBy                      string
	outputLines, totalLines          string
	maxLines                         string
	maxBytes                         int
}

// readTruncation reads details.truncation of a read result: the tool's own details, or the JSON a stored result carries.
func readTruncation(details any) *truncationView {
	switch value := detailsObject(details).(type) {
	case *tools.ReadDetails:
		if value == nil || value.Truncation == nil {
			return nil
		}
		return typedTruncationView(value.Truncation)
	case map[string]any:
		raw, ok := detailsObject(value["truncation"]).(map[string]any)
		if !ok {
			return nil
		}
		view := &truncationView{
			truncated:             truthyValue(raw["truncated"]),
			firstLineExceedsLimit: truthyValue(raw["firstLineExceedsLimit"]),
			outputLines:           jsTemplateText(raw, "outputLines"),
			totalLines:            jsTemplateText(raw, "totalLines"),
			maxLines:              strconv.Itoa(tools.DefaultMaxLinesUpstream),
			maxBytes:              tools.DefaultMaxBytesUpstream,
		}
		view.truncatedBy, _ = raw["truncatedBy"].(string)
		if limit, ok := numberValue(raw["maxLines"]); ok {
			view.maxLines = tui.JSNumberString(limit)
		}
		if limit, ok := numberValue(raw["maxBytes"]); ok && limit >= math.MinInt && limit < -math.MinInt {
			//portlint:allow numbers the guard above admits only values within int range (NaN fails both comparisons), so the conversion is defined; maxBytes is the whole byte count the read tool wrote
			view.maxBytes = int(limit)
		}
		return view
	}
	return nil
}

// typedTruncationView is the view of a truncation the read tool produced: every member is set, and an unset limit is the default.
func typedTruncationView(truncation *tools.TruncationResult) *truncationView {
	view := &truncationView{
		truncated:             truncation.Truncated,
		firstLineExceedsLimit: truncation.FirstLineExceedsLimit,
		truncatedBy:           truncation.TruncatedBy,
		outputLines:           strconv.Itoa(truncation.OutputLines),
		totalLines:            strconv.Itoa(truncation.TotalLines),
		maxLines:              strconv.Itoa(truncation.MaxLines),
		maxBytes:              truncation.MaxBytes,
	}
	if truncation.MaxLines == 0 {
		view.maxLines = strconv.Itoa(tools.DefaultMaxLinesUpstream)
	}
	if truncation.MaxBytes == 0 {
		view.maxBytes = tools.DefaultMaxBytesUpstream
	}
	return view
}

// truthyValue is JavaScript's truthiness of a decoded JSON value.
func truthyValue(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	}
	if number, ok := numberValue(value); ok {
		return number != 0 && !math.IsNaN(number)
	}
	return true
}

// numberValue is a decoded JSON number or a Go integer a tool stored in its details, as the float64 JavaScript holds.
func numberValue(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		number, err := value.Float64()
		return number, err == nil
	}
	return 0, false
}

// jsTemplateText is `${object[key]}`: "undefined" for a member the object lacks.
func jsTemplateText(object map[string]any, key string) string {
	value, present := object[key]
	if !present {
		return "undefined"
	}
	if number, ok := numberValue(value); ok {
		return tui.JSNumberString(number)
	}
	return tui.JSTemplateString(value)
}

// listRenderResult is upstream grep.ts, find.ts and ls.ts renderResult.
func listRenderResult(name string) extension.ToolRenderResultFunc {
	return func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		value := renderResultValue(result)
		body := makeListBodyRenderer(name, toolResultTextOutput(value, context.ShowImages), value.Details)
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
// collapsed. A frame reads the lines it draws and the line count: while the
// content streams in, the card state extends both from the previous frame, and
// a complete call reuses the row's highlighting of its whole file.
func writeRenderCall(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	theme := tui.ActiveTheme()
	fields := callFields(args, context)
	rawPath, _ := stringField(fields, "file_path", "path")
	panicOnUnlinkablePath(rawPath, context.Cwd)
	text := tui.FormatWriteHeaderArgs(writeHeaderFields(fields), context.Cwd)
	content, ok := stringField(fields, "content")
	lang := ""
	if rawPath != "" {
		lang = tui.LanguageFromPath(rawPath)
	}
	state := builtInState(context)
	state.mu.Lock()
	defer state.mu.Unlock()
	// A call whose content still streams in extends the cache of its highlighted lines (upstream renderCall:
	// fileContent !== null && !context.argsComplete ? updateWriteHighlightCacheIncremental : undefined).
	if ok && !context.ArgsComplete {
		state.writeHighlight = updateWriteHighlightCacheIncremental(state.writeHighlight, rawPath, content)
		if lang != "" {
			state.warmWholeHighlight(content, lang)
		}
	} else {
		state.writeHighlight = nil
		// A complete call draws its file from the highlighter once: a warm-up that has not started is dropped.
		state.writeWarmNext = ""
		state.writeWarmDone = state.writeWarmDone || context.ArgsComplete
	}
	cache := state.writeHighlight
	var plain writePlainLines
	if ok && lang == "" {
		plain = state.writePlain
		plain.update(content)
	}
	state.writePlain = plain
	if context.ArgsComplete {
		// Nothing read from a complete call's content outlives the call but the highlighter's result.
		state.writePlain = writePlainLines{}
	}
	switch {
	case !ok:
		text += "\n\n" + themeFg(theme.Error, "[invalid content arg - expected string]")
	case content != "":
		// A path with a language draws the cache's highlighted lines while the content streams in and the highlighter's
		// lines of the whole file once it is complete, any other path the content's lines (upstream formatWriteCall:
		// lang ? cache?.highlightedLines ?? highlighter.highlight(...) : normalizeDisplayText(fileContent).split("\n")).
		var complete []string
		if cache == nil && lang != "" {
			complete = trimTrailingEmptyLines(state.highlighter.Highlight(normalizeWriteText(content), lang))
		}
		var total int
		switch {
		case cache != nil:
			total = cache.lineCount()
		case complete != nil:
			total = len(complete)
		default:
			total = plain.count()
		}
		shown := total
		if !context.Expanded && shown > builtInPreviewLines {
			shown = builtInPreviewLines
		}
		styled := make([]string, 0, shown)
		switch {
		case cache != nil:
			for i := range shown {
				styled = append(styled, cache.line(i))
			}
		case complete != nil:
			styled = append(styled, complete[:shown]...)
		default:
			for line := range plain.lines(shown) {
				styled = append(styled, themeFg(theme.ToolOutput, normalizeWriteText(line)))
			}
		}
		text += "\n\n" + strings.Join(styled, "\n")
		if remaining := total - shown; remaining > 0 {
			text += moreLinesHint("\n... (" + strconv.Itoa(remaining) + " more lines, " + strconv.Itoa(total) + " total,")
		}
	}
	// The frame that completed the call drew the highlighter's lines of the whole file from the state the last warm-up left, and took
	// its units; a warm-up still running at that draw releases its own when it ends (runWriteWarmUps).
	state.releaseWarmUnitsOfAFinishedCall()
	// The row's Text is reused, and keeps its wrapped lines when a redraw sets the text it showed (upstream
	// WriteCallRenderComponent extends Text).
	component := reusableText(context.LastComponent)
	component.SetText(text)
	return component
}

// reusableText is the card's reusable Text: the last component of the renderer, or a new empty one.
func reusableText(last extension.Component) *tui.Text {
	if text, ok := last.(*tui.Text); ok && text != nil {
		return text
	}
	return tui.NewPaddedText("", 0, 0, nil)
}

// writeHeaderFields is the arguments the write header reads its path from. A path holding a lone surrogate reads U+FFFD
// in it (normalizeWriteText), so streamed arguments name the path as their decoded JSON text does.
func writeHeaderFields(fields map[string]any) map[string]any {
	var header map[string]any
	for _, key := range [...]string{"file_path", "path"} {
		path, ok := fields[key].(string)
		if !ok {
			continue
		}
		if replaced := replaceLoneSurrogates(path); replaced != path {
			if header == nil {
				header = maps.Clone(fields)
			}
			header[key] = replaced
		}
	}
	if header == nil {
		return fields
	}
	return header
}

// writePlainLines is the line structure formatWriteCall reads from the content of a write call whose path has no
// language, kept while the content streams in: each update counts the newlines it adds.
type writePlainLines struct {
	content  string
	newlines int
}

func (plain *writePlainLines) update(content string) {
	if strings.HasPrefix(content, plain.content) {
		plain.newlines += strings.Count(content[len(plain.content):], "\n")
	} else {
		plain.newlines = strings.Count(content, "\n")
	}
	plain.content = content
}

// count is len(trimTrailingEmptyLines(normalizeDisplayText(content).split("\n"))): a line is empty once its carriage
// returns are removed, so the empty lines at the end are the newlines after the last other byte.
func (plain *writePlainLines) count() int {
	content := plain.content
	end := len(content)
	for end > 0 && (content[end-1] == '\n' || content[end-1] == '\r') {
		end--
	}
	if end == 0 {
		return 0
	}
	return plain.newlines + 1 - strings.Count(content[end:], "\n")
}

// lines yields the first n lines of the content, carriage returns included.
func (plain *writePlainLines) lines(n int) iter.Seq[string] {
	return func(yield func(string) bool) {
		rest := plain.content
		for range n {
			line, after, _ := strings.Cut(rest, "\n")
			if !yield(line) {
				return
			}
			rest = after
		}
	}
}

// writeRenderResult is upstream write.ts renderResult: the error text of a
// failed write, else nothing.
func writeRenderResult(result extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	output := renderResultValue(result).Text()
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

// editRenderArgs is edit arguments as the edit renderers read them: upstream getRenderablePreviewInput, and the path
// members the header shows. One value stands for every argument text that decodes to the same path and edits.
type editRenderArgs struct {
	raw    []byte
	path   string
	edits  []tools.EditReplacement
	ok     bool
	header json.RawMessage
}

// editRenderInput is the decoded form of args. It decodes only when the argument bytes differ from the last call's: the
// renderers run on every update of the card, and decoding the edits each time cost a pass over all of them. The caller
// holds state.mu.
func (state *builtInRenderState) editRenderInput(args any) *editRenderArgs {
	raw, _ := args.(json.RawMessage)
	if last := state.editArgs; last != nil && last.raw != nil && bytes.Equal(last.raw, raw) {
		return last
	}
	path, edits, ok := editPreviewInput(args)
	header := pathRenderArgs(decodeRenderArgs(args))
	if last := state.editArgs; last != nil && last.ok == ok && last.path == path && slices.Equal(last.edits, edits) && bytes.Equal(last.header, header) {
		last.raw = slices.Clone(raw)
		return last
	}
	state.editArgs = &editRenderArgs{raw: slices.Clone(raw), path: path, edits: edits, ok: ok, header: header}
	return state.editArgs
}

// editRenderCall is upstream edit.ts renderCall: a Box holding the path and,
// once the arguments are complete, the diff the edits would make, computed
// off the UI loop, in the success or error background. The box reads the
// card's state when it draws, so the result renderer's settled diff and
// error show in it as they do in upstream's shared call component.
func editRenderCall(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
	state := builtInState(context)
	state.mu.Lock()
	input := state.editRenderInput(args)
	path, edits, ok := input.path, input.edits, input.ok
	state.editCall = true
	if state.editArgsFor != input {
		state.editArgsFor = input
		state.editPreview = nil
		state.editPending = false
		state.editSettledE = false
	}
	if !state.closed && context.ArgsComplete && ok && state.editPreview == nil && !state.editPending {
		state.editPending = true
		cwd, invalidate := context.Cwd, context.Invalidate
		state.tasks.Go(func() {
			state.mu.Lock()
			cancelled := state.closed || (state.lifetime != nil && state.lifetime.Err() != nil)
			state.mu.Unlock()
			if cancelled {
				return
			}
			preview := computeEditsDiff(path, edits, cwd)
			state.mu.Lock()
			current := !state.closed && (state.lifetime == nil || state.lifetime.Err() == nil) && state.editArgsFor == input
			if current {
				state.editPreview = preview
				state.editPending = false
			}
			state.mu.Unlock()
			if current && invalidate != nil {
				invalidate()
			}
		})
	}
	headerArgs := input.header
	state.mu.Unlock()
	editPath, _ := stringArg(headerArgs, "file_path", "path")
	panicOnUnlinkablePath(editPath, context.Cwd)
	header := tui.FormatEditHeader(headerArgs, context.Cwd)
	// The box is built once per preview, settled state and theme, and its rows are kept per width: the card draws on
	// every frame it is dirty, and drawing the diff again each time cost a pass over all of it.
	type boxKey struct {
		preview      tools.EditDiffOutcome
		settledError bool
		theme        *tui.Theme
	}
	var built boxKey
	var box *tui.Box
	cachedWidth, cached := -1, []string(nil)
	return linesComponent{render: func(width int) []string {
		state.mu.Lock()
		key := boxKey{preview: state.editPreview, settledError: state.editSettledE, theme: tui.ActiveTheme()}
		state.mu.Unlock()
		if box != nil && key == built && width == cachedWidth {
			return cached
		}
		if box == nil || key != built {
			box, built = newEditCallBox(header, key.preview, key.settledError, key.theme, context.OutputPad), key
		}
		cached, cachedWidth = box.Render(width), width
		return cached
	}}
}

// newEditCallBox is upstream edit.ts's call component: the header and, once it landed, the preview diff or its error, in
// the pending, success or error background.
func newEditCallBox(header string, preview tools.EditDiffOutcome, settledError bool, theme *tui.Theme, outputPad int) *tui.Box {
	token := "toolPendingBg"
	_, previewFailed := preview.(tools.EditDiffError)
	switch {
	case previewFailed:
		token = "toolErrorBg"
	case preview != nil:
		token = "toolSuccessBg"
	case settledError:
		token = "toolErrorBg"
	}
	open := theme.GetBgAnsi(token)
	box := tui.NewPaddedBox(outputPad, 1, func(text string) string { return open + text + tui.SGRBgReset })
	box.AddChild(tui.NewPaddedText(header, 0, 0, nil))
	if preview != nil {
		box.AddChild(tui.NewSpacer(1))
		switch outcome := preview.(type) {
		case tools.EditDiffError:
			box.AddChild(tui.NewPaddedText(themeFg(theme.Error, outcome.Error), 0, 0, nil))
		case tools.EditDiffResult:
			diff := outcome.Diff
			cachedWidth, cached := -1, []string(nil)
			box.AddChild(linesComponent{render: func(width int) []string {
				if width != cachedWidth {
					cached, cachedWidth = diffRows(diff, width), width
				}
				return cached
			}})
		}
	}
	return box
}

// diffRows is upstream renderDiff drawn in a Text of the given width.
func diffRows(diff string, width int) []string {
	return strings.Split(strings.Join(renderDiffString(diff, width), "\n"), "\n")
}

// editResultDiff is the result's details.diff when it is a string.
func editResultDiff(details any) (string, bool) {
	switch value := detailsObject(details).(type) {
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
	var preview tools.EditDiffOutcome
	state.mu.Lock()
	if state.editCall {
		if !context.IsError {
			if diff, isString := editResultDiff(value.Details); isString {
				state.editPreview = tools.EditDiffResult{Diff: diff}
				state.editArgsFor = state.editRenderInput(context.Args)
				state.editPending = false
			}
		}
		state.editSettledE = context.IsError
		preview = state.editPreview
	}
	state.mu.Unlock()

	previewDiff, previewError := "", ""
	switch outcome := preview.(type) {
	case tools.EditDiffResult:
		previewDiff = outcome.Diff
	case tools.EditDiffError:
		previewError = outcome.Error
	}
	empty := linesComponent{render: func(int) []string { return nil }}
	if context.IsError {
		if value.Text() == "" || value.Text() == previewError {
			return empty
		}
		output := themeFg(tui.ActiveTheme().Error, value.Text())
		return linesComponent{render: func(width int) []string {
			return append([]string{""}, tui.NewPaddedText(output, context.OutputPad, 0, nil).Render(width)...)
		}}
	}
	diff, _ := editResultDiff(value.Details)
	if diff == "" || diff == previewDiff {
		return empty
	}
	return linesComponent{render: func(width int) []string {
		rows := diffRows(diff, max(1, width-2*context.OutputPad))
		return append([]string{""}, tui.NewPaddedText(strings.Join(rows, "\n"), context.OutputPad, 0, nil).Render(width)...)
	}}
}
