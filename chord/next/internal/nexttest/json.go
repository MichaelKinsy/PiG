package nexttest

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ParseJSON decodes JSON text into strict JSON values (objects as *delta.JsonObject in text order), as JSON.parse does.
func ParseJSON(t testing.TB, text string) any {
	t.Helper()
	value, err := chordjson.Decode([]byte(text))
	if err != nil {
		t.Fatalf("ParseJSON(%q): %v", text, err)
	}
	return value
}

// JSONText is JSON.stringify(value): the value's JSON text.
func JSONText(t testing.TB, value any) string {
	t.Helper()
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		t.Fatalf("JSONText: %v", err)
	}
	return string(text)
}

// JSONEqual reports whether two JSON values are equal as vitest's toEqual compares them: objects by their members
// whatever their order, arrays by position, numbers by value. *delta.JsonObject and map[string]any are both objects.
func JSONEqual(got, want any) bool {
	if gotMembers, ok := objectMembers(got); ok {
		wantMembers, ok := objectMembers(want)
		if !ok || len(gotMembers) != len(wantMembers) {
			return false
		}
		for key, value := range gotMembers {
			other, present := wantMembers[key]
			if !present || !JSONEqual(value, other) {
				return false
			}
		}
		return true
	}
	if gotItems, ok := got.([]any); ok {
		wantItems, ok := want.([]any)
		if !ok || len(gotItems) != len(wantItems) {
			return false
		}
		for index := range gotItems {
			if !JSONEqual(gotItems[index], wantItems[index]) {
				return false
			}
		}
		return true
	}
	if gotNumber, ok := number(got); ok {
		wantNumber, ok := number(want)
		return ok && (gotNumber == wantNumber || math.IsNaN(gotNumber) && math.IsNaN(wantNumber))
	}
	if gotBytes, ok := got.([]byte); ok {
		wantBytes, ok := want.([]byte)
		return ok && bytes.Equal(gotBytes, wantBytes)
	}
	return reflect.DeepEqual(got, want)
}

// RequireJSONEqual fails the test unless got and want are JSONEqual.
func RequireJSONEqual(t testing.TB, got, want any) {
	t.Helper()
	if !JSONEqual(got, want) {
		t.Fatalf("got %s, want %s", describe(got), describe(want))
	}
}

func describe(value any) string {
	if text, err := jsstring.MarshalJSON(value); err == nil {
		return string(text)
	}
	return fmt.Sprintf("%#v", value)
}

func objectMembers(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case *delta.JsonObject:
		members := maps.Collect(typed.All())
		return members, true
	case map[string]any:
		return typed, true
	}
	return nil, false
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	}
	return 0, false
}
