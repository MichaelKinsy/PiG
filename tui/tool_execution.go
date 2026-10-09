package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// ToolExecutionState is the lifecycle stage of a tool call display.
type ToolExecutionState int

const (
	ToolStateRunning ToolExecutionState = iota
	ToolStateDone
	ToolStateError
)

// ToolExecutionComponent renders one tool call in the chat transcript.
//
// Visual model (mirrors upstream per-tool renderCall functions):
//
//	$ expr 20 + 22             ← bash: bold "$ <command>"
//	read README.md             ← read: bold "read <path>"
//	write out.txt              ← write: bold "write <path>"
//	edit main.go               ← edit: bold "edit <path>"
//	grep /pattern/ in .           ← grep: bold "grep /<pat>/ in <path>"
//	find *.go in .             ← find: bold "find <pat> in <path>"
//	ls src/                    ← ls: bold "ls <path>"
//
// No lifecycle markers (✓/▶/✗): upstream conveys state only via
// background color (pending, success, error). No "(N lines, Xs)"
// annotation in the header: upstream shows duration in body footer.
//
// State transitions:
//   - SetRunning: state → Running, output cleared
//   - SetResult:  state → Done or Error, body filled
//
// Output thresholds:
//   - autoCollapseLines: outputs longer than this start collapsed
//     (overridden to expanded for errors)
//   - bodyMaxLines: lines shown in the expanded view; overflow shows a
//     "\u2026 (N more lines)" footer line.
type ToolExecutionComponent struct {
	Container

	Name string
	// ToolCallID identifies the tool call the card shows (tool-execution.ts toolCallId); renderers receive it in their context.
	ToolCallID string
	// Label is ignored: the fallback call header names the tool by Name, as upstream createCallFallback does.
	//
	// Deprecated: a registered tool's card draws its definition; Label has no effect.
	Label string
	// ArgsPreview is a short human-readable rendering of the tool args.
	// Build it with FormatToolArgs() before assigning, or use SetRunning().
	ArgsPreview string

	// Cwd is the session working directory, used to resolve relative tool
	// paths to absolute file:// URLs for OSC-8 hyperlinks in the header.
	Cwd string

	State  ToolExecutionState
	Output string

	// Collapsed controls the renderer's expanded state. New tool cards start
	// collapsed to match upstream; final results may apply tool-specific rules.
	Collapsed bool

	// Elapsed is shown on done/error states when > 0.
	Elapsed time.Duration

	// DurationMs is the recorded execution time of the final result in milliseconds, which survives a session reload; nil when the
	// result carries none. A final result's recorded duration wins over the card's own clock.
	// upstream: tool-execution.ts result.durationMs
	DurationMs *int64

	// StartedAt records when the tool began executing. Used to render a live
	// "Elapsed X.Xs" footer while a shell tool runs (mirrors upstream bash.ts
	// renderResult, which ticks the elapsed every second during execution).
	StartedAt time.Time

	// Configurable thresholds. Zero values fall back to defaults.
	AutoCollapseLines int // default 8
	BodyMaxLines      int // default 40

	// BodyRenderer, when non-nil, replaces the default plain-text body
	// rendering. Used for per-tool rich displays: unified diff for
	// edit, line-numbered output for read, etc. The function receives
	// the available width and the current expanded state so it can show
	// a truncated preview or full output depending on Ctrl+O toggle.
	//
	// When set, the line-count annotation in the header is computed
	// from the renderer's row count instead of the raw Output text so
	// `(N lines, 1.2s)` accurately reflects what the user can see.
	BodyRenderer func(width int, expanded bool) []string

	// ImageBlocks holds image content blocks from tool results.
	// When non-empty and ShowImages is true, they render after the body.
	// Mirrors upstream tool-execution.ts imageComponents/imageSpacers.
	ImageBlocks []ImageBlock

	// ShowImages controls whether ImageBlocks are rendered.
	// Mirrors upstream ToolExecutionOptions.showImages.
	ShowImages bool

	// ImageWidthCells caps the width of rendered images in columns.
	// Mirrors upstream ToolExecutionOptions.imageWidthCells (default 60).
	ImageWidthCells int

	// imageComponents are the Image components of the current result, one per displayed image block. imageSources are their
	// inputs, so a re-render reuses an Image (and its converted PNG data and Kitty image ID) while its source is unchanged.
	// Mirrors tool-execution.ts imageComponents/imageSources.
	imageComponents []*Image
	imageSources    []toolImageSource

	// frontendImages are the images a frontend session draws natively,
	// decoded from frontendImageSources at frontendImageWidth, kept while
	// the blocks and the width stay (frontendImagesAt).
	frontendImages       []frontend.ViewImage
	frontendImageSources []ImageBlock
	frontendImageWidth   int

	// userToggled is true once the user has explicitly hit the toggle
	// key. After that we never re-apply auto-collapse, so a user who
	// expanded a long output doesn't lose it when SetResult re-fires.
	userToggled bool

	// IsPartial mirrors upstream tool-execution.ts isPartial. true while
	// the tool is still being streamed/executed, false after final result.
	// Controls the pending bg tint before execution completes.
	IsPartial bool

	// executionStarted mirrors upstream tool-execution.ts executionStarted.
	// Set when ToolExecutionStartEvent fires (tool begins executing).
	executionStarted bool

	// argsComplete mirrors upstream tool-execution.ts argsComplete.
	// Set when the message stream ends and args JSON is finalized.
	argsComplete bool

	// definition, when set, draws a registered tool definition's renderers
	// as upstream does. definitionDirty reruns them on the next render, as
	// every upstream state change reruns updateDisplay; a renderer may
	// invalidate the card from any goroutine.
	definition                *ToolDefinitionRenderers
	definitionArgs            json.RawMessage
	definitionResult          any
	definitionDirty           atomic.Bool
	definitionCall            Component
	definitionResultComponent Component
	mouseChild                Component
	mouseWidth                int
	mouseHeight               int
	mouseDirty                atomic.Bool
	contentBox                *Box
	selfRenderContainer       *Container
	legacyCard                *toolCardBox
	outputPad                 int // horizontal padding of the card (tool-execution.ts outputPad)
	ui                        TUI // asked for a render after an asynchronous change (tool-execution.ts ui)

	// compactHeader is the collapsed read card's upstream compact label
	// (FormatCompactReadHeader), or "" for the full header.
	compactHeader string
	// argumentsRaw is the call's argument JSON. arguments decodes it for a
	// frontend session on first use after it changes.
	argumentsRaw     json.RawMessage
	arguments        map[string]any
	argumentsDecoded bool
	// aborted records that the call ended because the user aborted its turn.
	// Only a frontend session reads it; the card draws an abort as an error.
	aborted bool
}

