package uiview

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// client is a test client: it keeps what it was sent and can be told to refuse.
type client struct {
	mu     sync.Mutex
	got    []Delivery
	refuse bool
}

func (c *client) sink(d Delivery) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refuse {
		return false
	}
	c.got = append(c.got, d)
	return true
}

func (c *client) seqs() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]uint64, len(c.got))
	for i, d := range c.got {
		out[i] = d.Seq
	}
	return out
}

func attach(t *testing.T, r *Registry, a ClientAbilities) (*client, *Attachment, Resync) {
	t.Helper()
	c := &client{}
	att, rs, err := r.Attach(a, c.sink)
	if err != nil {
		t.Fatal(err)
	}
	return c, att, rs
}

func TestDeliveriesAreNumberedInOperationOrder(t *testing.T) {
	r := New(Options{})
	c, att, rs := attach(t, r, richAbilities())
	if rs.Attachment != att.Generation() || rs.Seq != 0 || len(rs.Snapshots) != 0 {
		t.Fatalf("first resync = %+v", rs)
	}
	st, _ := r.Mount("p", mountArgs(t, "k", "one"))
	st, _ = r.Update("p", updateArgs(t, st.Mount, "two"))
	_, _ = r.Transition("p", "hide", RefArgs{Mount: st.Mount})
	_, _ = r.Transition("p", "show", RefArgs{Mount: st.Mount})
	r.Break("p", ReasonOwnerGone)
	want := []string{"1 live 1", "2 live 2", "3 hidden 2", "4 live 2", "5 broken 2"}
	var got []string
	for _, d := range c.got {
		if d.Attachment != att.Generation() || len(d.Snapshot.View) == 0 {
			t.Fatalf("delivery %+v is not a full snapshot of this attachment", d)
		}
		got = append(got, strconv.FormatUint(d.Seq, 10)+" "+string(d.Snapshot.State)+" "+strconv.FormatUint(d.Snapshot.Mount.Rev, 10))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("deliveries = %q, want %q", got, want)
	}
	// A second client has its own generation and its own numbers, and starts from the current mounts.
	_, att2, rs2 := attach(t, r, richAbilities())
	if att2.Generation() <= att.Generation() || rs2.Seq != 0 || len(rs2.Snapshots) != 1 || rs2.Snapshots[0].State != StateBroken {
		t.Fatalf("second attachment: generation %d resync %+v", att2.Generation(), rs2)
	}
}

func TestRefusedDeliveryOpensAGapUntilResync(t *testing.T) {
	r := New(Options{})
	c, att, _ := attach(t, r, richAbilities())
	other, _, _ := attach(t, r, richAbilities())
	st, _ := r.Mount("p", mountArgs(t, "k", "one"))
	c.refuse = true
	st, _ = r.Update("p", updateArgs(t, st.Mount, "two")) // delivery 2 is refused
	if !att.InGap() {
		t.Fatal("a refused delivery did not open a gap")
	}
	st, _ = r.Update("p", updateArgs(t, st.Mount, "three")) // nothing is sent in a gap
	_, _ = r.Mount("p", mountArgs(t, "k2", "other"))
	c.refuse = false
	_, _ = r.Update("p", updateArgs(t, st.Mount, "four")) // still nothing: only Resync ends a gap
	if got := c.seqs(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("deliveries taken = %v, want only 1", got)
	}
	// The refusing client does not hold another client back.
	if got := other.seqs(); len(got) != 5 {
		t.Fatalf("the other client took %v", got)
	}
	rs, err := att.Resync()
	wantOK(t, "resync", err)
	// The refused delivery used number 2 and nothing was numbered in the gap, so the next delivery is 3.
	if rs.Seq != 2 || len(rs.Snapshots) != 2 || rs.Snapshots[0].Mount.Rev != 4 || !strings.Contains(string(rs.Snapshots[0].View), "four") || att.InGap() {
		t.Fatalf("resync = seq %d, %d snapshots, in gap %v", rs.Seq, len(rs.Snapshots), att.InGap())
	}
	_, _ = r.Transition("p", "hide", RefArgs{Mount: rs.Snapshots[0].Mount})
	if got := c.seqs(); len(got) != 2 || got[1] != 3 {
		t.Fatalf("after the resync the client took %v, want 1 and 3", got)
	}
}

