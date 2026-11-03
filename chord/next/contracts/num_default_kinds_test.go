package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Pi contracts/schema.ts num/int `default`: any number is a number. Go spells a number as any integer or float kind, so a float32 or
// unsigned default is the same default and not a wrong type.
func TestNumAndIntTakeEveryGoNumberKindAsADefault(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"float32", float32(1.5), `{"type":"number","default":1.5}`},
		{"uint", uint(7), `{"type":"number","default":7}`},
		{"uint8", uint8(7), `{"type":"number","default":7}`},
		{"uint64", uint64(7), `{"type":"number","default":7}`},
		{"int8", int8(-3), `{"type":"number","default":-3}`},
		{"int16", int16(-3), `{"type":"number","default":-3}`},
		{"named int", litCount(4), `{"type":"number","default":4}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Num(Default(%T(%v))) panicked: %v", c.value, c.value, r)
				}
			}()
			sameJSON(t, contracts.Num(contracts.Default(c.value)), c.want)
		})
	}
	sameJSON(t, contracts.Int(contracts.Default(uint(2))), `{"type":"integer","default":2}`)
}
