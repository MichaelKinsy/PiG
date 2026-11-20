package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// messageEventRow is one handler of one event that edits it in place, at one of two levels: `top` assigns a member of the event object itself, `deep` edits inside the
// message (or the first user message of the messages list) the event carries.
// The pins of a top-level member: `type` assigns event.type, `once` assigns a member in the event's first dispatch only (and reports whether a later dispatch starts with it),
// `reassign` replaces a declared member wholesale, `delete` removes one.
type messageEventRow struct{ event, level string }

// memberRows are, for the events whose declared member a row reassigns or deletes, the member and the JSON a handler reassigns it to (agent_start declares none).
var memberRows = map[string]struct{ member, value string }{
	"message_start":  {"message", `{"role":"user","content":"` + inPlaceEdited + `","timestamp":1}`},
	"message_update": {"assistantMessageEvent", `{"type":"` + inPlaceEdited + `"}`},
	"turn_start":     {"turnIndex", `"` + inPlaceEdited + `"`},
	"agent_end":      {"messages", `[{"role":"user","content":"` + inPlaceEdited + `","timestamp":1}]`},
}

// rowApplies tells whether the event has a row at the level: the pins of a top-level member are the five events whose members a handler of Pi assigns and the later handlers
// see; a declared member is reassigned or deleted only where the event declares one.
func rowApplies(event, level string) bool {
	switch level {
	case "top":
		return true
	case "deep":
		return event != "turn_start" && event != "agent_start"
	case "type", "once":
		return slices.Contains([]string{"message_start", "message_update", "turn_start", "agent_start", "agent_end"}, event)
	}
	_, declared := memberRows[event]
	return declared
}

// deepApplies tells whether the row reads the message the event carries.
func (r messageEventRow) deepApplies() bool { return r.level == "top" || r.level == "deep" }

// topField is the member of the event the row's later handlers are asked about.
func (r messageEventRow) topField() string {
	switch r.level {
	case "type":
		return "type"
	case "reassign", "delete":
		return memberRows[r.event].member
	}
	return "probeTop"
}

// topHit tells whether the JSON of the topField member (or `absent`) shows what the row's edit did.
func (r messageEventRow) topHit(value string) bool {
	if r.level == "delete" {
		return value == "absent"
	}
	return strings.Contains(value, inPlaceEdited)
}

// editCommand is the fixture command line that subscribes the row's editing handler.
func (r messageEventRow) editCommand() (command, args string) {
	quoted := `"` + inPlaceEdited + `"`
	switch r.level {
	case "top", "deep":
		return "event_level_edit_probe", r.event + " " + r.level
	case "type":
		return "event_member_probe", r.event + " set type " + quoted
	case "once":
		return "event_member_probe", r.event + " once probeTop " + quoted
	case "reassign":
		return "event_member_probe", r.event + " set " + memberRows[r.event].member + " " + memberRows[r.event].value
	}
	return "event_member_probe", r.event + " delete " + memberRows[r.event].member
}

func (r messageEventRow) name() string { return r.event + "/" + r.level }

// messageEventRows are the events whose payload a handler can edit and Pi's runner hands to the next handler, or to the caller, by reference.
func messageEventRows() []messageEventRow {
	var rows []messageEventRow
	for _, event := range []string{"message_start", "message_update", "turn_start", "turn_end", "agent_start", "agent_end", "message_end", "context"} {
		for _, level := range []string{"top", "deep", "type", "once", "reassign", "delete"} {
			if rowApplies(event, level) {
				rows = append(rows, messageEventRow{event, level})
			}
		}
	}
	return rows
}

// messageEventObservation is what one row left, in the one shape Pi's session and PiG's are both reduced to: each field is a set, so the number of times an event is
// emitted (Pi's session also emits a system message) does not matter.
type messageEventObservation struct {
	// A is what the next handler of the editing extension saw, B what a handler of another extension saw: `:top` for an event whose own member carried the edit and
	// `<role>:deep` for a message (the event's, or the first user message of its list) that carried it.
	A, B []string
	// Entries are the roles of the session entries that hold the edit when the second prompt has finished.
	Entries []string
	// Requests tells for each of the two provider requests whether its body carries the edit.
	Requests [2]bool
	// Events are `<type>:<role>` of the events the session's own listeners received with the edit.
	Events []string
	// Stale tells whether a later dispatch of the event started with an edit a `once` row made in an earlier one.
	Stale bool
}

