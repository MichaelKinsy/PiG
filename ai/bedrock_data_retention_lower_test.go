//go:build !pig_strip_bedrock_converse_stream

package ai

import "testing"

// Node 24: /data retention mode/i.test("data retent\u0130on mode") === false; Go's strings.ToLower maps U+0130 to "i".
// bedrock-converse-stream.ts:402 formatBedrockError dataRetentionHint.
func TestBedrockDataRetentionHintMatchesLikeJSRegex(t *testing.T) {
	if bedrockDataRetentionHint("Data Retention Mode 'default' is not available") == "" {
		t.Error("ASCII case variant must get the hint")
	}
	if hint := bedrockDataRetentionHint("data retent\u0130on mode 'default'"); hint != "" {
		t.Errorf("U+0130 spelling got the hint: %q", hint)
	}
}