// ImageBlock describes one image from a tool result for rendering.
type ImageBlock struct {
	Data     string // base64-encoded image data
	MIMEType string
}

// ConvertedImage is a base64 image and its MIME type, the result of upstream
// image-convert.ts convertToPng.
type ConvertedImage struct {
	Data     string
	MimeType string
}

// toolImageSource is the input of one Image component (tool-execution.ts imageSources).
type toolImageSource struct {
	data, mimeType string
	widthCells     int
}

// ToolExecutionOptions are the optional constructor settings of a tool card (tool-execution.ts:25). A nil field is
// upstream's undefined: images show (`showImages ?? true`) and render 60 cells wide (`imageWidthCells ?? 60`).
type ToolExecutionOptions struct {
	ShowImages      *bool
	ImageWidthCells *int
	// OutputPad is options.outputPad, the horizontal padding of the card; nil keeps Pi's default of 1.
	OutputPad *int
}

// ToolDefinitionSource is upstream's `ToolRenderers | ToolDefinition` (tool-execution.ts:66): anything that carries the renderers a tool card draws with, the bare renderers or a full tool definition. A nil source is upstream's undefined.
type ToolDefinitionSource interface {
	ToolRenderers() *ToolDefinitionRenderers
}

// ToolRenderers makes the renderers themselves a ToolDefinitionSource (upstream's bare ToolRenderers member). A nil receiver is the undefined source.
func (r *ToolDefinitionRenderers) ToolRenderers() *ToolDefinitionRenderers { return r }

// NewToolExecutionComponent returns a Running-state component for a tool call
// (tool-execution.ts constructor(toolName, toolCallId, args, options = {}, toolDefinition, ui, cwd)). args are the call's current
// arguments: the card draws its header and, with a definition, its renderers from them. definition is the tool's registered renderers,
// nil for the built-in or generic presentation: a [ToolDefinitionSource], upstream's `ToolRenderers | ToolDefinition | undefined` (tool-execution.ts:66). ui is the TUI the card asks for a render when an asynchronous change, such as the image
// transcoder registering, needs one; nil asks for none. cwd resolves relative tool paths in the header.
func NewToolExecutionComponent(toolName, toolCallID string, args json.RawMessage, options ToolExecutionOptions, definition ToolDefinitionSource, ui TUI, cwd string) *ToolExecutionComponent {
	c := &ToolExecutionComponent{
		Name:            toolName,
		ToolCallID:      toolCallID,
		Cwd:             cwd,
		ui:              ui,
		State:           ToolStateRunning,
		Collapsed:       true,
		ShowImages:      true,
		ImageWidthCells: 60,
		IsPartial:       true,
		outputPad:       1,
	}
	if options.ShowImages != nil {
		c.ShowImages = *options.ShowImages
	}
	if options.ImageWidthCells != nil {
		c.ImageWidthCells = *options.ImageWidthCells
	}
	if options.OutputPad != nil {
		c.outputPad = *options.OutputPad
	}
	c.ArgsPreview = HeaderForTool(toolName, args, cwd)
	if len(args) > 0 {
		c.SetHeaderArgs(args)
	}
	if definition != nil {
		if renderers := definition.ToolRenderers(); renderers != nil {
			c.SetDefinition(renderers, args)
		}
	}
	c.addChildren()
	return c
}

// addChildren adds Pi's constructor children (tool-execution.ts:93-104): a spacer, the content box, then the image spacers and images. The card and the images are private children that render from the component's current state, so a parent Container never serves a stale frame.
func (c *ToolExecutionComponent) addChildren() {
	c.contentBox = NewPaddedBox(c.outputPad, 1, func(text string) string { return c.definitionBg()(text) })
	c.selfRenderContainer = NewContainer()
	c.legacyCard = &toolCardBox{children: []Component{toolSection(c.renderLegacyHeader), toolSection(c.renderLegacyBody)}, open: c.bgOpenSGR, pad: &c.outputPad}
	c.Add(NewSpacer(1))
	c.Add(toolSection(c.renderCard))
	c.Add(toolSection(c.renderToolImages))
}

// toolSection renders one section from the card's current state on every frame.
type toolSection func(width int) []string

func (f toolSection) Render(width int) []string { return f(width) }
func (toolSection) Invalidate()                 {}

// toolCardBox is the content box of a tool without a registered definition: padding one cell on every side, each row painted in the lifecycle background with paintBgWith, which keeps the tint after a body renderer's full SGR reset.
type toolCardBox struct {
	children []Component
	open     func() string
	pad      *int // horizontal padding, the component's outputPad
}

