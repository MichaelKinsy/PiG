package uiview

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func abilities() *ClientAbilities {
	return &ClientAbilities{
		Slots: []Slot{SlotPanel, SlotStatus}, Nodes: []string{"text"}, Markdown: true, ImageFormats: []string{"png"},
		Limits: Limits{Nodes: 8, Depth: 6, SnapshotBytes: 4096, TextBytes: 64, Mounts: 2, IntentArgBytes: 64},
		Input:  Input{Keyboard: true},
	}
}

func mountArgs(t *testing.T, key, text string, intents ...IntentDecl) MountArgs {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"key": key, "slot": "panel", "view": map[string]any{"root": map[string]any{"kind": "text", "text": text}}, "intents": intents})
	a, err := DecodeMount(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func updateArgs(t *testing.T, ref Ref, text string) UpdateArgs {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"mount": ref, "view": map[string]any{"root": map[string]any{"kind": "text", "text": text}}})
	a, err := DecodeUpdate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func wantCode(t *testing.T, err *Error, code Code) {
	t.Helper()
	if err == nil || err.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestNoClientNothingMounts(t *testing.T) {
	r := New(Options{})
	_, err := r.Mount("p", mountArgs(t, "k", "x"))
	wantCode(t, err, CodeUnsupported)
}

func TestMountUpdateStaleRejected(t *testing.T) {
	r := New(Options{})
	r.SetAbilities(abilities())
	st, err := r.Mount("p", mountArgs(t, "k", "one"))
	if err != nil || st.Mount.Epoch != 1 || st.Mount.Rev != 1 || st.State != StateLive {
		t.Fatalf("mount = %+v, %v", st, err)
	}
	st2, err := r.Update("p", updateArgs(t, st.Mount, "two"))
	if err != nil || st2.Mount.Rev != 2 {
		t.Fatalf("update = %+v, %v", st2, err)
	}
	_, err = r.Update("p", updateArgs(t, st.Mount, "stale"))
	wantCode(t, err, CodeStale)
	snaps := r.Snapshots()
	if len(snaps) != 1 || string(snaps[0].View) == "" || snaps[0].Mount.Rev != 2 {
		t.Fatalf("snapshots = %+v", snaps)
	}
	other := st.Mount
	other.Epoch = 99
	_, err = r.Update("p", updateArgs(t, other, "x"))
	wantCode(t, err, CodeStale)
	_, err = r.Update("q", updateArgs(t, st2.Mount, "x"))
	wantCode(t, err, CodeNotFound)
}

func TestFirstTerminalWinsAndKeyReusable(t *testing.T) {
	r := New(Options{})
	r.SetAbilities(abilities())
	st, _ := r.Mount("p", mountArgs(t, "k", "x"))
	if s, err := r.Transition("p", "hide", RefArgs{Mount: st.Mount}); err != nil || s.State != StateHidden {
		t.Fatalf("hide = %+v %v", s, err)
	}
	if s, err := r.Transition("p", "show", RefArgs{Mount: st.Mount}); err != nil || s.State != StateLive {
		t.Fatalf("show = %+v %v", s, err)
	}
	if s, err := r.Transition("p", "finish", RefArgs{Mount: st.Mount}); err != nil || s.State != StateFinished {
		t.Fatalf("finish = %+v %v", s, err)
	}
	s, err := r.Transition("p", "remove", RefArgs{Mount: st.Mount})
	wantCode(t, err, CodeTerminal)
	if s.State != StateFinished {
		t.Fatalf("the first terminal state must win, got %s", s.State)
	}
	st2, err := r.Mount("p", mountArgs(t, "k", "again"))
	if err != nil || st2.Mount.Epoch != 2 {
		t.Fatalf("remount = %+v %v", st2, err)
	}
}

func TestBreakReasons(t *testing.T) {
	r := New(Options{})
	r.SetAbilities(abilities())
	a, _ := r.Mount("p", mountArgs(t, "a", "x"))
	b, _ := r.Mount("q", mountArgs(t, "b", "x"))
	r.Break("p", ReasonOwnerGone)
	sa, _ := r.State("p", StateArgs{Key: "a"})
	sb, _ := r.State("q", StateArgs{Key: "b"})
	if sa.State != StateBroken || sa.Reason != ReasonOwnerGone || sb.State != StateLive {
		t.Fatalf("p=%+v q=%+v", sa, sb)
	}
	_, err := r.Transition("p", "remove", RefArgs{Mount: a.Mount})
	wantCode(t, err, CodeTerminal)
	r.Break("", ReasonShutdown)
	sb, _ = r.State("q", StateArgs{Key: "b"})
	if sb.Reason != ReasonShutdown {
		t.Fatalf("q = %+v", sb)
	}
	_ = b
}

func TestAdmissionLimits(t *testing.T) {
	r := New(Options{KnownKind: func(k string) bool { return k == "text" }})
	r.SetAbilities(abilities())
	raw := []byte(`{"key":"k","slot":"footer","view":{"root":{"kind":"text"}}}`)
	a, _ := DecodeMount(raw)
	_, err := r.Mount("p", a)
	wantCode(t, err, CodeUnsupported)
	a = mountArgs(t, "k", string(make([]byte, 65)))
	_, err = r.Mount("p", a)
	wantCode(t, err, CodeLimit)
	raw = []byte(`{"key":"k","slot":"panel","view":{"root":{"kind":"mystery"}}}`)
	a, _ = DecodeMount(raw)
	_, err = r.Mount("p", a)
	wantCode(t, err, CodeInvalid)
	if _, err := r.Mount("p", mountArgs(t, "a", "x")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Mount("p", mountArgs(t, "b", "x")); err != nil {
		t.Fatal(err)
	}
	_, err = r.Mount("p", mountArgs(t, "c", "x"))
	wantCode(t, err, CodeLimit)
	_, err = r.Mount("p", mountArgs(t, "a", "x"))
	wantCode(t, err, CodeInvalid)
}

func TestDecodeIsClosed(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown field": `{"key":"k","slot":"panel","view":{"root":{}},"extra":1}`,
		"bad slot":      `{"key":"k","slot":"sidebar","view":{"root":{}}}`,
		"empty key":     `{"key":"","slot":"panel","view":{"root":{}}}`,
		"no root":       `{"key":"k","slot":"panel","view":{}}`,
		"tool card":     `{"key":"k","slot":"tool-card","view":{"root":{}}}`,
		"trailing":      `{"key":"k","slot":"panel","view":{"root":{}}} {}`,
		"bad timeout":   `{"key":"k","slot":"panel","view":{"root":{}},"intents":[{"name":"p.go","arg":{"type":"boolean"},"timeoutMs":5}]}`,
		"bad shape":     `{"key":"k","slot":"panel","view":{"root":{}},"intents":[{"name":"p.go","arg":{"type":"blob"}}]}`,
	} {
		if _, err := DecodeMount([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestShapeValidate(t *testing.T) {
	obj := Shape{Type: "object", Properties: map[string]Shape{"n": {Type: "integer"}, "c": {Type: "enum", Enum: []string{"a", "b"}}, "l": {Type: "array", Items: &Shape{Type: "string"}, MaxItems: 2}}, Required: []string{"n"}}
	if err := obj.check(); err != nil {
		t.Fatal(err)
	}
	for raw, ok := range map[string]bool{
		`{"n":3}`: true, `{"n":3,"c":"b","l":["x"]}`: true,
		`{"n":3.5}`: false, `{}`: false, `{"n":1,"z":1}`: false, `{"n":1,"c":"z"}`: false, `{"n":1,"l":["a","b","c"]}`: false, `7`: false,
	} {
		if err := obj.Validate(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", raw, err, ok)
		}
	}
	deep := Shape{Type: "array", MaxItems: 1, Items: &Shape{Type: "array", MaxItems: 1, Items: &Shape{Type: "array", MaxItems: 1, Items: &Shape{Type: "array", MaxItems: 1, Items: &Shape{Type: "boolean"}}}}}
	if deep.check() == nil {
		t.Error("depth 5 shape accepted")
	}
}

type fakeInvoker struct {
	mu        sync.Mutex
	cancelled []string
	invoke    func(ctx context.Context, inv Invocation) (Result, error)
}

// Invoke runs the fake and records the ticket of every invocation whose context ended while it ran.
func (f *fakeInvoker) Invoke(ctx context.Context, inv Invocation) (Result, error) {
	res, err := f.invoke(ctx, inv)
	if ctx.Err() != nil {
		f.mu.Lock()
		f.cancelled = append(f.cancelled, inv.Ticket)
		f.mu.Unlock()
	}
	return res, err
}

func intentMount(t *testing.T, cancellable bool, timeout int) (*Registry, Status) {
	r := New(Options{})
	r.SetAbilities(abilities())
	st, err := r.Mount("p", mountArgs(t, "k", "x", IntentDecl{Name: "p.go", Arg: Shape{Type: "integer"}, Answer: &Shape{Type: "integer"}, Cancellable: cancellable, TimeoutMs: timeout}))
	if err != nil {
		t.Fatal(err)
	}
	return r, st
}

func TestInvokeOutcomes(t *testing.T) {
	r, st := intentMount(t, true, 0)
	echo := &fakeInvoker{invoke: func(_ context.Context, inv Invocation) (Result, error) {
		return Result{Outcome: OutcomeDone, Answer: inv.Arg}, nil
	}}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`41`), echo); res.Outcome != OutcomeDone || string(res.Answer) != "41" || res.Ticket == "" {
		t.Fatalf("done = %+v", res)
	}
	for name, c := range map[string]struct {
		intent string
		arg    string
	}{"unknown": {"p.nope", `1`}, "bad arg": {"p.go", `"x"`}, "too big": {"p.go", `1` + string(make([]byte, 0)) + "0000000000000000000000000000000000000000000000000000000000000000000"}} {
		if res := r.Invoke(context.Background(), "p", st.Mount, "", c.intent, json.RawMessage(c.arg), echo); res.Outcome != OutcomeRefused {
			t.Errorf("%s = %+v", name, res)
		}
	}
	bad := &fakeInvoker{invoke: func(context.Context, Invocation) (Result, error) {
		return Result{Outcome: OutcomeDone, Answer: json.RawMessage(`"x"`)}, nil
	}}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`1`), bad); res.Outcome != OutcomeFailed {
		t.Errorf("answer outside shape = %+v", res)
	}
	boom := &fakeInvoker{invoke: func(context.Context, Invocation) (Result, error) { return Result{}, errors.New("link down") }}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`1`), boom); res.Outcome != OutcomeFailed || res.Error != "link down" {
		t.Errorf("transport error = %+v", res)
	}
	forged := &fakeInvoker{invoke: func(context.Context, Invocation) (Result, error) { return Result{Outcome: OutcomeTimeout}, nil }}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`1`), forged); res.Outcome != OutcomeFailed {
		t.Errorf("plugin-chosen timeout = %+v", res)
	}
	_, _ = r.Update("p", updateArgs(t, st.Mount, "newer"))
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`1`), echo); res.Outcome != OutcomeStale {
		t.Errorf("old rev = %+v", res)
	}
}

