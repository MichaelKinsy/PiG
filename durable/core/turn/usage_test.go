package turn

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// TestUsageOracle replays src/harness/usage.ts (pi-durable main da866ada, run by oracle/gen_usage.mjs): addUsage,
// recordUsage over a pi.usage value, and addUsageState, each compared as JSON text.
func TestUsageOracle(t *testing.T) {
	var counts [3]int
	for n, f := range corpusLines(t, "usage.hex") {
		switch f[0] {
		case "A":
			total := parseObject(t, unhex(t, f[1]))
			AddUsage(total, parseObject(t, unhex(t, f[2])))
			if got, want := jv.Stringify(total), unhex(t, f[3]); got != want {
				t.Fatalf("line %d addUsage:\n got %s\nwant %s", n, got, want)
			}
			counts[0]++
		case "B":
			doc := parseObject(t, unhex(t, f[1]))
			RecordUsage(doc, f[2], unhex(t, f[3]), parseObject(t, unhex(t, f[4])))
			if got, want := jv.Stringify(doc), unhex(t, f[5]); got != want {
				t.Fatalf("line %d recordUsage:\n got %s\nwant %s", n, got, want)
			}
			counts[1]++
		case "S":
			sum := parseObject(t, unhex(t, f[1]))
			AddUsageState(sum, parseObject(t, unhex(t, f[2])))
			if got, want := jv.Stringify(sum), unhex(t, f[3]); got != want {
				t.Fatalf("line %d addUsageState:\n got %s\nwant %s", n, got, want)
			}
			counts[2]++
		}
	}
	if counts[0] < 100 || counts[1] < 100 || counts[2] < 100 {
		t.Fatalf("corpus covers %v cases", counts)
	}
}

// TestRecordUsageKeysAreOwn: a tool called `__proto__` or `toString` is an ordinary key of the ledger (usage.ts
// Object.hasOwn and defineProperty).
func TestRecordUsageKeysAreOwn(t *testing.T) {
	ledger := UsageInitial()
	usage := `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	for range 2 {
		RecordUsage(ledger, UsageTools, "__proto__", parseObject(t, usage))
		RecordUsage(ledger, UsageTools, "toString", parseObject(t, usage))
	}
	tools, _ := ledger.Get(UsageTools)
	for _, key := range []string{"__proto__", "toString"} {
		entry, _ := tools.(*jv.Object).Get(key)
		input, _ := entry.(*jv.Object).Get("input")
		if input != float64(2) {
			t.Errorf("%s input = %v, want the two calls added", key, input)
		}
	}
}