func (o *messageEventObservation) normalize() {
	for _, set := range []*[]string{&o.A, &o.B, &o.Entries, &o.Events} {
		slices.Sort(*set)
		*set = slices.Compact(*set)
		if *set == nil {
			*set = []string{}
		}
	}
}

// piObservations runs Pi's own AgentSession over every row (testdata/pi-session-edit-oracle.mjs).
func piObservations(t *testing.T) map[string]messageEventObservation {
	t.Helper()
	script, err := os.ReadFile("testdata/pi-session-edit-oracle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	pi, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	for _, row := range messageEventRows() {
		rows = append(rows, map[string]string{"event": row.event, "level": row.level, "member": memberRows[row.event].member, "value": memberRows[row.event].value})
	}
	out := testenv.PiEvalJSON(t, string(script), map[string]any{"pi": "file://" + pi, "rows": rows})
	var raw map[string]struct {
		Observed []struct {
			Who  string
			Role string
			Top  bool
			Deep bool
		}
		Entries []struct {
			Role string
			Hit  bool
		}
		Requests []bool
		Stale    bool
		Events   []struct {
			Type string
			Role string
			Hit  bool
		}
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	got := map[string]messageEventObservation{}
	for name, r := range raw {
		var o messageEventObservation
		for _, seen := range r.Observed {
			for flag, set := range map[string]bool{"top": seen.Top, "deep": seen.Deep} {
				if !set {
					continue
				}
				value := seen.Role + ":" + flag
				if flag == "top" {
					value = ":top"
				}
				if seen.Who == "A" {
					o.A = append(o.A, value)
				} else {
					o.B = append(o.B, value)
				}
			}
		}
		for _, e := range r.Entries {
			if e.Hit {
				o.Entries = append(o.Entries, e.Role)
			}
		}
		copy(o.Requests[:], r.Requests)
		o.Stale = r.Stale
		for _, e := range r.Events {
			if e.Hit {
				o.Events = append(o.Events, e.Type+":"+e.Role)
			}
		}
		o.normalize()
		got[name] = o
	}
	return got
}

// peerObserverSource is the extension that observes the edit from another extension: it reports, for each event it handles, the role of the message and whether the
// event's member or message carries the edit.
func peerObserverSource(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("testdata/event-peer-observer/main.mjs")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// observePiG runs one row against one harness: the editing handler and an observer of the same extension (the SDK fixture's commands), a Node extension that only
// observes, and a Session over a real provider that receives two prompts.
func observePiG(t *testing.T, tc harnessCase, row messageEventRow, holdConsumer bool) messageEventObservation {
	t.Helper()
	h := tc.make(t)
	t.Cleanup(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	})
	_, err := h.host.Load(context.Background(), subprocess.ExtConfig{Name: "event-peer-observer", Source: peerObserverSource(t), Enabled: true})
	if err != nil {
		t.Fatalf("load the peer observer: %v", err)
	}
	h.runner = inproc.NewRunner(h.host.Extensions(), t.TempDir())
	h.bridge.SetUIPromptScope(h.runner)

	command, commandArgs := row.editCommand()
	runConformanceCommandArgs(t, h, command, commandArgs)
	runConformanceCommandArgs(t, h, "event_field_probe", row.event+" "+row.topField())
	field := "message"
	if row.event == "agent_end" || row.event == "context" {
		field = "messages"
	}
	if row.deepApplies() && row.event != "turn_start" && row.event != "agent_start" {
		runConformanceCommandArgs(t, h, "event_field_probe", row.event+" "+field)
	}
	observed, requests, entries, events := runTwoPrompts(t, h, holdConsumer)

	var o messageEventObservation
	for _, line := range observed {
		switch {
		case strings.HasPrefix(line, "event_field:"+row.event+"."+row.topField()+"="):
			if row.topHit(strings.TrimSuffix(strings.TrimPrefix(line, "event_field:"+row.event+"."+row.topField()+"="), ":info")) {
				add(&o, "A", ":top")
			}
		case strings.HasPrefix(line, "member_probe_stale:"+row.event+":"):
			if strings.TrimSuffix(strings.TrimPrefix(line, "member_probe_stale:"+row.event+":"), ":info") == "true" {
				o.Stale = true
			}
		case strings.HasPrefix(line, "peerfacts:"+row.event+":"):
			var facts struct {
				ProbeTop, Type bool
				Present, HasX  []string
			}
			if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, "peerfacts:"+row.event+":"), ":info")), &facts); err != nil {
				t.Fatalf("peer facts %q: %v", line, err)
			}
			hit := false
			switch row.level {
			case "type":
				hit = facts.Type
			case "reassign":
				hit = slices.Contains(facts.HasX, row.topField())
			case "delete":
				hit = !slices.Contains(facts.Present, row.topField())
			default:
				hit = facts.ProbeTop
			}
			if hit {
				add(&o, "B", ":top")
			}
		case row.deepApplies() && strings.HasPrefix(line, "event_field:"+row.event+"."+field+"="):
			value := strings.TrimSuffix(strings.TrimPrefix(line, "event_field:"+row.event+"."+field+"="), ":info")
			if role, hit := messageEdit(value); hit {
				add(&o, "A", role+":deep")
			}
		case strings.HasPrefix(line, "peer:"+row.event+":"):
			parts := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(line, "peer:"+row.event+":"), ":info"), ":", 3)
			if len(parts) == 3 {
				if row.deepApplies() && parts[2] == "true" {
					add(&o, "B", parts[0]+":deep")
				}
			}
		}
	}
	for _, e := range entries {
		if strings.Contains(string(mustMarshal(t, e.message)), inPlaceEdited) {
			o.Entries = append(o.Entries, e.role)
		}
	}
	for i, body := range requests {
		if i < len(o.Requests) {
			o.Requests[i] = strings.Contains(string(body), inPlaceEdited)
		}
	}
	for _, e := range events {
		if e.hit {
			o.Events = append(o.Events, e.kind+":"+e.role)
		}
	}
	o.normalize()
	return o
}

