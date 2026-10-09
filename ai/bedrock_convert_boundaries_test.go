//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"testing"

	bdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
)

func TestProviderSurrogateSanitizationPreservesValidScalars(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"empty", "", ""}, {"ordinary", "hello", "hello"}, {"valid emoji", "Hello 🙈 World", "Hello 🙈 World"},
		{"lone high", "Text \xed\xa0\xbd here", "Text  here"}, {"lone low", "Text \xed\xb8\x80 here", "Text  here"},
		{"paired UTF16 units", "\xed\xa0\xbd\xed\xb8\x80", "😀"}, {"high then high-low pair", "\xed\xa0\xbd\xed\xa0\xbd\xed\xb8\x80", "😀"},
		{"replacement is a real scalar", "�", "�"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeSurrogates(tc.input); got != tc.want {
				t.Fatalf("sanitized=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestBedrockDocumentSanitizationRetainsJSONTypes(t *testing.T) {
	input := map[string]any{"": "drop", "items": []map[string]any{{"": "drop", "integer": 7, "fraction": 3.25, "boolean": false, "null": nil, "empty": ""}}}
	clean, err := sanitizeBedrockDocument(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bdoc.NewLazyDocument(clean).MarshalSmithyDocument()
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, encoded, `{"items":[{"integer":7,"fraction":3.25,"boolean":false,"null":null,"empty":""}]}`)
	if input[""] != "drop" || input["items"].([]map[string]any)[0][""] != "drop" {
		t.Fatal("mutated input")
	}
}

func BenchmarkBedrockReplayDocument(b *testing.B) {
	edits := make([]any, 128)
	for index := range edits {
		edits[index] = map[string]any{"oldText": "before", "newText": "after", "": ""}
	}
	input := JsonObject{"path": "/workspace/file.go", "edits": edits}
	raw, err := json.Marshal(input)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for b.Loop() {
		clean, err := sanitizeBedrockDocument(input)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := bdoc.NewLazyDocument(clean).MarshalSmithyDocument(); err != nil {
			b.Fatal(err)
		}
	}
}
