package ai

import (
	"maps"
	"net/http"
	"testing"
)

// upstream: packages/ai/src/utils/headers.ts headersToRecord copies Headers.entries into a record. Probed with Node 24: appending X-Foo "a" and x-foo "b", Set-Cookie "c1" and set-cookie "c2", Empty "" and Content-Type "text/plain" yields
// {"content-type":"text/plain","empty":"","set-cookie":"c2","x-foo":"a, b"}: names are lowercase, repeated values join with ", ", set-cookie entries stay separate so the record keeps the last, and an empty value is kept.
func TestHeadersToRecordMatchesHeadersEntries(t *testing.T) {
	headers := http.Header{}
	headers.Add("X-Foo", "a")
	headers.Add("x-foo", "b")
	headers.Add("Set-Cookie", "c1")
	headers.Add("set-cookie", "c2")
	headers.Add("Empty", "")
	headers.Set("Content-Type", "text/plain")
	want := map[string]string{"content-type": "text/plain", "empty": "", "set-cookie": "c2", "x-foo": "a, b"}
	if got := headersToRecord(headers); !maps.Equal(got, want) {
		t.Fatalf("headersToRecord = %v, want %v", got, want)
	}
	if got := headersToRecord(http.Header{}); got == nil || len(got) != 0 {
		t.Fatalf("an empty Headers yields an empty record, got %#v", got)
	}
}