func add(o *messageEventObservation, who, value string) {
	if who == "A" {
		o.A = append(o.A, value)
	} else {
		o.B = append(o.B, value)
	}
}

// messageEdit reads the JSON of an event's message (or messages) member, and reports the role of the message that is the target (the message, or the first user message
// of the list) and whether it or the member carries the edit.
func messageEdit(value string) (role string, hit bool) {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return "", false
	}
	target := decoded
	if list, ok := decoded.([]any); ok {
		target = nil
		if len(list) > 0 {
			target = list[0]
		}
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && m["role"] == "user" {
				target = item
				break
			}
		}
	}
	if m, ok := target.(map[string]any); ok {
		role, _ = m["role"].(string)
	}
	return role, strings.Contains(value, inPlaceEdited)
}

// mustMarshalQuiet is the JSON of value, or nothing when it has none.
func mustMarshalQuiet(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

type sessionEntryMessage struct {
	role    string
	message any
}

type sessionEventSeen struct {
	kind, role string
	hit        bool
}

// runTwoPrompts sends two prompts through a real Session over the harness's Runner and a provider on a recording server, and returns the notifications the handlers
// made, the bodies of the two requests, the message entries the session holds, and the events its listeners received.
func runTwoPrompts(t *testing.T, h *harness, holdConsumer bool) (observed []string, requests [][]byte, entries []sessionEntryMessage, events []sessionEventSeen) {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, body)
		n := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"test\",\"choices\":[{\"delta\":{\"content\":\"reply %d\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", n)
	}))
	defer server.Close()
	provider := newOpenAIProbeProvider(server.URL)
	defer func() { _ = provider.Close() }()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	bridged, diagnostics := coding.BridgeNewRunnerTools(h.runner.Tools())
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "probe", Provider: provider}, Runner: h.runner, Tools: bridged, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	// With holdConsumer the consumer of Events reads nothing until both prompts have finished, so every event it reads was published before, and read after, the
	// handlers' edits.
	release := make(chan struct{})
	events0 := session.Events()
	go func() {
		defer close(done)
		if holdConsumer {
			<-release
		}
		for event := range events0 {
			// The events a listener (the UI, the RPC stream) receives are the Go objects the extension handlers were handed a decode of: the message each carries is what
			// the stream writes, so the edit is in the stream when the message holds it.
			kind, role, carried := "", "", []byte(nil)
			switch e := event.(type) {
			case agent.MessageStartEvent:
				kind, carried = "message_start", mustMarshalQuiet(e.Message)
			case agent.MessageUpdateEvent:
				kind, carried = "message_update", mustMarshalQuiet(e.Message)
			case agent.MessageEndEvent:
				kind, carried = "message_end", mustMarshalQuiet(e.Message)
			case agent.TurnEndEvent:
				kind, carried = "turn_end", mustMarshalQuiet(e.Message)
			case agent.AgentEndEvent:
				kind, carried = "agent_end", mustMarshalQuiet(e.Messages)
			default:
				continue
			}
			var decoded any
			if json.Unmarshal(carried, &decoded) == nil {
				target := decoded
				if list, ok := decoded.([]any); ok {
					target = nil
					for _, item := range list {
						if m, ok := item.(map[string]any); ok && m["role"] == "user" {
							target = item
							break
						}
					}
				}
				if m, ok := target.(map[string]any); ok {
					role, _ = m["role"].(string)
				}
			}
			mu.Lock()
			events = append(events, sessionEventSeen{kind: kind, role: role, hit: strings.Contains(string(carried), inPlaceEdited)})
			mu.Unlock()
		}
	}()
	var sendErr error
	for _, prompt := range []string{"first", "second"} {
		if _, sendErr = session.Send(context.Background(), prompt); sendErr != nil {
			break
		}
	}
	close(release)
	for _, entry := range session.Entries() {
		var decoded struct {
			Type    string
			Message map[string]any
		}
		if err := json.Unmarshal(mustMarshal(t, entry), &decoded); err != nil || decoded.Type != "message" {
			continue
		}
		role, _ := decoded.Message["role"].(string)
		entries = append(entries, sessionEntryMessage{role: role, message: decoded.Message})
	}
	_ = session.Close()
	<-done
	if sendErr != nil {
		t.Fatal(sendErr)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), h.ui.Recorded()...), requests, entries, events
}

