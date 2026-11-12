package viewcarrier

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// wire is the client's side of a connection: the lines it was written, in order. While it is stalled the output
// holds what it is handed, as a connection whose reader stopped does.
type wire struct {
	t         *testing.T
	mu        sync.Mutex
	wake      *sync.Cond
	stalled   bool
	held, got []string
	responses []map[string]any
}

func (w *wire) write(line []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stalled {
		w.held = append(w.held, string(line))
	} else {
		w.got = append(w.got, string(line))
	}
}

func (w *wire) flushed(ctx context.Context) bool {
	defer context.AfterFunc(ctx, func() {
		w.mu.Lock()
		w.wake.Broadcast()
		w.mu.Unlock()
	})()
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.stalled && ctx.Err() == nil {
		w.wake.Wait()
	}
	return ctx.Err() == nil
}

func (w *wire) stall() {
	w.mu.Lock()
	w.stalled = true
	w.mu.Unlock()
}

func (w *wire) resume() {
	w.mu.Lock()
	w.stalled = false
	w.got = append(w.got, w.held...)
	w.held = nil
	w.wake.Broadcast()
	w.mu.Unlock()
}

func (w *wire) reply(response any, written func()) {
	raw, err := jsstring.MarshalJSON(response)
	if err != nil {
		w.t.Errorf("reply %+v: %v", response, err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		w.t.Errorf("reply %s: %v", raw, err)
	}
	w.write(append(raw, '\n'))
	w.mu.Lock()
	w.responses = append(w.responses, record)
	w.mu.Unlock()
	if written != nil {
		written()
	}
}

func (w *wire) lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.got)
}

// events returns the carrier's event lines the client was written.
func (w *wire) events() []string {
	var out []string
	for _, line := range w.lines() {
		if strings.HasPrefix(line, `{"type":"ui_view`) {
			out = append(out, line)
		}
	}
	return out
}

// response returns the last reply of command to the request id.
func (w *wire) response(id, command string) map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range slices.Backward(w.responses) {
		if r["id"] == id && r["command"] == command {
			return r
		}
	}
	return nil
}

type host struct {
	reg *uiview.Registry
	inv uiview.Invoker
}

func (h *host) Views() *uiview.Registry { return h.reg }
func (h *host) InvokeViewIntentFrom(ctx context.Context, t *uiview.Attachment, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result {
	return t.Invoke(ctx, extension, ref, nodeKey, intent, arg, h.inv)
}

// plugin answers "add" at once and holds "wait" until its request is cancelled.
type plugin struct{ started chan string }

func (p *plugin) Invoke(ctx context.Context, inv uiview.Invocation) (uiview.Result, error) {
	if inv.Intent == "wait" {
		p.started <- inv.Ticket
		<-ctx.Done()
		return uiview.Result{}, ctx.Err()
	}
	return uiview.Result{Outcome: uiview.OutcomeDone, Answer: json.RawMessage(`{"sum":3}`)}, nil
}

type fixture struct {
	t *testing.T
	w *wire
	h *host
	c *Carrier
	p *plugin
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	w := &wire{t: t}
	w.wake = sync.NewCond(&w.mu)
	p := &plugin{started: make(chan string, 8)}
	f := &fixture{t: t, w: w, p: p, h: &host{reg: uiview.New(uiview.Options{}), inv: p}}
	f.c = f.connect(w)
	return f
}

// connect returns the carrier of one more connection to the same session.
func (f *fixture) connect(w *wire) *Carrier {
	c := New(Transport{Host: func() Host { return f.h }, Write: w.write, Flushed: w.flushed})
	f.t.Cleanup(func() {
		w.resume()
		c.Close()
	})
	return c
}

func abilitiesJSON(bytes int) string {
	n := strconv.Itoa(bytes)
	return `{"slots":["panel"],"nodes":["text"],"markdown":false,"imageFormats":["png"],"limits":{"nodes":64,"depth":8,"snapshotBytes":` + n + `,"textBytes":` + n + `,"mounts":16,"intentArgBytes":64},"input":{"keyboard":true,"pointer":true,"text":true}}`
}

// do sends one command line as the rpc loop does and returns its reply, or nil while an intent is in flight.
func (f *fixture) do(line string) map[string]any {
	f.t.Helper()
	return f.doOn(f.c, f.w, line)
}

func (f *fixture) doOn(c *Carrier, w *wire, line string) map[string]any {
	f.t.Helper()
	var head struct {
		ID   json.RawMessage `json:"id"`
		Type string          `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &head); err != nil || !Handles(head.Type) {
		f.t.Fatalf("%s is not a carrier command: %v", line, err)
	}
	w.mu.Lock()
	before := len(w.responses)
	w.mu.Unlock()
	c.Handle(head.ID, head.Type, []byte(line), w.reply)
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.responses) == before {
		return nil
	}
	return w.responses[len(w.responses)-1]
}

// attach attaches the fixture's connection and returns the attachment's number as the client writes it.
func (f *fixture) attach(bytes int) string {
	f.t.Helper()
	r := f.do(`{"id":"attach","type":"ui_view_attach","abilities":` + abilitiesJSON(bytes) + `}`)
	if r["success"] != true {
		f.t.Fatalf("attach = %v", r)
	}
	return strconv.Itoa(int(r["data"].(map[string]any)["attachment"].(float64)))
}

const intentDecls = `,"intents":[{"name":"add","arg":{"type":"object"},"answer":{"type":"object","properties":{"sum":{"type":"integer"}},"required":["sum"]}},{"name":"wait","arg":{"type":"object"},"cancellable":true,"timeoutMs":60000}]`

func (f *fixture) mount(key, text, extra string) uiview.Ref {
	f.t.Helper()
	a, err := uiview.DecodeMount([]byte(`{"key":"` + key + `","slot":"panel","view":{"root":{"kind":"text","text":"` + text + `"}}` + extra + `}`))
	if err != nil {
		f.t.Fatal(err)
	}
	st, err := f.h.reg.Mount("p", a)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Mount
}

func (f *fixture) update(ref uiview.Ref, text string) uiview.Ref {
	f.t.Helper()
	return f.updateWith(ref, text, "")
}

// updateWith replaces the content of a mount; extra is what the content has besides its view.
func (f *fixture) updateWith(ref uiview.Ref, text, extra string) uiview.Ref {
	f.t.Helper()
	a, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":%q,"epoch":%d,"rev":%d},"view":{"root":{"kind":"text","text":%q}}%s}`, ref.Key, ref.Epoch, ref.Rev, text, extra))
	if err != nil {
		f.t.Fatal(err)
	}
	st, err := f.h.reg.Update("p", a)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Mount
}

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// settled waits until the client was written everything the attachment queued.
func (f *fixture) settled() {
	f.t.Helper()
	eventually(f.t, "the queue never drained", func() bool {
		f.c.mu.Lock()
		defer f.c.mu.Unlock()
		return f.c.att == nil || (len(f.c.att.queue) == 0 && f.c.att.flight == 0 && !f.c.att.gapOwed)
	})
}

