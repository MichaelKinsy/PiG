package ai

import (
	"reflect"
	"testing"
)

func TestTypeAlwaysMarshals(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{&typedReadDetails{}, true},
		{typedReadDetails{}, true},
		{&typedEditDetails{}, true},
		{&typedBashDetails{}, true},
		{&typedEmbedded{}, true},
		{&typedFloat{}, false},
		{&typedFloatPointer{}, false},
		{&typedInterface{}, false},
		{&typedMap{}, false},
		{&typedRecursive{}, false},
		{&typedSliceRecursive{}, false},
		{&typedMarshaler{}, false},
		{typedMarshaler{}, false},
		{&typedPointerMarshaler{}, false},
		{typedPointerMarshaler{}, false},
		{typedTextMarshaler(""), false},
		{&typedZeroer{}, false},
		{&typedHoldsMarshaler{}, false},
		{&typedChan{}, false},
		{func() {}, false},
		{1.5, false},
	} {
		if got := typeAlwaysMarshals(reflect.TypeOf(tc.value)); got != tc.want {
			t.Errorf("typeAlwaysMarshals(%T) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
