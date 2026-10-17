package jsparity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// nodeStringify runs one Node child for the test that writes JSON.stringify of each value it receives. A request line is {"units": [...]} for
// the string with those UTF-16 code units, {"bytes": [...]} for the string Node decodes from those UTF-8 bytes, {"bits": "<16 hex digits>"} for the double with that bit pattern, or {"bigint": "<decimal>"} for
// Number(BigInt(decimal)). Each answer is one line of JSON.stringify output, which never holds a raw line break.
type nodeStringify struct {
	in  io.WriteCloser
	out *bufio.Reader
}

func startNode(t *testing.T) *nodeStringify {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "node", "-e", `
const lines = require('node:readline').createInterface({ input: process.stdin });
const view = new DataView(new ArrayBuffer(8));
lines.on('line', (line) => {
  const request = JSON.parse(line);
  let value;
  if (request.units) { value = ''; for (let i = 0; i < request.units.length; i += 8192) value += String.fromCharCode(...request.units.slice(i, i + 8192)); }
  else if (request.bytes) value = Buffer.from(request.bytes).toString();
  else if (request.bits) { view.setBigUint64(0, BigInt('0x' + request.bits)); value = view.getFloat64(0); }
  else value = Number(BigInt(request.bigint));
  process.stdout.write(JSON.stringify(value) + '\n');
});`)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("node is required for the JSON.stringify differential: %v", err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Wait()
	})
	return &nodeStringify{in: in, out: bufio.NewReaderSize(out, 1<<16)}
}

func (n *nodeStringify) stringify(t *testing.T, request any) string {
	t.Helper()
	line, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.in.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	answer, err := n.out.ReadString('\n')
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	return answer[:len(answer)-1]
}

// NodeStringify starts the Node child of startNode for the external test package, whose encoders (jsstring) import this one.
func NodeStringify(t *testing.T) func(request any) string {
	node := startNode(t)
	return func(request any) string {
		t.Helper()
		return node.stringify(t, request)
	}
}

// RandomUnits, WTF8 and WTF8Surrogate are randomUnits, wtf8 and wtf8Surrogate for the external test package.
var (
	RandomUnits   = randomUnits
	WTF8          = wtf8
	WTF8Surrogate = wtf8Surrogate
)

// wtf8 is the Go string for units, cut at the given points and joined: a surrogate pair cut between its halves becomes two three-byte halves,
// which JavaScript joins back into one pair.
func wtf8(units []uint16, cuts ...int) string {
	var s string
	start := 0
	for _, cut := range cuts {
		s += jv.FromUTF16(units[start:cut])
		start = cut
	}
	return s + jv.FromUTF16(units[start:])
}

func randomUnits(rng *rand.Rand) []uint16 {
	pools := [][2]uint16{{0, 0x20}, {0x20, 0x7F}, {0x7F, 0xA0}, {0x2028, 0x202A}, {0xD800, 0xDC00}, {0xDC00, 0xE000}, {0xE000, 0xFFFF}, {0x80, 0x800}, {0xFFFD, 0xFFFE}}
	units := make([]uint16, rng.IntN(12))
	for i := range units {
		if rng.IntN(4) == 0 && i+1 < len(units) {
			// A well-formed pair, which a cut may split.
			units[i], units[i+1] = uint16(0xD800+rng.IntN(0x400)), uint16(0xDC00+rng.IntN(0x400))
			continue
		}
		if i > 0 && units[i-1] >= 0xD800 && units[i-1] < 0xDC00 && units[i] >= 0xDC00 && units[i] < 0xE000 {
			continue
		}
		pool := pools[rng.IntN(len(pools))]
		units[i] = pool[0] + uint16(rng.IntN(int(pool[1]-pool[0])))
	}
	return units
}