func (b *toolCardBox) Invalidate() {}

func (b *toolCardBox) Render(width int) []string {
	open := b.open()
	pad := *b.pad
	out := []string{paintBgWith(open, "", width)}
	for _, child := range b.children {
		for _, line := range child.Render(max(width-2*pad, 1)) {
			out = append(out, paintBgWith(open, strings.Repeat(" ", pad)+line, width))
		}
	}
	return append(out, paintBgWith(open, "", width))
}

// IsDirty reports whether the component needs re-rendering. While a shell
// tool runs, the live "Elapsed X.Xs" footer recomputes time.Since(StartedAt)
// on every frame driven by the 100ms tick loop, but the tick does not
// Invalidate this component. Reporting dirty while that footer is live keeps
// the per-child render cache from freezing the elapsed counter.
func (c *ToolExecutionComponent) IsDirty() bool {
	if c.invalidatable.IsDirty() {
		return true
	}
	if c.definition != nil {
		return c.definitionDirty.Load() || c.definitionComponentsDirty()
	}
	return c.State == ToolStateRunning && IsShellTool(c.Name) && !c.StartedAt.IsZero()
}

// Invalidate marks the card for redraw. A card with a definition also reruns
// its renderers, as upstream ToolExecutionComponent.invalidate calls
// updateDisplay.
func (c *ToolExecutionComponent) Invalidate() {
	c.mouseDirty.Store(true)
	c.definitionDirty.Store(true)
	for _, image := range c.imageComponents {
		image.Invalidate()
	}
	c.invalidatable.Invalidate()
}

// SetRunning marks the component as in-flight with the given pre-formatted
// args preview. Idempotent.
func (c *ToolExecutionComponent) SetRunning(argsPreview string) {
	c.State = ToolStateRunning
	c.ArgsPreview = argsPreview
	c.Output = ""
	c.Elapsed = 0
	c.Invalidate()
}

// SetStreaming updates the live output body during execution without changing
// expansion state. Only SetExpanded or Toggle changes that state while running.
func (c *ToolExecutionComponent) SetStreaming(snapshot string) {
	if c.State != ToolStateRunning {
		return
	}
	c.Output = snapshot
	c.Invalidate()
}

// UpdateArgs updates the displayed header from partial/complete args.
// Called progressively during streaming as ToolCallDelta events arrive.
// Mirrors upstream tool-execution.ts updateArgs(args): the tool name is fixed when the card is constructed.
func (c *ToolExecutionComponent) UpdateArgs(args json.RawMessage) {
	// Try to parse the partial JSON to get a header. Partial JSON will
	// fail to parse: that's OK, we fall back to the tool name.
	var raw json.RawMessage
	if json.Unmarshal(args, &raw) == nil {
		if c.definition != nil {
			c.definitionArgs = append(c.definitionArgs[:0], raw...)
		}
		c.ArgsPreview = HeaderForTool(c.Name, raw, c.Cwd)
		c.SetHeaderArgs(raw)
	}
	c.Invalidate()
}

// MarkExecutionStarted records that the tool has begun executing.
// Mirrors upstream tool-execution.ts markExecutionStarted.
func (c *ToolExecutionComponent) MarkExecutionStarted() {
	c.executionStarted = true
	if c.StartedAt.IsZero() {
		c.StartedAt = time.Now()
	}
	c.Invalidate()
}

// SetArgsComplete records that the args JSON is finalized.
// Mirrors upstream tool-execution.ts setArgsComplete.
func (c *ToolExecutionComponent) SetArgsComplete() {
	c.argsComplete = true
	c.Invalidate()
}

// SetResult finalises the component with output text and an error flag,
// applying the auto-collapse rule unless the user has already toggled.
func (c *ToolExecutionComponent) SetResult(output string, isError bool, elapsed time.Duration) {
	c.IsPartial = false
	if isError {
		c.State = ToolStateError
	} else {
		c.State = ToolStateDone
		// pig additive (D91): a successful result supersedes an earlier abort.
		c.aborted = false
	}
	c.Output = output
	c.Elapsed = elapsed
	// A card with a definition keeps its expansion: upstream updateResult
	// never changes it.
	if !c.userToggled && c.definition == nil {
		switch {
		case isError:
			// Errors are always auto-expanded: the LLM (and the user) need
			// to see what went wrong without an extra keystroke.
			c.Collapsed = false
		case c.BodyRenderer != nil:
			// Tools with BodyRenderer handle their own preview/expanded toggle.
			c.Collapsed = true
		case HasBuiltInToolRenderers(c.Name):
			// Built-ins receive their renderer before final delivery in production.
			// Keep the line-count fallback for direct/component-only callers.
			c.Collapsed = c.lineCount() > c.autoCollapseThreshold()
		default:
			// Upstream formatToolExecution shows the complete output when no tool
			// definition exists.
			c.Collapsed = false
		}
	}
	c.Invalidate()
}

// ToolResultContent is one block of a tool result's content: tool-execution.ts updateResult's `{ type, text?, data?, mimeType? }`.
type ToolResultContent struct {
	Type     string // "text" or "image"
	Text     string
	Data     string
	MimeType string
}

// ToolResultUpdate is the result argument of UpdateResult: tool-execution.ts updateResult's
// `{ content, details?, isError, durationMs? }`, plus the two Go-side carriers the card needs.
type ToolResultUpdate struct {
	Content []ToolResultContent
	// Details is the tool's structured details. A card with a registered definition reads them through Result.
	Details any
	IsError bool
	// DurationMs is the recorded execution time of a final result; nil for a partial result and for one stored without a duration.
	DurationMs *int64
	// Result is the structured result handed to the registered definition's result renderer (agent.AgentToolResult in production).
	// Nil leaves the renderer's result unchanged. Pi passes `{ content, details }` itself; the card has no agent types.
	Result any
	// Elapsed is the final result's wall time for the done/error footer; zero shows none (Pi's card keeps no such footer).
	Elapsed time.Duration
}

