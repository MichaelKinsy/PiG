// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

func notice(kind abi.NoticeKind, id uint32, body string) abi.Notice {
	return abi.Notice{Kind: kind, Payload: append(binary.LittleEndian.AppendUint32(nil, id), body...)}
}

func clientRig(t *testing.T, respond func(abi.Event) *abi.Step) (*driver.Client, *funcCore, *sqlhost.Host) {
	t.Helper()
	core := &funcCore{respond: respond}
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	host := sqlhost.New(sqlhost.Options{Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 }})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return driver.NewClient(host), core, host
}

func TestAPIReturnsOkAndRemoteErrorsAndEchoesTheRequestOp(t *testing.T) {
	var seen []string
	client, _, _ := clientRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventAPI {
			return nil
		}
		seen = append(seen, string(ev.Payload))
		if strings.Contains(string(ev.Payload), `"op":"entry"`) {
			return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeAPIResult, ev.ID, `{"error":{"name":"NotFound","message":"no entry 9"}}`)}}
		}
		return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeAPIResult, ev.ID, `{"ok":{"id":4}}`)}}
	})
	got, err := client.API(t.Context(), "conversation", map[string]any{"id": 4})
	if err != nil || string(got) != `{"id":4}` {
		t.Fatalf("got %s, %v", got, err)
	}
	_, err = client.API(t.Context(), "entry", map[string]any{"id": 9})
	var re *driver.CoreError
	if !errors.As(err, &re) || re.Name != "NotFound" || re.Message != "no entry 9" {
		t.Fatalf("err = %v", err)
	}
	if len(seen) != 2 || seen[0] != `{"id":4,"op":"conversation"}` {
		t.Fatalf("requests = %v", seen)
	}
}

func TestAnswersRouteByRequestIDAndNeverToAnotherRequest(t *testing.T) {
	var firstID uint32
	client, core, host := clientRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventAPI && firstID == 0 {
			firstID = ev.ID
			return nil // the answer comes later
		}
		if ev.Kind == abi.EventAPI {
			// answer the second request, then the first, out of order
			return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeAPIResult, ev.ID, `{"ok":"second"}`), notice(abi.NoticeAPIResult, firstID, `{"ok":"first"}`)}}
		}
		return nil
	})
	_ = host
	first := make(chan string, 1)
	go func() {
		got, err := client.API(t.Context(), "a", nil)
		if err != nil {
			first <- err.Error()
			return
		}
		first <- string(got)
	}()
	waitFor(t, "the first request", func() bool { return len(core.events(abi.EventAPI)) == 1 })
	got, err := client.API(t.Context(), "b", nil)
	if err != nil || string(got) != `"second"` {
		t.Fatalf("second = %s, %v", got, err)
	}
	select {
	case v := <-first:
		if v != `"first"` {
			t.Fatalf("first = %s", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first request never answered")
	}
}

func TestAPIFailsWhenTheHandleIsDiscardedAndWhenTheStepIsRejected(t *testing.T) {
	client, _, host := clientRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventAPI {
			return &abi.Step{Status: abi.StatusRejected, Err: []byte(`{"name":"Bad","message":"unknown op"}`)}
		}
		return nil
	})
	_, err := client.API(t.Context(), "nope", nil)
	if _, ok := errors.AsType[*sqlhost.RejectedError](err); !ok {
		t.Fatalf("a rejected step must fail the request with the host's rejection, got %v", err)
	}
	pending := make(chan error, 1)
	silent, _, shost := clientRig(t, nil)
	go func() { _, err := silent.API(t.Context(), "slow", nil); pending <- err }()
	time.Sleep(20 * time.Millisecond)
	_ = shost.Close(t.Context())
	select {
	case err := <-pending:
		if err == nil {
			t.Fatal("a request pending at close must fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending request survived the close")
	}
	_ = host
}

