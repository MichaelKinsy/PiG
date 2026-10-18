// pi: packages/codemode/src/runtime/vm.ts

package codemode

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The pieces of deterministic mode that Pi's deterministic.test.ts reaches only through the engine, pinned to what Pi's
// vm.ts computes (values taken by running its prng, fnv1a and deterministicWasi random_get in Node).

func TestPrngIsMulberry32LikePis(t *testing.T) {
	for _, c := range []struct {
		seed uint32
		want [3]uint32
	}{
		{0, [3]uint32{1144304738, 1416247, 958946056}},
		{1, [3]uint32{2693262067, 11749833, 2265367787}},
		{0x9e3779b9, [3]uint32{1541420728, 454851044, 2900350524}},
		{123456789, [3]uint32{1107202814, 4169434471, 3372958138}},
	} {
		next := prng(c.seed)
		if got := [3]uint32{next(), next(), next()}; got != c.want {
			t.Errorf("prng(%d) = %v, want %v", c.seed, got, c.want)
		}
	}
}

// random_get draws one PRNG value per four bytes, least significant byte first, and a partial last word keeps its low bytes.
func TestRandomGetFillsFourBytesPerDrawLikePis(t *testing.T) {
	p := make([]byte, 7)
	if n, err := newRandomSource(1).Read(p); n != 7 || err != nil {
		t.Fatalf("Read = %d, %v", n, err)
	}
	if want := []byte{215, 176, 142, 48, 206, 135, 90}; !bytes.Equal(p, want) {
		t.Fatalf("bytes = %v, want %v", p, want)
	}
}

// The prelude is identified by FNV-1a over its UTF-16 code units, so a character outside the BMP counts as two.
func TestPreludeHashIsFNV1aOverUTF16UnitsLikePis(t *testing.T) {
	for text, want := range map[string]string{"": "811c9dc5", "a": "e40c292c", "héllo 😀": "4dfa2d50"} {
		if got := preludeHash(text); got != want {
			t.Errorf("preludeHash(%q) = %s, want %s", text, got, want)
		}
	}
}

// vm.ts deterministicWasi: while the context is created the realtime clock reads (seed + 1) microseconds and the monotonic
// clock 0; afterwards realtime reads the result's time and the monotonic clock the time since the start, in whole ms.
func TestTheVirtualClockReadsLikePis(t *testing.T) {
	clock := newVirtualClock(&VmDeterministic{StartedAt: 1000.4, Seed: 7})
	if clock.realtimeNanos() != 8000 || clock.monotonicNanos() != 0 {
		t.Fatalf("creating: %d, %d", clock.realtimeNanos(), clock.monotonicNanos())
	}
	clock.creating = false
	if clock.realtimeNanos() != 1000_000_000 || clock.monotonicNanos() != 0 {
		t.Fatalf("started: %d, %d", clock.realtimeNanos(), clock.monotonicNanos())
	}
	clock.advance(2500.5)
	if clock.realtimeNanos() != 2501_000_000 || clock.monotonicNanos() != 1500_000_000 {
		t.Fatalf("advanced: %d, %d", clock.realtimeNanos(), clock.monotonicNanos())
	}
	clock.advance(10)
	if clock.now != 2500.5 {
		t.Fatalf("the clock went backwards to %v", clock.now)
	}
	// vm.ts rounds both readings with Math.round: 3000.7 - 1000.4 = 2000.3 reads 2000 ms, and 3000.7 reads 3001 ms; 3200 - 1000.4 =
	// 2199.6 reads 2200 ms, where truncation would read 2199.
	clock.advance(3000.7)
	if clock.realtimeNanos() != 3001_000_000 || clock.monotonicNanos() != 2000_000_000 {
		t.Fatalf("advanced to 3000.7: %d, %d", clock.realtimeNanos(), clock.monotonicNanos())
	}
	clock.advance(3200)
	if clock.realtimeNanos() != 3200_000_000 || clock.monotonicNanos() != 2200_000_000 {
		t.Fatalf("advanced to 3200: %d, %d", clock.realtimeNanos(), clock.monotonicNanos())
	}
}

