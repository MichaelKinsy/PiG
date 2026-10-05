package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	sandbox "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/outputfiles"
)

const (
	argsPreviewChars  = 200
	errorPreviewChars = 500
	// memoryLimitBytes is the heap limit for the QuickJS VM. The VM shares PiG's process, so without a limit a runaway
	// script could grow to wasm32's 4 GiB and take the session down. Overruns throw `InternalError: out of memory`
	// inside the script.
	memoryLimitBytes = 256 * 1024 * 1024
	// defaultMaxOutputTokens is the default token budget for script output.
	defaultMaxOutputTokens = 10_000
)

// StoreEntryType is the custom entry type holding one script's `store()` writes: StoreEntryData.
const StoreEntryType = "codemode-store"

// StoreEntryData is the data of a codemode-store entry.
type StoreEntryData struct {
	Set    map[string]json.RawMessage `json:"set"`
	Delete []string                   `json:"delete"`
}

// NestedCall is one nested tool call as the tool result reports it.
type NestedCall struct {
	// ID is the tool call id of the nested call, `<codemode call id>/<n>`.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Args is compact JSON of the arguments, truncated for display.
	Args   string `json:"args"`
	Status string `json:"status"`
	// DurationMs is nil until the call ends.
	DurationMs *float64 `json:"durationMs,omitempty"`
	// Error is the error text, truncated for display.
	Error string `json:"error,omitempty"`
	// Cost in USD of a `models.*` call that reported usage.
	Cost *float64 `json:"cost,omitempty"`
}

// The nested call statuses.
const (
	StatusRunning   = "running"
	StatusOK        = "ok"
	StatusError     = "error"
	StatusCancelled = "cancelled"
)

// ToolDetails are the details of a codemode tool result.
type ToolDetails struct {
	Calls []NestedCall `json:"calls"`
	// FullOutputPath is the temp file with the full text output, when the output was truncated.
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// truncateText is upstream's truncateText: maxChars UTF-16 code units, ending in `...` when cut.
func truncateText(text string, maxChars int) string {
	if jsstring.Length(text) <= maxChars {
		return text
	}
	units := []rune(text)
	kept, count := 0, 0
	for kept < len(units) {
		width := 1
		if units[kept] >= 0x10000 {
			width = 2
		}
		if count+width > maxChars-3 {
			break
		}
		count += width
		kept++
	}
	return string(units[:kept]) + "..."
}

func previewArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, args); err != nil {
		return ""
	}
	return truncateText(compact.String(), argsPreviewChars)
}