func TestMissedSequenceNumberIsRepairedByResync(t *testing.T) {
	r := New(Options{})
	c, att, _ := attach(t, r, richAbilities())
	st, _ := r.Mount("p", mountArgs(t, "k", "one"))
	st, _ = r.Update("p", updateArgs(t, st.Mount, "two"))
	_, _ = r.Update("p", updateArgs(t, st.Mount, "three"))
	// The client's transport lost delivery 2: it holds 1 and 3, sees the hole and resyncs.
	c.mu.Lock()
	c.got = append(c.got[:1], c.got[2:]...)
	c.mu.Unlock()
	if got := c.seqs(); got[1]-got[0] != 2 {
		t.Fatalf("the hole is not visible: %v", got)
	}
	rs, err := att.Resync()
	wantOK(t, "resync", err)
	if rs.Seq != 3 || len(rs.Snapshots) != 1 || rs.Snapshots[0].Mount.Rev != 3 {
		t.Fatalf("resync = %+v", rs)
	}
	// The resync carries the digest the lost and the last delivery had, so the client knows what it already drew.
	if last := c.got[len(c.got)-1]; last.Snapshot.Digest == "" || last.Snapshot.Digest != rs.Snapshots[0].Digest {
		t.Errorf("digest of delivery 3 %q, of the resync %q", last.Snapshot.Digest, rs.Snapshots[0].Digest)
	}
}

// slowInvoker answers when released, or reports the cancel it saw.
type slowInvoker struct {
	started   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
	calls     int
}

func (s *slowInvoker) Invoke(ctx context.Context, inv Invocation) (Result, error) {
	s.calls++
	close(s.started)
	select {
	case <-s.release:
		return Result{Outcome: OutcomeDone}, nil
	case <-ctx.Done():
		close(s.cancelled)
		return Result{}, ctx.Err()
	}
}