// UpdateResult is tool-execution.ts updateResult(result, isPartial = false): it records the result, converts its image blocks, and
// redraws. A partial result streams its text into the running card; a final one settles the card with its error state and duration.
func (c *ToolExecutionComponent) UpdateResult(result ToolResultUpdate, isPartial ...bool) {
	partial := len(isPartial) > 0 && isPartial[0]
	var text []string
	var images []ImageBlock
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			text = append(text, block.Text)
		case "image":
			images = append(images, ImageBlock{Data: block.Data, MIMEType: block.MimeType})
		}
	}
	output := strings.Join(text, "\n")
	if result.Result != nil {
		c.SetResultValue(result.Result)
	}
	c.ImageBlocks = images
	if partial {
		c.SetStreaming(output)
		return
	}
	c.DurationMs = result.DurationMs
	c.SetResult(output, result.IsError, result.Elapsed)
}

// FinalizeAborted freezes a still-running tool when its turn is aborted
// mid-execution. It transitions out of Running so the live "Elapsed X.Xs"
// footer stops recomputing time.Since(StartedAt) on every subsequent render
// (which otherwise forced a repaint on every keystroke and agent chunk,
// breaking terminal scrollback) while keeping any partial streamed output.
// No-op if the tool already reached a terminal state.
func (c *ToolExecutionComponent) FinalizeAborted(elapsed time.Duration) {
	if c.State != ToolStateRunning {
		return
	}
	out := c.Output
	if strings.TrimSpace(out) == "" {
		out = "Operation aborted"
	}
	c.SetResult(out, true, elapsed)
	c.aborted = true // pig additive (D91): reported as cancelled
}

// pig additive (D91): MarkAborted records that the call ended because the
// user aborted its turn, so a frontend session reports it as cancelled. It
// changes nothing the card draws. A later error result keeps the mark, since
// a tool reports the abort as its error; a successful result clears it.
func (c *ToolExecutionComponent) MarkAborted() { c.aborted = true }

// Aborted reports whether MarkAborted or FinalizeAborted ended the call.
func (c *ToolExecutionComponent) Aborted() bool { return c.aborted }

// SetExpanded forces the body open or closed and records the user's
// intent so subsequent SetResult calls don't snap it back. Used by
// global Ctrl+O (toggle-all-tools) so every component lands in the
// same state.
func (c *ToolExecutionComponent) SetExpanded(expanded bool) {
	c.Collapsed = !expanded
	c.userToggled = true
	c.Invalidate()
}

// Toggle flips the collapsed state and records that the user touched it
// so subsequent SetResult calls don't snap it back.
func (c *ToolExecutionComponent) Toggle() {
	c.Collapsed = !c.Collapsed
	c.userToggled = true
	c.Invalidate()
}

// Expand forces the body open.
func (c *ToolExecutionComponent) Expand() {
	c.Collapsed = false
	c.userToggled = true
	c.Invalidate()
}

// Collapse forces the body closed.
func (c *ToolExecutionComponent) Collapse() {
	c.Collapsed = true
	c.userToggled = true
	c.Invalidate()
}

// SetShowImages toggles image rendering. Mirrors upstream setShowImages.
// SetOutputPad is Pi's setOutputPad(outputPad) (tool-execution.ts:207): the card's horizontal padding, applied at once to the content box (updateDisplay's renderContainer.setPaddingX) and to the default card.
func (c *ToolExecutionComponent) SetOutputPad(outputPad int) {
	c.outputPad = outputPad
	c.contentBox.SetPaddingX(outputPad)
	c.Invalidate()
}

func (c *ToolExecutionComponent) SetShowImages(show bool) {
	c.ShowImages = show
	c.Invalidate()
}

// SetImageWidthCells updates the max image width. Mirrors upstream setImageWidthCells.
func (c *ToolExecutionComponent) SetImageWidthCells(width int) {
	c.ImageWidthCells = max(1, width)
	c.Invalidate()
}

// renderImages renders image blocks after the tool body.
// Mirrors upstream tool-execution.ts updateDisplay image section.
func (c *ToolExecutionComponent) renderImages(width int) []string {
	if !c.ShowImages || len(c.ImageBlocks) == 0 {
		return nil
	}
	caps := GetCapabilities()
	if caps.Images == "" {
		// Pi builds no Image component without a terminal image protocol; the fallback text is part of the
		// result text instead (render-utils getTextOutput, tool-execution.ts:356).
		return nil
	}
	previousImages, previousSources := c.imageComponents, c.imageSources
	c.imageComponents, c.imageSources = nil, nil
	var out []string
	for _, img := range c.ImageBlocks {
		if img.Data == "" || img.MIMEType == "" {
			continue
		}
		source := toolImageSource{data: img.Data, mimeType: img.MIMEType, widthCells: c.ImageWidthCells}
		index := len(c.imageComponents)
		var image *Image
		if index < len(previousSources) && previousSources[index] == source {
			image = previousImages[index]
		} else {
			image = NewImage(source.data, source.mimeType, ImageTheme{FallbackColor: func(s string) string { return fg(ActiveTheme().ToolOutput, s) }}, ImageOptions{MaxWidthCells: source.widthCells}, nil)
		}
		if source.mimeType != "image/png" {
			ensureImageTranscoder(func() {
				c.Invalidate()
				if c.ui != nil {
					c.ui.RequestRender()
				}
			})
		}
		c.imageComponents = append(c.imageComponents, image)
		c.imageSources = append(c.imageSources, source)
		out = append(out, "") // spacer
		out = append(out, image.Render(width)...)
	}
	return out
}