// vm.ts reads both clocks through Math.round, which floor(x + 0.5) misses where x + 0.5 rounds up in binary: Node reads
// BigInt(Math.round(0.49999999999999994)) * 1_000_000n as 0n, and setBigUint64 stores BigInt(Math.round(4503599627370497)) *
// 1_000_000n as 2594073385366405696 (mod 2^64), where floor(x + 0.5) reads 1 ms and 4503599627370498 ms (2594073385367405696).
func TestTheVirtualClockRoundsLikeMathRound(t *testing.T) {
	clock := newVirtualClock(&VmDeterministic{StartedAt: 0.49999999999999994})
	clock.creating = false
	if got := clock.realtimeNanos(); got != 0 {
		t.Errorf("realtime at 0.49999999999999994 ms = %d, want 0", got)
	}
	clock = newVirtualClock(&VmDeterministic{StartedAt: 0})
	clock.creating = false
	clock.advance(0.49999999999999994)
	if got := clock.monotonicNanos(); got != 0 {
		t.Errorf("monotonic at 0.49999999999999994 ms = %d, want 0", got)
	}
	clock = newVirtualClock(&VmDeterministic{StartedAt: 4503599627370497})
	clock.creating = false
	if got := uint64(clock.realtimeNanos()); got != 2594073385366405696 {
		t.Errorf("realtime at 4503599627370497 ms = %d, want 2594073385366405696", got)
	}
}

// protocol.ts: the messages of a deterministic run have Pi's JSON shapes, and decode back to what was sent.
func TestDeterministicMessagesHaveUpstreamsJSONShape(t *testing.T) {
	seq, now, properties, value := 3, 1768471201000.0, `{"code":7}`, "1"
	writes := "[]"
	for _, c := range []struct {
		message any
		want    string
	}{
		{WorkerToHostMessage{Type: MessageStarted, Fingerprint: "fp"}, `{"type":"started","fingerprint":"fp"}`},
		{WorkerToHostMessage{Type: MessageIdle, Seq: 2, Issued: 5}, `{"type":"idle","seq":2,"issued":5}`},
		{WorkerToHostMessage{Type: MessageDone, Ok: true, Value: &value, Writes: &writes, During: &During{Seq: 2, Issued: 5}}, `{"type":"done","ok":true,"value":"1","writes":"[]","during":{"seq":2,"issued":5}}`},
		{HostToWorkerMessage{Type: MessageResult, ID: 1, Ok: false, Seq: &seq, Now: &now, Properties: &properties}, `{"type":"result","id":1,"ok":false,"seq":3,"now":1768471201000,"properties":"{\"code\":7}"}`},
		{VmStartData{Code: "c", Tools: []VmStartTool{}, Globals: []VmStartGlobal{}, Store: map[string]string{}, Deterministic: &VmDeterministic{StartedAt: 5, Seed: 4294967295}}, `{"code":"c","tools":[],"globals":[],"store":{},"deterministic":{"startedAt":5,"seed":4294967295}}`},
	} {
		got, err := json.Marshal(c.message)
		if err != nil || string(got) != c.want {
			t.Errorf("%T = %s (%v), want %s", c.message, got, err, c.want)
		}
	}
	var decoded WorkerToHostMessage
	if err := json.Unmarshal([]byte(`{"type":"done","ok":false,"error":"{}","during":{"seq":1,"issued":2}}`), &decoded); err != nil || decoded.During == nil || *decoded.During != (During{1, 2}) {
		t.Errorf("decoded done = %+v (%v)", decoded, err)
	}
	if err := json.Unmarshal([]byte(`{"type":"idle","seq":4,"issued":9}`), &decoded); err != nil || decoded.Seq != 4 || decoded.Issued != 9 {
		t.Errorf("decoded idle = %+v (%v)", decoded, err)
	}
	// The hand-written encoder of the Worker module says the same.
	if got := string(appendRemoteMessage(nil, RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageIdle, Seq: 2, Issued: 5}})); got != `{"type":"idle","seq":2,"issued":5}` {
		t.Errorf("appended idle = %s", got)
	}
}
