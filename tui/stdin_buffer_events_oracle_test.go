package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type stdinProbeOp struct {
	Op      string `json:"op"`
	Event   string `json:"event,omitempty"`
	ID      string `json:"id,omitempty"`
	Data    string `json:"data"`
	CountOf *struct {
		Event string `json:"event"`
		ID    string `json:"id"`
	} `json:"countOf,omitempty"`
}

type stdinProbeAction struct {
	Add *struct {
		Event string `json:"event"`
		ID    string `json:"id"`
	} `json:"add,omitempty"`
	Remove *struct {
		Event string `json:"event"`
		ID    string `json:"id"`
	} `json:"remove,omitempty"`
}

type stdinProbe struct {
	Ops     []stdinProbeOp              `json:"ops"`
	Actions map[string]stdinProbeAction `json:"actions,omitempty"`
}

type stdinProbeState struct {
	Ret       *bool               `json:"ret"`
	Error     string              `json:"error"`
	Calls     []string            `json:"calls"`
	Names     []string            `json:"names"`
	Count     map[string]int      `json:"count"`
	Listeners map[string][]string `json:"listeners"`
	Raw       map[string][]string `json:"raw"`
	CountOf   *int                `json:"countOf,omitempty"`
}

// runStdinProbe drives a Pig StdinBuffer with the same program the Pi oracle ran.
func runStdinProbe(t *testing.T, probe stdinProbe) []stdinProbeState {
	t.Helper()
	buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10000000000, EscapeTimeout: 10000000000})
	listeners := map[string]*StdinBufferListener{}
	var calls []string
	var listener func(id string) *StdinBufferListener
	listener = func(id string) *StdinBufferListener {
		if l, ok := listeners[id]; ok {
			return l
		}
		l := NewStdinBufferListener(func(data string) {
			calls = append(calls, id+":"+jsonQuote(data))
			if action, ok := probe.Actions[id]; ok {
				if action.Add != nil {
					buffer.On(StdinBufferEvent(action.Add.Event), listener(action.Add.ID))
				}
				if action.Remove != nil {
					buffer.Off(StdinBufferEvent(action.Remove.Event), listener(action.Remove.ID))
				}
			}
		})
		listeners[id] = l
		return l
	}
	idOf := func(l *StdinBufferListener) string {
		for id, candidate := range listeners {
			if candidate == l {
				return id
			}
		}
		return "?"
	}
	var states []stdinProbeState
	for _, op := range probe.Ops {
		calls = nil
		var ret *bool
		var errName string
		event := StdinBufferEvent(op.Event)
		switch op.Op {
		case "on":
			buffer.On(event, listener(op.ID))
		case "addListener":
			buffer.AddListener(event, listener(op.ID))
		case "once":
			buffer.Once(event, listener(op.ID))
		case "prepend":
			buffer.PrependListener(event, listener(op.ID))
		case "prependOnce":
			buffer.PrependOnceListener(event, listener(op.ID))
		case "off":
			buffer.Off(event, listener(op.ID))
		case "removeListener":
			buffer.RemoveListener(event, listener(op.ID))
		case "removeAll":
			if op.Event != "" {
				buffer.RemoveAllListeners(event)
			} else {
				buffer.RemoveAllListeners()
			}
		case "emit":
			ret = new(buffer.Emit(event, op.Data))
		case "process":
			buffer.ProcessString(op.Data)
		case "flushTimeout":
			buffer.Flush()
		default:
			t.Fatalf("op %q", op.Op)
		}
		state := stdinProbeState{Ret: ret, Error: errName, Calls: append([]string{}, calls...), Names: []string{},
			Count: map[string]int{}, Listeners: map[string][]string{}, Raw: map[string][]string{}}
		for _, name := range buffer.EventNames() {
			state.Names = append(state.Names, string(name))
		}
		for _, name := range []StdinBufferEvent{StdinBufferEventData, StdinBufferEventPaste} {
			state.Count[string(name)] = buffer.ListenerCount(name)
			state.Listeners[string(name)] = []string{}
			for _, l := range buffer.Listeners(name) {
				state.Listeners[string(name)] = append(state.Listeners[string(name)], idOf(l))
			}
			state.Raw[string(name)] = []string{}
			for _, l := range buffer.RawListeners(name) {
				if l.Original() != nil {
					state.Raw[string(name)] = append(state.Raw[string(name)], fmt.Sprintf("once(%s)", idOf(l.Original())))
				} else {
					state.Raw[string(name)] = append(state.Raw[string(name)], idOf(l))
				}
			}
		}
		if op.CountOf != nil {
			state.CountOf = new(buffer.ListenerCount(StdinBufferEvent(op.CountOf.Event), listener(op.CountOf.ID)))
		}
		states = append(states, state)
	}
	buffer.Destroy()
	return states
}

