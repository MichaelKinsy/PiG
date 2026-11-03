package host

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// followServing serves followView through `self.follow(attach)` on a fresh host and returns what Get gives for it.
func followServing(t *testing.T, attach FollowAttach[delta.JsonValue]) (*Reference, error) {
	t.Helper()
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: errs.push})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(followView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			followed := self.Follow(attach)
			sum := self.Derive([]any{followed}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("n").(float64), nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"a": followed, "sum": sum},
			}}, nil
		}, ServeOptions{})
	})
	if _, err := host.Load([]HostFacet{{Name: "@test/app", Facet: facet}}, background); err != nil {
		t.Fatal(err)
	}
	return host.Get(background, followView, jsonObject(), GetOptions{ReadyTimeout: followMs(50)})
}

// packages/chord/src/next/host/follow.ts: `self.follow(attach)` calls attach once during activation with the source's observer; the attachment's
// value is the followed member's value, an absent address (attach resolving `undefined`) fails the activation `unavailable`, and a rejection is
// the activation's failure.
func TestFollowAttachServedThroughSelfFollowGivesTheMemberItsValueAndAnAbsentOrRejectedAttachmentFailsTheGet(t *testing.T) {
	t.Run("the attachment's value is the member's value, attached once with an observer and a context", func(t *testing.T) {
		var calls atomic.Int64
		var observed atomic.Bool
		var attach FollowAttach[delta.JsonValue] = func(observer FollowObserver[any], ctx context.Context) (*FollowAttachment[delta.JsonValue], error) {
			calls.Add(1)
			observed.Store(observer != nil && ctx != nil)
			return &FollowAttachment[delta.JsonValue]{Value: memberCount(3), Detach: func() {}}, nil
		}
		view, err := followServing(t, attach)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got := calls.Load(); got != 1 || !observed.Load() {
			t.Fatalf("attach ran %d times (observer and context given: %v), want once with both", got, observed.Load())
		}
		expectEqual(t, "member a", kmMember(t, view, "a"), delta.JsonValue(memberCount(3)))
		expectEqual(t, "member sum", kmMember(t, view, "sum"), delta.JsonValue(float64(3)))
	})

	t.Run("an absent address fails the activation unavailable", func(t *testing.T) {
		var attach FollowAttach[delta.JsonValue] = func(FollowObserver[any], context.Context) (*FollowAttachment[delta.JsonValue], error) {
			return nil, nil
		}
		if _, err := followServing(t, attach); err == nil || !strings.Contains(err.Error(), "followed address is absent") {
			t.Fatalf("get = %v, want it to fail with followed address is absent", err)
		}
	})

	t.Run("a rejected attachment is the activation's failure", func(t *testing.T) {
		rejection := errors.New("runtime refused")
		var attach FollowAttach[delta.JsonValue] = func(FollowObserver[any], context.Context) (*FollowAttachment[delta.JsonValue], error) {
			return nil, rejection
		}
		if _, err := followServing(t, attach); err == nil || !strings.Contains(err.Error(), "runtime refused") {
			t.Fatalf("get = %v, want it to fail with the rejection", err)
		}
	})
}