func TestInvokeStaleWhenMountChangesDuringAnswer(t *testing.T) {
	r, st := intentMount(t, false, 0)
	release := make(chan struct{})
	entered := make(chan struct{})
	slow := &fakeInvoker{invoke: func(context.Context, Invocation) (Result, error) {
		close(entered)
		<-release
		return Result{Outcome: OutcomeDone, Answer: json.RawMessage(`1`)}, nil
	}}
	out := make(chan Result)
	go func() { out <- r.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`1`), slow) }()
	<-entered
	_, _ = r.Update("p", updateArgs(t, st.Mount, "newer"))
	close(release)
	if res := <-out; res.Outcome != OutcomeStale {
		t.Fatalf("result = %+v", res)
	}
}

func waitCancelled(t *testing.T, f *fakeInvoker, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.cancelled)
		f.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("handler context did not end (want %d)", n)
}

func TestInvokeTimeoutAndCancelReachPlugin(t *testing.T) {
	r, st := intentMount(t, true, 100)
	hang := &fakeInvoker{invoke: func(ctx context.Context, _ Invocation) (Result, error) {
		<-ctx.Done()
		return Result{Outcome: OutcomeDone, Answer: json.RawMessage(`1`)}, nil
	}}
	start := time.Now()
	res := r.Invoke(context.Background(), "p", st.Mount, "", "p.go", json.RawMessage(`1`), hang)
	if res.Outcome != OutcomeTimeout || time.Since(start) < 90*time.Millisecond {
		t.Fatalf("timeout = %+v after %s", res, time.Since(start))
	}
	waitCancelled(t, hang, 1)
	hang.mu.Lock()
	if hang.cancelled[0] != res.Ticket {
		t.Fatalf("cancelled handlers = %v, ticket %s", hang.cancelled, res.Ticket)
	}
	hang.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	res = r.Invoke(ctx, "p", st.Mount, "", "p.go", json.RawMessage(`1`), hang)
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("caller cancel = %+v", res)
	}

	r2, st2 := intentMount(t, false, 100)
	release := make(chan struct{})
	var sawCancel atomic.Bool
	stubborn := &fakeInvoker{invoke: func(ctx context.Context, _ Invocation) (Result, error) {
		select {
		case <-ctx.Done():
			sawCancel.Store(true)
		case <-release:
		}
		return Result{Outcome: OutcomeDone, Answer: json.RawMessage(`1`)}, nil
	}}
	res = r2.Invoke(context.Background(), "p", st2.Mount, "", "p.go", json.RawMessage(`1`), stubborn)
	if res.Outcome != OutcomeTimeout {
		t.Fatalf("non-cancellable timeout = %+v", res)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	if sawCancel.Load() {
		t.Fatal("a non-cancellable intent's handler must not be cancelled")
	}
}

