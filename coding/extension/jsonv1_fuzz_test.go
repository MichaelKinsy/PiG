package extension

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzDecodeProviderHeaderEntriesMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			t.Skip("the reader returns once it has the headers, or the end of the keys, and never checked the closing token; the caller has parsed the JSON")
		}
		var out any
		var rangeErr *json.UnmarshalTypeError
		if errors.As(json.Unmarshal(data, &out), &rangeErr) {
			t.Skip("encoding/json v1 Token rejected a top-level number outside float64 range, where the reader now returns no entries for a non-object, as for any other")
		}
		got, gotErr := decodeProviderHeaderEntries(data)
		want, wantErr := decodeProviderHeaderEntriesV1(data)
		jsonseed.Same(t, data, "decodeProviderHeaderEntries", got, want, gotErr, wantErr)
	})
}

// FuzzRejectDuplicateLoginJSONFieldsMatchesV1 compares only whether the document is rejected: the messages carry encoding/json v1 and
// jsontext error text.
func FuzzRejectDuplicateLoginJSONFieldsMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Add([]byte(`{"palette":{"a":1,"a":2}}`))
	f.Add([]byte(`{"brand":["x"],"brand":["y"]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var out any
		var rangeErr *json.UnmarshalTypeError
		if errors.As(json.Unmarshal(data, &out), &rangeErr) {
			t.Skip("encoding/json v1 Token rejected a number outside float64 range here; the typed decode that follows rejects it still")
		}
		gotErr := rejectDuplicateLoginJSONFields(data)
		wantErr := rejectDuplicateLoginJSONFieldsV1(data)
		jsonseed.Same(t, data, "rejectDuplicateLoginJSONFields", nil, nil, gotErr, wantErr)
	})
}

func BenchmarkProviderHeaders(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { decodeProviderHeaderEntriesV1(d) }, func(d []byte) { decodeProviderHeaderEntries(d) })
}

func BenchmarkRejectDuplicateLoginJSONFields(b *testing.B) {
	jsonseed.Bench(b, []byte(`{"brand":["ab","cd"],"hero":["ef"],"mascot":["gh"],"palette":{"a":"#111111","b":"#222222"},"tagline":"x","description":"y","name":"n"}`),
		func(d []byte) { rejectDuplicateLoginJSONFieldsV1(d) }, func(d []byte) { rejectDuplicateLoginJSONFields(d) })
}
