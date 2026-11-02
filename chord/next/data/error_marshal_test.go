package data

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// directEncoder is ChordError's previous MarshalJSON: the wire object's own encoder.
type directEncoder struct{ err *ChordError }

func (d directEncoder) MarshalJSON() ([]byte, error) { return d.err.ToJSON().MarshalJSON() }

// TestChordErrorMarshalMatchesWireObject pins that a ChordError encoded by encoding/json or by JSON.stringify's encoder is byte-identical to its wire object encoded through the old MarshalJSON.
func TestChordErrorMarshalMatchesWireObject(t *testing.T) {
	for _, message := range []string{"a<b>&\u2028\u2029é", "bad\xffutf", "\xed\xa0\x80lone", "plain"} {
		err := &ChordError{Code: "x", Message: message, Reason: "r"}
		want, wantErr := json.Marshal(struct{ E any }{directEncoder{err}})
		got, gotErr := json.Marshal(struct{ E any }{err})
		if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(want, got) {
			t.Errorf("json.Marshal(%q): got %s (%v), want %s (%v)", message, got, gotErr, want, wantErr)
		}
		wantJS, wantJSErr := jsstring.MarshalJSON(struct{ E any }{directEncoder{err}})
		gotJS, gotJSErr := jsstring.MarshalJSON(struct{ E any }{err})
		if (wantJSErr == nil) != (gotJSErr == nil) || !bytes.Equal(wantJS, gotJS) {
			t.Errorf("jsstring.MarshalJSON(%q): got %s (%v), want %s (%v)", message, gotJS, gotJSErr, wantJS, wantJSErr)
		}
	}
}
