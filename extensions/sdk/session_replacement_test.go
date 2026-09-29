package sdk

import (
	"encoding/json"
	"testing"
)

func TestSessionIdentityClearsEmptyReplacementAndCaches(t *testing.T) {
	mirror := &sessionMirror{}
	mirror.subscribed.Store(true)
	mirror.applySessionUpdate(json.RawMessage(`{"sessionId":"old","entryCount":1,"leafId":"old-entry","entriesAppended":[{"type":"message","id":"old-entry","parentId":null,"message":{"role":"user","content":"old"}}]}`))
	if len(mirror.getBranchEntries()) != 1 {
		t.Fatal("initial branch missing")
	}
	mirror.applySessionUpdate(json.RawMessage(`{"sessionId":"new","entryCount":0,"leafId":""}`))
	if len(mirror.getEntries()) != 0 || len(mirror.getBranchEntries()) != 0 || len(mirror.branchCache) != 0 || len(mirror.branchDecoded) != 0 {
		t.Fatal("empty replacement retained outgoing history")
	}
}

func BenchmarkSessionMirrorReplacement(b *testing.B) {
	entries := realisticLog(1000)
	full, err := json.Marshal(map[string]any{"sessionId": "full", "entryCount": len(entries), "leafId": "e999", "entriesAppended": entries})
	if err != nil {
		b.Fatal(err)
	}
	empty := json.RawMessage(`{"sessionId":"empty","entryCount":0,"leafId":""}`)
	mirror := &sessionMirror{}
	mirror.subscribed.Store(true)
	b.ReportAllocs()
	b.SetBytes(int64(len(full)))
	for b.Loop() {
		mirror.applySessionUpdate(full)
		mirror.applySessionUpdate(empty)
	}
}
