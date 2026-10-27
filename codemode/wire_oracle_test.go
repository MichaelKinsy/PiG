package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// The pinned Pi runtime (serveCodemodeRemote, RemoteCodemodeSandbox, InlineCodemodeSandbox, CodemodeSandboxBase.start) and PiG run
// the same cases, and what each does at its seams must be equal: the frames of the remote exchange (the VM's messages and the
// client's replies, protocol.ts and remote.ts), the requests a remote sandbox sends, the events and refusals of a deterministic
// run (execution.ts), and the outcomes. testdata/wire_oracle.mjs is the Pi side; its toolBehavior and replyFor hold the same tool
// table as newWireTool and replyFor below. Every case runs twice: on PiG's prelude, whose fingerprint differs from Pi's by the prelude
// hash alone (divergence D145 patches the prelude), and on Pi's own prelude, which the oracle returns, where nothing may differ.

// wireTool is a tool or global of a case: its behavior is named by Kind, as in testdata/wire_oracle.mjs toolBehavior.
type wireTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Spread      bool   `json:"spread,omitempty"`
	Kind        string `json:"kind"`
}

type wireDeclared struct {
	Name        string `json:"name"`
	JSName      string `json:"jsName,omitempty"`
	Description string `json:"description,omitempty"`
}

type wireDeclaredGlobal struct {
	Name   string `json:"name"`
	Spread bool   `json:"spread,omitempty"`
}

type wireOutcome struct {
	OK         bool            `json:"ok"`
	Value      json.RawMessage `json:"value,omitempty"`
	Message    string          `json:"message,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

type wireAttempt struct {
	Seq     int         `json:"seq"`
	Now     any         `json:"now"`
	ID      int         `json:"id"`
	Outcome wireOutcome `json:"outcome"`
}

type wireCase struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
	Code string `json:"code,omitempty"`
	// serve
	Data                *VmStartData      `json:"data,omitempty"`
	MaxInterruptBudget  *int              `json:"maxInterruptBudget,omitempty"`
	MaxMemoryLimitBytes *uint32           `json:"maxMemoryLimitBytes,omitempty"`
	Replies             map[string]string `json:"replies,omitempty"`
	Reverse             bool              `json:"reverse,omitempty"`
	// Shape makes the client break the protocol: badSeq, dupSeq, dupEarly, omitSeq, omitNow or pastNow (testdata/wire_oracle.mjs).
	Shape      string          `json:"shape,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
	// serve (request), client, inline, start
	InterruptBudget  *int `json:"interruptBudget,omitempty"`
	MemoryLimitBytes uint32
	// client, inline
	Tools   []wireTool                 `json:"tools,omitempty"`
	Globals []wireTool                 `json:"globals,omitempty"`
	Store   map[string]json.RawMessage `json:"store,omitempty"`
	// start
	StartedAt   float64              `json:"startedAt"`
	Seed        uint32               `json:"seed"`
	DeclTools   []wireDeclared       `json:"declTools,omitempty"`
	DeclGlobals []wireDeclaredGlobal `json:"declGlobals,omitempty"`
	Attempts    []wireAttempt        `json:"attempts,omitempty"`
	// NoAnswer leaves the answering to the attempts: the run's own answer is not sent.
	NoAnswer bool `json:"noAnswer,omitempty"`
	// validate
	GlobalNames []string `json:"globalNames,omitempty"`
	ToolName    string   `json:"toolName,omitempty"`
	// Steps of a bridge case: what the stand-in VM sends and waits for. Control runs it as a deterministic run.
	Steps   []bridgeStep `json:"steps,omitempty"`
	Control bool         `json:"control,omitempty"`
	// Exchanges of a remote case: what the stand-in remote sends, one after another. Throws makes its Execute fail at the end.
	Exchanges []remoteExchange `json:"exchanges,omitempty"`
	Throws    bool             `json:"throws,omitempty"`
	// BadWasm makes the serve case's wasm module fail to load.
	BadWasm bool `json:"badWasm,omitempty"`
	// CloseOnCall closes the client's sandbox when the first call arrives.
	CloseOnCall bool `json:"closeOnCall,omitempty"`
	// Backend of a start case: "remote" runs the VM behind a remote sandbox, anything else inline.
	Backend string `json:"backend,omitempty"`
	// prelude replaces the prelude the VM runs, when set: Pi's own, which PiG's differs from by D145.
	prelude string
}

// MarshalJSON leaves out a zero MemoryLimitBytes: `undefined` for Pi.
func (c wireCase) MarshalJSON() ([]byte, error) {
	type plain wireCase
	raw, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	delete(fields, "MemoryLimitBytes")
	if c.MemoryLimitBytes != 0 {
		fields["memoryLimitBytes"], _ = json.Marshal(c.MemoryLimitBytes)
	}
	return json.Marshal(fields)
}

// wireRecord is what a case records. The Go side builds the same JSON as the Pi side.
type wireRecord map[string]any

func newWireTool(spec wireTool) Tool {
	name := spec.Name
	tool := Tool{Name: name, Description: spec.Description, Spread: spec.Spread}
	switch spec.Kind {
	case "echo":
		tool.Execute = func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
			text := "null"
			if args != nil {
				text = string(args)
			}
			quoted, _ := json.Marshal(name)
			return json.RawMessage(`{"tool":` + string(quoted) + `,"args":` + text + `}`), nil
		}
	case "fail":
		tool.Execute = func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("failing %s", name)
		}
	case "undef":
		tool.Execute = func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	case "number":
		tool.Execute = func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage("42"), nil }
	case "hang":
		tool.Execute = func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
	default:
		panic("unknown tool kind " + spec.Kind)
	}
	return tool
}

func newWireTools(specs []wireTool) []Tool {
	var tools []Tool
	for _, spec := range specs {
		tools = append(tools, newWireTool(spec))
	}
	return tools
}

