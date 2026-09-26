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

	"github.com/MichaelKinsy/PiG/tui/widthx"
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
	invalidatable

	Name  string
	Label string // human-readable display name (if set, used in header instead of Name)
	// ArgsPreview is a short human-readable rendering of the tool args.
	// Build it with FormatToolArgs() before assigning, or use SetRunning().
	ArgsPreview string

	// pig divergence (D59): generic extension cards retain structured args so
	// their collapsed preview can scale with width and Ctrl+O can reveal all data.
	structuredArgs       json.RawMessage
	renderStructuredArgs bool

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

	// StartedAt records when the tool began executing. Used to render a live
	// "Elapsed X.Xs" footer while a shell tool runs (mirrors upstream bash.ts
	// renderResult, which ticks the elapsed every second during execution).
	StartedAt time.Time

	// Configurable thresholds. Zero values fall back to defaults.
	AutoCollapseLines int // default 8
	BodyMaxLines      int // default 40

	// Render cache: short-circuits Render() when nothing changed.
	cachedState          ToolExecutionState
	cachedOutput         string
	cachedCollapsed      bool
	cachedWidth          int
	cachedIsPartial      bool
	cachedArgsPreview    string
	cachedStructuredArgs string
	cachedStructured     bool
	cachedLines          []string
	cachedTheme          *Theme

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

	// convertedImages caches Kitty PNG conversions by image index, keyed to
	// the source block they were made from. Mirrors upstream
	// tool-execution.ts convertedImages.
	convertedImages map[int]convertedToolImage

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

// convertedToolImage is one convertedImages entry: the conversion plus the
// source block it came from.
type convertedToolImage struct {
	sourceData     string
	sourceMimeType string
	ConvertedImage
}

// KittyImageConversion names one tool-result image that needs a PNG
// conversion before the Kitty graphics protocol can display it.
type KittyImageConversion struct {
	Index    int
	Data     string
	MimeType string
}

// PendingKittyImageConversions mirrors the selection half of upstream
// maybeConvertImagesForKitty: on a Kitty terminal it returns every image block
// with data and a MIME type that is not PNG and has no conversion cached for
// its current source. The caller converts each one off the UI loop and hands
// the result to ApplyConvertedImage on the loop.
func (c *ToolExecutionComponent) PendingKittyImageConversions() []KittyImageConversion {
	if GetCapabilities().Images != ImageProtocolKitty {
		return nil
	}
	var pending []KittyImageConversion
	for i, img := range c.ImageBlocks {
		if img.Data == "" || img.MIMEType == "" || img.MIMEType == "image/png" {
			continue
		}
		if cached, ok := c.convertedImages[i]; ok && cached.sourceData == img.Data && cached.sourceMimeType == img.MIMEType {
			continue
		}
		pending = append(pending, KittyImageConversion{Index: i, Data: img.Data, MimeType: img.MIMEType})
	}
	return pending
}

// ApplyConvertedImage mirrors the resolution half of upstream
// maybeConvertImagesForKitty. A failed conversion (nil) or one that finishes
// after its image block was replaced is ignored (upstream issue #8577);
// otherwise the conversion is cached and the component invalidated. It reports
// whether the conversion was applied, so the caller knows to request a render.
func (c *ToolExecutionComponent) ApplyConvertedImage(req KittyImageConversion, converted *ConvertedImage) bool {
	if converted == nil || req.Index < 0 || req.Index >= len(c.ImageBlocks) {
		return false
	}
	current := c.ImageBlocks[req.Index]
	if current.Data != req.Data || current.MIMEType != req.MimeType {
		return false
	}
	if c.convertedImages == nil {
		c.convertedImages = make(map[int]convertedToolImage)
	}
	c.convertedImages[req.Index] = convertedToolImage{
		sourceData:     req.Data,
		sourceMimeType: req.MimeType,
		ConvertedImage: *converted,
	}
	c.cachedLines = nil
	c.Invalidate()
	return true
}