func TestSubscribersSeeFullSnapshotsInOrder(t *testing.T) {
	r := New(Options{})
	r.SetAbilities(abilities())
	var got []Snapshot
	r.Subscribe(func(e Event) { got = append(got, e.Snapshot) })
	st, _ := r.Mount("p", mountArgs(t, "k", "one"))
	_, _ = r.Update("p", updateArgs(t, st.Mount, "two"))
	_, _ = r.Transition("p", "finish", RefArgs{Mount: st.Mount})
	if len(got) != 3 || got[0].Mount.Rev != 1 || got[1].Mount.Rev != 2 || got[2].State != StateFinished || len(got[2].View) == 0 {
		t.Fatalf("events = %+v", got)
	}
}

func TestAbilitiesAreCopiedAndAnnounced(t *testing.T) {
	r := New(Options{})
	var calls int
	r.OnAbilities(func() { calls++ })
	a := abilities()
	r.SetAbilities(a)
	a.Slots[0] = SlotFooter
	if got := r.Abilities(); got.Slots[0] != SlotPanel {
		t.Fatal("registry aliases the caller's slots")
	}
	r.SetAbilities(nil)
	if r.Abilities() != nil || calls != 2 {
		t.Fatalf("abilities = %v, calls = %d", r.Abilities(), calls)
	}
}
