package pico3

import (
	"fmt"
	"testing"
)

func TestDefineServiceRejectsReservedAndEmptyIds(t *testing.T) {
	// chord/src/api.ts:defineService rejects these identities before publishing a token.
	for _, test := range []struct{ id, want string }{
		{"", "Service ID must not be empty"},
		{"$chord.models", "Service IDs beginning with $chord. are reserved"},
	} {
		t.Run(test.want, func(t *testing.T) {
			defer func() {
				if got := fmt.Sprint(recover()); got != test.want {
					t.Errorf("got %q, want %q", got, test.want)
				}
			}()
			DefineService[string](test.id)
		})
	}
	if got := DefineService[string]("$chord"); got.Id() != "$chord" || got.Local() {
		t.Fatal(got)
	}
}

// DefineService keeps its published function type; the optional upstream options argument (chord/src/api.ts:70-82)
// is DefineServiceWithOptions, which enforces the same identity rules.
func TestDefineServiceKeepsPublishedFunctionTypeAndOptionsVariantMatchesUpstream(t *testing.T) {
	if got := defineWith(DefineService[string], "test.remote"); got.Id() != "test.remote" || got.Local() {
		t.Errorf("DefineService = %q local=%v", got.Id(), got.Local())
	}
	if got := DefineServiceWithOptions[string]("test.local", ServiceOptions{Local: true}); got.Id() != "test.local" || !got.Local() {
		t.Errorf("DefineServiceWithOptions local = %q local=%v", got.Id(), got.Local())
	}
	if got := DefineServiceWithOptions[string]("test.remote", ServiceOptions{}); got.Local() {
		t.Error("zero options defined a local service")
	}
	for _, id := range []string{"", "$chord.models"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("DefineServiceWithOptions(%q) did not panic", id)
				}
			}()
			DefineServiceWithOptions[string](id, ServiceOptions{Local: true})
		}()
	}
}

// defineWith fixes the parameter type, so passing DefineService[string] fails to compile if its published function type changes.
func defineWith(define func(string) ServiceDefinition[string], id string) ServiceDefinition[string] {
	return define(id)
}