// curePending are the rows PiG does not yet match Pi on, in every SDK and mode: the cure of D541's row 10 removes a row here, and the row then requires Pi's table.
// While a row is listed, the test requires it to still differ (a row that already matches Pi is a stale entry) and logs both.
var curePending = map[string]string{
	"message_start/top":   "the event is not one object across handlers and extensions",
	"message_update/top":  "the event is not one object across handlers and extensions",
	"message_update/deep": "an edit of the partial's content is not sent back (Pi: the entry and the next request hold it)",
	"turn_start/top":      "the event is not one object across handlers and extensions",
	"agent_start/top":     "the event is not one object across handlers and extensions",
	"agent_end/top":       "the event is not one object across handlers and extensions",
}

// curedSDKs are the SDKs (a word of the harness name) whose build already carries the edits of a top-level member to the later handlers of the dispatch: the rows of those edits must
// match Pi there, and must still differ in the others. message_update/deep is another cure and has no SDK yet.
var curedSDKs = []string{"node", "go", "python", "rust", "lisp"}

// rustRecorded are the rows a Rust handler cannot carry as Pi does, and what the Rust SDK does instead (D541, the owner's decision of 2026-11-17, option (a)): a Rust handler owns its
// event as a value, so replacing an object member (the message, the messages, the partial's event) is the same value change as editing inside it, and the reply cannot tell the two
// apart as Pi's caller does. message_start/reassign and agent_end/reassign: the later handlers see the replacement and the session saves it as an edit inside the message (Pi keeps the
// original); message_update/reassign: the later handlers do not see it and the session does not hold it (Pi: later handlers see it). A Rust harness must produce exactly these
// observations, so a Rust SDK that starts doing something else fails here until the record is amended.
var rustRecorded = map[string]messageEventObservation{
	"message_start/reassign":  {A: []string{":top"}, B: []string{":top"}, Entries: []string{"user"}, Requests: [2]bool{true, true}, Events: []string{"agent_end:user", "message_end:user", "message_start:user"}},
	"agent_end/reassign":      {A: []string{":top"}, B: []string{":top"}, Entries: []string{"user"}, Events: []string{"agent_end:user"}},
	"message_update/reassign": {A: []string{}, B: []string{}, Entries: []string{}, Events: []string{}},
}

