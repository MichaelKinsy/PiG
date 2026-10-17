//go:build !race

package jsstring

import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsparity"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// budgetPayload mirrors Pi-Bolt bench/fake_model_stress.py write:N: TypeScript lines with one `=>` and one newline each, the text a large
// write tool call carries.
func budgetPayload(size int) string {
	var b strings.Builder
	for n := 0; b.Len() < size; n++ {
		if n > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }", n, n%97, n)
	}
	return b.String()[:size]
}

// The chain models the session line of a message: each level is a PiG type whose MarshalJSON is jsstring.MarshalJSON of a struct holding the
// next level (MessageEntry -> AgentMessage -> AssistantMessage -> content block), and which also appends its own JSON.stringify text.
type chainLeaf struct {
	Content string `json:"content"`
}

func (v chainLeaf) AppendStringify(dst []byte) ([]byte, error) {
	dst = append(dst, `{"content":`...)
	return append(jsparity.AppendString(dst, v.Content), '}'), nil
}

type chain1 struct{ In chainLeaf }
type chain2 struct{ In chain1 }
type chain3 struct{ In chain2 }
type chain4 struct{ In chain3 }

func (v chain1) MarshalJSON() ([]byte, error) {
	return MarshalJSON(struct {
		A chainLeaf `json:"a"`
	}{v.In})
}
func (v chain2) MarshalJSON() ([]byte, error) {
	return MarshalJSON(struct {
		A chain1 `json:"a"`
	}{v.In})
}
func (v chain3) MarshalJSON() ([]byte, error) {
	return MarshalJSON(struct {
		A chain2 `json:"a"`
	}{v.In})
}
func (v chain4) MarshalJSON() ([]byte, error) {
	return MarshalJSON(struct {
		A chain3 `json:"a"`
	}{v.In})
}

func (v chain1) AppendStringify(dst []byte) ([]byte, error) { return appendChainLevel(dst, v.In) }
func (v chain2) AppendStringify(dst []byte) ([]byte, error) { return appendChainLevel(dst, v.In) }
func (v chain3) AppendStringify(dst []byte) ([]byte, error) { return appendChainLevel(dst, v.In) }
func (v chain4) AppendStringify(dst []byte) ([]byte, error) { return appendChainLevel(dst, v.In) }

func appendChainLevel(dst []byte, in interface {
	AppendStringify([]byte) ([]byte, error)
}) ([]byte, error) {
	dst, err := in.AppendStringify(append(dst, `{"a":`...))
	return append(dst, '}'), err
}

// fastestRun is the lowest ns/op and its bytes/op over a few benchmark runs, so one descheduled run on a loaded machine does not decide.
func fastestRun(run func()) (nsPerOp, bytesPerOp int64) {
	nsPerOp = 1 << 62
	for range 5 {
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				run()
			}
		})
		if ns := result.NsPerOp(); ns < nsPerOp {
			nsPerOp, bytesPerOp = ns, result.AllocedBytesPerOp()
		}
	}
	return nsPerOp, bytesPerOp
}

// A 200 KB string under four MarshalJSON levels encodes in one pass: the nested levels splice their text instead of being re-read, compacted
// and rewritten at every level (3.7 ms and 7x the payload in garbage before), so the line costs at most 0.5 ms and three times its size.
func TestStringifyChain200KBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	// A wall-clock budget: it runs in the serial startup-proxies job (automation/perf/startup-proxies.json requires it there), not beside
	// the parallel test packages, where memory-bandwidth contention on a 4-CPU runner took it from 0.2 ms to 0.6-0.7 ms.
	if os.Getenv("PIG_SERIAL_TIMING") == "" {
		t.Skip("wall-clock budget: run by make startup-proxies (PIG_SERIAL_TIMING=1)")
	}
	payload := budgetPayload(200 << 10)
	value := chain4{chain3{chain2{chain1{chainLeaf{payload}}}}}
	want := `{"a":{"a":{"a":{"a":{"content":` + string(jsparity.AppendString(nil, payload)) + `}}}}}`
	got, err := MarshalJSON(value)
	if err != nil || string(got) != want {
		t.Fatalf("chain differs from the direct text (err %v)", err)
	}
	ns, allocated := fastestRun(func() { _, _ = MarshalJSON(value) })
	t.Logf("chain: %d ns/op, %d B/op for %d bytes", ns, allocated, len(want))
	testenv.TimingGate(t, ns > 500_000, "MarshalJSON of the 4-level 200 KB chain takes %d ns, budget 500000", ns)
	if allocated > 3*int64(len(want)) {
		t.Errorf("MarshalJSON of the 4-level 200 KB chain allocates %d bytes, budget %d (3x the text)", allocated, 3*len(want))
	}
}

