package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/internal/nodebase64"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	sandbox "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/ecmascript"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
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
	// defaultMaxCodemodeWorkers bounds the codemode VMs alive across calls and sessions. It allows one replacement while a
	// cancelled VM is still exiting. PI_CODEMODE_MAX_WORKERS replaces it.
	defaultMaxCodemodeWorkers = 2
	// workerWait is how long a script waits for a free worker before failing. It bounds the wait when every worker is held by
	// a script that waits on another script, for example an in-process subagent that uses codemode.
	workerWait = 60 * time.Second
)

// maxCodemodeWorkers is PI_CODEMODE_MAX_WORKERS if it is a positive integer, else the default. It is read on every call so a host can
// set it at runtime. Ports execute.ts maxCodemodeWorkers: `Number.isInteger(value) && value > 0` over `Number(process.env.X)`, which reads
// the empty string as 0 and an unset variable as NaN.
func maxCodemodeWorkers() float64 {
	//perf:ok Pi reads the variable per call so SDK hosts can change it at runtime; this runs once per script start and admission pass, not per event
	value := jsnumber.Parse(os.Getenv("PI_CODEMODE_MAX_WORKERS"))
	if value > 0 && value == math.Trunc(value) && !math.IsInf(value, 0) {
		return value
	}
	return defaultMaxCodemodeWorkers
}

// codemodeWorkers is the admission shared by every codemode call in the process.
var codemodeWorkers = newWorkerAdmission()

// workerAdmission counts live codemode VMs and queues the scripts that wait for one, in arrival order. A VM holds its slot until
// it actually exits, not until its result is returned.
//
// Ports packages/coding-agent/src/extensions/codemode/execute.ts (activeCodemodeWorkers, codemodeWorkerWaiters,
// reserveCodemodeWorker, admitCodemodeWorkers, releaseCodemodeWorker).
type workerAdmission struct {
	mu     sync.Mutex
	active int
	// waiters are the scripts waiting for a slot, in arrival order.
	waiters []*workerWaiter
	// idle broadcasts, with mu held, when the last live VM has exited.
	idle *sync.Cond
}

// workerWaiter is one script waiting for a slot. admit closes granted after it counts the slot, both with mu held.
type workerWaiter struct {
	granted chan struct{}
	isGrant bool
}

func newWorkerAdmission() *workerAdmission {
	a := &workerAdmission{}
	a.idle = sync.NewCond(&a.mu)
	return a
}

// workerOutcome is why reserve returned.
type workerOutcome int

const (
	workerReserved workerOutcome = iota
	workerAborted
	workerTimedOut
)

func (o workerOutcome) String() string {
	return [...]string{"reserved", "aborted", "timeout"}[o]
}

// reserve takes one worker slot, waiting in arrival order while all are taken. With workerReserved the caller owns the slot and
// must call release exactly once; otherwise the context ended or timeout passed first and no slot is held.
func (a *workerAdmission) reserve(ctx context.Context, timeout time.Duration) (release func(), outcome workerOutcome) {
	if ctx.Err() != nil {
		return nil, workerAborted
	}
	w := &workerWaiter{granted: make(chan struct{})}
	a.mu.Lock()
	a.waiters = append(a.waiters, w)
	a.admit()
	a.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.granted:
		return a.release, workerReserved
	case <-ctx.Done():
		outcome = workerAborted
	case <-timer.C:
		outcome = workerTimedOut
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if w.isGrant {
		// The slot was granted as the wait ended; the wait ended first, so give the slot to the next waiter.
		a.active--
		a.admit()
		a.broadcastIfIdle()
		return nil, outcome
	}
	if i := slices.Index(a.waiters, w); i >= 0 {
		a.waiters = slices.Delete(a.waiters, i, i+1)
	}
	return nil, outcome
}

// admit gives free slots to waiting scripts in arrival order. The caller holds mu.
func (a *workerAdmission) admit() {
	for len(a.waiters) > 0 && float64(a.active) < maxCodemodeWorkers() {
		w := a.waiters[0]
		a.waiters = slices.Delete(a.waiters, 0, 1)
		a.active++
		w.isGrant = true
		//portlint:allow doubleclose the caller holds mu and the waiter left a.waiters on the line above, so no later pass reaches it and granted is closed once
		close(w.granted)
	}
}

func (a *workerAdmission) broadcastIfIdle() {
	if a.active == 0 {
		a.idle.Broadcast()
	}
}

func (a *workerAdmission) release() {
	a.mu.Lock()
	a.active--
	a.admit()
	a.broadcastIfIdle()
	a.mu.Unlock()
}

// workersStarted counts the reservations handed to a worker, one per script whose VM was admitted. The worker admission tests read it to tell a
// script that started a VM from one that did not.
var workersStarted atomic.Int64