type event struct {
	Type       string          `json:"type"`
	Attachment uint64          `json:"attachment"`
	Seq        uint64          `json:"seq"`
	Snapshot   uiview.Snapshot `json:"snapshot"`
}

func parseEvent(t *testing.T, line string) event {
	t.Helper()
	var e event
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("%.200s: %v", line, err)
	}
	return e
}

// drawn is what a client keeps of a snapshot to know what it draws.
func drawn(seq uint64, s uiview.Snapshot) string {
	return fmt.Sprintf("%d %s/%d/%d %s alt=%d degraded=%v %s", seq, s.Mount.Key, s.Mount.Epoch, s.Mount.Rev, s.State, s.Alternative, s.Degraded, s.Digest)
}

func code(r map[string]any) string {
	if r == nil {
		return "no reply"
	}
	if r["success"] == true {
		return "ok"
	}
	c, _ := r["code"].(string)
	if text, _ := r["error"].(string); !strings.HasPrefix(text, c+": ") {
		return "error text " + text + " lacks the code " + c
	}
	return c
}

// TestCarrierMessagesAreTheGoAPI attaches a wire client and a Go client with the same abilities and requires the wire
// client to be sent what the Go client is: the same numbers, mounts, states and digests, in the same order; then every
// other message against the call it stands for.
func TestCarrierMessagesAreTheGoAPI(t *testing.T) {
	f := newFixture(t)
	var ab uiview.ClientAbilities
	if err := json.Unmarshal([]byte(abilitiesJSON(4096)), &ab); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var goClient []string
	goAtt, _, aerr := f.h.reg.Attach(ab, func(d uiview.Delivery) bool {
		mu.Lock()
		defer mu.Unlock()
		goClient = append(goClient, drawn(d.Seq, d.Snapshot))
		return true
	})
	if aerr != nil {
		t.Fatal(aerr)
	}
	defer goAtt.Detach()
	n := f.attach(4096)
	if got, want := f.w.lines()[0], `{"id":"attach","type":"response","command":"ui_view_attach","success":true,"data":{"attachment":2,"seq":0,"snapshots":[]}}`+"\n"; got != want {
		t.Fatalf("attach reply\n  %s\nwant\n  %s", got, want)
	}

	ref := f.mount("k", "one", intentDecls)
	ref = f.updateWith(ref, "two", intentDecls)
	if _, err := f.h.reg.Transition("p", "hide", uiview.RefArgs{Mount: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.reg.Transition("p", "show", uiview.RefArgs{Mount: ref}); err != nil {
		t.Fatal(err)
	}
	other := f.mount("o", "other", "")
	if _, err := f.h.reg.Finish("p", uiview.FinishArgs{Mount: other, Result: json.RawMessage(`{"picked":2}`)}); err != nil {
		t.Fatal(err)
	}
	f.settled()
	var wireClient []string
	for _, line := range f.w.events() {
		e := parseEvent(t, line)
		if e.Type != "ui_view" || strconv.FormatUint(e.Attachment, 10) != n {
			t.Fatalf("event %.200s", line)
		}
		wireClient = append(wireClient, drawn(e.Seq, e.Snapshot))
	}
	mu.Lock()
	want := slices.Clone(goClient)
	mu.Unlock()
	if len(want) != 6 || !slices.Equal(wireClient, want) {
		t.Fatalf("the wire client was sent\n  %s\nthe Go client\n  %s", strings.Join(wireClient, "\n  "), strings.Join(want, "\n  "))
	}
	// The finish result rides in the snapshot, and a line is the delivery's records and nothing else.
	last := f.w.events()[5]
	if wantLine := `{"type":"ui_view","attachment":` + n + `,"seq":6,"snapshot":{"plugin":"p","slot":"panel","mount":{"key":"o","epoch":2,"rev":1},"state":"finished","result":{"picked":2},"view":{"root":{"kind":"text","text":"other"}},"digest":"` + parseEvent(t, last).Snapshot.Digest + `"}}` + "\n"; last != wantLine {
		t.Errorf("finish line\n  %s\nwant\n  %s", last, wantLine)
	}

	// Resync returns what the Go API returns for the same abilities.
	goState, _ := goAtt.Resync()
	r := f.do(`{"id":"rs","type":"ui_view_resync","attachment":` + n + `}`)
	data, _ := json.Marshal(r["data"])
	var rs uiview.Resync
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatal(err)
	}
	if rs.Seq != 6 || strconv.FormatUint(rs.Attachment, 10) != n || len(rs.Snapshots) != len(goState.Snapshots) {
		t.Fatalf("resync = %s", data)
	}
	for i := range rs.Snapshots {
		if drawn(0, rs.Snapshots[i]) != drawn(0, goState.Snapshots[i]) {
			t.Errorf("resync snapshot %d = %s, the Go client's %s", i, drawn(0, rs.Snapshots[i]), drawn(0, goState.Snapshots[i]))
		}
	}

	// An intent returns the plugin's answer.
	f.do(`{"id":"i1","type":"ui_view_intent","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1,"rev":2},"intent":"add","arg":{}}`)
	eventually(t, "the intent never settled", func() bool { return f.w.response("i1", "ui_view_intent") != nil })
	if d := f.w.response("i1", "ui_view_intent")["data"].(map[string]any); d["outcome"] != "done" || fmt.Sprint(d["answer"]) != "map[sum:3]" || d["ticket"] != "t1" {
		t.Errorf("intent = %v", d)
	}

	// A report reaches the plugin's diagnostics.
	if r := f.do(`{"id":"rp","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"warning","message":"no room","path":"root"}}`); code(r) != "ok" || fmt.Sprint(r["data"]) != "map[]" {
		t.Errorf("report = %v", r)
	}
	diag, _ := f.h.reg.Diagnostics("p", uiview.StateArgs{Key: "k"})
	if !slices.ContainsFunc(diag.Notices, func(n uiview.Notice) bool { return n.Code == "undrawn" && n.Message == "no room" && n.Path == "root" }) {
		t.Errorf("the plugin's notices lack the report: %+v", diag.Notices)
	}
	if r := f.do(`{"id":"rp2","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"nope","epoch":1},"notice":{"code":"undrawn","severity":"info"}}`); code(r) != "not_found" {
		t.Errorf("report on no mount = %v", r)
	}
	if r := f.do(`{"id":"rp3","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":9},"notice":{"code":"undrawn","severity":"info"}}`); code(r) != "stale" {
		t.Errorf("report on an old epoch = %v", r)
	}

	// New abilities: the reply, then the mounts that now resolve differently. Without the panel slot nothing is drawn.
	before := len(f.w.lines())
	narrow := strings.Replace(abilitiesJSON(4096), `"slots":["panel"]`, `"slots":["status"]`, 1)
	if r := f.do(`{"id":"ab","type":"ui_view_abilities","attachment":` + n + `,"abilities":` + narrow + `}`); code(r) != "ok" || fmt.Sprint(r["data"]) != "map[]" {
		t.Fatalf("abilities = %v", r)
	}
	if r := f.do(`{"id":"ab2","type":"ui_view_abilities","attachment":` + n + `,"abilities":` + abilitiesJSON(4096) + `}`); code(r) != "ok" {
		t.Fatalf("abilities = %v", r)
	}
	f.settled()
	after := f.w.lines()[before:]
	if len(after) != 4 || !strings.Contains(after[0], `"id":"ab"`) || !strings.Contains(after[1], `"id":"ab2"`) || parseEvent(t, after[2]).Seq != 7 || parseEvent(t, after[3]).Seq != 8 {
		t.Errorf("after the abilities changed the client was written\n  %s", strings.Join(after, "  "))
	}

	// Detach: the reply, no event after it, and the next intent is stale.
	if r := f.do(`{"id":"dt","type":"ui_view_detach","attachment":` + n + `}`); code(r) != "ok" || fmt.Sprint(r["data"]) != "map[]" {
		t.Fatalf("detach = %v", r)
	}
	count := len(f.w.events())
	f.update(ref, "after the detach")
	if r := f.do(`{"id":"i2","type":"ui_view_intent","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1,"rev":3},"intent":"add","arg":{}}`); r == nil || r["success"] != true || r["data"].(map[string]any)["outcome"] != "stale" {
		t.Errorf("intent after the detach = %v", r)
	}
	for _, line := range []string{
		`{"id":"x1","type":"ui_view_resync","attachment":` + n + `}`,
		`{"id":"x2","type":"ui_view_detach","attachment":` + n + `}`,
		`{"id":"x3","type":"ui_view_abilities","attachment":` + n + `,"abilities":` + abilitiesJSON(4096) + `}`,
		`{"id":"x4","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"info"}}`,
	} {
		if r := f.do(line); code(r) != "stale" {
			t.Errorf("%s after the detach = %v", line, r)
		}
	}
	if len(f.w.events()) != count {
		t.Errorf("an event was written after the detach")
	}
}

// TestCarrierUnknownMemberIsInvalid pins the strict shape: an unknown member of a command or of a record inside it
// rejects the command with code invalid, and so does a missing required member.
func TestCarrierUnknownMemberIsInvalid(t *testing.T) {
	f := newFixture(t)
	n := f.attach(4096)
	f.mount("k", "one", intentDecls)
	ab := abilitiesJSON(4096)
	f.settled()
	for _, line := range []string{
		`{"id":"1","type":"ui_view_attach","abilities":` + ab + `,"version":1}`,
		`{"id":"2","type":"ui_view_attach","abilities":` + strings.Replace(ab, `"markdown"`, `"extra":1,"markdown"`, 1) + `}`,
		`{"id":"3","type":"ui_view_attach"}`,
		`{"id":"4","type":"ui_view_abilities","attachment":` + n + `,"abilities":` + ab + `,"force":true}`,
		`{"id":"5","type":"ui_view_abilities","attachment":` + n + `}`,
		`{"id":"6","type":"ui_view_abilities","abilities":` + ab + `}`,
		`{"id":"7","type":"ui_view_resync","attachment":` + n + `,"since":3}`,
		`{"id":"8","type":"ui_view_resync"}`,
		`{"id":"9","type":"ui_view_detach","attachment":` + n + `,"reason":"bye"}`,
		`{"id":"10","type":"ui_view_intent","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"intent":"add","arg":{},"ticket":"t"}`,
		`{"id":"11","type":"ui_view_intent","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1,"gen":1},"intent":"add","arg":{}}`,
		`{"id":"12","type":"ui_view_intent","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"intent":"add"}`,
		`{"id":"13","type":"ui_view_intent","plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"intent":"add","arg":{}}`,
		`{"id":"14","type":"ui_view_intent_cancel","attachment":` + n + `}`,
		`{"type":"ui_view_intent_cancel"}`,
		`{"id":"16","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"notice":{"code":"undrawn","severity":"info"}}`,
		`{"id":"17","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"info","count":2}}`,
		`{"id":"18","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"refused","severity":"info"}}`,
		`{"id":"19","type":"ui_view_report","attachment":` + n + `,"plugin":"p","mount":{"key":"k","epoch":1}}`,
	} {
		if r := f.do(line); code(r) != "invalid" {
			t.Errorf("%s = %v, want code invalid", line, r)
		}
	}
	if len(f.w.events()) != 1 {
		t.Errorf("a rejected command changed what the client was sent: %v", f.w.events())
	}
}

// TestCarrierStalledReaderGetsOneGap holds the queue at its bound of events and one past it: a client that stopped
// reading is owed at most 256 events, the 257th is refused, and when it reads again it gets the queued events, exactly
// one ui_view_gap, and nothing more until it resyncs.
func TestCarrierStalledReaderGetsOneGap(t *testing.T) {
	f := newFixture(t)
	n := f.attach(4096)
	f.w.stall()
	ref := f.mount("k", "v1", "")
	for i := 2; i <= maxQueueEvents; i++ {
		ref = f.update(ref, "v"+strconv.Itoa(i))
	}
	if f.c.att.t.InGap() {
		t.Fatalf("the queue refused one of its first %d events", maxQueueEvents)
	}
	ref = f.update(ref, "one past")
	if !f.c.att.t.InGap() {
		t.Fatalf("the queue took event %d", maxQueueEvents+1)
	}
	// In a gap the registry sends nothing, so nothing more is queued.
	for i := range 5 {
		ref = f.update(ref, "in the gap "+strconv.Itoa(i))
	}
	if len(f.w.events()) != 0 {
		t.Fatalf("a stalled client was written %d events", len(f.w.events()))
	}
	f.w.resume()
	f.settled()
	events := f.w.events()
	if len(events) != maxQueueEvents+1 {
		t.Fatalf("the client was written %d events, want %d and one gap", len(events), maxQueueEvents)
	}
	for i, line := range events[:maxQueueEvents] {
		if e := parseEvent(t, line); e.Type != "ui_view" || e.Seq != uint64(i+1) {
			t.Fatalf("event %d = %.120s", i, line)
		}
	}
	if want := `{"type":"ui_view_gap","attachment":` + n + `,"seq":257}` + "\n"; events[maxQueueEvents] != want {
		t.Fatalf("after the queued events the client was written %.200s, want %s", events[maxQueueEvents], want)
	}
	ref = f.update(ref, "still in the gap")
	time.Sleep(20 * time.Millisecond)
	if got := len(f.w.events()); got != maxQueueEvents+1 {
		t.Fatalf("%d lines followed the gap before a resync", got-maxQueueEvents-1)
	}
	// The resync returns the state a Go client reads, and the numbers go on from its seq.
	r := f.do(`{"id":"rs","type":"ui_view_resync","attachment":` + n + `}`)
	data := r["data"].(map[string]any)
	snaps := data["snapshots"].([]any)
	state := f.h.reg.Snapshots()
	if data["seq"] != float64(maxQueueEvents+1) || len(snaps) != 1 || !strings.Contains(fmt.Sprint(snaps[0]), "still in the gap") || snaps[0].(map[string]any)["digest"] == "" || len(state) != 1 || state[0].Mount != ref {
		t.Fatalf("resync = %v", r)
	}
	ref = f.update(ref, "after the resync")
	f.settled()
	if e := parseEvent(t, f.w.events()[maxQueueEvents+1]); e.Seq != maxQueueEvents+2 || !strings.Contains(string(e.Snapshot.View), "after the resync") {
		t.Fatalf("after the resync the client was written %+v", e)
	}

	// A resync that arrives while the events are still queued supersedes them: the reply, then only later numbers.
	f.w.stall()
	before := len(f.w.lines())
	for i := range 3 {
		ref = f.update(ref, "queued "+strconv.Itoa(i))
		// The first one is handed to the output, where it waits for the client; the others stay queued.
		eventually(t, "the first event was never handed to the output", func() bool {
			f.c.mu.Lock()
			defer f.c.mu.Unlock()
			return f.c.att.flight == 1
		})
	}
	f.do(`{"id":"rs2","type":"ui_view_resync","attachment":` + n + `}`)
	f.update(ref, "after the second resync")
	f.w.resume()
	f.settled()
	var seqs []string
	for _, line := range f.w.lines()[before:] {
		if strings.Contains(line, `"id":"rs2"`) {
			seqs = append(seqs, "reply")
		} else {
			seqs = append(seqs, strconv.FormatUint(parseEvent(t, line).Seq, 10))
		}
	}
	// The first queued event was already handed to the output when the resync arrived.
	if got := strings.Join(seqs, ","); got != "259,reply,262" {
		t.Errorf("around the second resync the client was written %s, want 259,reply,262", got)
	}
}

// lineBytes returns the bytes of the ui_view line of a mount or update whose text is text bytes long, for one-digit
// numbers and a one-byte key: it mounts key with a short text and measures the line.
func (f *fixture) lineOverhead(key string) (uiview.Ref, int) {
	f.t.Helper()
	before := len(f.w.events())
	ref := f.mount(key, "0123456789", "")
	f.settled()
	return ref, len(f.w.events()[before]) - 10
}

// TestCarrierQueueByteBound fills the queue to exactly its byte bound, then one byte past it.
func TestCarrierQueueByteBound(t *testing.T) {
	for _, past := range []int{0, 1} {
		t.Run("past="+strconv.Itoa(past), func(t *testing.T) {
			f := newFixture(t)
			f.attach(2 << 20)
			ref, overhead := f.lineOverhead("k")
			f.w.stall()
			text := strings.Repeat("a", maxQueueBytes/4-overhead)
			for range 3 {
				ref = f.update(ref, text)
			}
			if f.c.att.t.InGap() {
				t.Fatal("the queue refused an event below its byte bound")
			}
			f.update(ref, text+strings.Repeat("a", past))
			if got := f.c.att.t.InGap(); got != (past == 1) {
				t.Fatalf("with %d bytes queued the delivery was refused: %v", maxQueueBytes+past, got)
			}
			f.w.resume()
			f.settled()
			total := 0
			for _, line := range f.w.events()[1:] {
				if strings.HasPrefix(line, `{"type":"ui_view",`) {
					total += len(line)
				}
			}
			if want := maxQueueBytes - past*(maxQueueBytes/4); total != want {
				t.Errorf("the client was written %d bytes of events, want %d", total, want)
			}
		})
	}
}

// TestCarrierMessageBound sends a snapshot whose line is exactly the message bound and one a byte larger, as an event
// and inside a reply that carries several: the larger one is carried degraded and the plugin is told.
func TestCarrierMessageBound(t *testing.T) {
	f := newFixture(t)
	n := f.attach(5 << 20)
	_, overhead := f.lineOverhead("a")
	atBound := strings.Repeat("a", maxMessageBytes-overhead)
	f.mount("b", atBound, "")
	f.mount("c", atBound, "")
	f.mount("d", atBound+"a", "")
	f.settled()
	events := f.w.events()
	if len(events) != 4 {
		t.Fatalf("%d events", len(events))
	}
	empty, _ := uiview.Digest([]byte(`{"view":null}`))
	check := func(what string, key string, raw []byte, whole bool) {
		t.Helper()
		var s uiview.Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s %s: %v", what, key, err)
		}
		if whole && (s.Degraded || len(s.View) < maxMessageBytes-overhead || len(s.Digest) != 64 || s.Digest == empty) {
			t.Errorf("%s %s at the bound: degraded=%v view %d bytes digest %s", what, key, s.Degraded, len(s.View), s.Digest)
		}
		if !whole && (!s.Degraded || string(s.View) != "null" || s.Digest != empty || s.Mount.Key != key || s.State != uiview.StateLive) {
			t.Errorf("%s %s past the bound: degraded=%v view %.40s digest %s", what, key, s.Degraded, s.View, s.Digest)
		}
	}
	for i, key := range []string{"b", "c"} {
		if len(events[i+1]) != maxMessageBytes {
			t.Errorf("the line of %s is %d bytes, want the bound %d", key, len(events[i+1]), maxMessageBytes)
		}
		e := parseEvent(t, events[i+1])
		raw, _ := json.Marshal(e.Snapshot)
		check("event", key, raw, true)
	}
	e := parseEvent(t, events[3])
	raw, _ := json.Marshal(e.Snapshot)
	check("event", "d", raw, false)
	diag, _ := f.h.reg.Diagnostics("p", uiview.StateArgs{Key: "d"})
	if !slices.ContainsFunc(diag.Notices, func(n uiview.Notice) bool {
		return n.Code == "limit" && n.Path == "delivery" && n.Measured != nil && *n.Measured == maxMessageBytes+1 && n.Limit != nil && *n.Limit == maxMessageBytes
	}) {
		t.Errorf("the plugin was not told: %+v", diag.Notices)
	}
	if diag, _ := f.h.reg.Diagnostics("p", uiview.StateArgs{Key: "c"}); len(diag.Notices) != 0 {
		t.Errorf("the mount at the bound has notices: %+v", diag.Notices)
	}

	// A reply carries several snapshots: each obeys the bound of a line, the reply as a whole does not.
	f.do(`{"id":"rs","type":"ui_view_resync","attachment":` + n + `}`)
	lines := f.w.lines()
	reply := lines[len(lines)-1]
	if len(reply) < 2*maxMessageBytes {
		t.Fatalf("the resync reply is %d bytes", len(reply))
	}
	var parsed struct {
		Data struct {
			Snapshots []json.RawMessage `json:"snapshots"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(reply), &parsed); err != nil || len(parsed.Data.Snapshots) != 4 {
		t.Fatalf("resync reply: %v, %d snapshots", err, len(parsed.Data.Snapshots))
	}
	check("reply", "b", parsed.Data.Snapshots[1], true)
	check("reply", "c", parsed.Data.Snapshots[2], true)
	check("reply", "d", parsed.Data.Snapshots[3], false)

	// A command line past the bound is refused before it is read.
	long := `{"id":"big","type":"ui_view_intent","attachment":` + n + `,"plugin":"p","mount":{"key":"a","epoch":1,"rev":1},"intent":"add","arg":{"pad":"`
	long += strings.Repeat("a", maxMessageBytes-len(long)-3) + `"}}`
	f.do(long)
	eventually(t, "the intent of a command line at the bound never settled", func() bool { return f.w.response("big", "ui_view_intent") != nil })
	// The line is read; the registry then refuses the argument, which is past the client's own intentArgBytes.
	if r := f.w.response("big", "ui_view_intent"); r["success"] != true || r["data"].(map[string]any)["outcome"] != "refused" {
		t.Errorf("a command line of %d bytes = %v", len(long), r)
	}
	if r := f.do(strings.Replace(strings.Replace(long, `"pad":"`, `"pad":"a`, 1), `"id":"big"`, `"id":"BIG"`, 1)); code(r) != "limit" {
		t.Errorf("a command line of %d bytes = %v, want code limit", len(long)+1, r)
	}
}

// TestCarrierManagedAssetIsUnsupported: assets ride inline. A mount with a managed asset is carried degraded and the
// plugin reads why.
func TestCarrierManagedAssetIsUnsupported(t *testing.T) {
	f := newFixture(t)
	f.attach(4096)
	asset := func(delivery string) string {
		return `,"assets":[{"id":"img","mime":"image/png","size":4,"sha256":"` + strings.Repeat("ab", 32) + `","delivery":"` + delivery + `"}]`
	}
	f.mount("in", "inline", asset("inline"))
	f.mount("mg", "managed", asset("managed"))
	f.settled()
	events := f.w.events()
	if len(events) != 2 {
		t.Fatalf("%d events", len(events))
	}
	if e := parseEvent(t, events[0]); e.Snapshot.Degraded || len(e.Snapshot.Assets) != 1 || !strings.Contains(string(e.Snapshot.View), "inline") {
		t.Errorf("inline asset: %s", events[0])
	}
	if e := parseEvent(t, events[1]); !e.Snapshot.Degraded || len(e.Snapshot.Assets) != 0 || string(e.Snapshot.View) != "null" {
		t.Errorf("managed asset: %s", events[1])
	}
	diag, _ := f.h.reg.Diagnostics("p", uiview.StateArgs{Key: "mg"})
	if !slices.ContainsFunc(diag.Notices, func(n uiview.Notice) bool { return n.Code == "unsupported" && n.Path == "delivery" }) {
		t.Errorf("the plugin was not told: %+v", diag.Notices)
	}
}

// TestCarrierAttachmentBounds: one attachment per connection at a time, and the registry's bound of 8 counts the Go
// clients and the wire clients together.
func TestCarrierAttachmentBounds(t *testing.T) {
	f := newFixture(t)
	var ab uiview.ClientAbilities
	if err := json.Unmarshal([]byte(abilitiesJSON(4096)), &ab); err != nil {
		t.Fatal(err)
	}
	var goClients []*uiview.Attachment
	for range 7 {
		a, _, err := f.h.reg.Attach(ab, nil)
		if err != nil {
			t.Fatal(err)
		}
		goClients = append(goClients, a)
	}
	n := f.attach(4096)
	if n != "8" {
		t.Fatalf("the eighth attachment is %s", n)
	}
	if r := f.do(`{"id":"again","type":"ui_view_attach","abilities":` + abilitiesJSON(4096) + `}`); code(r) != "limit" {
		t.Errorf("a second attachment on the connection = %v", r)
	}
	second := &wire{t: t}
	second.wake = sync.NewCond(&second.mu)
	c2 := f.connect(second)
	attach := `{"id":"ninth","type":"ui_view_attach","abilities":` + abilitiesJSON(4096) + `}`
	if r := f.doOn(c2, second, attach); code(r) != "limit" {
		t.Errorf("the ninth attachment of the host = %v", r)
	}
	goClients[0].Detach()
	if r := f.doOn(c2, second, attach); code(r) != "ok" {
		t.Errorf("an attachment after one ended = %v", r)
	}
	// After a detach the connection attaches again.
	if r := f.do(`{"id":"dt","type":"ui_view_detach","attachment":8}`); code(r) != "ok" {
		t.Fatalf("detach = %v", r)
	}
	if r := f.do(`{"id":"re","type":"ui_view_attach","abilities":` + abilitiesJSON(4096) + `}`); code(r) != "ok" {
		t.Errorf("attach after detach = %v", r)
	}
}

// TestCarrierSessionReplaced: an attachment belongs to one session's registry. Once the session was replaced the next
// command under it is stale, and the same connection attaches to the new session.
func TestCarrierSessionReplaced(t *testing.T) {
	f := newFixture(t)
	n := f.attach(4096)
	ref := f.mount("k", "one", intentDecls)
	old := f.h.reg
	f.h = &host{reg: uiview.New(uiview.Options{}), inv: f.p}
	old.Break("", uiview.ReasonSessionReplaced)
	f.settled()
	if e := parseEvent(t, f.w.events()[1]); e.Snapshot.State != uiview.StateBroken || e.Snapshot.Reason != uiview.ReasonSessionReplaced {
		t.Errorf("the replaced session's mount arrived as %+v", e.Snapshot.Status)
	}
	if r := f.do(`{"id":"rs","type":"ui_view_resync","attachment":` + n + `}`); code(r) != "stale" {
		t.Errorf("resync under the replaced session = %v", r)
	}
	if r := f.do(fmt.Sprintf(`{"id":"i","type":"ui_view_intent","attachment":%s,"plugin":"p","mount":{"key":"k","epoch":%d,"rev":%d},"intent":"add","arg":{}}`, n, ref.Epoch, ref.Rev)); r == nil || r["data"].(map[string]any)["outcome"] != "stale" {
		t.Errorf("intent under the replaced session = %v", r)
	}
	if got := f.attach(4096); got != "1" {
		t.Errorf("the new session's attachment is %s", got)
	}
	f.mount("k2", "two", "")
	f.settled()
	if e := parseEvent(t, f.w.events()[2]); e.Seq != 1 || e.Snapshot.Mount.Key != "k2" {
		t.Errorf("the new session's first event = %+v", e)
	}
	// Replacing the session while attached also lets the connection attach without a command in between.
	f.h = &host{reg: uiview.New(uiview.Options{}), inv: f.p}
	if got := f.attach(4096); got != "1" {
		t.Errorf("attach straight after a replacement = %s", got)
	}
}

// TestCarrierIntentCancel: the cancel names the intent request by its id, the intent replies cancelled, and an
// intent sent without an id still settles but cannot be cancelled.
func TestCarrierIntentCancel(t *testing.T) {
	f := newFixture(t)
	n := f.attach(4096)
	ref := f.mount("k", "one", intentDecls)
	intent := func(id, name string) string {
		head := `{`
		if id != "" {
			head += `"id":"` + id + `",`
		}
		return head + fmt.Sprintf(`"type":"ui_view_intent","attachment":%s,"plugin":"p","mount":{"key":"k","epoch":%d,"rev":%d},"intent":%q,"arg":{}}`, n, ref.Epoch, ref.Rev, name)
	}
	if r := f.do(intent("w1", "wait")); r != nil {
		t.Fatalf("an intent in flight replied %v", r)
	}
	<-f.p.started
	if r := f.do(intent("w1", "add")); code(r) != "invalid" {
		t.Errorf("a second intent under an id in flight = %v", r)
	}
	if r := f.do(`{"id":"nope","type":"ui_view_intent_cancel"}`); code(r) != "not_found" {
		t.Errorf("cancel of an unknown id = %v", r)
	}
	if r := f.do(`{"id":"w1","type":"ui_view_intent_cancel"}`); code(r) != "ok" || r["command"] != "ui_view_intent_cancel" {
		t.Errorf("cancel = %v", r)
	}
	eventually(t, "the cancelled intent never settled", func() bool { return f.w.response("w1", "ui_view_intent")["success"] == true })
	if d := f.w.response("w1", "ui_view_intent")["data"].(map[string]any); d["outcome"] != "cancelled" || d["ticket"] == "" {
		t.Errorf("cancelled intent = %v", d)
	}
	if r := f.do(`{"id":"w1","type":"ui_view_intent_cancel"}`); code(r) != "not_found" {
		t.Errorf("cancel of a settled intent = %v", r)
	}
	// Without an id: it settles, and its reply has no id.
	f.do(intent("", "add"))
	eventually(t, "the intent without an id never settled", func() bool {
		f.w.mu.Lock()
		defer f.w.mu.Unlock()
		last := f.w.responses[len(f.w.responses)-1]
		_, has := last["id"]
		data, _ := last["data"].(map[string]any)
		return last["command"] == "ui_view_intent" && !has && data["outcome"] == "done"
	})
	// Two without an id run side by side; neither can be named by a cancel.
	f.do(intent("", "wait"))
	f.do(intent("", "wait"))
	<-f.p.started
	<-f.p.started
	if r := f.do(`{"type":"ui_view_intent_cancel"}`); code(r) != "invalid" {
		t.Errorf("cancel without an id = %v", r)
	}
	f.do(`{"id":"dt","type":"ui_view_detach","attachment":` + n + `}`)
	eventually(t, "the intents in flight at the detach never settled", func() bool {
		f.w.mu.Lock()
		defer f.w.mu.Unlock()
		stale := 0
		for _, r := range f.w.responses {
			if d, _ := r["data"].(map[string]any); r["command"] == "ui_view_intent" && d["outcome"] == "stale" {
				stale++
			}
		}
		return stale == 2
	})
}

// TestCarrierCloseSettlesIntentAndLeaksNothing closes the connection with an intent in flight and events queued for a
// client that stopped reading: the intent settles stale, its handler is cancelled, and every goroutine the carrier
// started has ended.
func TestCarrierCloseSettlesIntentAndLeaksNothing(t *testing.T) {
	before := runtime.NumGoroutine()
	w := &wire{t: t}
	w.wake = sync.NewCond(&w.mu)
	p := &plugin{started: make(chan string, 1)}
	f := &fixture{t: t, w: w, p: p, h: &host{reg: uiview.New(uiview.Options{}), inv: p}}
	f.c = New(Transport{Host: func() Host { return f.h }, Write: w.write, Flushed: w.flushed})
	n := f.attach(4096)
	ref := f.mount("k", "one", intentDecls)
	f.settled()
	w.stall()
	for i := range 300 {
		ref = f.updateWith(ref, "v"+strconv.Itoa(i), intentDecls)
	}
	f.do(fmt.Sprintf(`{"id":"w","type":"ui_view_intent","attachment":%s,"plugin":"p","mount":{"key":"k","epoch":%d,"rev":%d},"intent":"wait","arg":{}}`, n, ref.Epoch, ref.Rev))
	<-p.started
	if got := runtime.NumGoroutine(); got <= before {
		t.Fatalf("%d goroutines with an attachment and an intent in flight, %d before", got, before)
	}
	f.c.Close()
	r := w.response("w", "ui_view_intent")
	if r == nil || r["success"] != true || r["data"].(map[string]any)["outcome"] != "stale" {
		t.Fatalf("the intent in flight when the connection closed = %v", r)
	}
	eventually(t, "goroutines outlived the connection", func() bool { return runtime.NumGoroutine() <= before })
	if got := len(f.h.reg.Snapshots()); got != 1 {
		t.Errorf("%d mounts after the connection closed", got)
	}
	// The registry holds no attachment: with no client a mount is unsupported.
	a, _ := uiview.DecodeMount([]byte(`{"key":"z","slot":"panel","view":{"root":{"kind":"text","text":"z"}}}`))
	if _, err := f.h.reg.Mount("p", a); err == nil || err.Code != uiview.CodeUnsupported {
		t.Errorf("a mount after the connection closed = %v", err)
	}
}

// TestCarrierNoHost: a session without an extension host has no registry to attach to.
func TestCarrierNoHost(t *testing.T) {
	w := &wire{t: t}
	w.wake = sync.NewCond(&w.mu)
	f := &fixture{t: t, w: w}
	f.c = New(Transport{Host: func() Host { return nil }, Write: w.write, Flushed: w.flushed})
	defer f.c.Close()
	if r := f.do(`{"id":"a","type":"ui_view_attach","abilities":` + abilitiesJSON(4096) + `}`); code(r) != "unsupported" {
		t.Errorf("attach without a host = %v", r)
	}
}