// runningElapsedLine returns the live "Elapsed X.Xs" footer shown while a
// shell tool is executing, or "" otherwise. Mirrors upstream bash.ts renderResult,
// which shows `Elapsed <formatDuration>` while the result is partial and
// switches to `Took` on completion (makeBashBodyRenderer handles the `Took`
// side). The tickSpinner 100ms render loop keeps this value current; Render
// bypasses its cache while this is live so the elapsed advances.
func (c *ToolExecutionComponent) runningElapsedLine() string {
	if c.State != ToolStateRunning || !IsShellTool(c.Name) || c.StartedAt.IsZero() {
		return ""
	}
	muted := ActiveTheme().Muted
	if muted == "" {
		muted = "\x1b[38;2;128;128;128m"
	}
	return muted + "Elapsed " + FormatToolDuration(time.Since(c.StartedAt)) + "\x1b[39m"
}

// runningElapsedRows renders the live shell footer through Text at the card's
// inner width. Upstream adds Text("\n"+label, 0, 0), so the leading blank row,
// word wrapping, ANSI continuation, and row padding all come from Text.
func (c *ToolExecutionComponent) runningElapsedRows(width int) []string {
	line := c.runningElapsedLine()
	if line == "" {
		return nil
	}
	return NewPaddedText("\n"+line, 0, 0, nil).Render(max(width, 1))
}

// HandleMouse delegates to nested renderer components before toggling a completed or partial result. Images and the outer spacer never toggle the card.
func (c *ToolExecutionComponent) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if c.definition == nil && (event.Type != MouseClick || event.Button != MouseButtonLeft || !c.definitionHasResult()) {
		return nil
	}
	if c.mouseWidth != event.Width || c.mouseDirty.Load() {
		c.Render(event.Width)
	}
	if event.Y < 1 || event.Y > c.mouseHeight {
		return nil
	}
	if c.mouseChild != nil {
		event.Y--
		event.Height = c.mouseHeight
		return DispatchMouseEvent(c.mouseChild, event)
	}
	if HasBuiltInToolRenderers(c.Name) && (event.X < 1 || event.X-1 >= max(1, event.Width-2) || event.Y < 2 || event.Y >= c.mouseHeight) {
		return nil
	}
	if result := c.handleResultMouse(event); result != nil {
		return &TuiMouseDispatchResult{TuiMouseEventResult: *result}
	}
	return nil
}

func (c *ToolExecutionComponent) handleResultMouse(event TuiMouseEvent) *TuiMouseEventResult {
	if !c.definitionHasResult() || event.Type != MouseClick || event.Button != MouseButtonLeft {
		return nil
	}
	c.Toggle()
	return &TuiMouseEventResult{Handled: true}
}

// Render mirrors upstream render (tool-execution.ts:262-292): a registered definition with renderShell "self" draws its components after one blank row, or nothing when they draw nothing; every other card is the Container of spacer, content box and images.
func (c *ToolExecutionComponent) Render(width int) []string {
	c.mouseDirty.Store(false)
	if c.definition != nil {
		if c.definitionDirty.Load() || c.definitionCall == nil {
			c.updateDefinition()
		}
		if c.definition.Self {
			return c.renderSelfShell(width)
		}
	}
	return c.Container.Render(width)
}

// renderCard draws the content box: the definition's Box, or the legacy card for a tool without one. It records the card's height and width for mouse dispatch.
func (c *ToolExecutionComponent) renderCard(width int) []string {
	if c.definition != nil {
		content := c.contentBox.Render(width)
		c.mouseChild, c.mouseWidth, c.mouseHeight = c.contentBox, width, len(content)
		return content
	}
	width = max(width, 3)
	content := c.legacyCard.Render(width)
	c.mouseChild, c.mouseWidth, c.mouseHeight = nil, width, len(content)
	return content
}

func (c *ToolExecutionComponent) renderToolImages(width int) []string { return c.renderImages(width) }

// renderLegacyHeader draws the call header, wrapped across rows like upstream's Text.
func (c *ToolExecutionComponent) renderLegacyHeader(width int) []string {
	return wrapText(c.renderHeaderInner(width), width)
}

// renderLegacyBody draws what follows the header: the live elapsed footer before any output, the collapsed preview, or the separator, result body and footer.
func (c *ToolExecutionComponent) renderLegacyBody(width int) []string {
	if c.Output == "" && c.BodyRenderer == nil {
		return c.runningElapsedRows(width)
	}
	if c.Collapsed && c.BodyRenderer == nil {
		// A registered definition without a result renderer uses upstream's
		// first-ten-lines fallback and can be expanded by click or Ctrl+O.
		return append(append([]string{""}, c.renderCollapsedPreview(width)...), c.runningElapsedRows(width)...)
	}
	// Per-tool BodyRenderers receive the expanded flag and always own their
	// preview-to-full transition. This preserves upstream tool-execution.ts
	// behavior and keeps the collapsed bash preview visible.
	//
	// Upstream's result components (bash, read, write, edit) all start their output with a leading empty line, which separates the call header from the body. When the body renderer emits nothing (a collapsed read card, or a renderShell:"self" renderer with no lines), skip the separator so the empty body leaves no stray blank row: upstream #5299 (read.ts collapsed returns "").
	var out []string
	if body := c.renderResultBody(width); len(body) > 0 {
		out = append(append(out, ""), body...)
	}
	return append(out, c.runningElapsedRows(width)...)
}