// A 200 KB string encodes in about the time of one escape pass, with no rewrite pass over the encoder's output (jsText) and no second copy:
// at least three times as fast as that encoding (referenceMarshalJSON, the encoder plus jsText), and with one allocation's worth of bytes.
func TestMarshalJSONString200KBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	// A wall-clock budget: it runs in the serial startup-proxies job (automation/perf/startup-proxies.json requires it there), not beside
	// the parallel test packages, where memory-bandwidth contention on a 4-CPU runner took it from 0.2 ms to 0.6-0.7 ms.
	if os.Getenv("PIG_SERIAL_TIMING") == "" {
		t.Skip("wall-clock budget: run by make startup-proxies (PIG_SERIAL_TIMING=1)")
	}
	payload := budgetPayload(200 << 10)
	ns, allocated := fastestRun(func() { _, _ = MarshalJSON(payload) })
	rewrite, _ := fastestRun(func() { _, _ = referenceMarshalJSON(payload) })
	t.Logf("string: %d ns/op, %d B/op; encoder plus rewrite: %d ns/op", ns, allocated, rewrite)
	testenv.TimingGate(t, 3*ns > rewrite, "MarshalJSON of a 200 KB string takes %d ns, the encoder plus rewrite %d ns: want at least 3x faster", ns, rewrite)
	if allocated > 2*int64(len(payload)) {
		t.Errorf("MarshalJSON of a 200 KB string allocates %d bytes, budget %d", allocated, 2*len(payload))
	}
}

// A large value does not stay pinned in a pool after the call: pooled buffers above 64 KiB are dropped (golang/go#23199), so one 1 MiB
// line does not hold megabytes until the next two collections.
func TestMarshalJSONPoolsDropLargeBuffers(t *testing.T) {
	payload := strings.Repeat("x", 1<<20)
	retained := func() int64 {
		runtime.GC()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 4 {
			if _, err := MarshalJSON(payload); err != nil {
				t.Fatal(err)
			}
			if _, err := StringifyLength(payload); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		return int64(after.HeapAlloc) - int64(before.HeapAlloc)
	}
	if got := retained(); got > 512<<10 {
		t.Fatalf("encoding a 1 MiB string leaves %d bytes live after a collection, want at most 512 KiB", got)
	}
	if !bytes.Equal(must(MarshalJSON("a\u2028b")), []byte("\"a\u2028b\"")) {
		t.Fatal("U+2028 escaped")
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// A body with escapes that JSON.stringify does not write (the "=>" of each line and an ESC from tool output) is copied in spans between
// backslashes, not a byte at a time: at least twice as fast as the byte loop on a 200 KB write body (about 3.8x measured; 1.0x before).
func TestUnescapeForStringifyCopiesSpans(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	encoded, err := stdjson.Marshal("\x1b[0m" + budgetPayload(200<<10))
	if err != nil {
		t.Fatal(err)
	}
	want := referenceUnescapeForStringify(encoded)
	if got := UnescapeForStringify(encoded); !bytes.Equal(got, want) {
		t.Fatal("UnescapeForStringify differs from the byte loop")
	}
	ns, _ := fastestRun(func() { _ = UnescapeForStringify(encoded) })
	loop, _ := fastestRun(func() { _ = referenceUnescapeForStringify(encoded) })
	t.Logf("%d bytes: %d ns/op; byte loop %d ns/op", len(encoded), ns, loop)
	testenv.TimingGate(t, 2*ns > loop, "UnescapeForStringify of a 200 KB body takes %d ns, the byte loop %d ns: want at least 2x faster", ns, loop)
}
