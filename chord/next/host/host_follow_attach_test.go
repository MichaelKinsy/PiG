package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// packages/chord/src/next/host/follow.ts: `self.follow(attach)` calls attach with the source's observer during activation; the attachment's value
// is the source's value, an absent address (attach resolving `undefined`) fails the activation `unavailable`, and a rejection is the failure.
func TestFollowAttachGivesTheSourceItsValueAndAnAbsentOrRejectedAttachmentFailsTheActivation(t *testing.T) {
	t.Run("the attachment's value is the source's value, attached once with an observer", func(t *testing.T) {
		h := newMemberHarness(t)
		calls := 0
		var observed FollowObserver[any]
		var attach FollowAttach[delta.JsonValue] = func(observer FollowObserver[any], ctx context.Context) (*FollowAttachment[delta.JsonValue], error) {
			calls++
			observed = observer
			if ctx != memberBackground {
				t.Errorf("attach got context %v, want the activation's", ctx)
			}
			return &FollowAttachment[delta.JsonValue]{Value: memberCount(3), Detach: func() {}}, nil
		}
		source := newFollowSource(attach, followOwner{label: "view", ctx: memberBackground, ended: func(chord.WatchEnd) {}}, h.batch, h.loop)
		if err := source.attach(memberBackground); err != nil {
			t.Fatalf("attach: %v", err)
		}
		if calls != 1 || observed == nil {
			t.Fatalf("attach ran %d times with observer %v, want once with an observer", calls, observed)
		}
		expectEqual(t, "value", h.read(t, source), delta.JsonValue(memberCount(3)))
	})

	t.Run("an absent address is unavailable", func(t *testing.T) {
		h := newMemberHarness(t)
		var attach FollowAttach[delta.JsonValue] = func(FollowObserver[any], context.Context) (*FollowAttachment[delta.JsonValue], error) { return nil, nil }
		source := newFollowSource(attach, followOwner{label: "view: follow #1", ctx: memberBackground, ended: func(chord.WatchEnd) {}}, h.batch, h.loop)
		var chordErr *data.ChordError
		err := source.attach(memberBackground)
		if !errors.As(err, &chordErr) || chordErr.Code != data.CodeUnavailable || !strings.Contains(chordErr.Message, "view: follow #1: the followed address is absent") {
			t.Fatalf("attach of an absent address = %v, want unavailable naming the follow", err)
		}
	})

	t.Run("a rejected attachment is the activation's error", func(t *testing.T) {
		h := newMemberHarness(t)
		rejection := errors.New("runtime refused")
		var attach FollowAttach[delta.JsonValue] = func(FollowObserver[any], context.Context) (*FollowAttachment[delta.JsonValue], error) { return nil, rejection }
		source := newFollowSource(attach, followOwner{label: "view", ctx: memberBackground, ended: func(chord.WatchEnd) {}}, h.batch, h.loop)
		if err := source.attach(memberBackground); !errors.Is(err, rejection) {
			t.Fatalf("attach = %v, want the rejection", err)
		}
	})
}