// wantObservation is what the row must read for the harness: Pi's table, except the recorded behaviour of the Rust SDK.
func wantObservation(pi map[string]messageEventObservation, harness, row string) messageEventObservation {
	if recorded, ok := rustRecorded[row]; ok && slices.Contains(strings.Split(harness, "-"), "rust") {
		return recorded
	}
	return pi[row]
}

// curedBy lists, for a row of curePending, the SDKs whose build already cures it.
var curedBy = map[string][]string{}

func init() {
	const members = "a member of the event assigned, reassigned or deleted by a handler is not seen by the later handlers of the dispatch"
	for _, row := range messageEventRows() {
		if row.level == "type" || row.level == "once" || row.level == "reassign" || row.level == "delete" {
			curePending[row.name()] = members
		}
	}
	for name := range curePending {
		if name == "message_update/deep" {
			continue
		}
		curedBy[name] = append(curedBy[name], curedSDKs...)
	}
}

// pendingFor tells whether the row is still pending for the harness.
func pendingFor(harness, row string) (string, bool) {
	reason, pending := curePending[row]
	if !pending {
		return "", false
	}
	words := strings.Split(harness, "-")
	for _, sdk := range curedBy[row] {
		if slices.Contains(words, sdk) {
			return "", false
		}
	}
	return reason, true
}

// TestInPlaceMessageEventEditsMatchPi pins what a handler's in-place edit of a message event does, at two levels, against Pi's own AgentSession: an edit of a member of the
// event object itself, and an edit inside the message (or messages) the event carries. For each of the events the table is what the next handler of the extension sees,
// what a handler of another extension sees, which entries the session holds with the edit, which of the two provider requests carry it, and which events the session's
// own listeners receive with it.
func TestInPlaceMessageEventEditsMatchPi(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	pi := piObservations(t)
	for _, row := range messageEventRows() {
		t.Logf("pi %-22s A=%v B=%v entries=%v requests=%v events=%v stale=%v", row.name(), pi[row.name()].A, pi[row.name()].B, pi[row.name()].Entries, pi[row.name()].Requests, pi[row.name()].Events, pi[row.name()].Stale)
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			for _, row := range messageEventRows() {
				t.Run(row.name(), func(t *testing.T) {
					got := observePiG(t, tc, row, false)
					want := wantObservation(pi, tc.name, row.name())
					t.Logf("pig %-22s A=%v B=%v entries=%v requests=%v events=%v stale=%v", row.name(), got.A, got.B, got.Entries, got.Requests, got.Events, got.Stale)
					if reason, pending := pendingFor(tc.name, row.name()); pending {
						if reflect.DeepEqual(got, want) {
							t.Errorf("%s %s matches Pi: remove it from curePending (%s)", tc.name, row.name(), reason)
						}
						return
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s %s differs from Pi\n got %+v\nwant %+v", tc.name, row.name(), got, want)
					}
				})
			}
		})
	}
}

// TestHeldEventsConsumerReadsTheMessageAsPublished is the failing shape of the data race: a turn_end handler of a Node extension (the packed SDK over a subprocess)
// edits the assistant message's content in place while the consumer of Session.Events has read nothing yet. The consumer then reads the message_end event of that
// message after the edit, and must read the text the message had when the event was published, as Pi's listeners, running at the event, read it.
func TestHeldEventsConsumerReadsTheMessageAsPublished(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	var row messageEventRow
	for _, r := range messageEventRows() {
		if r.name() == "turn_end/deep" {
			row = r
		}
	}
	if row.name() != "turn_end/deep" {
		t.Fatal("the turn_end/deep row is missing")
	}
	ran := false
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node-packed" {
			continue
		}
		ran = true
		t.Run(tc.name, func(t *testing.T) {
			got := observePiG(t, tc, row, true)
			for _, e := range got.Events {
				if e == "message_end:assistant" {
					t.Errorf("the held consumer's message_end of the assistant message shows the turn_end handler's later edit %q (events with the edit: %v)", inPlaceEdited, got.Events)
				}
			}
			if !slices.Contains(got.Entries, "assistant") {
				t.Errorf("the session's entry of the assistant message lacks the edit (entries with it: %v): the handler's edit did not run", got.Entries)
			}
		})
	}
	if !ran {
		t.Fatal("the subprocess-node-packed harness case is missing")
	}
}