// workerReservation is the one slot a script reserved. It covers the one worker the script starts: acquireWorker hands the slot to the
// worker, which gives it back when it exits, and releaseUnused gives it back when no worker took it. A launch that resumes after
// the script's result settled finds it released and starts no worker.
//
// Ports execute.ts's `reserved` flag and acquireWorker.
type workerReservation struct {
	reserved atomic.Bool
	release  func()
}

func newWorkerReservation(release func()) *workerReservation {
	r := &workerReservation{release: release}
	r.reserved.Store(true)
	return r
}

// acquireWorker is the sandbox's AcquireWorker. From here the returned release frees the slot, once, when the worker actually exits.
func (r *workerReservation) acquireWorker() (func(), error) {
	if !r.reserved.CompareAndSwap(true, false) {
		return nil, errors.New("The script already settled")
	}
	workersStarted.Add(1)
	var once sync.Once
	return func() { once.Do(r.release) }, nil
}

// releaseUnused frees the slot unless a worker took it.
func (r *workerReservation) releaseUnused() {
	if r.reserved.CompareAndSwap(true, false) {
		r.release()
	}
}

// StoreEntryType is the custom entry type holding one script's `store()` writes: StoreEntryData.
const StoreEntryType = "codemode-store"

// StoreEntryData is the data of a codemode-store entry.
type StoreEntryData struct {
	Set    map[string]json.RawMessage `json:"set"`
	Delete []string                   `json:"delete"`
	// Keys is the order MarshalJSON writes Set's keys in (StoreWrites.SetKeys); keys it lacks follow in JavaScript enumeration order.
	Keys []string `json:"-"`
}

// MarshalJSON writes the keys with their UTF-16 units, so an unpaired surrogate in a key reaches the entry as its \u escape rather than as U+FFFD.
func (d StoreEntryData) MarshalJSON() ([]byte, error) {
	set := []byte{'{'}
	for i, key := range sandbox.OrderStoreKeys(d.Set, d.Keys) {
		name, err := ecmascript.String(key).MarshalJSON()
		if err != nil {
			return nil, err
		}
		if i > 0 {
			set = append(set, ',')
		}
		set = append(append(append(set, name...), ':'), d.Set[key]...)
	}
	deleted := make([]ecmascript.String, len(d.Delete))
	for i, key := range d.Delete {
		deleted[i] = ecmascript.String(key)
	}
	encoded, err := json.Marshal(deleted)
	if err != nil {
		return nil, err
	}
	out := append([]byte(`{"set":`), set...)
	out = append(out, `},"delete":`...)
	out = append(out, encoded...)
	return append(out, '}'), nil
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
	return jsstring.Slice(text, 0, maxChars-3) + "..."
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
	result := outcome.Result
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

// valueText is like the script's `text()`: strings as is, other values (null included) as compact JSON.
//
// upstream: execute.ts:251-254 valueText.
func valueText(value json.RawMessage) string {
	var text string
	// sdkjson keeps an unpaired surrogate escape as WTF-8, as it stays a lone unit in a JavaScript string; encoding/json would make it U+FFFD.
	if len(value) > 0 && value[0] == '"' && sdkjson.Unmarshal(value, &text) == nil {
		return text
	}
	return string(value)
}

// storeEntryData is the data of a codemode-store entry when it passes upstream's isStoreEntryData: `set` is a non-null
// object (an array applies its indexes as keys, as Object.entries does) and `delete` is an array of strings.
func storeEntryData(raw json.RawMessage) (set map[string]json.RawMessage, deleted []string, ok bool) {
	var fields struct {
		Set    json.RawMessage   `json:"set"`
		Delete []json.RawMessage `json:"delete"`
	}
	if json.Unmarshal(raw, &fields) != nil || fields.Delete == nil {
		return nil, nil, false
	}
	deleted = make([]string, len(fields.Delete))
	for i, member := range fields.Delete {
		var key ecmascript.String
		if len(member) == 0 || member[0] != '"' || key.UnmarshalJSON(member) != nil {
			return nil, nil, false
		}
		deleted[i] = string(key)
	}
	if set, ok := decodeObjectMembers(fields.Set); ok {
		return set, deleted, true
	}
	var items []json.RawMessage
	if json.Unmarshal(fields.Set, &items) != nil || items == nil {
		return nil, nil, false
	}
	set = make(map[string]json.RawMessage, len(items))
	for i, item := range items {
		set[strconv.Itoa(i)] = item
	}
	return set, deleted, true
}

// decodeObjectMembers reads the members of a JSON object, keeping the code units of a key as JSON.parse does: an unpaired surrogate escape stays
// distinct from every other (WTF-8 in the Go key) instead of becoming U+FFFD. A repeated key keeps its last value.
func decodeObjectMembers(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	members := map[string]json.RawMessage{}
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return nil, false
		}
		var key ecmascript.String
		if key.UnmarshalJSON(bytes.TrimLeft(raw[start:decoder.InputOffset()], ", \t\r\n")) != nil {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		members[string(key)] = value
	}
	return members, true
}

