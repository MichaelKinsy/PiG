package codingagent

import "testing"

func TestQuotedLabel(t *testing.T) {
	if got := quotedLabel("openai"); got != `"openai"` {
		t.Fatal(got)
	}
	if got := quotedLabel(`a"b`); got != `"a\"b"` {
		t.Fatal(got)
	}
}