// upstream: packages/tui/src/stdin-buffer.ts:281 `class StdinBuffer extends EventEmitter<StdinBufferEventMap>` and :336, :450 emit("paste"),
// emit("data"). The probes run on the installed Pi StdinBuffer (Node's EventEmitter): registration order, once and prepend wrappers,
// removal of the latest registration, emit's snapshot semantics, event names, counts, listeners and rawListeners, the default and
// set limit, and the data and paste events process() emits. Each op is followed by the buffer's observable listener state.
func TestStdinBufferEventsMatchPi(t *testing.T) {
	d, p := "data", "paste"
	probes := []stdinProbe{
		{Ops: []stdinProbeOp{{Op: "emit", Event: d, Data: "x"}, {Op: "on", Event: d, ID: "a"}, {Op: "emit", Event: d, Data: "x"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "a"}, {Op: "on", Event: d, ID: "b"}, {Op: "prepend", Event: d, ID: "c"}, {Op: "emit", Event: d, Data: "k"}}},
		{Ops: []stdinProbeOp{{Op: "once", Event: d, ID: "a"}, {Op: "emit", Event: d, Data: "1"}, {Op: "emit", Event: d, Data: "2"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "a"}, {Op: "prependOnce", Event: d, ID: "b"}, {Op: "emit", Event: d, Data: "1"}, {Op: "emit", Event: d, Data: "2"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "a"}, {Op: "on", Event: d, ID: "a"}, {Op: "off", Event: d, ID: "a"}, {Op: "emit", Event: d, Data: "x"}, {Op: "removeListener", Event: d, ID: "a"}, {Op: "emit", Event: d, Data: "x"}}},
		// off removes the most recent registration of a listener, not the first.
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "a"}, {Op: "on", Event: d, ID: "b"}, {Op: "on", Event: d, ID: "a"}, {Op: "off", Event: d, ID: "a"}, {Op: "emit", Event: d, Data: "x"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "a"}, {Op: "off", Event: d, ID: "zz"}, {Op: "off", Event: p, ID: "a"}, {Op: "emit", Event: d, Data: "x"}}},
		{Ops: []stdinProbeOp{{Op: "once", Event: d, ID: "a"}, {Op: "off", Event: d, ID: "a"}, {Op: "emit", Event: d, Data: "x"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: p, ID: "a"}, {Op: "on", Event: d, ID: "b"}, {Op: "on", Event: p, ID: "c"}, {Op: "removeAll", Event: p}, {Op: "on", Event: p, ID: "d"}, {Op: "removeAll"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "a"}, {Op: "once", Event: d, ID: "a"}, {Op: "on", Event: d, ID: "b"}, {Op: "emit", Event: d, Data: "x"}, {Op: "emit", Event: d, Data: "y"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "a"}, {Op: "once", Event: d, ID: "a"}, {Op: "emit", Event: d, Data: "x", CountOf: nil}, {Op: "on", Event: d, ID: "b", CountOf: &struct {
			Event string `json:"event"`
			ID    string `json:"id"`
		}{Event: d, ID: "a"}}}},
		// A listener added during an emit waits for the next emit; one removed during it is still called.
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "adder"}, {Op: "on", Event: d, ID: "b"}, {Op: "emit", Event: d, Data: "1"}, {Op: "emit", Event: d, Data: "2"}},
			Actions: map[string]stdinProbeAction{"adder": {Add: &struct {
				Event string `json:"event"`
				ID    string `json:"id"`
			}{Event: d, ID: "late"}}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "remover"}, {Op: "on", Event: d, ID: "victim"}, {Op: "emit", Event: d, Data: "1"}, {Op: "emit", Event: d, Data: "2"}},
			Actions: map[string]stdinProbeAction{"remover": {Remove: &struct {
				Event string `json:"event"`
				ID    string `json:"id"`
			}{Event: d, ID: "victim"}}}},
		// process() emits data per complete sequence and paste with the content, in order.
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "d"}, {Op: "on", Event: p, ID: "p"}, {Op: "process", Data: "ab"}, {Op: "process", Data: "\x1b[200~hello\x1b[201~x"}, {Op: "process", Data: "\x1b[A"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "d"}, {Op: "on", Event: p, ID: "p"}, {Op: "process", Data: "q\x1b[200~par"}, {Op: "process", Data: "t\x1b[201~"}, {Op: "process", Data: ""}}},
		{Ops: []stdinProbeOp{{Op: "once", Event: d, ID: "d"}, {Op: "process", Data: "ab"}, {Op: "process", Data: "cd"}}},
		{Ops: []stdinProbeOp{{Op: "on", Event: d, ID: "d"}, {Op: "process", Data: "\x1b"}, {Op: "flushTimeout"}}},
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/stdin_buffer_events.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		States []stdinProbeState `json:"states"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, probe := range probes {
		got := runStdinProbe(t, probe)
		if !reflect.DeepEqual(got, expected[i].States) {
			for j := range got {
				if !reflect.DeepEqual(got[j], expected[i].States[j]) {
					t.Errorf("probe %d op %d %+v:\n  Pig %+v\n  Pi  %+v", i, j, probe.Ops[j], got[j], expected[i].States[j])
					break
				}
			}
		}
	}
}

// jsonQuote is JSON.stringify of a string.
func jsonQuote(s string) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	return strings.TrimSuffix(out.String(), "\n")
}
