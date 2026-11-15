package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Go-only (no Pi case): a Pi decision's `settle` promise never settles once the decision is released; a Go task that waits
// for it (`settleWait`, a route's) must end. It ends through a context, the decision's lifetime, which `release` cancels.

// noLong is a `blocked` that reports nothing: a route's decision is no attempt.
type noLong struct{}

func (noLong) blockedBy(*blocker) {}

func TestDecisionSettleWait(t *testing.T) {
	made := func(t *testing.T) (*chordHost, *Decision) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "write"})
		h := s.h.(*chordHost)
		var decision *Decision
		var err error
		h.enter(func() {
			decision = newDecision(decisionOwner{host: h, label: "test.route", caller: h.own(), moved: func() {}})
			err = decision.declare(func(self AdmitSelf) (any, error) {
				return self.Derive(nil, func([]any) (delta.JsonValue, error) { return Allow("v"), nil }), nil
			}, background)
		})
		must(t, err)
		return h, decision
	}

	t.Run("a settled decision's waiter gets its allow", func(t *testing.T) {
		_, decision := made(t)
		found := decision.settleWait(noLong{}, background)
		if found.kind != verdictAllow || found.value != "v" {
			t.Fatalf("verdict = %+v, want allow v", found)
		}
	})

	t.Run("a released decision's waiter returns, and what it returns is neither an allow nor a denial", func(t *testing.T) {
		h, decision := made(t)
		h.enter(decision.release)
		found := decision.settleWait(noLong{}, background)
		if found.kind != verdictBroken || found.err == nil || found.err.Code != data.CodeReleased {
			t.Fatalf("verdict = %+v, want broken released", found)
		}
		// The same through `settle`, the form an attempt's parked check takes: its callers act on allow and deny only.
		var promise *keyedPromise[*verdict]
		h.enter(func() { promise = decision.settle(noLong{}, background) })
		got, err := promise.await(background)
		if err != nil || got.kind != verdictBroken || got.err.Code != data.CodeReleased {
			t.Fatalf("settle = %+v, %v, want broken released", got, err)
		}
	})
}
