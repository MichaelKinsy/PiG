package ai

import (
	"reflect"

	"github.com/MichaelKinsy/PiG/internal/scalarjson"
)

// cloneScalarCompat preserves the serialized copy's scalar values, empty-map omission, and explicit empty fallback list without a JSON round trip.
func cloneScalarCompat(in *ModelCompat) (*ModelCompat, bool) {
	out := *in
	if !scalarjson.CloneFields(reflect.ValueOf(&out).Elem()) {
		return nil, false
	}
	return &out, true
}
