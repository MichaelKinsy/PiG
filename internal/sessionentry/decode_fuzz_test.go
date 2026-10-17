package sessionentry

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// FuzzDecodeFileEntryWritesBackWhatItRead: any JSON record decodes without a panic and, for a record the decoder keeps raw (a RawEntry or a header), writes back byte for byte; a decoded member writes a valid JSON record.
func FuzzDecodeFileEntryWritesBackWhatItRead(f *testing.F) {
	for _, seed := range []string{
		`{"type":"session","version":3,"id":"a","timestamp":"t","cwd":"/x"}`,
		`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"user","content":"hi","timestamp":1}}`,
		`{"type":"thinking_level_change","id":"a","parentId":"b","timestamp":"t","thinkingLevel":"high"}`,
		`{"type":"model_change","id":"a","parentId":null,"timestamp":"t","provider":"p","modelId":"m"}`,
		`{"type":"compaction","id":"a","parentId":null,"timestamp":"t","summary":"s","firstKeptEntryId":"b","tokensBefore":1}`,
		`{"type":"label","id":"a","parentId":null,"timestamp":"t","targetId":"b"}`,
		`{"type":"branch_summary","id":"a","parentId":null,"timestamp":"t","fromId":"b","summary":"s"}`,
		`{"type":"custom_message","id":"a","parentId":null,"timestamp":"t","customType":"c","content":"x","display":true}`,
		`{"type":"custom","id":"a","parentId":null,"timestamp":"t","customType":"c","data":{"k":1}}`,
		`{"type":"session_info","id":"a","parentId":null,"timestamp":"t","name":"n"}`,
		`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"toolResult","toolCallId":"c","toolName":"n","content":[{"type":"text","text":"x"}],"isError":false,"timestamp":1}}`,
		`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"bashExecution","command":"ls","output":"","exitCode":0,"cancelled":false,"truncated":false,"timestamp":1}}`,
		// Members the entry types do not name, and members out of declaration order: Pi keeps the parsed object, so a rewrite keeps both.
		`{"cwd":"/x","type":"session","extra":{"k":[1,2]},"version":3,"timestamp":"t","id":"a"}`,
		`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"user","content":"hi","timestamp":1,"extra":true},"extra":1}`,
		`{"thinkingLevel":"high","type":"thinking_level_change","id":"a","parentId":"b","timestamp":"t","note":"n"}`,
		`{"type":"model_change","id":"a","parentId":null,"timestamp":"t","provider":"p","modelId":"m","extra":null}`,
		`{"type":"compaction","id":"a","parentId":null,"timestamp":"t","summary":"s","firstKeptEntryId":"b","tokensBefore":1,"details":{"x":1}}`,
		`{"type":"x"}`, `[]`, `null`, `"s"`, `{"type":"session","id":1}`, `{"type":"message","id":"a","parentId":null,"timestamp":"t","message":7}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		raw := json.RawMessage(text)
		if !json.Valid(raw) {
			return
		}
		entry := DecodeFileEntry(raw)
		if entry == nil {
			t.Fatalf("DecodeFileEntry(%q) = nil", text)
		}
		written, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal of the decoded %q: %v", text, err)
		}
		if !json.Valid(written) {
			t.Fatalf("decoded %q writes invalid JSON %q", text, written)
		}
		var before, after any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		_ = dec.Decode(&before)
		dec = json.NewDecoder(bytes.NewReader(written))
		dec.UseNumber()
		_ = dec.Decode(&after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("decoded %q writes %q", text, written)
		}
		switch entry.(type) {
		case RawEntry, SessionHeader, *SessionHeader:
			compact, _ := json.Marshal(json.RawMessage(text))
			if !bytes.Equal(written, compact) {
				t.Fatalf("raw entry %q writes %q", text, written)
			}
		}
	})
}
