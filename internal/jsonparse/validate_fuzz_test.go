package jsonparse

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// FuzzValidateAgreesWithEncodingJSON: for text that is valid UTF-8 and short enough to stay clear of encoding/json's nesting limit, Validate reports an error exactly when encoding/json rejects the text, and never panics or reports an empty message.
func FuzzValidateAgreesWithEncodingJSON(f *testing.F) {
	for _, seed := range []string{`{}`, `[1,2,3]`, `{"a":[true,false,null]}`, `"\u00e9"`, `{"a":`, `[1,]`, `tru`, `-`, `01`, `1e`, `"\ud800"`, "\"a\nb\"", ` {"a" : 1 } `, `[`, `{"a":1,}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if !utf8.ValidString(text) || len(text) > 2000 {
			return
		}
		err := Validate([]byte(text))
		if valid := json.Valid([]byte(text)); valid != (err == nil) {
			t.Fatalf("Validate(%q) = %v but json.Valid = %v", text, err, valid)
		}
		if err != nil && err.Error() == "" {
			t.Fatalf("Validate(%q) gave an empty message", text)
		}
	})
}