// canonical is a value as `any` decoded from its JSON, so key order and number spelling do not matter.
func canonical(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// jsText is a string of the VM as JSON carries it: an unpaired surrogate (WTF-8 in a Go string) is one U+FFFD, as Pi's output
// reads once it crossed JSON into this test.
func jsText(text string) string { return string(utf16.Decode(jsstring.ToUTF16(text))) }

func rawJSON(raw json.RawMessage) any {
	if raw == nil {
		return map[string]any{"undefined": true}
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return "undecodable:" + string(raw)
	}
	return out
}

// resultRecord is the result as testdata/wire_oracle.mjs resultJson records it.
func resultRecord(result Result) map[string]any {
	record := map[string]any{}
	record["ok"] = result.OK
	if result.OK {
		record["value"] = rawJSON(result.Value)
		set := map[string]any{}
		for key, value := range result.StoreWrites.Set {
			set[jsText(key)] = rawJSON(value)
		}
		deleted := []any{}
		for _, key := range result.StoreWrites.Delete {
			deleted = append(deleted, jsText(key))
		}
		record["storeWrites"] = map[string]any{"set": set, "delete": deleted}
	} else {
		failure := map[string]any{"kind": string(result.Error.Kind), "message": jsText(result.Error.Message)}
		if result.Error.Name != "" {
			failure["name"] = jsText(result.Error.Name)
		}
		if result.Error.Stack != "" {
			failure["stack"] = jsText(result.Error.Stack)
		}
		record["error"] = failure
	}
	output := []any{}
	for _, item := range result.Output {
		entry := map[string]any{"type": string(item.Type)}
		if item.Type == OutputItemText {
			entry["text"] = jsText(item.Text)
			if item.Console {
				entry["console"] = true
			}
		} else {
			entry["data"], entry["mimeType"] = item.Data, item.MimeType
		}
		output = append(output, entry)
	}
	record["output"] = output
	calls := []any{}
	for _, call := range result.Calls {
		calls = append(calls, map[string]any{"name": call.Name, "status": string(call.Status)})
	}
	record["calls"] = calls
	if result.During != nil {
		record["during"] = result.During
	}
	return record
}

// replyFor is the client's reply to one call of a serve case, the table of testdata/wire_oracle.mjs replyFor.
func replyFor(kind string, call RemoteMessage, seq int, deterministic *VmDeterministic, properties json.RawMessage, shape string) *RemoteReply {
	if kind == "stop" {
		return &RemoteReply{HostToWorkerMessage: HostToWorkerMessage{Type: MessageStop}}
	}
	reply := HostToWorkerMessage{Type: MessageResult, ID: call.ID, Ok: true}
	switch kind {
	case "fail":
		reply.Ok = false
		payload := "failing " + call.Name
		reply.Payload = &payload
	case "undef":
	case "number":
		payload := "42"
		reply.Payload = &payload
	default:
		args := "null"
		if call.Args != nil {
			args = *call.Args
		}
		quoted, _ := json.Marshal(call.Name)
		var parsed any
		_ = json.Unmarshal([]byte(args), &parsed)
		compact, _ := json.Marshal(parsed)
		payload := `{"tool":` + string(quoted) + `,"args":` + string(compact) + `}`
		reply.Payload = &payload
	}
	if deterministic != nil {
		now := deterministic.StartedAt + float64(seq)*1000
		reply.Seq, reply.Now = &seq, &now
		switch shape {
		case "badSeq":
			zero := 0
			reply.Seq = &zero
		case "dupSeq":
			one := 1
			reply.Seq = &one
		case "dupEarly":
			two := 2
			reply.Seq = &two
		case "omitSeq":
			reply.Seq = nil
		case "omitNow":
			reply.Now = nil
		case "pastNow":
			past := deterministic.StartedAt - float64(seq)*5000
			reply.Now = &past
		}
		if kind == "fail" && properties != nil {
			text := string(properties)
			reply.Properties = &text
		}
	}
	return &RemoteReply{HostToWorkerMessage: reply}
}

type wireFrame struct {
	Messages any `json:"messages"`
	Reply    any `json:"reply"`
}

// serve runs ServeRemote, with the prelude of the case when it names one.
func (c wireCase) serve(ctx context.Context, request RemoteRequest, exchange RemoteExchange) {
	options := c.serveOptions()
	if c.BadWasm {
		options.Wasm = func() ([]byte, error) { return nil, errors.New("no module") }
	}
	if c.prelude == "" {
		ServeRemote(ctx, request, exchange, options)
		return
	}
	serveRemote(ctx, request, exchange, options, func(budget int) vmLauncher {
		return inlineLauncher(wasmEngineLoader(options.Wasm, ""), c.prelude, budget)
	})
}

// wireContext bounds a case: one that does not settle fails against what Pi recorded instead of hanging the test.
func wireContext(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithTimeout(t.Context(), 60*time.Second)
}

func (c wireCase) serveOptions() ServeOptions {
	options := ServeOptions{}
	if c.MaxInterruptBudget != nil {
		options.MaxInterruptBudget = *c.MaxInterruptBudget
	}
	if c.MaxMemoryLimitBytes != nil {
		options.MaxMemoryLimitBytes = *c.MaxMemoryLimitBytes
	}
	return options
}

// recordingExchange answers the calls of a serve case with its reply table and records every frame.
type recordingExchange struct {
	t      *testing.T
	spec   wireCase
	mu     sync.Mutex
	frames []*wireFrame
	seq    int
	held   func()
}

func (r *recordingExchange) exchange(_ context.Context, messages []RemoteMessage, reply func(*RemoteReply, error)) {
	r.mu.Lock()
	frame := &wireFrame{Messages: canonical(r.t, messages), Reply: "none"}
	r.frames = append(r.frames, frame)
	var call *RemoteMessage
	for i := range messages {
		if messages[i].Type == MessageCall {
			call = &messages[i]
			break
		}
	}
	if call == nil {
		frame.Reply = nil
		r.mu.Unlock()
		reply(nil, nil)
		return
	}
	kind := r.spec.Replies[call.Name]
	if kind == "" {
		kind = "echo"
	}
	if kind == "reject" {
		frame.Reply = "rejected"
		r.mu.Unlock()
		reply(nil, fmt.Errorf("the exchange failed"))
		return
	}
	var deterministic *VmDeterministic
	if r.spec.Data != nil {
		deterministic = r.spec.Data.Deterministic
	}
	if deterministic != nil {
		r.seq++
	}
	answer := replyFor(kind, *call, r.seq, deterministic, r.spec.Properties, r.spec.Shape)
	answered := canonical(r.t, answer)
	if r.spec.Reverse {
		if r.held == nil {
			r.held = func() {
				r.mu.Lock()
				frame.Reply = answered
				r.mu.Unlock()
				reply(answer, nil)
			}
			r.mu.Unlock()
			return
		}
		first := r.held
		frame.Reply = answered
		r.mu.Unlock()
		reply(answer, nil)
		first()
		return
	}
	frame.Reply = answered
	r.mu.Unlock()
	reply(answer, nil)
}

func (c wireCase) runServe(t *testing.T) wireRecord {
	exchange := &recordingExchange{t: t, spec: c}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	request := RemoteRequest{Data: *c.Data}
	if c.InterruptBudget != nil {
		request.InterruptBudget = *c.InterruptBudget
	}
	c.serve(ctx, request, exchange.exchange)
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	return wireRecord{"returned": ctx.Err() == nil, "frames": canonical(t, exchange.frames)}
}

func (c wireCase) runClient(t *testing.T) wireRecord {
	ctx, cancel := wireContext(t)
	defer cancel()
	var mu sync.Mutex
	requests := []any{}
	frames := []*wireFrame{}
	var sandbox *RemoteSandbox
	remote := remoteFunc(func(ctx context.Context, request RemoteRequest, exchange RemoteExchange) error {
		mu.Lock()
		requests = append(requests, canonical(t, request))
		mu.Unlock()
		recording := func(ctx context.Context, messages []RemoteMessage, reply func(*RemoteReply, error)) {
			frame := &wireFrame{Messages: canonical(t, messages), Reply: "none"}
			mu.Lock()
			frames = append(frames, frame)
			mu.Unlock()
			exchange(ctx, messages, func(answer *RemoteReply, err error) {
				mu.Lock()
				switch {
				case err != nil:
					frame.Reply = "rejected"
				case answer == nil:
					frame.Reply = nil
				default:
					frame.Reply = canonical(t, answer)
				}
				mu.Unlock()
				reply(answer, err)
			})
			// Pi closes from a microtask queued before it awaits the exchange (wire_oracle.mjs), so the host has handled the call, and
			// recorded it, when the close runs. Here the host handles it when exchange returns: a close started before that can end
			// the execution first, which then has no record of the call. Close waits for this very Execute to return, so it runs beside it.
			if c.CloseOnCall {
				for _, message := range messages {
					if message.Type == MessageCall {
						go func() { _ = sandbox.Close() }()
						break
					}
				}
			}
		}
		c.serve(ctx, request, recording)
		return nil
	})
	options := RemoteSandboxOptions{
		Remote: func() (Remote, error) { return remote, nil }, Tools: newWireTools(c.Tools), Globals: newWireTools(c.Globals),
		MemoryLimitBytes: c.MemoryLimitBytes,
	}
	if c.InterruptBudget != nil {
		options.InterruptBudget = *c.InterruptBudget
	}
	var err error
	sandbox, err = NewRemoteSandbox(options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sandbox.Execute(ctx, c.Code, ExecuteOptions{Store: c.Store})
	if err != nil {
		t.Fatal(err)
	}
	_ = sandbox.Close()
	mu.Lock()
	defer mu.Unlock()
	return wireRecord{"requests": requests, "frames": canonical(t, frames), "result": canonical(t, resultRecord(result))}
}

func (c wireCase) inlineSandbox(t *testing.T) *InlineSandbox {
	options := InlineSandboxOptions{SandboxOptions: SandboxOptions{Tools: newWireTools(c.Tools), Globals: newWireTools(c.Globals), MemoryLimitBytes: c.MemoryLimitBytes}}
	if c.InterruptBudget != nil {
		options.InterruptBudget = *c.InterruptBudget
	}
	if c.prelude == "" {
		sandbox, err := NewInlineSandbox(options)
		if err != nil {
			t.Fatal(err)
		}
		return sandbox
	}
	// The same sandbox NewInlineSandbox builds, with the prelude the case names.
	budget := options.InterruptBudget
	if budget == 0 {
		budget = DefaultInterruptBudget
	}
	sandbox, err := newSandbox(options.SandboxOptions, func(s *Sandbox) vmLauncher {
		s.prelude = c.prelude
		return inlineLauncher(s.engineFor, s.prelude, budget)
	})
	if err != nil {
		t.Fatal(err)
	}
	return &InlineSandbox{sandbox}
}

func (c wireCase) runInline(t *testing.T) wireRecord {
	ctx, cancel := wireContext(t)
	defer cancel()
	sandbox := c.inlineSandbox(t)
	defer func() { _ = sandbox.Close() }()
	result, err := sandbox.Execute(ctx, c.Code, ExecuteOptions{Store: c.Store})
	if err != nil {
		t.Fatal(err)
	}
	return wireRecord{"result": canonical(t, resultRecord(result))}
}

func (c wireCase) runStart(t *testing.T) wireRecord {
	ctx, cancel := wireContext(t)
	defer cancel()
	var sandbox interface {
		Start(context.Context, string, RunOptions) (*Run, error)
		Close() error
	}
	if c.Backend == "remote" {
		options := RemoteSandboxOptions{Remote: func() (Remote, error) {
			return remoteFunc(func(ctx context.Context, request RemoteRequest, exchange RemoteExchange) error {
				c.serve(ctx, request, exchange)
				return nil
			}), nil
		}}
		if c.InterruptBudget != nil {
			options.InterruptBudget = *c.InterruptBudget
		}
		remote, err := NewRemoteSandbox(options)
		if err != nil {
			t.Fatal(err)
		}
		sandbox = remote
	} else {
		sandbox = c.inlineSandbox(t)
	}
	defer func() { _ = sandbox.Close() }()
	var mu sync.Mutex
	events := []any{}
	attempts := []any{}
	var run *Run
	attempted := false
	seq := 0
	options := RunOptions{StartedAt: c.StartedAt, Seed: c.Seed, Store: c.Store}
	for _, tool := range c.DeclTools {
		options.Tools = append(options.Tools, DeclaredTool(tool))
	}
	for _, global := range c.DeclGlobals {
		options.Globals = append(options.Globals, DeclaredGlobal(global))
	}
	started := make(chan struct{})
	options.OnStarted = func(fingerprint string) {
		mu.Lock()
		events = append(events, map[string]any{"started": fingerprint})
		mu.Unlock()
	}
	options.OnIdle = func(seq, issued int) {
		mu.Lock()
		events = append(events, map[string]any{"idle": []int{seq, issued}})
		mu.Unlock()
	}
	options.OnCall = func(call HostCall) {
		<-started
		mu.Lock()
		entry := map[string]any{"id": call.ID, "target": call.Target, "name": call.Name}
		if call.Args != nil {
			entry["args"] = rawJSON(call.Args)
		}
		events = append(events, map[string]any{"call": entry})
		first := !attempted
		attempted = true
		mu.Unlock()
		if first {
			for _, attempt := range c.Attempts {
				now := 0.0
				switch v := attempt.Now.(type) {
				case float64:
					now = v
				case string:
					now = map[string]float64{"NaN": nan(), "Infinity": inf()}[v]
				}
				outcome := Outcome{OK: attempt.Outcome.OK, Value: attempt.Outcome.Value, Message: attempt.Outcome.Message, Properties: attempt.Outcome.Properties}
				message := "ok"
				if err := run.Deliver(attempt.Seq, now, attempt.ID, outcome); err != nil {
					message = err.Error()
				}
				mu.Lock()
				attempts = append(attempts, message)
				mu.Unlock()
			}
		}
		if c.NoAnswer {
			return
		}
		kind := c.Replies[call.Name]
		if kind == "" {
			kind = "echo"
		}
		mu.Lock()
		seq++
		n := seq
		mu.Unlock()
		outcome := Outcome{OK: true}
		switch kind {
		case "fail":
			outcome = Outcome{Message: "failing " + call.Name, Properties: c.Properties}
		case "undef":
		case "number":
			outcome.Value = json.RawMessage("42")
		default:
			args := "null"
			if call.Args != nil {
				args = string(call.Args)
			}
			quoted, _ := json.Marshal(call.Name)
			outcome.Value = json.RawMessage(`{"tool":` + string(quoted) + `,"args":` + args + `}`)
		}
		go func() {
			if err := run.Deliver(n, c.StartedAt+float64(n)*1000, call.ID, outcome); err != nil {
				mu.Lock()
				attempts = append(attempts, "deliver: "+err.Error())
				mu.Unlock()
			}
		}()
	}
	var err error
	run, err = sandbox.Start(ctx, c.Code, options)
	if err != nil {
		t.Fatal(err)
	}
	close(started)
	result := run.Result()
	mu.Lock()
	defer mu.Unlock()
	return wireRecord{"events": canonical(t, events), "attempts": canonical(t, attempts), "finished": run.Finished(), "result": canonical(t, resultRecord(result))}
}

// runValidate records what building a sandbox and starting a run refuse, with the messages of execution.ts.
func (c wireCase) runValidate(t *testing.T) wireRecord {
	record := wireRecord{}
	message := func(err error) any {
		if err == nil {
			return nil
		}
		return err.Error()
	}
	var globals []Tool
	for _, name := range c.GlobalNames {
		globals = append(globals, Tool{Name: name, Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }})
	}
	sandbox, err := NewSandbox(SandboxOptions{Globals: globals})
	record["globals"] = message(err)
	if sandbox != nil {
		_ = sandbox.Close()
	}
	plain, err := NewSandbox(SandboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	toolName := c.ToolName
	if toolName == "" {
		toolName = "echo"
	}
	echo := Tool{Name: toolName, Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }}
	record["register"] = message(plain.RegisterTool(echo))
	record["registerAgain"] = message(plain.RegisterTool(echo))
	record["unregister"] = []bool{plain.UnregisterTool(toolName), plain.UnregisterTool(toolName)}
	record["reRegister"] = message(plain.RegisterTool(echo))
	options := RunOptions{StartedAt: c.StartedAt, Seed: c.Seed, OnCall: func(HostCall) {}}
	for _, tool := range c.DeclTools {
		options.Tools = append(options.Tools, DeclaredTool(tool))
	}
	for _, global := range c.DeclGlobals {
		options.Globals = append(options.Globals, DeclaredGlobal(global))
	}
	run, err := plain.Start(t.Context(), "return 1", options)
	record["start"] = message(err)
	if run != nil {
		run.Result()
	}
	return record
}

// remoteExchange is one exchange of a stand-in remote: the messages in the JSON of remote.ts. AfterStop makes the Go remote wait
// until the client stopped it before it sends them, where Pi's remote only sends them next.
type remoteExchange struct {
	Messages  []json.RawMessage `json:"messages"`
	AfterStop bool              `json:"afterStop,omitempty"`
}

func (c wireCase) runRemote(t *testing.T) wireRecord {
	ctx, cancel := wireContext(t)
	defer cancel()
	var mu sync.Mutex
	replies := []any{}
	remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
		for _, entry := range c.Exchanges {
			if entry.AfterStop {
				<-ctx.Done()
			}
			var messages []RemoteMessage
			for _, raw := range entry.Messages {
				var message RemoteMessage
				if err := json.Unmarshal(raw, &message); err != nil {
					t.Errorf("bad message %s: %v", raw, err)
					return nil
				}
				messages = append(messages, message)
			}
			reply, err := exchangeAndWait(context.Background(), exchange, messages...)
			if err != nil {
				t.Errorf("exchange: %v", err)
				return nil
			}
			mu.Lock()
			replies = append(replies, canonical(t, reply))
			mu.Unlock()
		}
		if c.Throws {
			return errors.New("remote exploded")
		}
		return nil
	})
	sandbox, err := NewRemoteSandbox(RemoteSandboxOptions{Remote: func() (Remote, error) { return remote, nil }, Tools: newWireTools(c.Tools), Globals: newWireTools(c.Globals)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := sandbox.Execute(ctx, c.Code, ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = sandbox.Close()
	mu.Lock()
	defer mu.Unlock()
	return wireRecord{"replies": replies, "result": canonical(t, resultRecord(result))}
}

// bridgeStep is a message the stand-in VM sends, in the JSON of protocol.ts, or the number of replies it waits for.
type bridgeStep struct {
	Send json.RawMessage `json:"send,omitempty"`
	Wait *int            `json:"wait,omitempty"`
}

// bridgeChannel is the stand-in VM of a bridge case.
type bridgeChannel struct {
	t      *testing.T
	steps  []bridgeStep
	events vmEvents
	mu     sync.Mutex
	posted []any
	wake   chan struct{}
}

func (c *bridgeChannel) start() {
	go func() {
		for _, step := range c.steps {
			if step.Send != nil {
				var message RemoteMessage
				if err := json.Unmarshal(step.Send, &message); err != nil {
					c.t.Errorf("bad message %s: %v", step.Send, err)
					return
				}
				if message.Type == MessageFailure {
					c.events.failure(message.Failure)
				} else {
					c.events.message(messageFromWire(message.WorkerToHostMessage))
				}
				continue
			}
			for {
				c.mu.Lock()
				done := len(c.posted) >= *step.Wait
				c.mu.Unlock()
				if done {
					break
				}
				<-c.wake
			}
		}
	}()
}

func (c *bridgeChannel) post(r callResult) {
	c.mu.Lock()
	c.posted = append(c.posted, canonical(c.t, replyToWire(r)))
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *bridgeChannel) stop() {}

func (c wireCase) runBridge(t *testing.T) wireRecord {
	ctx, cancel := wireContext(t)
	defer cancel()
	channel := &bridgeChannel{t: t, steps: c.Steps, wake: make(chan struct{}, 1)}
	sandbox, err := newSandbox(SandboxOptions{Tools: newWireTools(c.Tools), Globals: newWireTools(c.Globals)}, func(*Sandbox) vmLauncher {
		return func(_ context.Context, _ VmStartData, events vmEvents) (vmChannel, error) {
			channel.events = events
			return channel, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	var result Result
	if c.Control {
		options := RunOptions{OnCall: func(HostCall) {}}
		for _, tool := range c.DeclTools {
			options.Tools = append(options.Tools, DeclaredTool(tool))
		}
		run, err := sandbox.Start(ctx, c.Code, options)
		if err != nil {
			t.Fatal(err)
		}
		result = run.Result()
	} else if result, err = sandbox.Execute(ctx, c.Code, ExecuteOptions{}); err != nil {
		t.Fatal(err)
	}
	channel.mu.Lock()
	defer channel.mu.Unlock()
	posted := append([]any{}, channel.posted...)
	// The tools answer from goroutines: the order of the answers is not a contract, their content is.
	sort.Slice(posted, func(i, j int) bool {
		return posted[i].(map[string]any)["id"].(float64) < posted[j].(map[string]any)["id"].(float64)
	})
	return wireRecord{"posted": posted, "result": canonical(t, resultRecord(result))}
}

func (c wireCase) run(t *testing.T) wireRecord {
	switch c.Mode {
	case "serve":
		return c.runServe(t)
	case "client":
		return c.runClient(t)
	case "inline":
		return c.runInline(t)
	case "start":
		return c.runStart(t)
	case "validate":
		return c.runValidate(t)
	case "bridge":
		return c.runBridge(t)
	case "remote":
		return c.runRemote(t)
	}
	panic("unknown mode " + c.Mode)
}

func nan() float64 { var zero float64; return zero / zero }
func inf() float64 { one, zero := 1.0, 0.0; return one / zero }

func wireData(code string, tools []VmStartTool, globals []VmStartGlobal) *VmStartData {
	if tools == nil {
		tools = []VmStartTool{}
	}
	if globals == nil {
		globals = []VmStartGlobal{}
	}
	return &VmStartData{Code: code, Tools: tools, Globals: globals, Store: map[string]string{}}
}

func wireCases() []wireCase {
	echo := []VmStartTool{{Name: "echo", JSName: "echo", Description: "echoes"}}
	allTools := []VmStartTool{{Name: "echo", JSName: "echo"}, {Name: "fail", JSName: "fail"}, {Name: "undef", JSName: "undef"}, {Name: "number", JSName: "number"}}
	deterministic := &VmDeterministic{StartedAt: 1_700_000_000_000, Seed: 42}
	budget := func(n int) *int { return &n }
	memory := func(n uint32) *uint32 { return &n }
	var cases []wireCase
	add := func(c wireCase) { cases = append(cases, c) }
	with := func(data *VmStartData, f func(*VmStartData)) *VmStartData {
		f(data)
		return data
	}

	// serve: the frames of the remote exchange.
	add(wireCase{Name: "returns a value with output and store writes in one terminal batch", Mode: "serve", Data: wireData(
		`text("a"); console.log("b", 1); image({ type: "image", data: "iVBORw0KGgo=", mimeType: "image/png" }); store("k", { x: 1 }); store("gone", undefined); return { v: [1, 2] }`, nil, nil)})
	add(wireCase{Name: "a call carries the output buffered before it", Mode: "serve", Data: wireData(
		`text("before"); const r = await tools.echo({ a: 1 }); text("after"); return r`, echo, nil)})
	add(wireCase{Name: "answers of every kind reach the script", Mode: "serve", Replies: map[string]string{"fail": "fail", "undef": "undef", "number": "number"}, Data: wireData(
		`const a = await tools.fail({}).catch((e) => e.message); const b = await tools.undef(); const c = await tools.number(); return [a, b === undefined, c]`, allTools, nil)})
	add(wireCase{Name: "global calls name their target and spread their arguments", Mode: "serve", Data: wireData(
		`return [await lookup("x"), await ns.sum(1, 2, 3), await lookup()]`, nil, []VmStartGlobal{{Name: "lookup"}, {Name: "ns.sum", Spread: true}})})
	add(wireCase{Name: "a thrown error is a failed done with its JSON", Mode: "serve", Data: wireData(`text("x"); throw new TypeError("bad")`, nil, nil)})
	add(wireCase{Name: "a syntax error is a failed done", Mode: "serve", Data: wireData(`return (`, nil, nil)})
	add(wireCase{Name: "output is flushed every 1000 items", Mode: "serve", Data: wireData(`for (let i = 0; i < 2500; i++) console.log(i); return 1`, nil, nil)})
	add(wireCase{Name: "output is flushed at 1 MiB of characters", Mode: "serve", Data: wireData(
		`text("x".repeat(1 << 19)); text("y".repeat(1 << 19)); text("z"); return 1`, nil, nil)})
	add(wireCase{Name: "a stop reply ends the server without a terminal message", Mode: "serve", Replies: map[string]string{"echo": "stop"}, Data: wireData(`await tools.echo(1); return 2`, echo, nil)})
	add(wireCase{Name: "a rejected exchange ends the server", Mode: "serve", Replies: map[string]string{"echo": "reject"}, Data: wireData(`text("o"); await tools.echo(1); return 2`, echo, nil)})
	add(wireCase{Name: "a request without an output limit runs a script that returns nothing", Mode: "serve", Data: wireData(`exit()`, nil, nil)})

	// serve: output that is empty, and image output counts toward the flush by its data.
	add(wireCase{Name: "empty text and console output keep their text", Mode: "serve", Data: wireData(`console.log(); text(""); console.log(""); return 1`, nil, nil)})
	add(wireCase{Name: "image output is flushed at 1 MiB of data characters", Mode: "serve", Data: wireData(
		`const data = "iVBORw0KGgo" + "A".repeat((1 << 19) + 1); image({ type: "image", data, mimeType: "image/png" }); image({ type: "image", data, mimeType: "image/png" }); text("z"); return 1`, nil, nil)})
	add(wireCase{Name: "a module that fails to load fails the execution", Mode: "serve", BadWasm: true, Data: wireData(`return 1`, nil, nil)})

	// serve: the interrupt budget and the memory limit are the request's, capped at the remote's.
	for _, loop := range []int{100, 1000, 10_000, 30_000, 100_000, 1_000_000} {
		for _, b := range []int{3, 10, 60} {
			add(wireCase{Name: fmt.Sprintf("budget %d runs a loop of %d iterations", b, loop), Mode: "serve", InterruptBudget: budget(b),
				Data: wireData(fmt.Sprintf(`let n = 0; for (let i = 0; i < %d; i++) n += i; return n`, loop), nil, nil)})
		}
	}
	add(wireCase{Name: "a request cannot raise the remote's budget", Mode: "serve", InterruptBudget: budget(1_000_000_000), MaxInterruptBudget: budget(20),
		Data: wireData(`let n = 0; for (let i = 0; i < 1000000; i++) n += i; return n`, nil, nil)})
	add(wireCase{Name: "a request may lower the remote's budget", Mode: "serve", InterruptBudget: budget(40), MaxInterruptBudget: budget(1_000_000_000),
		Data: wireData(`let n = 0; for (let i = 0; i < 1000000; i++) n += i; return n`, nil, nil)})
	add(wireCase{Name: "a request without a budget gets the remote's", Mode: "serve", MaxInterruptBudget: budget(30),
		Data: wireData(`let n = 0; for (let i = 0; i < 1000000; i++) n += i; return n`, nil, nil)})
	for _, jobs := range []int{100, 500, 1000, 5000} {
		add(wireCase{Name: fmt.Sprintf("budget 20 and %d promise jobs", jobs), Mode: "serve", InterruptBudget: budget(20),
			Data: wireData(fmt.Sprintf(`for (let i = 0; i < %d; i++) await null; return 1`, jobs), nil, nil)})
	}
	add(wireCase{Name: "a script that awaits forever is stopped by the budget", Mode: "serve", InterruptBudget: budget(20), Data: wireData(`while (true) await null`, nil, nil)})
	oom := `const a = []; for (;;) a.push("x".repeat(1000) + a.length)`
	add(wireCase{Name: "a request cannot raise the remote's memory", Mode: "serve", MaxMemoryLimitBytes: memory(2 << 20), Data: with(wireData(oom, nil, nil), func(d *VmStartData) { d.MemoryLimitBytes = 256 << 20 })})
	add(wireCase{Name: "a request may lower the remote's memory", Mode: "serve", MaxMemoryLimitBytes: memory(256 << 20), Data: with(wireData(oom, nil, nil), func(d *VmStartData) { d.MemoryLimitBytes = 2 << 20 })})
	add(wireCase{Name: "a request without a memory limit gets the remote's", Mode: "serve", MaxMemoryLimitBytes: memory(2 << 20), Data: wireData(oom, nil, nil)})
	add(wireCase{Name: "deep recursion fails as a catchable error at the in-thread stack size", Mode: "serve", Data: wireData(
		`let d = 0; function f() { d++; f() } try { f() } catch (e) { return [d, e.constructor.name, e.message] }`, nil, nil)})

	// serve: a deterministic VM reports what it is and each drain.
	add(wireCase{Name: "a deterministic run reports its fingerprint, each drain and where it ended", Mode: "serve", Data: with(wireData(
		`const t = Date.now(); const r = Math.random(); const a = await tools.echo(1); const b = await tools.echo(2); return [t, r, Date.now(), new Date().getTimezoneOffset(), typeof WeakRef, typeof FinalizationRegistry, a, b]`, echo, nil),
		func(d *VmStartData) { d.Deterministic = deterministic })})
	add(wireCase{Name: "the fingerprint names the memory limit and the budget", Mode: "serve", InterruptBudget: budget(777), MaxMemoryLimitBytes: memory(4 << 20),
		Data: with(wireData(`return 1`, nil, nil), func(d *VmStartData) { d.Deterministic = deterministic; d.MemoryLimitBytes = 8 << 20 })})
	add(wireCase{Name: "the fingerprint without a request budget names the remote's default", Mode: "serve",
		Data: with(wireData(`return 1`, nil, nil), func(d *VmStartData) { d.Deterministic = deterministic })})
	add(wireCase{Name: "results delivered out of order are applied in seq order", Mode: "serve", Reverse: true, Data: with(wireData(
		`const [a, b] = await Promise.all([tools.echo("a"), tools.echo("b")]); return [a, b]`, echo, nil), func(d *VmStartData) { d.Deterministic = deterministic })})
	add(wireCase{Name: "a rejection carries the properties of its result", Mode: "serve", Replies: map[string]string{"fail": "fail"}, Properties: json.RawMessage(`{"code":"E_X","n":3}`),
		Data: with(wireData(`try { await tools.fail({}) } catch (e) { return [e.message, e.code, e.n] }`, allTools, nil), func(d *VmStartData) { d.Deterministic = deterministic })})
	add(wireCase{Name: "a deterministic script that throws reports where it threw", Mode: "serve", Data: with(wireData(
		`await tools.echo(1); throw new Error("late")`, echo, nil), func(d *VmStartData) { d.Deterministic = deterministic })})
	add(wireCase{Name: "a deterministic syntax error", Mode: "serve", Data: with(wireData(`return (`, nil, nil), func(d *VmStartData) { d.Deterministic = deterministic })})
	add(wireCase{Name: "a deterministic script with a store and its calls in a global", Mode: "serve", Data: with(wireData(
		`const v = load("seed"); store("out", [v, await lookup(v)]); return v`, nil, []VmStartGlobal{{Name: "lookup"}}), func(d *VmStartData) {
		d.Deterministic = deterministic
		d.Store = map[string]string{"seed": "7"}
	})})

	for _, tc := range []struct {
		name    string
		request uint32
		max     *uint32
	}{
		{"a request below the cap", 2 << 20, memory(4 << 20)},
		{"a request above the cap", 8 << 20, memory(4 << 20)},
		{"a request without a cap", 8 << 20, nil},
		{"no request and no cap", 0, nil},
		{"no request under a cap", 0, memory(4 << 20)},
	} {
		add(wireCase{Name: "memory of a deterministic run: " + tc.name, Mode: "serve", MaxMemoryLimitBytes: tc.max,
			Data: with(wireData(`return 1`, nil, nil), func(d *VmStartData) { d.Deterministic = deterministic; d.MemoryLimitBytes = tc.request })})
	}
	for _, b := range []int{12, 20, 23, 24, 25, 26, 30} {
		add(wireCase{Name: fmt.Sprintf("budget %d and 1000 promise jobs", b), Mode: "serve", InterruptBudget: budget(b),
			Data: wireData(`for (let i = 0; i < 1000; i++) await null; return 1`, nil, nil)})
	}
	add(wireCase{Name: "a deterministic store with several keys", Mode: "serve", Data: with(wireData(`return [load("a"), load("b"), load("c")]`, nil, nil), func(d *VmStartData) {
		d.Deterministic = deterministic
		d.Store = map[string]string{"c": "3", "a": "1", "b": `{"x":[2]}`}
	})})

	// serve: a client that breaks the deterministic protocol.
	deterministicEcho := func(code string) *VmStartData {
		return with(wireData(code, echo, nil), func(d *VmStartData) { d.Deterministic = deterministic })
	}
	for _, shape := range []string{"badSeq", "dupSeq", "omitSeq", "omitNow", "pastNow"} {
		add(wireCase{Name: "a client whose results break the protocol: " + shape, Mode: "serve", Shape: shape,
			Data: deterministicEcho(`const a = await tools.echo(1); const t = Date.now(); const b = await tools.echo(2); return [a, t, Date.now(), b]`)})
	}

	add(wireCase{Name: "a client whose results break the protocol: dupEarly", Mode: "serve", Shape: "dupEarly",
		Data: deterministicEcho(`const [a, b] = await Promise.all([tools.echo(1), tools.echo(2)]); return [a, b]`)})

	// client: what a remote sandbox sends and how it answers.
	tools := []wireTool{{Name: "echo", Description: "echoes", Kind: "echo"}, {Name: "fail", Kind: "fail"}, {Name: "undef", Kind: "undef"}, {Name: "number", Kind: "number"}, {Name: "my-tool", Kind: "echo"}, {Name: "hold", Kind: "hang"}}
	globals := []wireTool{{Name: "lookup", Kind: "echo"}, {Name: "ns.sum", Kind: "number", Spread: true}}
	add(wireCase{Name: "a remote sandbox sends its tables, store and limits, and answers calls", Mode: "client", Tools: tools, Globals: globals, InterruptBudget: budget(50_000), MemoryLimitBytes: 32 << 20,
		Store: map[string]json.RawMessage{"seed": json.RawMessage("7")},
		Code:  `text("a"); const r = await tools.echo({ a: 1 }); const g = await lookup("q"); const s = await ns.sum(1, 2); const m = await tools["my-tool"](); store("k", r); return [r, g, s, m, load("seed"), ALL_TOOLS.length]`})
	add(wireCase{Name: "a failing tool is a catchable error and an undefined result stays undefined", Mode: "client", Tools: tools,
		Code: `try { await tools.fail({}) } catch (e) { text(e.message) } const u = await tools.undef(); return [u === undefined, await tools.number()]`})
	add(wireCase{Name: "a remote sandbox without a budget sends none", Mode: "client", Tools: tools, Code: `return await tools.number()`})
	add(wireCase{Name: "a script that computes past the remote's budget times out", Mode: "client", Tools: tools, InterruptBudget: budget(50), Code: `let n = 0; for (let i = 0; i < 1000000; i++) n += i; return n`})
	add(wireCase{Name: "the remote's memory cap holds against the request", Mode: "client", MaxMemoryLimitBytes: memory(2 << 20), MemoryLimitBytes: 256 << 20, Code: oom})
	add(wireCase{Name: "a script error crosses the exchange", Mode: "client", Tools: tools, Code: `text("x"); await tools.echo(1); throw new RangeError("far")`})
	add(wireCase{Name: "an exit crosses the exchange without a value", Mode: "client", Code: `text("x"); exit("done"); text("y")`})

	add(wireCase{Name: "closing the sandbox answers a pending exchange with a stop", Mode: "client", CloseOnCall: true,
		Tools: []wireTool{{Name: "hold", Kind: "hang"}}, Code: `text("before"); await tools.hold(); text("after")`})

	// inline: the budget counts across the whole execution, and the bridge breaks as one error.
	for _, loop := range []int{100, 1000, 10_000, 100_000} {
		add(wireCase{Name: fmt.Sprintf("an inline sandbox with a budget of 25 runs %d iterations", loop), Mode: "inline", InterruptBudget: budget(25),
			Code: fmt.Sprintf(`let n = 0; for (let i = 0; i < %d; i++) n += i; return n`, loop)})
	}
	add(wireCase{Name: "an inline sandbox counts the budget across waits on tools", Mode: "inline", InterruptBudget: budget(30), Tools: tools,
		Code: `let n = 0; for (let k = 0; k < 4; k++) { for (let i = 0; i < 2000; i++) n += i; await tools.number() } return n`})
	for _, code := range []string{
		`Array.prototype.toJSON = function () { return "}" }; return [1]`,
		`Object.prototype.toJSON = function () { return 5 }; store("k", { a: 1 }); return 1`,
		`const original = JSON.stringify; JSON.stringify = () => "not json{"; return 1`,
		`JSON.stringify = () => "[1]"; store("k", 1); return 2`,
		`JSON.stringify = (v) => v === undefined ? undefined : "[[1]]"; store("k", 1); return 2`,
		`JSON.stringify = () => '[["k"]]'; store("k", 1); return 2`,
		`JSON.stringify = () => '[["k", 1]]'; store("k", 1); return 2`,
		`JSON.stringify = () => '[["k", "{bad"]]'; store("k", 1); return 2`,
		`JSON.stringify = () => '[[1]]'; store("k", 1); return 2`,
		`JSON.stringify = () => '[]'; throw new Error("x")`,
		`JSON.stringify = () => '5'; throw new Error("x")`,
		`JSON.stringify = () => '{"message": 1}'; throw new Error("x")`,
		`JSON.stringify = () => '{"message": "m", "name": 2}'; throw new Error("x")`,
		`JSON.stringify = () => '{"message": "m", "stack": 2}'; throw new Error("x")`,
		`JSON.stringify = () => 'null'; throw new Error("x")`,
		`JSON.stringify = () => '{"message": "m", "name": "N", "stack": "s"}'; throw new Error("x")`,
	} {
		add(wireCase{Name: "a patched built-in: " + code, Mode: "inline", Tools: tools, Code: code})
	}

	// start: the events of a deterministic run and what deliver refuses.
	declTools := []wireDeclared{{Name: "echo", Description: "echoes"}, {Name: "my-tool", JSName: "renamed"}, {Name: "fail"}, {Name: "number"}, {Name: "undef"}}
	declGlobals := []wireDeclaredGlobal{{Name: "lookup"}, {Name: "ns.sum", Spread: true}}
	add(wireCase{Name: "a run reports its start, calls and idles in order", Mode: "start", StartedAt: 1_700_000_000_000, Seed: 9, DeclTools: declTools, DeclGlobals: declGlobals, Replies: map[string]string{"ns.sum": "number"},
		Code: `text("go"); const a = await tools.echo({ n: 1 }); const b = await tools.renamed("x"); const c = await lookup("q"); const d = await ns.sum(1, 2, 3); return [a, b, c, d, Date.now()]`})
	add(wireCase{Name: "a run refuses what cannot be answered and leaves the call waiting", Mode: "start", StartedAt: 5000, Seed: 1, DeclTools: declTools,
		Attempts: []wireAttempt{
			{Seq: 1, Now: "NaN", ID: 1, Outcome: wireOutcome{OK: true}},
			{Seq: 1, Now: "Infinity", ID: 1, Outcome: wireOutcome{OK: true}},
			{Seq: 0, Now: 5000.0, ID: 1, Outcome: wireOutcome{OK: true}},
			{Seq: -3, Now: 5000.0, ID: 1, Outcome: wireOutcome{OK: true}},
			{Seq: 1, Now: 5000.0, ID: 99, Outcome: wireOutcome{OK: true}},
			{Seq: 1, Now: 5000.0, ID: 1, Outcome: wireOutcome{Message: "m", Properties: json.RawMessage("[1]")}},
			{Seq: 1, Now: 5000.0, ID: 1, Outcome: wireOutcome{Message: "m", Properties: json.RawMessage("null")}},
			{Seq: 1, Now: 5000.0, ID: 1, Outcome: wireOutcome{Message: "m", Properties: json.RawMessage("7")}},
			{Seq: 1, Now: 5000.0, ID: 1, Outcome: wireOutcome{Message: "m", Properties: json.RawMessage(`"s"`)}},
		}, Code: `return await tools.echo(1)`})
	add(wireCase{Name: "a result number delivered twice is refused", Mode: "start", StartedAt: 5000, Seed: 1, DeclTools: declTools,
		NoAnswer: true, Attempts: []wireAttempt{
			{Seq: 1, Now: 6000.0, ID: 1, Outcome: wireOutcome{OK: true, Value: json.RawMessage(`"first"`)}},
			{Seq: 1, Now: 7000.0, ID: 1, Outcome: wireOutcome{OK: true, Value: json.RawMessage(`"second"`)}},
			{Seq: 2, Now: 7000.0, ID: 1, Outcome: wireOutcome{OK: true, Value: json.RawMessage(`"again"`)}},
		},
		Code: `return await tools.echo(1)`})
	add(wireCase{Name: "a rejection with properties reaches the script", Mode: "start", StartedAt: 5000, Seed: 1, DeclTools: declTools, Replies: map[string]string{"fail": "fail"}, Properties: json.RawMessage(`{"structuredOutput":{"a":[1]},"name":"x"}`),
		Code: `try { await tools.fail(1) } catch (e) { return [e.message, e.structuredOutput, e.name] }`})
	add(wireCase{Name: "a run whose script throws after a call reports where it ended", Mode: "start", StartedAt: 5000, Seed: 1, DeclTools: declTools,
		Code: `await tools.echo(1); await tools.number(); throw new Error("after two")`})
	add(wireCase{Name: "a run with a syntax error", Mode: "start", StartedAt: 5000, Seed: 1, Code: `return (`})
	add(wireCase{Name: "a run's store reads and writes", Mode: "start", StartedAt: 5000, Seed: 1, DeclTools: declTools, Store: map[string]json.RawMessage{"a": json.RawMessage(`{"n":1}`)},
		Code: `store("b", load("a").n + 1); await tools.number(); return load("a")`})
	add(wireCase{Name: "an undefined answer reaches the script as undefined", Mode: "start", StartedAt: 5000, Seed: 1, DeclTools: declTools, Replies: map[string]string{"undef": "undef"},
		Code: `const a = await tools.undef(); return [a === undefined, typeof a]`})
	for _, backend := range []string{"remote"} {
		add(wireCase{Name: "a run behind a " + backend + " sandbox reports its start, calls and idles", Mode: "start", Backend: backend, StartedAt: 1_700_000_000_000, Seed: 9, DeclTools: declTools, DeclGlobals: declGlobals,
			Replies: map[string]string{"fail": "fail", "ns.sum": "number"}, Properties: json.RawMessage(`{"code":"E_Y"}`),
			Code: `text("go"); const a = await tools.echo({ n: 1 }); const b = await tools.renamed("x"); let e; try { await tools.fail(1) } catch (x) { e = [x.message, x.code] } const d = await ns.sum(1, 2, 3); return [a, b, e, d, Date.now()]`})
		add(wireCase{Name: "a run behind a " + backend + " sandbox refuses what cannot be answered", Mode: "start", Backend: backend, StartedAt: 5000, Seed: 1, DeclTools: declTools,
			Attempts: []wireAttempt{{Seq: 1, Now: "NaN", ID: 1, Outcome: wireOutcome{OK: true}}, {Seq: 1, Now: 5000.0, ID: 99, Outcome: wireOutcome{OK: true}}}, Code: `return await tools.echo(1)`})
	}
	add(wireCase{Name: "a run with a budget fingerprints it", Mode: "start", StartedAt: 5000, Seed: 1, InterruptBudget: budget(12345), Code: `return 1`})

	// bridge: the host's reading of what a VM reports, with messages a real VM cannot be made to send.
	send := func(messages ...string) []bridgeStep {
		var steps []bridgeStep
		for _, m := range messages {
			steps = append(steps, bridgeStep{Send: json.RawMessage(m)})
		}
		return steps
	}
	wait := func(n int) bridgeStep { return bridgeStep{Wait: &n} }
	bridgeCase := func(name string, steps []bridgeStep) {
		add(wireCase{Name: "a stand-in VM: " + name, Mode: "bridge", Code: "ignored", Steps: steps, Tools: tools, Globals: globals})
	}
	jsonText := func(s string) string { raw, _ := json.Marshal(s); return string(raw) }
	for _, value := range []string{`{bad`, ``, `1`, `"s"`, `[1,{"a":null}]`} {
		bridgeCase("a return value of "+value, send(`{"type":"done","ok":true,"value":`+jsonText(value)+`,"writes":"[]"}`))
	}
	bridgeCase("no return value", send(`{"type":"done","ok":true,"writes":"[]"}`))
	bridgeCase("no store writes", send(`{"type":"done","ok":true,"value":"1"}`))
	for _, writes := range []string{`{bad`, `5`, `{}`, `null`, `[5]`, `[[1]]`, `[[]]`, `[["k",1]]`, `[["k","1","x"]]`, `[["k","{bad"]]`, `[["k"]]`, `[[null]]`, `["k"]`,
		`[["k","1"],["z"],["k","2"],["__proto__","3"],["b","[1]"],["2","1"],["1","1"]]`, `[["a\ud800","1"],["\ud800\udc00","\"x\""]]`, `[["k","1"],["k"]]`} {
		bridgeCase("store writes "+writes, send(`{"type":"done","ok":true,"value":"1","writes":`+jsonText(writes)+`}`))
	}
	for _, text := range []string{`{bad`, ``, `5`, `null`, `"str"`, `[]`, `[1]`, `{}`, `{"message":1}`, `{"message":"m","name":2}`, `{"message":"m","stack":2}`, `{"message":"m","name":null}`, `{"message":"m","name":"N","stack":"S"}`, `{"message":"m"}`, `{"message":"\ud800"}`, `{"name":"N","message":""}`} {
		bridgeCase("a script error of "+text, send(`{"type":"done","ok":false,"error":`+jsonText(text)+`}`))
	}
	bridgeCase("a failed done without an error", send(`{"type":"done","ok":false}`))
	bridgeCase("an unknown message", send(`{"type":"bogus"}`))
	bridgeCase("a result is not a VM message", send(`{"type":"result","id":1,"ok":true}`))
	bridgeCase("a crash", send(`{"type":"crash","message":"wasm trap"}`))
	bridgeCase("a failure of the launcher", send(`{"type":"failure","error":{"kind":"sandbox","message":"gone"}}`))
	bridgeCase("started and idle are ignored outside a deterministic run", send(`{"type":"started","fingerprint":"fp"}`, `{"type":"idle","seq":0,"issued":0}`, `{"type":"done","ok":true,"value":"1","writes":"[]"}`))
	bridgeCase("what follows the outcome is ignored", send(`{"type":"done","ok":true,"value":"1","writes":"[]"}`, `{"type":"output","item":{"type":"text","text":"late"}}`, `{"type":"crash","message":"late"}`))
	bridgeCase("output of every kind in order", send(`{"type":"output","item":{"type":"text","text":"a"}}`, `{"type":"output","item":{"type":"text","text":"b","console":true}}`,
		`{"type":"output","item":{"type":"image","data":"AAAA","mimeType":"image/png"}}`, `{"type":"output","item":{"type":"text","text":""}}`, `{"type":"crash","message":"stop"}`))
	bridgeCase("calls are answered with the tool's outcome", append(append(send(
		`{"type":"call","id":1,"target":"tool","name":"echo","args":"{\"a\":[1]}"}`, `{"type":"call","id":2,"target":"tool","name":"fail"}`, `{"type":"call","id":3,"target":"tool","name":"undef","args":"1"}`,
		`{"type":"call","id":4,"target":"tool","name":"number"}`, `{"type":"call","id":5,"target":"tool","name":"nope"}`, `{"type":"call","id":6,"target":"global","name":"lookup","args":"[\"q\"]"}`,
		`{"type":"call","id":7,"target":"global","name":"nope"}`, `{"type":"call","id":8,"target":"other","name":"lookup"}`, `{"type":"call","id":9,"target":"global","name":"ns.sum","args":"[1,2]"}`), wait(9)),
		send(`{"type":"done","ok":true,"value":"1","writes":"[]"}`)...))
	bridgeCase("a call id used twice", send(`{"type":"call","id":1,"target":"tool","name":"hold"}`, `{"type":"call","id":1,"target":"tool","name":"echo"}`))
	add(wireCase{Name: "a stand-in VM: a call that is not answered stays cancelled", Mode: "bridge", Code: "ignored", Tools: []wireTool{{Name: "hold", Kind: "hang"}},
		Steps: send(`{"type":"call","id":1,"target":"tool","name":"hold"}`, `{"type":"done","ok":true,"value":"1","writes":"[]"}`)})
	add(wireCase{Name: "a stand-in VM: a deterministic run reads calls and refuses a bad argument list", Mode: "bridge", Control: true, Code: "ignored", DeclTools: []wireDeclared{{Name: "echo"}},
		Steps: send(`{"type":"started","fingerprint":"fp"}`, `{"type":"call","id":1,"target":"tool","name":"echo","args":"{bad"}`)})
	add(wireCase{Name: "a stand-in VM: a deterministic run with a valid call and no arguments", Mode: "bridge", Control: true, Code: "ignored", DeclTools: []wireDeclared{{Name: "echo"}},
		Steps: send(`{"type":"call","id":1,"target":"tool","name":"echo"}`, `{"type":"call","id":2,"target":"global","name":"g","args":"[1]"}`, `{"type":"done","ok":true,"during":{"seq":0,"issued":2},"value":"1","writes":"[]"}`)})
	add(wireCase{Name: "a stand-in VM: a run that ends in an error reports where", Mode: "bridge", Control: true, Code: "ignored",
		Steps: send(`{"type":"done","ok":false,"error":"{\"message\":\"m\"}","during":{"seq":3,"issued":4}}`)})

	// remote: how a client reads what a remote sends.
	exchanges := func(batches ...[]string) []remoteExchange {
		var out []remoteExchange
		for _, batch := range batches {
			entry := remoteExchange{}
			for _, m := range batch {
				entry.Messages = append(entry.Messages, json.RawMessage(m))
			}
			out = append(out, entry)
		}
		return out
	}
	done := `{"type":"done","ok":true,"value":"1","writes":"[]"}`
	remoteCase := func(name string, exchanges []remoteExchange, throws bool) {
		add(wireCase{Name: "a stand-in remote: " + name, Mode: "remote", Code: "ignored", Exchanges: exchanges, Throws: throws, Tools: tools, Globals: globals})
	}
	remoteCase("a terminal message and nothing after it", exchanges([]string{done}), false)
	afterStop := exchanges([]string{done}, []string{`{"type":"output","item":{"type":"text","text":"x"}}`})
	afterStop[1].AfterStop = true
	remoteCase("an exchange after the client stopped is answered with a stop", afterStop, false)
	remoteCase("a call is answered with its result", exchanges([]string{`{"type":"output","item":{"type":"text","text":"o"}}`, `{"type":"call","id":1,"target":"tool","name":"echo","args":"[1]"}`}, []string{done}), false)
	remoteCase("output without a call is answered with nothing", exchanges([]string{`{"type":"output","item":{"type":"text","text":"o"}}`}, []string{done}), false)
	remoteCase("only the first call of a batch is awaited", exchanges([]string{`{"type":"call","id":1,"target":"tool","name":"echo"}`, `{"type":"call","id":2,"target":"tool","name":"hold"}`}, []string{done}), false)
	remoteCase("a failure message ends the execution", exchanges([]string{`{"type":"output","item":{"type":"text","text":"o"}}`, `{"type":"failure","error":{"kind":"timeout","message":"late"}}`}), false)
	remoteCase("a script error from the remote", exchanges([]string{`{"type":"done","ok":false,"error":"{\"name\":\"TypeError\",\"message\":\"m\",\"stack\":\"s\"}"}`}), false)
	remoteCase("a crash", exchanges([]string{`{"type":"crash","message":"trap"}`}), false)
	remoteCase("a message of an unknown type", exchanges([]string{`{"type":"bogus"}`}), false)
	remoteCase("a remote that returns without a terminal message", nil, false)
	remoteCase("a remote that returns after output only", exchanges([]string{`{"type":"output","item":{"type":"text","text":"o"}}`}), false)
	remoteCase("a remote that fails", nil, true)
	remoteCase("a remote that fails after a terminal message", exchanges([]string{done}), true)
	remoteCase("a remote that fails after a failure message", exchanges([]string{`{"type":"failure","error":{"kind":"sandbox","message":"gone"}}`}), true)
	remoteCase("a failure message without a call is answered with nothing", exchanges([]string{`{"type":"failure","error":{"kind":"sandbox","message":"gone"}}`}), false)

	// validate: the refusals of the sandbox base.
	add(wireCase{Name: "a global with a bad name", Mode: "validate", GlobalNames: []string{"ok", "not valid"}, StartedAt: 1, Seed: 1})
	add(wireCase{Name: "a global with three parts", Mode: "validate", GlobalNames: []string{"a.b.c"}, StartedAt: 1, Seed: 1})
	add(wireCase{Name: "a global that shadows a built-in", Mode: "validate", GlobalNames: []string{"console"}, StartedAt: 1, Seed: 1})
	add(wireCase{Name: "a global registered twice", Mode: "validate", GlobalNames: []string{"dup", "dup"}, StartedAt: 1, Seed: 1})
	add(wireCase{Name: "a global that is also a namespace", Mode: "validate", GlobalNames: []string{"ns.member", "ns"}, StartedAt: 1, Seed: 1})
	add(wireCase{Name: "a run with a bad start time", Mode: "validate", StartedAt: -1, Seed: 1})
	add(wireCase{Name: "a run with duplicate tool names", Mode: "validate", StartedAt: 1, Seed: 1, DeclTools: []wireDeclared{{Name: "a"}, {Name: "a"}}})
	add(wireCase{Name: "a run with duplicate tool script names", Mode: "validate", StartedAt: 1, Seed: 1, DeclTools: []wireDeclared{{Name: "a-b"}, {Name: "a_b"}}})
	add(wireCase{Name: "a run with a bad global", Mode: "validate", StartedAt: 1, Seed: 1, DeclGlobals: []wireDeclaredGlobal{{Name: "x y"}}})
	// The message names the tool or global as the script wrote it, quotes and all: JavaScript interpolates, it does not escape.
	for _, name := range []string{`we"ird`, `back\slash`, "tab\there", "new\nline", "é", "\u2028", "\U0001F600", "\x00"} {
		add(wireCase{Name: fmt.Sprintf("names that need escaping in a message: %q", name), Mode: "validate", ToolName: name, GlobalNames: []string{name, name}, StartedAt: 1, Seed: 1,
			DeclGlobals: []wireDeclaredGlobal{{Name: name}}})
		add(wireCase{Name: fmt.Sprintf("a global namespace that needs escaping: %q", name), Mode: "validate", GlobalNames: []string{"ns" + name + ".a", "ns" + name}, StartedAt: 1, Seed: 1})
	}
	for _, name := range []string{"tools", "ALL_TOOLS", "console", "text", "image", "exit", "globalThis", "store", "load", "Tools", "all_tools", "storeX", "$a", "_1", "a$_1", "1a", "a-b", "", ".a", "a.", "a..b", "a.b.", "é", "a\n", "a b", "console.log", "tools.x", "Object", "undefined"} {
		add(wireCase{Name: fmt.Sprintf("global name %q", name), Mode: "validate", GlobalNames: []string{name}, StartedAt: 1, Seed: 1, DeclGlobals: []wireDeclaredGlobal{{Name: name}}})
	}
	add(wireCase{Name: "a run with a start time of zero", Mode: "validate", StartedAt: 0, Seed: 1})
	add(wireCase{Name: "a run with a fractional start time", Mode: "validate", StartedAt: 1.5, Seed: 1})
	add(wireCase{Name: "a valid run", Mode: "validate", StartedAt: 1, Seed: 1, DeclTools: []wireDeclared{{Name: "a"}}})
	return cases
}

func TestWireBehaviorMatchesPi(t *testing.T) {
	cases := wireCases()
	wasmPath, err := filepath.Abs("assets/quickjs.wasm")
	if err != nil {
		t.Fatal(err)
	}
	scriptPath, err := filepath.Abs("testdata/wire_oracle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Cases     []map[string]any `json:"cases"`
		Prelude   string           `json:"prelude"`
		Constants struct {
			DefaultInterruptBudget int `json:"defaultInterruptBudget"`
			InlineMaxStackSize     int `json:"inlineMaxStackSize"`
		} `json:"constants"`
	}
	pioracle.Run(t, `
const { run } = await import(pathToFileURL(process.env.ORACLE_SCRIPT).href);
emit(await run(input, root, process.env.QUICKJS_WASM));
`, cases, &want, "QUICKJS_WASM="+wasmPath, "ORACLE_SCRIPT="+scriptPath)
	if len(want.Cases) != len(cases) {
		t.Fatalf("Pi recorded %d cases for %d", len(want.Cases), len(cases))
	}
	if want.Constants.DefaultInterruptBudget != DefaultInterruptBudget || want.Constants.InlineMaxStackSize != inlineMaxStackSize {
		t.Errorf("DefaultInterruptBudget, inlineMaxStackSize = %d, %d; Pi %d, %d", DefaultInterruptBudget, inlineMaxStackSize, want.Constants.DefaultInterruptBudget, want.Constants.InlineMaxStackSize)
	}
	compare := func(t *testing.T, c wireCase, got any, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.MarshalIndent(got, "", " ")
			wantJSON, _ := json.MarshalIndent(want, "", " ")
			t.Errorf("%s\nPiG %s\nPi  %s", c.Mode, clip(gotJSON), clip(wantJSON))
		}
	}
	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// PiG's prelude is Pi's with divergence D145 applied, so the fingerprint of a deterministic run names another prelude hash:
			// the hash is the only thing masked here, and the run below with Pi's own prelude compares the whole fingerprint.
			compare(t, c, maskPreludeHash(canonical(t, c.run(t))), maskPreludeHash(canonical(t, want.Cases[i])))
			// With Pi's own prelude nothing differs, the fingerprint included.
			c.prelude = want.Prelude
			compare(t, c, canonical(t, c.run(t)), any(want.Cases[i]))
		})
	}
}

var preludeHashPattern = regexp.MustCompile(`prelude [0-9a-f]{8}`)

// maskPreludeHash replaces the prelude hash of every fingerprint in a record.
func maskPreludeHash(value any) any {
	switch v := value.(type) {
	case string:
		return preludeHashPattern.ReplaceAllString(v, "prelude <hash>")
	case []any:
		for i := range v {
			v[i] = maskPreludeHash(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = maskPreludeHash(v[k])
		}
	}
	return value
}

func clip(raw []byte) string {
	if len(raw) > 6000 {
		return string(raw[:6000]) + "\n…"
	}
	return string(raw)
}