// AppendString writes what Node's JSON.stringify writes for the same UTF-16 string, whether its surrogate pairs arrive whole or as two halves.
func TestAppendStringMatchesNode(t *testing.T) {
	node := startNode(t)
	check := func(units []uint16, s string) {
		t.Helper()
		want := node.stringify(t, map[string][]uint16{"units": units})
		if got := string(AppendString(nil, s)); got != want {
			t.Fatalf("AppendString(%q) (units %x)\n got: %s\nwant: %s", s, units, got, want)
		}
	}
	fixed := [][]uint16{
		{}, {'"', '\\', '/', '\b', '\f', '\n', '\r', '\t', 0, 0x1F, 0x7F}, {'<', '>', '&', '\''}, {0x2028, 0x2029, 0xFEFF},
		{0xD83D, 0xDE00}, {0xD800}, {0xDFFF}, {0xDC00, 0xD800}, {0xD800, 0xD800, 0xDC00}, {'a', 0xDBFF}, {0xFFFD},
	}
	for _, units := range fixed {
		check(units, wtf8(units))
		for cut := 1; cut < len(units); cut++ {
			check(units, wtf8(units, cut))
		}
	}
	// A byte that is not WTF-8 cannot come from a JavaScript string; it stands for U+FFFD.
	check([]uint16{'a', 0xFFFD, 'b'}, "a\xffb")
	// Ill-formed bytes are one U+FFFD per maximal subpart, as Node decodes them (Buffer.from(bytes).toString()).
	for _, c := range []struct {
		units []uint16
		bytes string
	}{
		{[]uint16{0xFFFD, 0xFFFD}, "\xed\xa0"}, {[]uint16{0xFFFD}, "\xf0\x9f\x98"}, {[]uint16{0xFFFD}, "\xe2\x82"}, {[]uint16{0xFFFD}, "\xc3"},
		{[]uint16{0xFFFD, 'A'}, "\xf0\x9f\x98A"}, {[]uint16{0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD}, "\xf4\x90\x80\x80"}, {[]uint16{0xFFFD, 0xFFFD}, "\xc0\xaf"},
		{[]uint16{0xFFFD, 0xFFFD, 0xFFFD}, "\xe0\x80\x80"}, {[]uint16{0xFFFD, 0xFFFD}, "\xe1\x80\xe1\x80"}, {[]uint16{0xFFFD, 0xFFFD}, "\xf1\x80\x80\xf1"},
	} {
		check(c.units, c.bytes)
	}

	rng := rand.New(rand.NewPCG(1, 2))
	// Random byte strings, mostly ill-formed UTF-8, against Node's decoder.
	pool := []byte{'a', 0x80, 0x8F, 0x90, 0x9F, 0xA0, 0xBF, 0xC0, 0xC2, 0xDF, 0xE0, 0xE1, 0xED, 0xEF, 0xF0, 0xF1, 0xF4, 0xF5, 0xFF}
	for range 2000 {
		raw := make([]byte, rng.IntN(8))
		for i := range raw {
			raw[i] = pool[rng.IntN(len(pool))]
		}
		if wtf8Surrogate(raw) {
			// Three bytes ED A0..BF 80..BF are how a Go string holds a lone UTF-16 unit; they stand for that unit, not for ill-formed input.
			continue
		}
		ints := make([]int, len(raw))
		for i, b := range raw {
			ints[i] = int(b)
		}
		want := node.stringify(t, map[string][]int{"bytes": ints})
		if got := string(AppendString(nil, string(raw))); got != want {
			t.Fatalf("AppendString(%q)\n got: %s\nwant: %s", raw, got, want)
		}
	}
	for range 2000 {
		units := randomUnits(rng)
		var cuts []int
		for i := 1; i < len(units); i++ {
			if rng.IntN(3) == 0 {
				cuts = append(cuts, i)
			}
		}
		check(units, wtf8(units, cuts...))
	}
}

