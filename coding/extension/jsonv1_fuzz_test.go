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
		if _, ok := errors.AsType[*json.UnmarshalTypeError](json.Unmarshal(data, &out)); ok {
			t.Skip("JSON.parse gives Infinity for a top-level number outside float64 range, a non-object, so the reader returns no entries as Pi does; encoding/json v1 Token rejected it, which is not the behaviour to match")
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
		jsonseed.SkipJSKeys(t, data)
		gotErr := rejectDuplicateLoginJSONFields(data)
		wantErr := rejectDuplicateLoginJSONFieldsV1(data)
		jsonseed.Same(t, data, "rejectDuplicateLoginJSONFields", nil, nil, gotErr, wantErr)
	})
}

func BenchmarkProviderHeaders(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { _, _ = decodeProviderHeaderEntriesV1(d) }, func(d []byte) { _, _ = decodeProviderHeaderEntries(d) })
}

func BenchmarkRejectDuplicateLoginJSONFields(b *testing.B) {
	jsonseed.Bench(b, []byte(`{"brand":["ab","cd"],"hero":["ef"],"mascot":["gh"],"palette":{"a":"#111111","b":"#222222"},"tagline":"x","description":"y","name":"n"}`),
		func(d []byte) { _ = rejectDuplicateLoginJSONFieldsV1(d) }, func(d []byte) { _ = rejectDuplicateLoginJSONFields(d) })
}
