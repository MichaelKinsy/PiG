// Ports packages/durable/src/codemode/output.ts's behavior; Pi tests it through the code mode tool, so the expectations are
// what Pi's formatResult returns for the corpus of testdata/output-oracle.mjs.

// pi: packages/durable/src/codemode/output.ts

package codemode

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type outputCase struct {
	Name  string `json:"name"`
	Input struct {
		Result struct {
			OK     bool             `json:"ok"`
			Output []cm.OutputItem  `json:"output"`
			Value  *json.RawMessage `json:"value"`
			Error  *cm.Error        `json:"error"`
		} `json:"result"`
		WallMs          float64      `json:"wallMs"`
		GeneratedImages int          `json:"generatedImages"`
		MaxOutputTokens int          `json:"maxOutputTokens"`
		Calls           []NestedCall `json:"calls"`
		Limits          struct {
			MaxBytes int `json:"maxBytes"`
			MaxLines int `json:"maxLines"`
		} `json:"limits"`
		WriteFails bool `json:"writeFails"`
	} `json:"input"`
	Formatted struct {
		Content        []json.RawMessage `json:"content"`
		FullOutputPath *string           `json:"fullOutputPath"`
		Error          *string           `json:"error"`
	} `json:"formatted"`
	Written []struct {
		Name string  `json:"name"`
		Size int     `json:"size"`
		Text *string `json:"text"`
	} `json:"written"`
}

func TestFormatResultMatchesPi(t *testing.T) {
	raw, err := os.ReadFile("testdata/output-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []outputCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			result := cm.Result{OK: c.Input.Result.OK, Output: c.Input.Result.Output, Error: c.Input.Result.Error}
			if c.Input.Result.Value != nil {
				result.Value = *c.Input.Result.Value
			}
			type written struct {
				name string
				size int
				text *string
			}
			var writes []written
			write := func(name string, data any) (string, string) {
				entry := written{name: name}
				switch data := data.(type) {
				case string:
					entry.size, entry.text = jsstring.Length(data), &data
				case []byte:
					entry.size = len(data)
				}
				writes = append(writes, entry)
				if c.Input.WriteFails {
					return "", "disk full"
				}
				return "/out/" + name, ""
			}
			content, fullOutputPath, err := formatResult(result, formatOptions{
				generatedImages: c.Input.GeneratedImages, wallMs: c.Input.WallMs, maxOutputTokens: c.Input.MaxOutputTokens,
				calls: c.Input.Calls, limits: outputLimits{c.Input.Limits.MaxBytes, c.Input.Limits.MaxLines}, write: write,
			})
			if c.Formatted.Error != nil {
				if err == nil {
					t.Fatalf("formatResult succeeded, Pi failed with %s", *c.Formatted.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := make([]any, len(content))
			for i, item := range content {
				switch item := item.(type) {
				case ai.TextContent:
					got[i] = map[string]any{"type": "text", "text": wellFormed(item.Text)}
				case ai.ImageContent:
					got[i] = map[string]any{"type": "image", "data": item.Data, "mimeType": item.MimeType}
				}
			}
			var want []any
			for _, item := range c.Formatted.Content {
				var decoded any
				if err := json.Unmarshal(item, &decoded); err != nil {
					t.Fatal(err)
				}
				want = append(want, decoded)
			}
			if !reflect.DeepEqual(normalize(got), normalize(want)) {
				t.Errorf("content = %s\nwant      %s", mustJSON(got), mustJSON(want))
			}
			wantPath := ""
			if c.Formatted.FullOutputPath != nil {
				wantPath = *c.Formatted.FullOutputPath
			}
			if fullOutputPath != wantPath {
				t.Errorf("fullOutputPath = %q, want %q", fullOutputPath, wantPath)
			}
			if len(writes) != len(c.Written) {
				t.Fatalf("%d files written, Pi wrote %d", len(writes), len(c.Written))
			}
			for i, w := range c.Written {
				if writes[i].name != w.Name || writes[i].size != w.Size || (w.Text != nil) != (writes[i].text != nil) || (w.Text != nil && *w.Text != *writes[i].text) {
					t.Errorf("file %d = %+v, want %+v", i, writes[i], w)
				}
			}
		})
	}
}

// wellFormed is what JSON decoding of Pi's lone surrogates gives: U+FFFD (the Go strings keep them as WTF-8).
func wellFormed(text string) string { return string(utf16.Decode(jsstring.ToUTF16(text))) }

func normalize(v any) any {
	encoded, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(encoded, &out)
	return out
}

func mustJSON(v any) string {
	encoded, _ := json.MarshalIndent(v, "", " ")
	return string(encoded)
}