func TestIntentUnderAnEndedAttachmentIsStale(t *testing.T) {
	r := New(Options{})
	_, keep, _ := attach(t, r, richAbilities())
	_, att, _ := attach(t, r, richAbilities())
	st, _ := r.Mount("p", mountArgs(t, "k", "one", IntentDecl{Name: "fx.wait", Arg: Shape{Type: "object"}, Cancellable: true}))

	// In flight when the attachment ends: settled stale, and the cancellable handler is cancelled.
	inv := &slowInvoker{started: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
	out := make(chan Result, 1)
	go func() {
		out <- att.Invoke(context.Background(), "p", st.Mount, "", "fx.wait", json.RawMessage(`{}`), inv)
	}()
	<-inv.started
	att.Detach()
	select {
	case res := <-out:
		if res.Outcome != OutcomeStale || res.Ticket == "" {
			t.Fatalf("in-flight intent = %+v, want stale", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the in-flight intent never settled")
	}
	select {
	case <-inv.cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler was not cancelled")
	}

	// Raised after the end: stale, and the plugin is not reached.
	never := &slowInvoker{started: make(chan struct{})}
	if res := att.Invoke(context.Background(), "p", st.Mount, "", "fx.wait", json.RawMessage(`{}`), never); res.Outcome != OutcomeStale || never.calls != 0 {
		t.Fatalf("intent under an ended attachment = %+v after %d calls", res, never.calls)
	}
	// The caller's own cancel stays cancelled, and another attachment still works.
	ctx, cancel := context.WithCancel(context.Background())
	third := &slowInvoker{started: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
	go func() { out <- keep.Invoke(ctx, "p", st.Mount, "", "fx.wait", json.RawMessage(`{}`), third) }()
	<-third.started
	cancel()
	if res := <-out; res.Outcome != OutcomeCancelled {
		t.Fatalf("a caller's cancel = %+v", res)
	}
	// Every other operation of an ended attachment fails stale, and a second Detach is harmless.
	if _, err := att.Resync(); err == nil || err.Code != CodeStale {
		t.Errorf("resync of an ended attachment: %v", err)
	}
	wantCode(t, att.UpdateAbilities(richAbilities()), CodeStale)
	wantCode(t, att.Report("p", st.Mount, Notice{Code: NoticeUndrawn, Severity: SeverityInfo}), CodeStale)
	att.Detach()
}

func TestDetachWaitsForADeliveryInProgress(t *testing.T) {
	r := New(Options{})
	entered, leave := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	taken, running := 0, false
	att, _, err := r.Attach(richAbilities(), func(Delivery) bool {
		mu.Lock()
		taken++
		first := taken == 1
		running = true
		mu.Unlock()
		if first {
			close(entered)
			<-leave
		}
		mu.Lock()
		running = false
		mu.Unlock()
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = r.Mount("p", mountArgs(t, "k", "one")) }()
	<-entered
	detached := make(chan bool, 1)
	go func() {
		att.Detach()
		mu.Lock()
		detached <- running
		mu.Unlock()
	}()
	select {
	case <-detached:
		t.Fatal("Detach returned while a delivery was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(leave)
	if stillRunning := <-detached; stillRunning {
		t.Fatal("Detach returned before the delivery did")
	}
	// After Detach nothing is delivered, and with no client left nothing can be mounted.
	if _, err := r.Mount("p", mountArgs(t, "k2", "two")); err == nil || err.Code != CodeUnsupported {
		t.Fatalf("mount with no client: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if taken != 1 {
		t.Fatalf("%d deliveries, want 1", taken)
	}
}

func TestUpdateAbilitiesRedeliversOnlyWhatChanged(t *testing.T) {
	r := New(Options{})
	c, att, _ := attach(t, r, richAbilities())
	plain, _ := r.Mount("p", mountArgs(t, "plain", "plain"))
	args, err := decodeRecord(t, mountRecord("alt", map[string]any{"alternatives": []any{
		map[string]any{"when": when("display.colors", "atLeast", 1000), "view": textView("truecolor")}}}))
	wantOK(t, "decode", err)
	if _, err := r.Mount("p", args); err != nil {
		t.Fatal(err)
	}
	more := richAbilities()
	more.Claims[0].Value = json.RawMessage(`4096`)
	wantOK(t, "update abilities", att.UpdateAbilities(more))
	got := c.got
	if len(got) != 3 || got[2].Seq != 3 || got[2].Snapshot.Mount.Key != "alt" || got[2].Snapshot.Alternative != 1 || got[0].Snapshot.Mount != plain.Mount {
		t.Fatalf("after the claims changed: %d deliveries, last %+v", len(got), got[len(got)-1].Snapshot.Status)
	}
	if got[1].Snapshot.Digest == got[2].Snapshot.Digest {
		t.Error("the alternative has the digest of the view")
	}
	// The same abilities again change nothing, so nothing is sent.
	wantOK(t, "update abilities", att.UpdateAbilities(more))
	if len(c.got) != 3 {
		t.Fatalf("an unchanged update sent %d deliveries", len(c.got)-3)
	}
	// Dropping a slot sends nothing.
	st, _ := r.Mount("p", MountArgs{Key: "s", Slot: SlotStatus, Content: mountArgs(t, "x", "status").Content})
	more.Slots = []Slot{SlotStatus}
	wantOK(t, "update abilities", att.UpdateAbilities(more))
	more.Slots = []Slot{SlotStatus, SlotPanel}
	n := len(c.got)
	wantOK(t, "update abilities", att.UpdateAbilities(more))
	// A client that stopped drawing a slot dropped its mounts, so regaining the slot brings them again.
	if len(c.got) != n+2 || c.got[n].Snapshot.Mount.Key != "plain" || c.got[n+1].Snapshot.Mount.Key != "alt" || st.Mount.Key != "s" {
		t.Fatalf("regaining the panel slot sent %d deliveries", len(c.got)-n)
	}
	wantCode(t, att.UpdateAbilities(ClientAbilities{Claims: []Claim{{Name: "bad", Value: json.RawMessage(`1`)}}}), CodeInvalid)
}

func TestSeveralClientsMergeAndDegrade(t *testing.T) {
	r := New(Options{})
	calls := 0
	r.OnAbilities(func() { calls++ })
	big := richAbilities()
	small := *abilities() // 8 nodes, 64 text bytes, 4096 snapshot bytes, keyboard only, no drawing, no claims
	small.Slots = []Slot{SlotPanel, SlotFooter}
	small.Claims = []Claim{{Name: "display.kind", Value: json.RawMessage(`"terminal"`)}, {Name: "display.colors", Value: json.RawMessage(`8`)}}
	_, _, _ = attach(t, r, big)
	if got := r.Abilities(); len(got.Claims) != 4 || got.Limits.TextBytes != 256 {
		t.Fatalf("one client's abilities are not its own: %+v", got)
	}
	cs, small1, _ := attach(t, r, small)
	m := r.Abilities()
	if strings.Join(slotNames(m.Slots), "+") != "panel+status+footer" || m.Limits.TextBytes != 256 || m.Limits.Nodes != 1024 || m.Limits.Mounts != 8 ||
		!m.Input.Pointer || m.Drawing == nil || len(m.Claims) != 1 || m.Claims[0].Name != "display.kind" || calls != 2 {
		t.Fatalf("merged abilities = %+v (%d announcements)", m, calls)
	}
	// 100 bytes of text fit the big client and not the small one, which is sent the fallback and marked degraded.
	rec := mountRecord("k", map[string]any{"fallback": map[string]any{"kind": "text", "id": "fb", "text": "short"}})
	long := map[string]any{"root": map[string]any{"kind": "text", "id": "v", "text": strings.Repeat("w", 100)}}
	rec["view"] = long
	args, err := decodeRecord(t, rec)
	wantOK(t, "decode", err)
	if _, err := r.Mount("p", args); err != nil {
		t.Fatalf("a mount one client can draw was refused: %v", err)
	}
	d := cs.got[0].Snapshot
	if !d.Degraded || d.Alternative != 0 || string(d.View) != `{"root":{"id":"fb","kind":"text","text":"short"}}` || d.Drawing != nil {
		t.Fatalf("small client was sent %s degraded=%v", d.View, d.Degraded)
	}
	diag, _ := r.Diagnostics("p", StateArgs{Key: "k"})
	if len(diag.Notices) != 1 || diag.Notices[0].Code != "limit" || diag.Notices[0].Path != "delivery" || diag.Notices[0].Measured == nil || *diag.Notices[0].Measured != 100 || *diag.Notices[0].Limit != 64 {
		t.Fatalf("notices = %+v", diag.Notices)
	}
	// Without a fallback that fits the client is sent the mount with no view.
	rec2 := mountRecord("k2", nil)
	rec2["view"] = long
	args2, _ := decodeRecord(t, rec2)
	if _, err := r.Mount("p", args2); err != nil {
		t.Fatal(err)
	}
	if d := cs.got[1].Snapshot; !d.Degraded || d.View != nil {
		t.Fatalf("small client was sent %s", d.View)
	}
	// A mount no client can draw is refused with the reason of a client that draws its slot.
	rec3 := mountRecord("k3", nil)
	rec3["view"] = textView(strings.Repeat("w", 300))
	args3, _ := decodeRecord(t, rec3)
	if _, err := r.Mount("p", args3); err == nil || err.Code != CodeLimit {
		t.Fatalf("a view too long for every client: %v", err)
	}
	// A slot only one client draws is sent to that client alone.
	n := len(cs.got)
	if _, err := r.Mount("p", MountArgs{Key: "f", Slot: SlotFooter, Content: mountArgs(t, "x", "foot").Content}); err != nil {
		t.Fatal(err)
	}
	if len(cs.got) != n+1 || cs.got[n].Snapshot.Slot != SlotFooter {
		t.Fatalf("footer mount reached the small client %d times", len(cs.got)-n)
	}
	small1.Detach()
	if got := r.Abilities(); len(got.Slots) != 2 || len(got.Claims) != 4 || calls != 3 {
		t.Fatalf("after the detach: %+v (%d announcements)", got, calls)
	}
}

func slotNames(s []Slot) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = string(v)
	}
	return out
}

// rfc8785Numbers is the table of RFC 8785 Appendix B, "Number Serialization Samples": the IEEE 754 double as hex and
// its required serialization. https://www.rfc-editor.org/rfc/rfc8785#appendix-B
var rfc8785Numbers = []struct{ ieee, want string }{
	{"0000000000000000", "0"},
	{"8000000000000000", "0"},
	{"0000000000000001", "5e-324"},
	{"8000000000000001", "-5e-324"},
	{"7fefffffffffffff", "1.7976931348623157e+308"},
	{"ffefffffffffffff", "-1.7976931348623157e+308"},
	{"4340000000000000", "9007199254740992"},
	{"c340000000000000", "-9007199254740992"},
	{"4430000000000000", "295147905179352830000"},
	{"44b52d02c7e14af5", "9.999999999999997e+22"},
	{"44b52d02c7e14af6", "1e+23"},
	{"44b52d02c7e14af7", "1.0000000000000001e+23"},
	{"444b1ae4d6e2ef4e", "999999999999999700000"},
	{"444b1ae4d6e2ef4f", "999999999999999900000"},
	{"444b1ae4d6e2ef50", "1e+21"},
	{"3eb0c6f7a0b5ed8c", "9.999999999999997e-7"},
	{"3eb0c6f7a0b5ed8d", "0.000001"},
	{"41b3de4355555553", "333333333.3333332"},
	{"41b3de4355555554", "333333333.33333325"},
	{"41b3de4355555555", "333333333.3333333"},
	{"41b3de4355555556", "333333333.3333334"},
	{"41b3de4355555557", "333333333.33333343"},
	{"becbf647612f3696", "-0.0000033333333333333333"},
	{"43143ff3c1cb0959", "1424953923781206.2"},
}

func TestCanonicalNumbersOfRFC8785(t *testing.T) {
	for _, tc := range rfc8785Numbers {
		bits, err := strconv.ParseUint(tc.ieee, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		// 'e' with 17 digits round-trips every double, in a spelling far from the canonical one.
		in := strconv.FormatFloat(math.Float64frombits(bits), 'e', 17, 64)
		got, err := Canonical([]byte(in))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s (%s) = %q, %v; want %q", tc.ieee, in, got, err, tc.want)
		}
	}
	// The same appendix: NaN and Infinity are errors. JSON cannot spell them, and a number that overflows to one is rejected.
	for _, bad := range []string{"1e400", "-1e400", "NaN", "Infinity"} {
		if got, err := Canonical([]byte(bad)); err == nil {
			t.Errorf("%s = %q, want an error", bad, got)
		}
	}
}

func TestCanonicalFormOfRFC8785(t *testing.T) {
	// RFC 8785 section 3.2.2, "Serialization of Primitive Data Types": the sample input and its required output.
	in := `{
  "numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
  "string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
  "literals": [null, true, false]
}`
	want := `{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€$\u000f\nA'B\"\\\\\"/"}`
	if got, err := Canonical([]byte(in)); err != nil || string(got) != want {
		t.Errorf("section 3.2.2:\n got %s (%v)\nwant %s", got, err, want)
	}
	// RFC 8785 section 3.2.3, "Sorting of Object Properties": names sort by UTF-16 code units, so the emoji (a pair
	// starting at D83D) comes before U+FB33, the reverse of their code point order.
	in = `{
  "\u20ac": "Euro Sign",
  "\r": "Carriage Return",
  "\ufb33": "Hebrew Letter Dalet With Dagesh",
  "1": "One",
  "\ud83d\ude00": "Emoji: Grinning Face",
  "\u0080": "Control",
  "\u00f6": "Latin Small Letter O With Diaeresis"
}`
	want = "{\"\\r\":\"Carriage Return\",\"1\":\"One\",\"\u0080\":\"Control\",\"\u00f6\":\"Latin Small Letter O With Diaeresis\"," +
		"\"\u20ac\":\"Euro Sign\",\"\U0001F600\":\"Emoji: Grinning Face\",\"\ufb33\":\"Hebrew Letter Dalet With Dagesh\"}"
	if got, err := Canonical([]byte(in)); err != nil || string(got) != want {
		t.Errorf("section 3.2.3:\n got %q (%v)\nwant %q", got, err, want)
	}
	// Nested objects sort at every level, arrays keep their order, and integer-like names sort as text ("10" before "2").
	if got, err := Canonical([]byte(`{"b":[{"z":1,"a":2}],"a":{"2":true,"10":false,"":null}}`)); err != nil || string(got) != `{"a":{"":null,"10":false,"2":true},"b":[{"a":2,"z":1}]}` {
		t.Errorf("nested = %s (%v)", got, err)
	}
	for what, bad := range map[string]string{
		"lone high surrogate":    `{"a":"\ud800"}`,
		"lone low surrogate":     `["\udead"]`,
		"lone surrogate name":    `{"\udc00":1}`,
		"duplicate name":         `{"a":1,"a":2}`,
		"duplicate escaped name": `{"a":1,"\u0061":2}`,
		"invalid UTF-8":          "\"\xff\"",
		"two values":             `1 2`,
		"trailing comma":         `[1,]`,
		"empty":                  ``,
	} {
		if got, err := Canonical([]byte(bad)); err == nil {
			t.Errorf("%s: %q, want an error", what, got)
		}
	}
	// An escaped backslash before "ud800" is text, not a surrogate.
	if got, err := Canonical([]byte(`"\\ud800"`)); err != nil || string(got) != `"\\ud800"` {
		t.Errorf(`"\\ud800" = %s (%v)`, got, err)
	}
	a, err1 := Digest([]byte(`{"view":{"root":{"kind":"text","text":"x"}},"n":1.0}`))
	b, err2 := Digest([]byte(`{ "n": 1, "view": {"root": {"text": "x", "kind": "text"}} }`))
	if err1 != nil || err2 != nil || a != b || len(a) != 64 {
		t.Errorf("digests %q (%v) and %q (%v) differ for one value", a, err1, b, err2)
	}
	// SHA-256 of the two bytes {} : a fixed point any implementation can check.
	if d, _ := Digest([]byte(` { } `)); d != "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a" {
		t.Errorf("digest of {} = %s", d)
	}
}
