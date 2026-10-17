package export

import (
	"strings"
	"testing"
)

// Pi exports sm.getHeader() and sm.getEntries(), the objects JSON.parse built from the file, so the payload holds
// JSON.stringify's text (export-html/index.ts:296-301). Expectations from Node 24: JSON.stringify(JSON.parse(line)).
func TestFromJSONLHoldsJSONStringifyTextLikePi(t *testing.T) {
	header := `{"type":"session","version":3,"id":"s" , "cwd":"\/tmp"}`
	entry := `{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"a <b>&amp; \u0041 \/ \u2028 \ud800 1.0e2"}],"timestamp":1735689601000.0,"x":1E2,"y":-0,"z":0.10}}`
	data, err := FromJSONL([]byte(strings.Join([]string{header, entry}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data.Header), `{"type":"session","version":3,"id":"s","cwd":"/tmp"}`; got != want {
		t.Errorf("header = %s\nwant     %s", got, want)
	}
	want := "{\"type\":\"message\",\"id\":\"u1\",\"parentId\":null,\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"a <b>&amp; A / \u2028 \\ud800 1.0e2\"}],\"timestamp\":1735689601000,\"x\":100,\"y\":0,\"z\":0.1}}"
	if len(data.Entries) != 1 || string(data.Entries[0]) != want {
		t.Errorf("entries = %s\nwant      %s", data.Entries, want)
	}
	if data.LeafID == nil || *data.LeafID != "u1" {
		t.Errorf("leafId = %v, want u1", data.LeafID)
	}
}
