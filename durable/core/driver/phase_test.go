// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// phaseRig opens a core that asks for one phase effect per payload, in order, each after the previous one completes.
func phaseRig(t *testing.T, phases *driver.Phases, payloads ...string) (*funcCore, *sqlhost.Host) {
	t.Helper()
	next := func(i int) *abi.Step {
		if i >= len(payloads) {
			return nil
		}
		return &abi.Step{Effects: []abi.Effect{{ID: uint32(i + 1), Kind: abi.EffectPhase, Payload: []byte(payloads[i])}}}
	}
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventAbort:
			return &abi.Step{Effects: []abi.Effect{{ID: 99, Kind: abi.EffectCancel, Payload: abi.U32Payload(1)}}}
		case abi.EventOpen:
			return next(0)
		case abi.EventPhaseDone:
			return next(int(ev.ID))
		}
		return nil
	}}
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "p.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	host := sqlhost.New(sqlhost.Options{Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectPhase: phases.Handler()}})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	return core, host
}

func TestPhasesRunInOrderAndCompleteAsResultOrThrown(t *testing.T) {
	var mu sync.Mutex
	var got []driver.PhaseEffect
	phases := &driver.Phases{Run: func(_ context.Context, e driver.PhaseEffect) error {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
		if e.Kind == "bad" {
			return errors.New("phase failed")
		}
		return nil
	}}
	core, _ := phaseRig(t, phases,
		`{"taskId":7,"kind":"review","phase":"gather","record":{"n":1},"invocation":4}`,
		`{"taskId":7,"kind":"bad","phase":"gather","record":null,"invocation":5}`,
		`{"taskId":7,"kind":"review","record":{"n":2},"invocation":6,"abort":true}`)
	waitFor(t, "three completions", func() bool { return len(core.events(abi.EventPhaseDone)) == 3 })
	done := core.events(abi.EventPhaseDone)
	if done[0].ID != 1 || done[0].Phase != abi.OutcomeResult || string(done[0].Payload) != "null" {
		t.Fatalf("first completion = %+v", done[0])
	}
	if done[1].ID != 2 || done[1].Phase != abi.OutcomeThrown || string(done[1].Payload) != `{"name":"Error","message":"phase failed"}` {
		t.Fatalf("a failing phase completes as thrown: %+v %s", done[1], done[1].Payload)
	}
	if done[2].ID != 3 || done[2].Phase != abi.OutcomeResult {
		t.Fatalf("abort handler completion = %+v", done[2])
	}
	mu.Lock()
	defer mu.Unlock()
	first := got[0]
	if first.TaskID != 7 || first.Kind != "review" || first.Phase == nil || *first.Phase != "gather" || string(first.Record) != `{"n":1}` || first.Invocation != 4 || first.Abort {
		t.Fatalf("effect = %+v", first)
	}
	if last := got[2]; !last.Abort || last.Phase != nil || last.Invocation != 6 {
		t.Fatalf("abort effect = %+v", last)
	}
}

// A cancel only aborts the effect's signal: the handler's terminal event still reaches the core, which settles on it (PROTOCOL.md,
// host rules; Pi awaits the handler it signalled).
func TestACancelledPhaseStillPostsItsCompletion(t *testing.T) {
	started := make(chan struct{})
	returned := make(chan error, 1)
	phases := &driver.Phases{Run: func(ctx context.Context, _ driver.PhaseEffect) error {
		close(started)
		<-ctx.Done()
		returned <- ctx.Err()
		return ctx.Err()
	}}
	core, host := phaseRig(t, phases, `{"taskId":1,"kind":"k","phase":"p","record":null,"invocation":1}`)
	<-started
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventAbort, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancel never reached the phase")
	}
	waitFor(t, "the completion of the cancelled phase", func() bool { return len(core.events(abi.EventPhaseDone)) == 1 })
	if done := core.events(abi.EventPhaseDone)[0]; done.Phase != abi.OutcomeThrown {
		t.Fatalf("a cancelled phase that returns its error completes as thrown: %+v", done)
	}
	if host.Err() != nil {
		t.Fatal(host.Err())
	}
}

func TestPhaseWithoutARuntimeOrWithAnUndecodablePayloadDiscardsTheHandle(t *testing.T) {
	for name, tc := range map[string]struct {
		phases  *driver.Phases
		payload string
		want    string
	}{
		"no runtime": {&driver.Phases{}, `{"taskId":1,"kind":"k"}`, "no custom task runtime"},
		"bad json":   {&driver.Phases{Run: func(context.Context, driver.PhaseEffect) error { return nil }}, `{"taskId":`, "phase payload"},
	} {
		t.Run(name, func(t *testing.T) {
			_, host := phaseRig(t, tc.phases, tc.payload)
			waitFor(t, "the handle to be discarded", func() bool { return host.Err() != nil })
			if !strings.Contains(host.Err().Error(), tc.want) {
				t.Fatalf("err = %v", host.Err())
			}
		})
	}
}

// Pi's uncaught throw in a phase handler completes the phase as thrown; a Go handler that panics is the same fault (the in-process
// scheduler's runHandler recovers it), so the host reports it instead of crashing.
func TestAPanickingPhaseHandlerCompletesAsThrown(t *testing.T) {
	phases := &driver.Phases{Run: func(context.Context, driver.PhaseEffect) error { panic(errors.New("phase panicked")) }}
	core, _ := phaseRig(t, phases, `{"taskId":7,"kind":"review","phase":"gather","record":{"n":1},"invocation":4}`)
	waitFor(t, "the completion", func() bool { return len(core.events(abi.EventPhaseDone)) == 1 })
	done := core.events(abi.EventPhaseDone)[0]
	if done.Phase != abi.OutcomeThrown || string(done.Payload) != `{"name":"Error","message":"phase panicked"}` {
		t.Fatalf("completion = %+v %s", done, done.Payload)
	}
}