// AppendNumber writes what Node's JSON.stringify writes for the same double, and AppendInt what it writes for the double JavaScript holds for an
// integer.
func TestAppendNumberMatchesNode(t *testing.T) {
	node := startNode(t)
	check := func(f float64) {
		t.Helper()
		want := node.stringify(t, map[string]string{"bits": fmt.Sprintf("%016x", math.Float64bits(f))})
		if got := string(AppendNumber(nil, f)); got != want {
			t.Fatalf("AppendNumber(%v) (bits %016x)\n got: %s\nwant: %s", f, math.Float64bits(f), got, want)
		}
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 1, -1, 1e21, 1e21 - 65536, 999999999999999900000, 1e-7, 1.0000000000000001e-7, 1e-6, 9.999999999999999e-7,
		1 << 53, 1<<53 + 2, maxSafeInteger, 1e15, 1e15 + 0.5, 123456789012345680000, 0.1, 0.30000000000000004, 5e-324, math.SmallestNonzeroFloat64 * 3,
		2.2250738585072014e-308, math.MaxFloat64, -math.MaxFloat64, math.NaN(), math.Inf(1), math.Inf(-1), 1.5, -2.5e-8, 4.35, 1e300} {
		check(f)
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for range 2000 {
		check(math.Float64frombits(rng.Uint64()))
		check(float64(rng.Int64N(1<<62)) / math.Pow(10, float64(rng.IntN(30))))
	}

	checkInt := func(n int64) {
		t.Helper()
		want := node.stringify(t, map[string]string{"bigint": strconv.FormatInt(n, 10)})
		if got := string(AppendInt(nil, n)); got != want {
			t.Fatalf("AppendInt(%d)\n got: %s\nwant: %s", n, got, want)
		}
	}
	for _, n := range []int64{0, 1, -1, maxSafeInteger, -maxSafeInteger, maxSafeInteger + 1, maxSafeInteger + 2, -maxSafeInteger - 2, math.MaxInt64, math.MinInt64, 1e18, 123456789012345678} {
		checkInt(n)
	}
	for range 2000 {
		checkInt(int64(rng.Uint64()))
		checkInt(rng.Int64N(1 << 54))
	}
}

func TestAppendBoolAndOpt(t *testing.T) {
	for _, c := range []struct {
		opt     Opt[int64]
		present bool
		want    string
	}{{Opt[int64]{}, false, "null"}, {Null[int64](), true, "null"}, {Some[int64](0), true, "0"}, {Some[int64](7), true, "7"}} {
		if c.opt.Present() != c.present {
			t.Errorf("%+v Present = %t", c.opt, !c.present)
		}
		if got := string(AppendOpt(nil, c.opt, AppendInt)); got != c.want {
			t.Errorf("%+v = %s, want %s", c.opt, got, c.want)
		}
	}
	if v, ok := Some("x").Get(); !ok || v != "x" {
		t.Error("Some(x).Get")
	}
	if _, ok := Null[string]().Get(); ok || !Null[string]().IsNull() {
		t.Error("Null")
	}
	if string(AppendBool(AppendBool(nil, true), false)) != "truefalse" {
		t.Error("AppendBool")
	}
}

// IsIndexKey accepts exactly the keys a JavaScript object enumerates first.
func TestIsIndexKeyMatchesNode(t *testing.T) {
	keys := []string{"0", "1", "01", "-1", "1.0", "4294967294", "4294967295", "4294967296", "", " 1", "1e3", "12a", "9007199254740991"}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `const keys = JSON.parse(process.argv[1]); const o = {z: 0}; for (const k of keys) o[k] = 0; console.log(JSON.stringify(Object.keys(o)));`, mustJSON(t, keys))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	if err := json.Unmarshal(out, &order); err != nil {
		t.Fatal(err)
	}
	// Node lists the index keys before "z"; every other key follows it.
	for i, key := range order {
		before := i < slicesIndex(order, "z")
		if key != "z" && before != IsIndexKey(key) {
			t.Errorf("IsIndexKey(%q) = %t, Node enumerates it %s z", key, IsIndexKey(key), map[bool]string{true: "before", false: "after"}[before])
		}
	}
}

func slicesIndex(items []string, item string) int {
	for i, v := range items {
		if v == item {
			return i
		}
	}
	return -1
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wtf8Surrogate(raw []byte) bool {
	for i := 0; i+2 < len(raw); i++ {
		if raw[i] == 0xED && raw[i+1] >= 0xA0 && raw[i+1] <= 0xBF && raw[i+2]&0xC0 == 0x80 {
			return true
		}
	}
	return false
}
