package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/eventwire"
)

// eventWirePeer is an extension process that answers each event request with reply(args) and records the args it received.
type eventWirePeer struct {
	mu   sync.Mutex
	args []string
}

func (p *eventWirePeer) received() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.args...)
}

// serve answers requests on conn until it closes. It reports a protocol failure through errs.
func (p *eventWirePeer) serve(conn net.Conn, reply func(args []byte) string, errs chan<- error) {
	for {
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		var env Envelope
		if err := json.Unmarshal(body, &env); err != nil {
			errs <- err
			return
		}
		if env.Type != MsgRequest || env.Request == nil || env.Request.Method != "event" {
			errs <- fmt.Errorf("unexpected frame %s", body)
			return
		}
		p.mu.Lock()
		p.args = append(p.args, string(env.Request.Args))
		p.mu.Unlock()
		out, err := json.Marshal(Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Result: json.RawMessage(reply(env.Request.Args))}})
		if err != nil {
			errs <- err
			return
		}
		binary.BigEndian.PutUint32(header[:], uint32(len(out)))
		if _, err := conn.Write(append(header[:], out...)); err != nil {
			return
		}
	}
}

// eventWireStep is one handler of a dispatch under test: a subprocess handler answered by reply, or, when reply is nil, a Go handler that runs edit.
type eventWireStep struct {
	reply func(args []byte) string
	edit  func(event any, ctx context.Context)
}

// eventWireDispatch registers steps as extensions in order, each subprocess handler wrapped to record, before it runs, the encoding a handler with no shared state would send (oracle) and whether the dispatch held an encoding of the current event (reused). It returns the runner and, per subprocess step, its peer.
func eventWireDispatch(t *testing.T, event string, steps []eventWireStep) (*inproc.Runner, []*eventWirePeer, *[]string, *[]bool) {
	t.Helper()
	host := NewHost(t.TempDir())
	errs := make(chan error, len(steps))
	t.Cleanup(func() {
		select {
		case err := <-errs:
			t.Error(err)
		default:
		}
	})
	var oracle []string
	var reused []bool
	extensions := make([]extension.Extension, len(steps))
	peers := make([]*eventWirePeer, len(steps))
	for i, step := range steps {
		ext := extension.Extension{Path: fmt.Sprintf("/ext/%d", i), ResolvedPath: fmt.Sprintf("/ext/%d", i), Handlers: map[string][]extension.HandlerFn{}}
		if step.reply == nil {
			ext.AddEventHandler(event, 1, func(args ...any) (any, error) {
				step.edit(args[0], args[1].(context.Context))
				return nil, nil
			})
			extensions[i] = ext
			continue
		}
		hostEnd, peerEnd := net.Pipe()
		t.Cleanup(func() { _ = hostEnd.Close(); _ = peerEnd.Close() })
		conn := NewConn(ext.Path, hostEnd)
		conn.Start(t.Context())
		peers[i] = &eventWirePeer{}
		go peers[i].serve(peerEnd, step.reply, errs)
		inner := withConn(&managedExt{config: ExtConfig{Name: ext.Path}, host: host}, conn).makeEventHandler(event, 1)
		ext.AddJSONEventHandler(event, 1, func(args ...any) (any, error) {
			ctx := args[1].(context.Context)
			_, held := eventwire.FromContext(ctx).Args()
			reused = append(reused, held)
			fresh, err := json.Marshal(wireEventPayload(args[0]))
			if err != nil {
				return nil, err
			}
			if event == "before_agent_start" {
				if fresh, err = promptOptionsEventArgs(ctx, fresh); err != nil {
					return nil, err
				}
			}
			oracle = append(oracle, string(fresh))
			return inner(args...)
		})
		extensions[i] = ext
	}
	return inproc.NewRunner(extensions, t.TempDir()), peers, &oracle, &reused
}

