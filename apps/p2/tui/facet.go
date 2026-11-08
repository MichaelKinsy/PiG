package tui

// Helpers the plugins' TUI facets share.

import (
	"context"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// Percent is an overlay size of percent of the terminal ("90%").
func Percent(percent float64) *pitui.OverlayValue {
	return &pitui.OverlayValue{Value: percent, Percent: true}
}

type memberSource struct{ member *host.ReferenceMember }

func (s memberSource) Value() delta.JsonValue {
	value, _ := s.member.Value()
	return value
}

func (s memberSource) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	return s.member.Watch(ctx)
}

// FollowMember calls fn with a reference member's value (at once when it has one, then at each change) until stop. fn runs off the owner loop: post to it.
func FollowMember(member *host.ReferenceMember, fn func(delta.JsonValue)) (stop func()) {
	return p2runtime.FollowJSON[delta.JsonValue](memberSource{member}, fn)
}
