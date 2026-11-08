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

// JSONObject is a Chord JSON object as the facets read it.
type JSONObject interface{ Value(string) any }

// JObj is value as an object, nil when it is none.
func JObj(value any) JSONObject {
	object, _ := value.(JSONObject)
	return object
}

// JField is member key of an object value, nil when value is no object.
func JField(value any, key string) any {
	if object := JObj(value); object != nil {
		return object.Value(key)
	}
	return nil
}

// JStr is value as a string, "" when it is none.
func JStr(value any) string {
	text, _ := value.(string)
	return text
}

// JNum is value as a number, 0 when it is none.
func JNum(value any) float64 {
	number, _ := value.(float64)
	return number
}

// JBool is value as a boolean.
func JBool(value any) bool {
	flag, _ := value.(bool)
	return flag
}

// JList is value as a list, nil when it is none.
func JList(value any) []any {
	list, _ := value.([]any)
	return list
}