func textOf(result agent.AgentToolResult) string {
	var parts []string
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toScriptValue is the value a script receives for a nested call: a tool that declares `outputSchema` resolves to its
// `structuredContent`, also for error results that carry one (such as MCP results with `isError`); any other tool
// resolves to its text content. Other failures reject with the tool's error text.
func toScriptValue(tool extension.AgentTool, outcome extension.AgentToolCallOutcome) (json.RawMessage, error) {
	result, _ := outcome.Result.(agent.AgentToolResult)
	if len(tool.OutputSchema) > 0 && result.StructuredContent != nil {
		return result.StructuredContent, nil
	}
	text := textOf(result)
	if outcome.IsError {
		if text == "" {
			text = fmt.Sprintf("Tool %q failed", tool.Name)
		}
		return nil, errors.New(text)
	}
	return json.Marshal(text)
}

// valueText is like the script's `text()`: strings as is, other values as compact JSON.
func valueText(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	return string(value)
}

// storeOf reads the values of `load()`: the `codemode-store` entries on the branch, applied from the root. Entries
// with malformed data are ignored.
func storeOf(branch []codingagent.SessionEntry) map[string]json.RawMessage {
	store := map[string]json.RawMessage{}
	for _, entry := range branch {
		if entry.Base.Type != "custom" {
			continue
		}
		var fields struct {
			CustomType string `json:"customType"`
			Data       *struct {
				Set    map[string]json.RawMessage `json:"set"`
				Delete []string                   `json:"delete"`
			} `json:"data"`
		}
		raw, err := json.Marshal(entry)
		if err != nil || json.Unmarshal(raw, &fields) != nil || fields.CustomType != StoreEntryType {
			continue
		}
		if fields.Data == nil || fields.Data.Set == nil || fields.Data.Delete == nil {
			continue
		}
		for _, key := range fields.Data.Delete {
			delete(store, key)
		}
		maps.Copy(store, fields.Data.Set)
	}
	return store
}

// session is the part of the session a tool reaches through its context: the branch to read `load()` values from and
// the entry writer for `store()`.
//
// upstream: codemode/execute.ts:285 (`ctx.sessionManager.getBranch()`), 301 (`options.appendEntry`, which index.ts:35 binds to `pi.appendEntry`).
type session struct {
	manager interface {
		GetBranch() []codingagent.SessionEntry
	}
	tool *extension.ToolContext
}

func (s session) branch() []codingagent.SessionEntry { return s.manager.GetBranch() }

func (s session) appendEntry(customType string, data any) error {
	return s.tool.AppendEntry(customType, data)
}

// sessionOf returns the session behind a tool context, when the host exposes it.
func sessionOf(tc *extension.ToolContext) *session {
	if tc == nil {
		return nil
	}
	manager, err := tc.SessionManager()
	if err != nil || manager == nil {
		return nil
	}
	branch, ok := manager.(interface {
		GetBranch() []codingagent.SessionEntry
	})
	if !ok {
		return nil
	}
	return &session{manager: branch, tool: tc}
}

func spillOutput(text string) (string, error) {
	return outputfiles.WriteFile("pi-codemode", ".txt", []byte(text))
}

// imageExtensions are the file extensions of the image types `image()` accepts. It must list every type the
// sandbox's `image()` detects.
var imageExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// saveImages saves each image to a temp file and puts a text item with its path before it. The model sees the image
// but has no other way to reach its bytes: scripts cannot write files, and `write` only takes text. Images shown more
// than once are saved once. A failed write (disk full, unwritable temp dir) must not discard the result of a script
// whose tool calls already ran, so it becomes part of the label. An image type without a file extension fails the
// call, after the other images were saved.
func saveImages(items []ai.ToolResultMessageContent) ([]ai.ToolResultMessageContent, error) {
	type pending struct {
		label string
		err   error
	}
	labels := map[string]*pending{}
	var order []*pending
	var wg sync.WaitGroup
	for _, item := range items {
		image, ok := item.(ai.ImageContent)
		if !ok || labels[image.Data] != nil {
			continue
		}
		entry := &pending{}
		labels[image.Data] = entry
		order = append(order, entry)
		wg.Go(func() {
			data := imageprocessing.DecodeNodeBase64(image.Data)
			kind := image.MimeType + ", " + tools.FormatSize(len(data))
			extension, known := imageExtensions[image.MimeType]
			if !known {
				entry.err = errors.New("No file extension for image type " + image.MimeType)
				return
			}
			path, err := outputfiles.WriteFile("pi-codemode", extension, data)
			if err != nil {
				entry.label = "[Image (" + kind + ") could not be saved: " + err.Error() + "]"
				return
			}
			entry.label = "[Image saved to " + path + " (" + kind + ")]"
		})
	}
	wg.Wait()
	for _, entry := range order {
		if entry.err != nil {
			return nil, entry.err
		}
	}
	out := make([]ai.ToolResultMessageContent, 0, len(items)+len(labels))
	for _, item := range items {
		if image, ok := item.(ai.ImageContent); ok {
			out = append(out, ai.TextContent{Text: labels[image.Data].label})
		}
		out = append(out, item)
	}
	return out, nil
}

// truncateOutput applies the token budget: when the combined text exceeds it, the text items become one item that
// keeps the start and end of the text, and images follow it. The full text is written to a temp file.
func truncateOutput(items []ai.ToolResultMessageContent, maxTokens int64) ([]ai.ToolResultMessageContent, string) {
	var texts []string
	for _, item := range items {
		if text, ok := item.(ai.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	combined := strings.Join(texts, "\n")
	units := utf16Units(combined)
	budget := int(min(maxTokens*charsPerToken, math.MaxInt32))
	if len(texts) == 0 || len(units) <= budget {
		return items, ""
	}
	headChars := budget / 2
	tailChars := budget - headChars
	removed := len(units) - headChars - tailChars
	head := decodeUnits(units[:headChars])
	tail := ""
	if tailChars > 0 {
		tail = decodeUnits(units[len(units)-tailChars:])
	}
	text := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s…%d tokens truncated…%s",
		ceilDiv(len(units), charsPerToken), strings.Count(combined, "\n")+1, head, ceilDiv(removed, charsPerToken), tail)
	path, err := spillOutput(combined)
	if err == nil {
		text += "\n\n[Full output: " + path + " (read with offset/limit)]"
	} else {
		text += "\n\n[Could not save the full output: " + err.Error() + "]"
	}
	out := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
	for _, item := range items {
		if _, isText := item.(ai.TextContent); !isText {
			out = append(out, item)
		}
	}
	return out, path
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

func formatCallSummary(calls []NestedCall) string {
	if len(calls) == 0 {
		return "No tool calls were made."
	}
	parts := make([]string, len(calls))
	for i, call := range calls {
		parts[i] = call.Name + " (" + call.Status + ")"
	}
	return "Tool calls made before the failure (they are not undone): " + strings.Join(parts, ", ")
}

func formatError(failure *sandbox.Error, calls []NestedCall) string {
	var head string
	switch failure.Kind {
	case sandbox.ErrorScript:
		head = failure.Stack
		if head == "" {
			name := failure.Name
			if name == "" {
				name = "Error"
			}
			head = name + ": " + failure.Message
		}
	case sandbox.ErrorTimeout:
		head = "Script timed out: " + failure.Message
	case sandbox.ErrorAborted:
		head = "Script aborted: " + failure.Message
	default:
		head = "Script sandbox failed: " + failure.Message
	}
	return head + "\n\n" + formatCallSummary(calls)
}

func millisSince(start time.Time) float64 {
	return float64(time.Since(start)) / float64(time.Millisecond)
}

// Execute runs one script. Without a session context (a plain Agent or a direct call) scripts cannot call tools,
// `store()` starts empty, and writes are dropped.
//
// Ports packages/coding-agent/src/extensions/codemode/execute.ts (executeCodemode).
func Execute(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate agent.ToolUpdateCallback, options Options) (agent.AgentToolResult, error) {
	startedAt := time.Now()
	var input struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("invalid codemode input: %w", err)
	}
	parsed, err := sandbox.ParseCodemodeSource(input.Code)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	tc := extension.ToolContextFromContext(ctx)
	run := &run{toolCallID: toolCallID, onUpdate: onUpdate, tc: tc, options: options}

	var callable []extension.AgentTool
	if tc != nil {
		tools, err := tc.Tools()
		if err != nil {
			return agent.AgentToolResult{}, err
		}
		callable = callableTools(tools)
	}
	// ALL_TOOLS entries carry the declaration.
	samples := make(map[string]string, len(callable))
	for _, tool := range callable {
		samples[tool.Name] = sandbox.RenderToolSample(toDeclaration(tool), 0)
	}
	sandboxTools := make([]sandbox.Tool, len(callable))
	for i, tool := range callable {
		sandboxTools[i] = sandbox.Tool{Name: tool.Name, Description: samples[tool.Name], Execute: run.nestedCall(tool), AwaitsInitiation: true}
	}
	timeoutMs := math.Inf(1)
	if parsed.Options.TimeoutMs != nil {
		timeoutMs = float64(*parsed.Options.TimeoutMs)
	}
	globals := discoveryGlobals(callable, samples, tc)
	if options.Models {
		if models := modelRuntimeOf(tc); models != nil {
			globals = append(globals, run.modelGlobals(models)...)
		}
	}
	box, err := sandbox.NewSandbox(sandbox.SandboxOptions{
		Tools:            sandboxTools,
		Globals:          globals,
		TimeoutMs:        timeoutMs,
		MemoryLimitBytes: memoryLimitBytes,
		CacheDir:         options.CacheDir,
	})
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	defer func() { _ = box.Close() }()

	var store map[string]json.RawMessage
	if session := sessionOf(tc); session != nil {
		store = storeOf(session.branch())
	}
	result, err := box.Execute(ctx, parsed.Code, sandbox.ExecuteOptions{Store: store})
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	// Calls still marked running were cut off by the script ending, a timeout, or an abort.
	calls := run.finishCalls()

	items := make([]ai.ToolResultMessageContent, 0, len(result.Output)+2)
	for _, item := range result.Output {
		if item.Type == "text" {
			items = append(items, ai.TextContent{Text: item.Text})
		} else {
			items = append(items, ai.ImageContent{Data: item.Data, MimeType: item.MimeType})
		}
	}
	if result.OK {
		writes := result.StoreWrites
		if session := sessionOf(tc); session != nil && (len(writes.Set) > 0 || len(writes.Delete) > 0) {
			data := StoreEntryData{Set: writes.Set, Delete: writes.Delete}
			if data.Set == nil {
				data.Set = map[string]json.RawMessage{}
			}
			if data.Delete == nil {
				data.Delete = []string{}
			}
			// upstream: execute.ts:298-301 (no catch: an append failure rejects the tool with that error).
			if err := session.appendEntry(StoreEntryType, data); err != nil {
				return agent.AgentToolResult{}, err
			}
		}
		// pi extension: a returned value is appended like text().
		if result.Value != nil {
			items = append(items, ai.TextContent{Text: valueText(result.Value)})
		}
	} else {
		items = append(items, ai.TextContent{Text: "Script error:\n" + formatError(result.Error, calls)})
	}
	if generated := run.imagesGenerated(); generated > 0 && !slices.ContainsFunc(items, func(item ai.ToolResultMessageContent) bool {
		_, isImage := item.(ai.ImageContent)
		return isImage
	}) {
		plural := "s"
		if generated == 1 {
			plural = ""
		}
		items = append(items, ai.TextContent{Text: fmt.Sprintf("Note: models.generateImages() returned %d image%s that the script did not show. Show each image block of result.output with image(block).", generated, plural)})
	}

	maxTokens := int64(defaultMaxOutputTokens)
	if parsed.Options.MaxOutputTokens != nil {
		maxTokens = *parsed.Options.MaxOutputTokens
	}
	truncated, fullOutputPath := truncateOutput(items, maxTokens)
	// After truncation, which joins the text items and moves images after them, so each path stays next to its image
	// and is never cut.
	output, err := saveImages(truncated)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	title := "Script failed"
	if result.OK {
		title = "Script completed"
	}
	header := title + "\nWall time " + jsstring.ToFixed(time.Since(startedAt).Seconds(), 1) + " seconds\nOutput:\n"
	details := ToolDetails{Calls: calls, FullOutputPath: fullOutputPath}
	return agent.AgentToolResult{
		Content: append([]ai.ToolResultMessageContent{ai.TextContent{Text: header}}, output...),
		Details: details,
		IsError: !result.OK,
		Usage:   run.modelUsage,
	}, nil
}