// bgOpenSGR returns the lifecycle bg open sequence for the current
// state. Mirrors upstream `tool-execution.ts:236-241` exactly.
func (c *ToolExecutionComponent) bgOpenSGR() string {
	switch c.State {
	case ToolStateRunning:
		return ToolPendingBgOpen()
	case ToolStateError:
		return ToolErrorBgOpen()
	default:
		return ToolSuccessBgOpen()
	}
}

func (c *ToolExecutionComponent) renderHeaderInner(width int) string {
	body := c.headerBody()
	if body == "" {
		// Fallback: bold toolTitle tool name, matching upstream
		// tool-execution.ts:136 default renderCall.
		body = toolTitleText(c.Name)
	}

	// The header is already fully styled by HeaderForTool / the per-tool
	// formatters (bold toolTitle name + accent/linked path). Let it wrap
	// naturally: upstream renders the call header inside a Text component
	// that word-wraps via wrapTextWithAnsi.
	return body
}

// flattenVisualRows splits any element that carries embedded newlines into one
// element per row. Body renderers that wrap long styled lines (e.g. the diff
// renderer's styleAndWrap) join wrapped rows with "\n"; the bg-paint loop
// paints one string per terminal row, so unsplit rows would leave the wrapped
// continuation unpainted at column 0.
func flattenVisualRows(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.IndexByte(l, '\n') < 0 {
			out = append(out, l)
			continue
		}
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

func (c *ToolExecutionComponent) renderResultBody(width int) []string {
	// Per-tool custom renderer takes precedence. It already returns
	// styled lines; we just emit them directly inside the bg-painted
	// box (no `│ ` prefix anymore: the bg tint provides the framing,
	// matching upstream `tool-execution.ts:240`).
	if c.BodyRenderer != nil {
		// A BodyRenderer may return a single string carrying embedded
		// newlines: the diff renderer wraps long lines via styleAndWrap,
		// which joins the wrapped visual rows with "\n". The caller paints
		// one string per terminal row, so each embedded row must become its
		// own element or the wrapped continuation renders at column 0 with no
		// background paint (visible gaps in a wrapped edit diff).
		return flattenVisualRows(c.BodyRenderer(width, !c.Collapsed))
	}
	if c.Output == "" {
		return nil
	}
	// Sanitize the raw tool output before rendering. Tool output
	// (especially from bash) routinely contains terminal control codes
	// like CUP (`\x1b[H`), ED (`\x1b[2J`, clear screen), `\x1b[?25l`
	// (hide cursor), or carriage-return-only animation frames. If we
	// emit them verbatim, they execute in OUR terminal and corrupt the
	// TUI: wiping the screen, jumping the cursor, hiding our editor.
	// stripControlEscapes keeps SGR color codes (so `ls --color`,
	// ripgrep, etc. still look right) and drops everything else.
	cleanOutput := stripControlEscapes(c.Output)
	// Replace tabs before rendering: tab stops differ across terminals
	// and many terminals don't paint background color through tab stops,
	// causing visible gaps in the bg-tinted tool box.
	cleanOutput = strings.ReplaceAll(cleanOutput, "\t", "   ")
	lines := strings.Split(strings.TrimRight(cleanOutput, "\n"), "\n")
	max := c.bodyMaxLines()
	totalLines := len(lines)
	truncated := false
	if max > 0 && len(lines) > max {
		lines = lines[:max]
		truncated = true
	}
	innerWidth := width
	if innerWidth < 1 {
		innerWidth = 1
	}
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		// read.ts formatReadResult displays failures without syntax highlighting, in toolOutput color.
		if c.Name == "read" && c.State == ToolStateError {
			l = fg(ActiveTheme().ToolOutput, l)
		}
		// Wrap long lines instead of truncating: upstream renders tool
		// output inside a Text component which wraps via wrapTextWithAnsi.
		wrapped := wrapText(l, innerWidth)
		out = append(out, wrapped...)
	}
	if truncated {
		more := totalLines - max
		out = append(out, fmt.Sprintf("\033[2m… (%d earlier line%s suppressed by hard cap)\033[0m", more, plural(more)))
	}
	return out
}

func (c *ToolExecutionComponent) lineCount() int {
	if c.Output == "" {
		return 0
	}
	// strings.Count of \n + 1 if the last line lacks a newline.
	n := strings.Count(c.Output, "\n")
	if !strings.HasSuffix(c.Output, "\n") {
		n++
	}
	return n
}

func (c *ToolExecutionComponent) autoCollapseThreshold() int {
	if c.AutoCollapseLines > 0 {
		return c.AutoCollapseLines
	}
	return 8
}

const fallbackPreviewLines = 10

// renderCollapsedPreview returns the first ten plain-text output lines and an
// upstream-compatible "more lines" hint for a definition without a result
// renderer.
func (c *ToolExecutionComponent) renderCollapsedPreview(width int) []string {
	cleanOutput := stripControlEscapes(c.Output)
	cleanOutput = strings.ReplaceAll(cleanOutput, "\t", "   ")
	lines := strings.Split(strings.TrimRight(cleanOutput, "\n"), "\n")

	if width < 1 {
		width = 1
	}

	displayLines := lines
	remaining := 0
	if len(lines) > fallbackPreviewLines {
		displayLines = lines[:fallbackPreviewLines]
		remaining = len(lines) - fallbackPreviewLines
	}
	var out []string
	for _, line := range displayLines {
		out = append(out, wrapText(fg(ActiveTheme().ToolOutput, line), width)...)
	}
	if remaining > 0 {
		hint := fmt.Sprintf("... (%d more line%s, ctrl+o to expand)", remaining, plural(remaining))
		out = append(out, wrapText(fg(ActiveTheme().Muted, hint), width)...)
	}
	return out
}

