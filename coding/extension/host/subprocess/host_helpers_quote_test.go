package subprocess

import (
	"encoding/json"
	"strings"
	"testing"
)

// JSON.stringify leaves < > & U+2028 and U+2029 as they are; encoding/json escapes them, so the host writes the frontmatter result itself.
func TestParseFrontmatterResultKeepsMarkupAndLineSeparatorsLiteral(t *testing.T) {
	h := &Host{}
	args, _ := json.Marshal(ParseFrontmatterArgs{Content: "---\n\"k<>&\": \"<b>&\u2028\u2029\\\"\\n\"\n---\nbody <x> &\u2028x"})
	result, err := h.handleParseFrontmatter(&CallPayload{Method: CallParseFrontmatter, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"frontmatter\":{\"k<>&\":\"<b>&\u2028\u2029\\\"\\n\"},\"body\":\"body <x> &\u2028x\"}"
	if string(result.Result) != want {
		t.Fatalf("result = %q, want %q", result.Result, want)
	}
	if strings.Contains(string(result.Result), `\u00`) {
		t.Fatalf("result %q has an escape JSON.stringify would not write", result.Result)
	}
	var decoded struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(result.Result, &decoded); err != nil || decoded.Body != "body <x> &\u2028x" {
		t.Fatalf("result is not the JSON it spells: %v %q", err, decoded.Body)
	}
}