func TestInspectAndAbortCarryTheRequestID(t *testing.T) {
	client, _, _ := clientRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventInspect && ev.Kind != abi.EventAbort {
			return nil
		}
		var body struct {
			RequestID uint32 `json:"requestId"`
			Query     string `json:"query"`
		}
		_ = json.Unmarshal(ev.Payload, &body)
		return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeAPIResult, body.RequestID, `{"ok":"`+string(rune('a'+ev.Kind%20))+body.Query+`"}`)}}
	})
	got, err := client.Inspect(t.Context(), map[string]any{"query": "harness"})
	if err != nil || !strings.HasSuffix(string(got), `harness"`) {
		t.Fatalf("inspect = %s, %v", got, err)
	}
	if got, err = client.Abort(t.Context(), map[string]any{"taskId": 3}); err != nil || got == nil {
		t.Fatalf("abort = %s, %v", got, err)
	}
}

func txCore(log *[]string) func(abi.Event) *abi.Step {
	queued := false
	return func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventTx {
			return nil
		}
		var op struct {
			Op    string          `json:"op"`
			State json.RawMessage `json:"state"`
		}
		_ = json.Unmarshal(ev.Payload, &op)
		*log = append(*log, op.Op+string(op.State))
		switch op.Op {
		case "begin":
			if !queued {
				queued = true
				return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeTxResult, ev.ID, `{"waiting":true}`), notice(abi.NoticeTxResult, ev.ID, `{"ready":true}`)}}
			}
			return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeTxResult, ev.ID, `{"ready":true}`)}}
		case "reject":
			return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeTxResult, ev.ID, `{"rejected":{"name":"ReadAfterWrite","message":"read after write"}}`)}}
		case "end", "abort":
			return nil
		}
		return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeTxResult, ev.ID, `{"result":{"echo":"`+op.Op+`"}}`)}}
	}
}

func TestTransactionBeginsAfterReadyRunsOpsInOrderAndEndsWithState(t *testing.T) {
	var log []string
	client, _, _ := clientRig(t, txCore(&log))
	tx, err := client.Begin(t.Context(), map[string]any{"conversationId": 3})
	if err != nil {
		t.Fatal(err)
	}
	a, err := tx.Op(t.Context(), "entry", map[string]any{"id": 1})
	if err != nil || string(a) != `{"echo":"entry"}` {
		t.Fatalf("op = %s, %v", a, err)
	}
	if _, err := tx.Op(t.Context(), "reject", nil); err == nil || !strings.Contains(err.Error(), "read after write") {
		t.Fatalf("a rejection is Pi's error: %v", err)
	}
	if err := tx.End(t.Context(), map[string]any{"next": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Op(t.Context(), "entry", nil); !errors.Is(err, driver.ErrTxEnded) {
		t.Fatalf("op after end: %v", err)
	}
	want := `begin|entry|reject|end{"next":1}`
	if got := strings.Join(log, "|"); got != want {
		t.Fatalf("core saw %s, want %s", got, want)
	}
}

func TestTransactionAbortSendsTheErrorAndFurtherOpsFail(t *testing.T) {
	var log []string
	var abortBody string
	core := txCore(&log)
	client, _, _ := clientRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventTx && strings.Contains(string(ev.Payload), `"abort"`) {
			abortBody = string(ev.Payload)
		}
		return core(ev)
	})
	tx, err := client.Begin(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Abort(t.Context(), errors.New("callback failed")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(abortBody, `"error":{"name":"Error","message":"callback failed"}`) {
		t.Fatalf("abort body = %s", abortBody)
	}
	if _, err := tx.Op(t.Context(), "entry", nil); !errors.Is(err, driver.ErrTxEnded) {
		t.Fatalf("op after abort: %v", err)
	}
	if err := tx.Abort(t.Context(), errors.New("again")); err != nil {
		t.Fatalf("abort is idempotent: %v", err)
	}
}

func TestBoundTransactionRunsOpsWithoutBegin(t *testing.T) {
	var log []string
	client, _, _ := clientRig(t, txCore(&log))
	tx := client.Bind(41)
	got, err := tx.Op(t.Context(), "doc", map[string]any{"address": "x"})
	if err != nil || string(got) != `{"echo":"doc"}` {
		t.Fatalf("op = %s, %v", got, err)
	}
	if strings.Join(log, "|") != "doc" {
		t.Fatalf("log = %v", log)
	}
}

func TestBeginWaitsForReadyWhileTheLineIsHeldByAnother(t *testing.T) {
	var txID uint32
	client, core, host := clientRig(t, func(ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventTx:
			txID = ev.ID
			return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeTxResult, ev.ID, `{"waiting":true}`)}}
		case abi.EventAbort: // the other holder ended: the line is ours
			return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeTxResult, txID, `{"ready":true}`)}}
		}
		return nil
	})
	begun := make(chan error, 1)
	go func() { _, err := client.Begin(t.Context(), nil); begun <- err }()
	waitFor(t, "the begin to be queued", func() bool { return len(core.events(abi.EventTx)) == 1 })
	select {
	case err := <-begun:
		t.Fatalf("Begin returned (%v) while the line was held: a waiting answer is not ready", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventAbort, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-begun:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Begin never saw ready")
	}
}

