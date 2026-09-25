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