func (c *ToolExecutionComponent) bodyMaxLines() int {
	// Default 0 (no cap). Honored only when a caller explicitly sets
	// a hard upper bound on body rendering. Earlier versions clipped
	// to 40 here, but Ctrl+O is a toggle: the "… (N more, Ctrl+O to
	// expand)" footer was a lie because there was no way to reveal
	// the suppressed lines once the body was already expanded.
	return c.BodyMaxLines
}

// FormatToolArgs renders a JSON object as a compact `key:val, key:val`
// preview suitable for the tool-call header. Falls back to the raw JSON
// for non-object inputs. Long string values are truncated with "\u2026" so
// the header never overflows the terminal width.
//
// Examples:
//
//	{"path":"x","limit":10}                \u2192  path:"x", limit:10
//	{"command":"git status --porcelain"}    \u2192  command:"git status \u2026"
//	[]                                      \u2192  []
func FormatToolArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Not an object \u2014 just compact-print.
		var v any
		if json.Unmarshal(raw, &v) == nil {
			b, _ := json.Marshal(v)
			return truncateArg(string(b), 80)
		}
		return truncateArg(string(raw), 80)
	}
	keys := slices.Sorted(maps.Keys(obj))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+formatArgValue(obj[k]))
	}
	return truncateArg(strings.Join(parts, ", "), 80)
}

// noEscapeJSON serialises v to JSON without HTML escaping so & < >
// appear as-is in display strings (not as \u0026 \u003c \u003e).
func noEscapeJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

func formatArgValue(v any) string {
	switch t := v.(type) {
	case string:
		return strconvQuote(truncateArg(t, 40))
	case bool, float64, int, int64:
		return noEscapeJSON(t)
	case nil:
		return "null"
	case []any:
		return fmt.Sprintf("[%d]", len(t))
	case map[string]any:
		return fmt.Sprintf("{%d}", len(t))
	default:
		return noEscapeJSON(t)
	}
}

