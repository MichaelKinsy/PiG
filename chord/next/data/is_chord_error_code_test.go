package data_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Pi next/data/errors.ts:79-85 `isChordError(error, code?)`: one function; the optional code adds `error.code === code` to
// the `instanceof ChordError` question.
func TestIsChordErrorTakesAnOptionalCode(t *testing.T) {
	gone := data.NewChordError(data.ChordErrorInit{Code: data.CodeGone, Message: "gone"}, nil)
	cases := []struct {
		name string
		err  error
		code []data.ChordErrorCode
		want bool
	}{
		{"no code, a ChordError", gone, nil, true},
		{"matching code", gone, []data.ChordErrorCode{data.CodeGone}, true},
		{"other code", gone, []data.ChordErrorCode{data.CodeInvalid}, false},
		{"no code, a plain error", errors.New("x"), nil, false},
		{"a code, a plain error", errors.New("x"), []data.ChordErrorCode{data.CodeGone}, false},
		{"no code, nil", nil, nil, false},
		{"a code, a typed nil", (*data.ChordError)(nil), []data.ChordErrorCode{data.CodeGone}, false},
		{"a wrapper is not instanceof", fmt.Errorf("wrap: %w", gone), []data.ChordErrorCode{data.CodeGone}, false},
	}
	for _, c := range cases {
		got, ok := data.IsChordError(c.err, c.code...)
		if ok != c.want {
			t.Errorf("%s: IsChordError = %v, want %v", c.name, ok, c.want)
		}
		if ok && got != gone {
			t.Errorf("%s: returned %p, want the ChordError itself", c.name, got)
		}
		if !ok && got != nil {
			t.Errorf("%s: returned %p with false", c.name, got)
		}
	}
}