// storeOf reads the values of `load()`: the `codemode-store` entries on the branch, applied from the root. Entries
// with malformed data are ignored.
func storeOf(branch []codingagent.SessionEntry) map[string]json.RawMessage {
	store := map[string]json.RawMessage{}
	for _, entry := range branch {
		if entry.Base().Type != "custom" {
			continue
		}
		var fields struct {
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
		}
		raw, err := json.Marshal(entry)
		if err != nil || json.Unmarshal(raw, &fields) != nil || fields.CustomType != StoreEntryType {
			continue
		}
		set, deleted, ok := storeEntryData(fields.Data)
		if !ok {
			continue
		}
		for _, key := range deleted {
			delete(store, key)
		}
		maps.Copy(store, set)
	}
	return store
}

// session is the part of the session a tool reaches through its context: the branch to read `load()` values from and
// the entry writer for `store()`.
//
// upstream: codemode/execute.ts:285 (`ctx.sessionManager.getBranch()`), 301 (`options.appendEntry`, which index.ts:35 binds to `pi.appendEntry`).
type session struct {
	manager extension.ReadonlySessionManager
	tool    *extension.ToolContext
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
	return &session{manager: manager, tool: tc}
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
			data := nodebase64.Decode(image.Data)
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

// formatOutput lays out the script's output so the model can tell items apart: providers join adjacent text blocks with
// a newline or with nothing. With more than one text item (`text()` or the returned value), each starts with a
// `==> text N/M <==` line. `console.*` lines follow all other output in one `<console_output>` block.
//
// upstream: execute.ts formatOutput
func formatOutput(output []sandbox.OutputItem) []ai.ToolResultMessageContent {
	total := 0
	for _, item := range output {
		if item.Type == sandbox.OutputItemText && !item.Console {
			total++
		}
	}
	items := make([]ai.ToolResultMessageContent, 0, len(output)+1)
	var consoleLines []string
	index := 0
	for _, item := range output {
		switch {
		case item.Type != sandbox.OutputItemText:
			items = append(items, ai.ImageContent{Data: item.Data, MimeType: item.MimeType})
		case item.Console:
			consoleLines = append(consoleLines, item.Text)
		default:
			index++
			text := item.Text
			if total > 1 {
				text = fmt.Sprintf("==> text %d/%d <==\n%s", index, total, item.Text)
			}
			items = append(items, ai.TextContent{Text: text})
		}
	}
	if len(consoleLines) > 0 {
		items = append(items, ai.TextContent{Text: "<console_output>\n" + strings.Join(consoleLines, "\n") + "\n</console_output>"})
	}
	return items
}

// joinAdjacentText joins adjacent text items into one, each part starting on its own line, in one pass per run of text items as
// Pi 1.2.0 does (Pi #10678).
//
// upstream: execute.ts joinAdjacentText
func joinAdjacentText(items []ai.ToolResultMessageContent) []ai.ToolResultMessageContent {
	joined := make([]ai.ToolResultMessageContent, 0, len(items))
	var run strings.Builder
	inRun := false
	flush := func() {
		if inRun {
			joined = append(joined, ai.TextContent{Text: run.String()})
			run.Reset()
			inRun = false
		}
	}
	for _, item := range items {
		text, isText := item.(ai.TextContent)
		if !isText {
			flush()
			joined = append(joined, item)
			continue
		}
		if !inRun {
			inRun = true
			run.Grow(len(text.Text))
			run.WriteString(text.Text)
			continue
		}
		if all := run.String(); all != "" && !strings.HasSuffix(all, "\n") {
			run.WriteByte('\n')
		}
		run.WriteString(text.Text)
	}
	flush()
	return joined
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
	// Show the script as running before its first nested call, so the row's timer starts.
	run.publish()

	var callable []extension.AgentTool
	if tc != nil {
		tools, err := tc.Tools()
		if err != nil {
			return agent.AgentToolResult{}, err
		}
		callable = callableTools(tools)
	}
	// ALL_TOOLS entries carry the declaration.
	// upstream: index.ts getToolGuidelines reads pi.getAllTools().
	guidelines := map[string][]string{}
	if tc != nil {
		for _, info := range tc.GetAllTools() {
			guidelines[info.Name] = info.PromptGuidelines
		}
	}
	samples := make(map[string]string, len(callable))
	for _, tool := range callable {
		sample, err := sandbox.RenderToolSample(toDeclaration(tool, guidelines[tool.Name]), sandbox.ToolRenderOptions{})
		if err != nil {
			return agent.AgentToolResult{}, err
		}
		samples[tool.Name] = sample
	}
	sandboxTools := make([]sandbox.Tool, len(callable))
	for i, tool := range callable {
		sandboxTools[i] = sandbox.Tool{Name: tool.Name, Description: samples[tool.Name], Execute: run.nestedCall(tool), AwaitsInitiation: true}
	}
	deadline := math.Inf(1)
	if parsed.Options.TimeoutMs != nil {
		deadline = float64(*parsed.Options.TimeoutMs)
	}
	globals := discoveryGlobals(callable, samples, tc)
	if options.Models {
		if models := modelRuntimeOf(tc); models != nil {
			globals = append(globals, run.modelGlobals(models)...)
		}
	}

	// The wait for a worker counts toward the script's deadline.
	waitStartedAt := time.Now()
	wait := workerWait
	if limit := parsed.Options.TimeoutMs; limit != nil && *limit < workerWait.Milliseconds() {
		wait = time.Duration(*limit) * time.Millisecond
	}
	release, admission := codemodeWorkers.reserve(ctx, wait)
	waitedMs := float64(time.Since(waitStartedAt)) / float64(time.Millisecond)
	if admission != workerReserved {
		return run.workerUnavailable(ctx, admission, deadline, startedAt), nil
	}
	// The reservation covers the one worker this script starts. A launch that resumes after the result settled finds it
	// released and starts no worker.
	reservation := newWorkerReservation(release)
	box, err := sandbox.NewSandbox(sandbox.SandboxOptions{
		Tools:            sandboxTools,
		Globals:          globals,
		TimeoutMs:        math.Max(1, math.Round(deadline-waitedMs)),
		MemoryLimitBytes: memoryLimitBytes,
		CacheDir:         options.CacheDir,
		AcquireWorker:    reservation.acquireWorker,
	})
	if err != nil {
		reservation.releaseUnused()
		return agent.AgentToolResult{}, err
	}
	defer func() {
		_ = box.Close()
		reservation.releaseUnused()
	}()

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

	scriptOutput := slices.Clone(result.Output)
	if result.OK {
		writes := result.StoreWrites
		if session := sessionOf(tc); session != nil && (len(writes.Set) > 0 || len(writes.Delete) > 0) {
			data := StoreEntryData{Set: writes.Set, Delete: writes.Delete, Keys: writes.SetKeys()}
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
			scriptOutput = append(scriptOutput, sandbox.OutputItem{Type: sandbox.OutputItemText, Text: valueText(result.Value)})
		}
	}
	items := formatOutput(scriptOutput)
	if !result.OK {
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
	truncated, fullOutputPath := truncateOutput(joinAdjacentText(items), maxTokens)
	// After truncation, which joins the text items and moves images after them, so each path stays next to its image
	// and is never cut.
	saved, err := saveImages(truncated)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	output := joinAdjacentText(saved)
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

// workerUnavailable is the result of a script that got no worker, because it was aborted while it waited or its deadline or the
// wait limit passed first. The script did not start.
func (r *run) workerUnavailable(ctx context.Context, admission workerOutcome, deadline float64, startedAt time.Time) agent.AgentToolResult {
	var failure string
	switch {
	case admission == workerAborted:
		failure = "Script aborted: " + abortReason(ctx)
	case deadline <= float64(workerWait/time.Millisecond):
		failure = "Script timed out: Execution timed out after " + jsnumber.String(deadline) + " ms while waiting for a free codemode worker"
	default:
		failure = fmt.Sprintf("No codemode worker became free within %d seconds. All %s workers, shared by every session in this process, are held by other scripts. Retry after they finish.", workerWait/time.Second, jsnumber.String(maxCodemodeWorkers()))
	}
	waited := jsstring.ToFixed(time.Since(startedAt).Seconds(), 1)
	return agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{
			ai.TextContent{Text: "Script failed\nWall time " + waited + " seconds\nOutput:\n"},
			ai.TextContent{Text: "Script error:\n" + failure + "\n\nThe script did not start."},
		},
		Details: ToolDetails{Calls: r.snapshot()},
		IsError: true,
	}
}

// abortReason is the message of an abort, as the sandbox words it: the context's cause, with Node's text for a plain abort
// and for a deadline. Ports execute.ts `reason instanceof Error ? reason.message : "Execution aborted"` for the abort
// signal's reason.
func abortReason(ctx context.Context) string {
	cause := context.Cause(ctx)
	switch {
	case cause == nil || errors.Is(cause, context.Canceled):
		return "This operation was aborted"
	case errors.Is(cause, context.DeadlineExceeded):
		return "The operation was aborted due to timeout"
	}
	return cause.Error()
}