// strconvQuote wraps strconv.Quote without pulling the import (avoids
// extra surface in this small helper file).
func strconvQuote(s string) string {
	// json.Marshal HTML-escapes &, <, > to \u0026 etc., which leaks into
	// the tool-call header display ("chmod +x foo \u0026\u0026 bar").
	// Use a json.Encoder with SetEscapeHTML(false) to get clean output.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

// FormatReadHeader returns the styled `read <path>` header for read tool
// calls. Mirrors upstream read.ts formatReadCall: bold toolTitle `read`,
// the path via renderToolPath (accent + ~/ + OSC-8 link, or the invalid-arg
// marker for a non-string path), and a warning-colored `:start-end` line
// range.
func FormatReadHeader(raw json.RawMessage, cwd string) string {
	args := decodeToolArgs(raw)
	return toolTitleText("read") + " " + renderToolPathFromArgs(args, cwd) + readLineRange(args)
}

// FormatWriteHeader returns the styled `write <path>` header for write
// tool calls. Mirrors upstream write.ts formatWriteCall.
func FormatWriteHeader(raw json.RawMessage, cwd string) string {
	return toolTitleText("write") + " " + renderToolPathFromArgs(decodeToolArgs(raw), cwd)
}

// FormatEditHeader returns the styled `edit <path>` header for edit tool
// calls. Mirrors upstream renderers/edit.ts formatEditCall.
func FormatEditHeader(raw json.RawMessage, cwd string) string {
	return toolTitleText("edit") + " " + renderToolPathFromArgs(decodeToolArgs(raw), cwd)
}

// FormatGrepHeader returns the styled grep call. Mirrors upstream
// renderers/grep.ts formatGrepCall: bold toolTitle `grep`, accent
// `/pattern/`, toolOutput ` in <path>` ($HOME shortened, "." by default),
// then optional ` (glob)` and ` limit N` suffixes. Non-string pattern or
// path arguments render as the invalid-arg marker.
func FormatGrepHeader(raw json.RawMessage) string {
	args := decodeToolArgs(raw)
	theme := ActiveTheme()
	header := toolTitleText("grep") + " " + listPatternText(args["pattern"], true) +
		fg(theme.ToolOutput, " in "+listPathText(args["path"]))
	if glob, ok := renderStr(args["glob"]); ok && glob != "" {
		header += fg(theme.ToolOutput, " ("+glob+")")
	}
	if limit, present := args["limit"]; present {
		header += fg(theme.ToolOutput, " limit "+jsTemplateString(limit))
	}
	return header
}

// FormatFindHeader returns the styled find call. Mirrors upstream
// renderers/find.ts formatFindCall.
func FormatFindHeader(raw json.RawMessage) string {
	args := decodeToolArgs(raw)
	theme := ActiveTheme()
	header := toolTitleText("find") + " " + listPatternText(args["pattern"], false) +
		fg(theme.ToolOutput, " in "+listPathText(args["path"]))
	if limit, present := args["limit"]; present {
		header += fg(theme.ToolOutput, " (limit "+jsTemplateString(limit)+")")
	}
	return header
}

// FormatLsHeader returns the styled ls call. Mirrors upstream
// renderers/ls.ts formatLsCall: renderToolPath with emptyFallback ".".
func FormatLsHeader(raw json.RawMessage, cwd string) string {
	args := decodeToolArgs(raw)
	header := toolTitleText("ls") + " "
	switch path, ok := renderStr(args["path"]); {
	case !ok:
		header += invalidArgText()
	case path == "":
		header += renderToolPath(".", cwd)
	default:
		header += renderToolPath(path, cwd)
	}
	if limit, present := args["limit"]; present {
		header += fg(ActiveTheme().ToolOutput, " (limit "+jsTemplateString(limit)+")")
	}
	return header
}

// decodeToolArgs decodes a tool call's arguments as upstream receives them:
// a JSON object, or nothing while the arguments are still streaming.
func decodeToolArgs(raw json.RawMessage) map[string]any {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	return args
}

// listPatternText renders grep's accent `/pattern/` or find's accent
// `pattern`, or the invalid-arg marker for a non-string pattern.
func listPatternText(v any, slashes bool) string {
	pattern, ok := renderStr(v)
	if !ok {
		return invalidArgText()
	}
	if slashes {
		pattern = "/" + pattern + "/"
	}
	return fg(ActiveTheme().Accent, pattern)
}

// listPathText mirrors grep/find's `shortenPath(rawPath || ".")`, or the
// invalid-arg marker for a non-string path.
func listPathText(v any) string {
	path, ok := renderStr(v)
	if !ok {
		return invalidArgText()
	}
	if path == "" {
		path = "."
	}
	return shortenPath(path)
}

// FormatBuiltinToolHeader dispatches to the per-tool header formatter
// matching upstream's renderCall functions. cwd resolves relative paths
// to absolute file:// URLs for the OSC-8 hyperlink. Returns "" if the
// tool has no custom header format (falls back to FormatToolArgs).
func FormatBuiltinToolHeader(toolName string, args json.RawMessage, cwd string) string {
	switch toolName {
	case "bash", "powershell":
		prompt, _ := ShellToolPrompt(toolName)
		return FormatShellHeader(args, prompt)
	case "read":
		return FormatReadHeader(args, cwd)
	case "write":
		return FormatWriteHeader(args, cwd)
	case "edit":
		return FormatEditHeader(args, cwd)
	case "grep":
		return FormatGrepHeader(args)
	case "find":
		return FormatFindHeader(args)
	case "ls":
		return FormatLsHeader(args, cwd)
	}
	return ""
}

// headerBody is the call header: upstream read.ts renderCall draws the
// compact label unless the tool output is expanded, and a result alone never
// expands the card.
func (c *ToolExecutionComponent) headerBody() string {
	if c.compactHeader != "" && (!c.userToggled || c.Collapsed) {
		return c.compactHeader
	}
	return c.ArgsPreview
}

// SetHeaderArgs records the call arguments the collapsed read card's
// compact label and a frontend session's tool node are drawn from.
func (c *ToolExecutionComponent) SetHeaderArgs(args json.RawMessage) {
	c.compactHeader = ""
	if c.Name == "read" {
		c.compactHeader = FormatCompactReadHeader(args, c.Cwd)
	}
	c.argumentsRaw = append(c.argumentsRaw[:0], args...)
	c.argumentsDecoded = false
	c.Invalidate()
}

// decodedArguments returns the argument object, or nil when the arguments
// are not a complete JSON object yet.
func (c *ToolExecutionComponent) decodedArguments() map[string]any {
	if !c.argumentsDecoded {
		c.arguments = nil
		var arguments map[string]any
		if json.Unmarshal(c.argumentsRaw, &arguments) == nil {
			c.arguments = arguments
		}
		c.argumentsDecoded = true
	}
	return c.arguments
}

// HeaderForTool returns the fully styled call header for any tool: the
// builtin per-tool formatter when one matches, otherwise the upstream
// default of a bold toolTitle tool name followed by its compact args
// (tool-execution.ts:136 / formatToolExecution). The returned string is
// self-styled; renderHeaderInner emits it verbatim.
func HeaderForTool(name string, args json.RawMessage, cwd string) string {
	if h := FormatBuiltinToolHeader(name, args, cwd); h != "" {
		return h
	}
	if a := FormatToolArgs(args); a != "" {
		return toolTitleText(name) + " " + a
	}
	return toolTitleText(name)
}

func truncateArg(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "\u2026"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// stripControlEscapes removes terminal control sequences from `s` while
// preserving SGR color codes. Used to sanitize tool output before
// rendering it inside the TUI: a bash script that does `clear` or
// `printf '\x1b[H'` would otherwise blow away our display.
//
// Kept:
//   - SGR sequences (`ESC [ ... m`) for `ls --color`, ripgrep, etc.
//   - Plain text, tabs, newlines, regular carriage returns.
//
// Dropped:
//   - Cursor positioning (`H`, `f`, `A`, `B`, `C`, `D`, `G`, `s`, `u`)
//   - Erase commands (`J`, `K`)
//   - Mode set/reset (`?...h`, `?...l`): hide cursor, alt screen, etc.
//   - OSC sequences (`ESC ]` … BEL or ST)
//   - Lone ESC, BEL, and other C0 control bytes (except \t \n \r).
func stripControlEscapes(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c == 0x1b && i+1 < len(s) {
			switch s[i+1] {
			case '[':
				// CSI sequence: scan for final byte in 0x40–0x7E.
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					if s[j] == 'm' {
						// Keep SGR (color) sequences.
						out.WriteString(s[i : j+1])
					}
					i = j + 1
					continue
				}
				// Unterminated CSI: drop the rest defensively.
				return out.String()
			case ']':
				// OSC sequence: terminated by BEL (0x07) or ST (ESC \).
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			default:
				// Two-byte ESC sequence (e.g. ESC = / ESC > / ESC c). Drop both.
				i += 2
				continue
			}
		}
		if c == 0x1b {
			// Lone ESC at end of string: drop.
			i++
			continue
		}
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			// Other C0 control bytes (BEL, BS, FF, ...): drop.
			i++
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}
