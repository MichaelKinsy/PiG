package routingtest_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// packages/server/src/testing/host.ts TestHarness: failAttachmentRelease, nextServiceResult and failClose are readable scripted state; a scripted
// service result is consumed by the next call, which then reports {"ok":true} again, the default.
// Pi: packages/server/src/testing/host.ts:34 (failAttachmentRelease)
// Pi: packages/server/src/testing/host.ts:37 (nextServiceResult)
// Pi source: packages/server/src/testing/host.ts:25-38 (TestHarness).
// mutation-checked: negating the condition `gate != nil` at host.go:238 fails it.
func TestTestHarnessScriptedStateReadsBack(t *testing.T) {
	harness := routingtest.NewTestHarness(routing.BasicSessionMetadata{ID: "session-1"})
	if harness.FailAttachmentRelease() != nil || string(harness.NextServiceResult()) != `{"ok":true}` {
		t.Fatalf("a new harness carries scripted state: release %v, result %s", harness.FailAttachmentRelease(), harness.NextServiceResult())
	}
	failure := errors.New("release failed")
	harness.SetFailAttachmentRelease(failure)
	if !errors.Is(harness.FailAttachmentRelease(), failure) {
		t.Fatalf("FailAttachmentRelease() = %v", harness.FailAttachmentRelease())
	}
	harness.SetNextServiceResult(json.RawMessage(`{"scripted":1}`))
	if string(harness.NextServiceResult()) != `{"scripted":1}` {
		t.Fatalf("NextServiceResult() = %s", harness.NextServiceResult())
	}
	got, err := harness.InvokeService(chord.ServiceCall{ServiceId: "test.session", Member: "run", Args: []json.RawMessage{}})
	mustNoError(t, err)
	if string(got) != `{"scripted":1}` {
		t.Fatalf("scripted call = %s", got)
	}
	got, err = harness.InvokeService(chord.ServiceCall{ServiceId: "test.session", Member: "run", Args: []json.RawMessage{}})
	mustNoError(t, err)
	if string(got) != `{"ok":true}` || string(harness.NextServiceResult()) != `{"ok":true}` {
		t.Fatalf("call after the scripted result = %s (next result %s)", got, harness.NextServiceResult())
	}
}

// host.ts gateNextClose: the gated Close is counted, parks at the gate, and completes only after the gate's release resolves; the following Close is not gated.
// Pi: packages/server/src/testing/host.ts:102 (gateNextClose)
// Pi source: packages/server/src/testing/host.ts:38 (nextCloseGate) and :83-90.
// mutation-checked: the mutant "Close does not resolve Closed" fails it.
func TestTestHarnessGatesTheNextClose(t *testing.T) {
	ctx := boundedContext(t)
	harness := routingtest.NewTestHarness(routing.BasicSessionMetadata{ID: "session-1"})
	gate := harness.GateNextClose()
	done := make(chan error, 1)
	go func() { done <- harness.Close(ctx) }()
	await(t, ctx, gate.Entered.Promise(), "close gate entry")
	if harness.CloseCount() != 1 {
		t.Fatalf("CloseCount() = %d at the gate, want 1", harness.CloseCount())
	}
	select {
	case err := <-done:
		t.Fatalf("the gated close finished before its release: %v", err)
	case <-harness.Closed().Promise():
		t.Fatal("Closed resolved before the gate's release")
	default:
	}
	gate.Release.Resolve(struct{}{})
	mustNoError(t, <-done)
	await(t, ctx, harness.Closed().Promise(), "Closed")
	mustNoError(t, harness.Close(ctx))
	if harness.CloseCount() != 2 {
		t.Fatalf("CloseCount() = %d, want 2", harness.CloseCount())
	}
}