func TestCallbackRunsOnTheTransactionTheCoreOpened(t *testing.T) {
	var log []string
	txs := txCore(&log)
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventOpen {
			return &abi.Step{Effects: []abi.Effect{effect(1, abi.EffectCallback, payload.CallbackEffect{Name: "conversationCreated", TxID: 77, Payload: json.RawMessage(`{"id":5}`)})}}
		}
		if ev.Kind == abi.EventHookDone && ev.ID == 1 {
			return &abi.Step{Effects: []abi.Effect{effect(2, abi.EffectCallback, payload.CallbackEffect{Name: "broken", TxID: 78})}}
		}
		if ev.Kind == abi.EventHookDone && ev.ID == 2 {
			return &abi.Step{Effects: []abi.Effect{effect(3, abi.EffectCallback, payload.CallbackEffect{Name: "missing", TxID: 79})}}
		}
		return txs(ev)
	}}
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var host *sqlhost.Host
	callbacks := &driver.Callbacks{Named: map[string]driver.Callback{
		"conversationCreated": func(ctx context.Context, payload json.RawMessage, tx *driver.Tx) (any, error) {
			got, err := tx.Op(ctx, "doc", map[string]any{"payload": payload})
			return map[string]any{"saw": json.RawMessage(got)}, err
		},
		"broken": func(context.Context, json.RawMessage, *driver.Tx) (any, error) { return nil, errors.New("init failed") },
	}}
	host = sqlhost.New(sqlhost.Options{Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectCallback: callbacks.Handler()}})
	callbacks.Client = driver.NewClient(host)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	// Later effects may discard the host, so read completions from what the core received rather than through completion.
	byID := func(id uint32) (abi.Event, bool) {
		for _, e := range core.events(abi.EventHookDone) {
			if e.ID == id {
				return e, true
			}
		}
		return abi.Event{}, false
	}
	waitFor(t, "the first completion", func() bool { _, ok := byID(1); return ok })
	ev, _ := byID(1)
	if ev.Phase != abi.OutcomeResult || string(ev.Payload) != `{"saw":{"echo":"doc"}}` {
		t.Fatalf("callback completion = outcome %d %s", ev.Phase, ev.Payload)
	}
	waitFor(t, "the broken callback's completion", func() bool { _, ok := byID(2); return ok })
	broken, _ := byID(2)
	if ev := broken; ev.Phase != abi.OutcomeThrown || string(ev.Payload) != `{"name":"Error","message":"init failed"}` {
		t.Fatalf("a failing callback completes as thrown: outcome %d %s", ev.Phase, ev.Payload)
	}
	waitFor(t, "an uninstalled callback to discard the host", func() bool { return host.Err() != nil })
	if !strings.Contains(host.Err().Error(), `"missing"`) {
		t.Fatalf("err = %v", host.Err())
	}
	if strings.Join(log, "|") != "doc" {
		t.Fatalf("the core saw %v: a bound transaction is not begun again", log)
	}
}