// NewToolExecutionComponent returns a Running-state component for the
// given tool. Args may be empty.
func NewToolExecutionComponent(name, argsPreview string) *ToolExecutionComponent {
	return &ToolExecutionComponent{
		Name:            name,
		ArgsPreview:     argsPreview,
		State:           ToolStateRunning,
		Collapsed:       true,
		ShowImages:      true,
		ImageWidthCells: 60,
		IsPartial:       true,
	}
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
	c.definitionDirty.Store(true)
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

// SetStructuredArgs enables the generic extension tool-details renderer and
// retains a valid argument value for width-aware collapsed and expanded views.
func (c *ToolExecutionComponent) SetStructuredArgs(args json.RawMessage) {
	c.renderStructuredArgs = true
	if len(args) > 0 && json.Valid(args) {
		c.structuredArgs = append(c.structuredArgs[:0], args...)
	}
	c.Invalidate()
}

// UpdateArgs updates the displayed header from partial/complete args.
// Called progressively during streaming as ToolCallDelta events arrive.
// Mirrors upstream tool-execution.ts updateArgs.
func (c *ToolExecutionComponent) UpdateArgs(name string, partialArgsJSON string) {
	if name != "" {
		c.Name = name
	}
	// Try to parse the partial JSON to get a header. Partial JSON will
	// fail to parse: that's OK, we fall back to the tool name.
	var raw json.RawMessage
	if json.Unmarshal([]byte(partialArgsJSON), &raw) == nil {
		if c.definition != nil {
			c.definitionArgs = append(c.definitionArgs[:0], raw...)
		}
		if c.renderStructuredArgs {
			c.structuredArgs = append(c.structuredArgs[:0], raw...)
		} else {
			c.ArgsPreview = HeaderForTool(c.Name, raw, c.Cwd)
		}
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
		case c.renderStructuredArgs:
			// pig divergence (D59): generic extension cards stay compact even
			// when their result is short because their arguments may be hidden.
			c.Collapsed = true
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
}

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
		// No image protocol: render fallback text for each image.
		var out []string
		for _, img := range c.ImageBlocks {
			out = append(out, "") // spacer
			dims := GetImageDimensions(img.Data, img.MIMEType)
			out = append(out, ImageFallback(img.MIMEType, dims, ""))
		}
		return out
	}
	maxW := min(width-2, c.ImageWidthCells)
	if maxW <= 0 {
		maxW = min(width, 60)
	}
	var out []string
	for i, img := range c.ImageBlocks {
		// Upstream updateDisplay: prefer a conversion made from this exact
		// source block, and on Kitty skip a non-PNG image entirely (no
		// spacer) until its conversion lands.
		if cached, ok := c.convertedImages[i]; ok && cached.sourceData == img.Data && cached.sourceMimeType == img.MIMEType {
			img = ImageBlock{Data: cached.Data, MIMEType: cached.MimeType}
		}
		if caps.Images == ImageProtocolKitty && img.MIMEType != "image/png" {
			continue
		}
		dims := ImageDimensions{WidthPx: 800, HeightPx: 600}
		if got := GetImageDimensions(img.Data, img.MIMEType); got != nil {
			dims = *got
		}
		out = append(out, "") // spacer between images
		result := RenderImage(img.Data, dims, ImageRenderOptions{
			MaxWidthCells:       maxW,
			PreserveAspectRatio: true,
			Name:                "",
		})
		if result != nil {
			for range max(result.Rows-1, 0) {
				out = append(out, "")
			}
			moveUp := ""
			if result.Rows > 1 {
				moveUp = "\x1b[" + itoa(result.Rows-1) + "A"
			}
			out = append(out, moveUp+result.Sequence)
		} else {
			out = append(out, ImageFallback(img.MIMEType, &dims, ""))
		}
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

// Render emits header + (optional) body lines, all bg-painted in the
// lifecycle color (pending / success / error). Row 2.9a.
//
// Upstream wraps content in Box(paddingX=1, paddingY=1, bgFn) which
// adds 1-row vertical padding above and below, plus 1-column left
// padding on each content line. We replicate this layout exactly:
// top-pad, header, separator, body, bottom-pad: all bg-painted.
func (c *ToolExecutionComponent) Render(width int) []string {
	if c.definition != nil {
		return c.renderDefinition(width)
	}
	if width < 3 {
		width = 3
	}
	// While a shell tool is running, the "Elapsed X.Xs" footer advances every
	// render tick, so the line cache must not short-circuit it.
	liveShell := c.State == ToolStateRunning && IsShellTool(c.Name) && !c.StartedAt.IsZero()
	// Cache check: return cached lines when nothing changed.
	if !liveShell && c.cachedLines != nil &&
		c.cachedState == c.State &&
		c.cachedOutput == c.Output &&
		c.cachedCollapsed == c.Collapsed &&
		c.cachedWidth == width &&
		c.cachedIsPartial == c.IsPartial &&
		c.cachedArgsPreview == c.ArgsPreview &&
		c.cachedStructuredArgs == string(c.structuredArgs) &&
		c.cachedStructured == c.renderStructuredArgs &&
		c.cachedTheme == ActiveTheme() {
		return c.cachedLines
	}
	bgOpen := c.bgOpenSGR()
	headerInner := c.renderHeaderInner(width - 2)

	// Leading blank line: mirrors upstream Spacer(1) inside ToolExecutionComponent
	// constructor (tool-execution.ts:63) which adds one row of vertical padding
	// before the content box, visually separating consecutive tool blocks.
	out := make([]string, 0, 8)
	out = append(out, "")

	// Top padding row: mirrors upstream Box(paddingX=1, paddingY=1, bgFn).
	out = append(out, paintBgWith(bgOpen, "", width))

	// Header always painted, even when collapsed: lifecycle tint stays
	// visible at-a-glance. Wrap long headers (e.g. bash commands) across
	// multiple lines rather than truncating, matching upstream's Text
	// component behavior.
	headerLines := wrapText(headerInner, width-2)
	for _, hl := range headerLines {
		out = append(out, paintBgWith(bgOpen, " "+hl, width))
	}

	if c.Output == "" && c.BodyRenderer == nil && (!c.renderStructuredArgs || c.Collapsed) {
		// No output yet. While a shell tool runs, Text supplies the footer's
		// leading separator and any wrapped continuation rows.
		for _, line := range c.runningElapsedRows(width - 2) {
			out = append(out, paintBgWith(bgOpen, " "+line, width))
		}
		out = append(out, paintBgWith(bgOpen, "", width)) // close the frame
		out = append(out, c.renderImages(width)...)
		c.saveCachedRender(width, out)
		return out
	}
	if c.Collapsed && c.BodyRenderer == nil {
		// A registered definition without a result renderer uses upstream's
		// first-ten-lines fallback and can be expanded by click or Ctrl+O.
		out = append(out, paintBgWith(bgOpen, "", width)) // separator
		for _, line := range c.renderCollapsedPreview(width - 2) {
			out = append(out, paintBgWith(bgOpen, " "+line, width))
		}
		for _, line := range c.runningElapsedRows(width - 2) {
			out = append(out, paintBgWith(bgOpen, " "+line, width))
		}
		out = append(out, paintBgWith(bgOpen, "", width)) // bottom pad
		out = append(out, c.renderImages(width)...)
		c.saveCachedRender(width, out)
		return out
	}
	// Per-tool BodyRenderers receive the expanded flag and always own their
	// preview-to-full transition. This preserves upstream tool-execution.ts
	// behavior and keeps the collapsed bash preview visible.

	// Separator line between call header and result body.
	// Upstream's result components (bash, read, write, edit) all start
	// their output with a leading empty line: e.g. bash.ts:246
	// `return ["", ...(state.cachedLines ?? [])]` or read.ts:105
	// `let text = "\n${displayLines...}"`. This separates the call
	// header from the body content visually inside the bg-painted box.
	//
	// When the body renderer emits nothing (e.g. a collapsed read card,
	// or a renderShell:"self" extension renderer with no lines), skip the
	// separator so the empty body doesn't leave a stray blank row inside
	// the box: mirrors upstream #5299 (read.ts collapsed returns "").
	body := c.renderBody(width - 2)
	footer := c.runningElapsedRows(width - 2)
	if len(body) > 0 {
		out = append(out, paintBgWith(bgOpen, "", width))
		for _, line := range body {
			out = append(out, paintBgWith(bgOpen, " "+line, width))
		}
	}
	for _, line := range footer {
		out = append(out, paintBgWith(bgOpen, " "+line, width))
	}

	// Bottom padding row: mirrors upstream Box paddingY=1.
	out = append(out, paintBgWith(bgOpen, "", width))

	out = append(out, c.renderImages(width)...)
	c.saveCachedRender(width, out)
	return out
}

func (c *ToolExecutionComponent) saveCachedRender(width int, lines []string) {
	c.cachedState = c.State
	c.cachedOutput = c.Output
	c.cachedCollapsed = c.Collapsed
	c.cachedWidth = width
	c.cachedIsPartial = c.IsPartial
	c.cachedArgsPreview = c.ArgsPreview
	c.cachedStructuredArgs = string(c.structuredArgs)
	c.cachedStructured = c.renderStructuredArgs
	c.cachedLines = lines
	c.cachedTheme = ActiveTheme()
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

// renderHeaderInner builds the header content (no surrounding bg)
// for an inner width budget. Caller paints + pads.
//
// Upstream renders tool call headers via per-tool `renderCall` functions:
//   - bash/powershell: bold "$ <command>" / "PS> <command>" (renderers/bash.ts formatShellCall)
//   - read: bold "read" + accent "<path>"          (read.ts formatReadCall)
//   - write: bold "write" + accent "<path>"        (write.ts formatWriteCall)
//   - edit: bold "edit" + accent "<path>"          (edit.ts formatEditCall)
//   - grep: bold "grep" + accent "/<pat>/" + " in <path>"  (grep.ts)
//   - find: bold "find" + accent "<pat>" + " in <path>"    (find.ts)
//   - ls: bold "ls" + accent "<path>"              (ls.ts formatLsCall)
//   - fallback: bold "<toolName>"                  (ToolExecutionComponent)
//
// No lifecycle markers (✓/▶/✗): upstream conveys state only via bg color.
// No "(N lines, Xs)" annotation: upstream shows duration in body footer.
func (c *ToolExecutionComponent) renderStructuredArgsHeader(width int) string {
	name := c.Name
	if c.Label != "" {
		name = c.Label
	}
	title := toolTitleText(name)
	if !c.Collapsed || len(c.structuredArgs) == 0 {
		return title
	}

	var compact bytes.Buffer
	if json.Compact(&compact, c.structuredArgs) != nil || compact.Len() == 0 {
		return title
	}
	args := compact.String()
	full := title + " " + args
	if widthx.VisibleWidth(full) <= width {
		return full
	}

	hint := "… (ctrl+o to expand)"
	budget := width - widthx.VisibleWidth(title) - widthx.VisibleWidth(hint) - 2
	if budget < 1 {
		return title + "\n" + hint
	}
	preview := widthx.TruncateToWidth(args, budget, "", false)
	return title + " " + preview + " " + hint
}

func (c *ToolExecutionComponent) renderHeaderInner(width int) string {
	if c.renderStructuredArgs {
		return c.renderStructuredArgsHeader(width)
	}
	body := c.ArgsPreview
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

func (c *ToolExecutionComponent) renderBody(width int) []string {
	var out []string
	if c.renderStructuredArgs && !c.Collapsed {
		out = append(out, c.renderExpandedStructuredArgs(width)...)
	}

	result := c.renderResultBody(width)
	if len(out) > 0 && len(result) > 0 {
		out = append(out, "")
	}
	return append(out, result...)
}

func (c *ToolExecutionComponent) renderExpandedStructuredArgs(width int) []string {
	if len(c.structuredArgs) == 0 {
		return nil
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, c.structuredArgs, "", "  "); err != nil {
		return nil
	}
	out := []string{"Arguments:"}
	for line := range strings.SplitSeq(pretty.String(), "\n") {
		out = append(out, wrapPreservingCells(line, width)...)
	}
	return out
}

func wrapPreservingCells(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	segments := graphemeSegments(text)
	if len(segments) == 0 {
		return []string{""}
	}
	var rows []string
	var row strings.Builder
	rowWidth := 0
	for _, segment := range segments {
		if rowWidth > 0 && rowWidth+segment.Width > width {
			rows = append(rows, row.String())
			row.Reset()
			rowWidth = 0
		}
		row.WriteString(segment.Text)
		rowWidth += segment.Width
	}
	if row.Len() > 0 {
		rows = append(rows, row.String())
	}
	return rows
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
// the path via renderToolPath (accent + ~/ + OSC-8 link), and a warning-
// colored `:start-end` line range.
func FormatReadHeader(raw json.RawMessage, cwd string) string {
	var p struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Offset   *int   `json:"offset,omitempty"`
		Limit    *int   `json:"limit,omitempty"`
	}
	_ = json.Unmarshal(raw, &p)
	path := p.FilePath
	if path == "" {
		path = p.Path
	}
	header := toolTitleText("read") + " " + renderToolPath(path, cwd)
	if p.Offset != nil || p.Limit != nil {
		start := 1
		if p.Offset != nil {
			start = *p.Offset
		}
		rng := fmt.Sprintf(":%d", start)
		if p.Limit != nil {
			rng = fmt.Sprintf(":%d-%d", start, start+*p.Limit-1)
		}
		header += fg(ActiveTheme().Warning, rng)
	}
	return header
}

// FormatWriteHeader returns the styled `write <path>` header for write
// tool calls. Mirrors upstream write.ts formatWriteCall.
func FormatWriteHeader(raw json.RawMessage, cwd string) string {
	var p struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(raw, &p)
	path := p.FilePath
	if path == "" {
		path = p.Path
	}
	return toolTitleText("write") + " " + renderToolPath(path, cwd)
}

// FormatEditHeader returns the styled `edit <path>` header for edit tool
// calls. Mirrors upstream edit.ts formatEditCall.
func FormatEditHeader(raw json.RawMessage, cwd string) string {
	var p struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Patch    string `json:"patch"`
		Multi    []struct {
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
		} `json:"multi"`
	}
	_ = json.Unmarshal(raw, &p)
	path := p.FilePath
	if path == "" {
		path = p.Path
	}
	paths := make([]string, 0, len(p.Multi))
	if path != "" {
		paths = append(paths, path)
	}
	for _, edit := range p.Multi {
		editPath := edit.FilePath
		if editPath == "" {
			editPath = edit.Path
		}
		if editPath != "" && !slices.Contains(paths, editPath) {
			paths = append(paths, editPath)
		}
	}
	for line := range strings.SplitSeq(p.Patch, "\n") {
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if patchPath, ok := strings.CutPrefix(line, prefix); ok {
				patchPath = strings.TrimSpace(patchPath)
				if patchPath != "" && !slices.Contains(paths, patchPath) {
					paths = append(paths, patchPath)
				}
				break
			}
		}
	}
	if len(paths) == 0 {
		return toolTitleText("edit") + " " + renderToolPath("", cwd)
	}
	header := toolTitleText("edit") + " " + renderToolPath(paths[0], cwd)
	if remaining := len(paths) - 1; remaining > 0 {
		header += fg(ActiveTheme().Muted, fmt.Sprintf(" (+%d file%s)", remaining, plural(remaining)))
	}
	return header
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