// echoPromptOptions answers a before_agent_start request as the Node runtime does for a handler that edits nothing: the options it received, with edit applied to their members first.
func echoPromptOptions(edit func(options map[string]json.RawMessage)) func(args []byte) string {
	return func(args []byte) string {
		var event struct {
			SystemPromptOptions map[string]json.RawMessage `json:"systemPromptOptions"`
		}
		if err := json.Unmarshal(args, &event); err != nil {
			return "null"
		}
		options := event.SystemPromptOptions
		if edit != nil {
			edit(options)
		}
		encoded, _ := json.Marshal(options)
		reply := `{"_pigPromptSelectedTools":` + string(options["selectedTools"]) + `,"_pigPromptOptions":` + string(encoded)
		if sections, ok := options["sections"]; ok {
			reply += `,"_pigPromptSections":` + string(sections)
		}
		return reply + "}"
	}
}

func received(peers []*eventWirePeer) []string {
	var all []string
	for _, peer := range peers {
		if peer != nil {
			all = append(all, peer.received()...)
		}
	}
	return all
}

// upstream: runner.ts emitBeforeAgentStart hands every handler the one shared options object, so an edit reaches the next handler. The subprocess handlers of one dispatch share the event's encoding (eventwire) only while no change was recorded: each handler receives exactly the encoding of the event as it stands, an edit by a Go handler or in a subprocess handler's reply reaches the next handler's request and prompt, and a handler after one whose reply repeated the last applied reply receives the reused encoding.
func TestBeforeAgentStartSubprocessHandlersShareTheEventEncodingUntilAnEdit(t *testing.T) {
	echo := echoPromptOptions(nil)
	editByReply := echoPromptOptions(func(options map[string]json.RawMessage) {
		options["appendSystemPrompt"] = json.RawMessage(`"FROM-REPLY"`)
	})
	fixed := func([]byte) string {
		return `{"_pigPromptSelectedTools":["read"],"_pigPromptOptions":{"appendSystemPrompt":"FIXED","cwd":"/work"}}`
	}
	// A reply whose options do not decode fails the handler after its sections and tools were written, and the edit stays (runner.ts:1339).
	failing := func([]byte) string {
		return `{"_pigPromptSelectedTools":["read","bash"],"_pigPromptOptions":{"cwd":5}}`
	}
	steps := []eventWireStep{
		{reply: echo}, // 0: encodes; its reply is applied
		{reply: echo}, // 1: encodes after the apply; its reply repeats and is skipped
		{reply: echo}, // 2: reuses
		{edit: func(_ any, ctx context.Context) {
			extension.BeforeAgentStartOptions(ctx).AppendSystemPrompt = "FROM-GO"
		}},
		{reply: echo},        // 4: encodes after the Go edit; its new reply is applied
		{reply: editByReply}, // 5: encodes after the apply; its reply edits
		{reply: echo},        // 6: encodes after the edit; its reply repeats the edit
		{reply: echo},        // 7: reuses
		{reply: echo},        // 8: reuses
		{reply: fixed},       // 9: reuses; its reply edits
		{edit: func(_ any, ctx context.Context) {
			extension.BeforeAgentStartOptions(ctx).AppendSystemPrompt = "FROM-GO-2"
		}},
		{reply: fixed},   // 11: encodes after the Go edit; its reply, the same as the one applied last, still overrides that edit
		{reply: echo},    // 12: encodes after the apply
		{reply: failing}, // 13: encodes after the apply; its reply selects tools and fails on its options
		{reply: echo},    // 14: encodes the selection the failed reply left
	}
	runner, peers, oracle, reused := eventWireDispatch(t, "before_agent_start", steps)
	result, err := runner.EmitBeforeAgentStart(t.Context(), extension.UserPromptTrigger("hello", nil), extension.BuildSystemPromptOptions{Cwd: "/work", SelectedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	sent := received(peers)
	if len(sent) != len(*oracle) || len(sent) != 13 {
		t.Fatalf("requests = %d, oracle encodings = %d, want 13 each", len(sent), len(*oracle))
	}
	for i := range sent {
		if sent[i] != (*oracle)[i] {
			t.Errorf("handler %d received %s, want the event's own encoding %s", i, sent[i], (*oracle)[i])
		}
	}
	if want := []bool{false, false, true, false, false, false, true, true, true, false, false, false, false}; fmt.Sprint(*reused) != fmt.Sprint(want) {
		t.Errorf("encoding reused per subprocess handler = %v, want %v", *reused, want)
	}
	for i, want := range []string{"", "", "", "FROM-GO", "FROM-GO", "FROM-REPLY", "FROM-REPLY", "FROM-REPLY", "FROM-REPLY", "FROM-GO-2", "FIXED", "FIXED", "FIXED"} {
		var event struct {
			SystemPrompt        string `json:"systemPrompt"`
			SystemPromptOptions struct {
				AppendSystemPrompt string `json:"appendSystemPrompt"`
			} `json:"systemPromptOptions"`
		}
		if err := json.Unmarshal([]byte(sent[i]), &event); err != nil {
			t.Fatal(err)
		}
		// The prompt is rendered from the options the handler receives: it carries their appended text and no other edit's.
		if event.SystemPromptOptions.AppendSystemPrompt != want || (want != "" && strings.Count(event.SystemPrompt, want) != 1) || strings.Count(event.SystemPrompt, "FROM-") != strings.Count(want, "FROM-") {
			t.Errorf("handler %d: appendSystemPrompt %q, prompt %q; want %q in both", i, event.SystemPromptOptions.AppendSystemPrompt, event.SystemPrompt, want)
		}
	}
	for i, want := range map[int]string{11: `["read"]`, 12: `["read","bash"]`} {
		var event struct {
			SystemPromptOptions struct {
				SelectedTools json.RawMessage `json:"selectedTools"`
			} `json:"systemPromptOptions"`
		}
		if err := json.Unmarshal([]byte(sent[i]), &event); err != nil {
			t.Fatal(err)
		}
		if string(event.SystemPromptOptions.SelectedTools) != want {
			t.Errorf("handler %d: selectedTools %s, want %s", i, event.SystemPromptOptions.SelectedTools, want)
		}
	}
	if result == nil || result.SystemPromptOptions == nil || result.SystemPromptOptions.AppendSystemPrompt != "FIXED" {
		t.Errorf("combined options = %+v, want the reply's edit", result)
	}
}

// upstream: runner.ts emitToolCall hands every handler the one event, so an input edit reaches the next handler. A subprocess handler's reply that carries an input, and a Go handler, change the event; the next handler receives the edited input, and a handler after one that edited nothing reuses the encoding.
func TestToolCallSubprocessHandlersShareTheEventEncodingUntilAnInputEdit(t *testing.T) {
	keep := func([]byte) string { return `{"_pigToolCallResult":null}` }
	editInput := func([]byte) string {
		return `{"_pigToolCallInput":{"command":"from reply"},"_pigToolCallResult":null}`
	}
	steps := []eventWireStep{
		{reply: keep},      // 0: encodes
		{reply: editInput}, // 1: reuses; its reply edits the input
		{reply: keep},      // 2: encodes the edit
		{reply: keep},      // 3: reuses
		{edit: func(event any, _ context.Context) {
			call := event.(extension.CustomToolCallEvent)
			call.Input["command"] = "from go"
			*call.WireInput = json.RawMessage(`{"command":"from go"}`)
		}},
		{reply: keep}, // 5: encodes the Go edit
		{reply: keep}, // 6: reuses
	}
	runner, peers, oracle, reused := eventWireDispatch(t, "tool_call", steps)
	wire := json.RawMessage(`{"command":"original"}`)
	event := extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: &wire}, ToolName: "bash", Input: map[string]any{"command": "original"}}
	if _, err := runner.EmitToolCall(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	sent := received(peers)
	if len(sent) != 6 || len(*oracle) != 6 {
		t.Fatalf("requests = %d, oracle encodings = %d, want 6 each", len(sent), len(*oracle))
	}
	for i, want := range []string{"original", "original", "from reply", "from reply", "from go", "from go"} {
		if sent[i] != (*oracle)[i] {
			t.Errorf("handler %d received %s, want the event's own encoding %s", i, sent[i], (*oracle)[i])
		}
		if !strings.Contains(sent[i], `"input":{"command":"`+want+`"}`) {
			t.Errorf("handler %d received %s, want input command %q", i, sent[i], want)
		}
	}
	if want := []bool{false, true, false, true, false, true}; fmt.Sprint(*reused) != fmt.Sprint(want) {
		t.Errorf("encoding reused per subprocess handler = %v, want %v", *reused, want)
	}
}
