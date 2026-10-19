package sdk

import (
	"io"
	"net"
	"reflect"
	"runtime"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The memo's value is exactly what the old per-handler decode gave, for every payload shape, on the first and on a repeated call.
func TestEventArgsMemoGivesTheValueJSONUnmarshalGives(t *testing.T) {
	for _, args := range []string{
		`{"a":1,"b":[],"c":{},"d":null,"e":"x y","f":[1,"2",{"g":[[]]}],"h":true}`,
		`{"context":{"contextEntries":[{"sourceEntry":{"type":"message","content":[]}}]}}`,
		`{}`, `[]`, `[1,2]`, `"text"`, `12`, `null`, `true`, `{"a":`, `not json`,
		`{"emoji":"😀","lone":"\udc00","big":1e309}`,
	} {
		var want map[string]any
		_ = json.Unmarshal([]byte(args), &want)
		m := &eventArgsMemo{}
		for call := range 3 {
			if got := decodeReleased(m, []byte(args)); !reflect.DeepEqual(got, want) {
				t.Fatalf("args %s call %d: got %#v, want %#v", args, call, got, want)
			}
		}
	}
}

// Isolation: a handler changing (or keeping and later changing) its value is never seen by the next handler of the same payload.
func TestEventArgsMemoHandsEveryCallerItsOwnCopy(t *testing.T) {
	args := []byte(`{"context":{"contextEntries":[{"sourceEntry":{"id":"e1"}}]},"list":[1,2],"empty":[]}`)
	m := &eventArgsMemo{}
	first, release := m.decode(args) // pinned: the next decode is a hit, not a fresh decode after a collection
	defer release()
	first["context"].(map[string]any)["contextEntries"].([]any)[0].(map[string]any)["sourceEntry"].(map[string]any)["id"] = "sentinel"
	first["list"] = append(first["list"].([]any), 3)
	first["new"] = true
	second := decodeReleased(m, args)
	if id := second["context"].(map[string]any)["contextEntries"].([]any)[0].(map[string]any)["sourceEntry"].(map[string]any)["id"]; id != "e1" {
		t.Fatalf("second handler saw the first handler's change: id = %v", id)
	}
	if len(second["list"].([]any)) != 2 || second["new"] != nil {
		t.Fatalf("second handler saw a top-level change: %#v", second)
	}
	if empty := second["empty"].([]any); empty == nil {
		t.Fatal("an empty array must stay a non-nil []any, as json.Unmarshal gives")
	}
}

// FD-5: the caller reusing its buffer after the call must not change a cached entry.
func TestEventArgsMemoKeepsItsOwnCopyOfTheBytes(t *testing.T) {
	buf := []byte(`{"k":"one"}`)
	m := &eventArgsMemo{}
	_, release := m.decode(buf)
	defer release()
	copy(buf, `{"k":"two"}`)
	if got := decodeReleased(m, []byte(`{"k":"one"}`)); got["k"] != "one" {
		t.Fatalf("cached entry followed the caller's buffer: %v", got)
	}
	if got := decodeReleased(m, buf); got["k"] != "two" {
		t.Fatalf("changed bytes must decode afresh: %v", got)
	}
}

// The repeat costs a clone, not a decode: the gain this lever exists for (fused ext10: K handlers of one turn_end).
func TestEventArgsMemoRepeatIsCheaperThanADecode(t *testing.T) {
	args := []byte(`{"context":{"contextEntries":[` + repeatEntry(200) + `]},"message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`)
	decode := testing.AllocsPerRun(20, func() { var d map[string]any; _ = json.Unmarshal(args, &d) })
	m := &eventArgsMemo{}
	_, release := m.decode(args)
	defer release()
	repeat := testing.AllocsPerRun(20, func() { _ = decodeReleased(m, args) })
	if repeat >= decode/2 {
		t.Fatalf("a repeated payload allocates %.0f, a decode %.0f: the memo must cost well under half a decode", repeat, decode)
	}
}

func repeatEntry(n int) string {
	out := make([]byte, 0, n*96)
	for i := range n {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"sourceEntry":{"type":"message","id":"abcdefgh","message":{"role":"user","content":[{"type":"text","text":"hello world"}]}}}`...)
	}
	return string(out)
}

// A cached payload is cloned on every hit too: two later handlers never share one value.
func TestEventArgsMemoCachedHitsAreIndependent(t *testing.T) {
	args := []byte(`{"context":{"messages":[{"role":"user"}]}}`)
	m := &eventArgsMemo{}
	_, release := m.decode(args)
	defer release()
	second := decodeReleased(m, args)
	second["context"].(map[string]any)["messages"].([]any)[0].(map[string]any)["role"] = "sentinel"
	third := decodeReleased(m, args)
	if role := third["context"].(map[string]any)["messages"].([]any)[0].(map[string]any)["role"]; role != "user" {
		t.Fatalf("a cached hit handed out a shared value: role = %v", role)
	}
}

// decodeReleased decodes as a handler that has returned: its pin is released.
func decodeReleased(m *eventArgsMemo, args []byte) map[string]any {
	data, release := m.decode(args)
	release()
	return data
}

// The memo keeps an entry only while a handler that decoded it runs: after the handlers return and a collection runs it holds nothing, so an idle process retains no payload (bounded lifetime). A pinned entry survives a collection and serves the next handler.
func TestEventArgsMemoHoldsNothingAfterItsHandlersAndAGC(t *testing.T) {
	m := &eventArgsMemo{}
	args := []byte(`{"context":{"contextEntries":[` + repeatEntry(50) + `]}}`)
	_, release := m.decode(args)
	runtime.GC()
	_ = decodeReleased(m, args)
	if m.hits != 1 || m.held() != 1 {
		t.Fatalf("pinned entry: hits %d, held %d; want a hit and one entry", m.hits, m.held())
	}
	release()
	_ = decodeReleased(m, []byte(`{"other":true}`))
	runtime.GC()
	if held := m.held(); held != 0 || len(m.pinned) != 0 {
		t.Fatalf("after the handlers returned and a collection: %d entries held, %d pinned", held, len(m.pinned))
	}
}

// With the collector running, the handlers of one dispatch still share the decode: K handlers in turn, each allocating as a handler does, decode a fresh payload per dispatch. The hit rate is logged; a rate below 80% would mean the weak entries rarely survive between handlers.
func TestEventArgsMemoHitRateWithTheCollectorOn(t *testing.T) {
	const dispatches, k = 40, 10
	m := &eventArgsMemo{}
	var sink []map[string]any
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for d := range dispatches {
		args := []byte(`{"dispatch":` + strconv.Itoa(d) + `,"context":{"contextEntries":[` + repeatEntry(200) + `]}}`)
		for range k {
			data, release := m.decode(args)
			sink = append(sink[:0], data)
			release()
		}
	}
	runtime.ReadMemStats(&after)
	if after.NumGC == before.NumGC {
		t.Fatal("no collection ran: the rate would not measure weak entries")
	}
	rate := float64(m.hits) / float64(m.hits+m.misses)
	t.Logf("hits %d, misses %d over %d dispatches of %d handlers, %d collections: hit rate %.2f (ideal %.2f)", m.hits, m.misses, dispatches, k, after.NumGC-before.NumGC, rate, float64(k-1)/k)
	if rate < 0.8 {
		t.Fatalf("hit rate %.2f with the collector on", rate)
	}
	_ = sink
}

// Only a fused extension's connection uses the memo: an extension in its own process decodes its args itself and keeps nothing.
func TestOnlyFusedConnectionsShareDecodes(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	if newConn(a).fused {
		t.Fatal("a byte connection is treated as fused")
	}
	if !newConn(fakePartsConn{a}).fused {
		t.Fatal("a framePartsReader connection is not treated as fused")
	}
}

type fakePartsConn struct{ net.Conn }

func (fakePartsConn) ReadFrameParts() ([]byte, []byte, error) { return nil, nil, io.EOF }
