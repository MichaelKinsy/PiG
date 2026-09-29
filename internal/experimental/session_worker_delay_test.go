package experimental

import "testing"

// upstream: packages/coding-agent/src/experimental/session-worker.ts:472-478 lifecycleDelay parses with Number() and accepts only non-negative safe integers. Number() rejects numeric separators, so every underscore spelling fails even where Go's strconv would accept it.
func TestWorkerLifecycleDelayMatchesECMAScriptNumber(t *testing.T) {
	const name = "__PI_SESSION_WORKER_TEST_DELAY_MS"
	for _, row := range []struct {
		value string
		want  int
		fails bool
	}{
		{value: "", want: 0},
		{value: " \t25\n", want: 25},
		{value: "+7", want: 7},
		{value: "-0", want: 0},
		{value: "1e3", want: 1000},
		{value: "5.", want: 5},
		{value: "010", want: 10},
		{value: "0x10", want: 16},
		{value: "0X10", want: 16},
		{value: "0o17", want: 15},
		{value: "0b101", want: 5},
		{value: "9007199254740991", want: 9_007_199_254_740_991},
		{value: "9007199254740992", fails: true},
		{value: "0x20000000000000", fails: true},
		{value: "0xffffffffffffffff", fails: true},
		{value: "0x1fffffffffffff", want: 9_007_199_254_740_991},
		{value: "1_0", fails: true},
		{value: "1e1_0", fails: true},
		{value: "0x1_0", fails: true},
		{value: "0b1_1", fails: true},
		{value: "0o1_7", fails: true},
		{value: "0x", fails: true},
		{value: "-1", fails: true},
		{value: "1.5", fails: true},
		{value: "Infinity", fails: true},
		{value: "NaN", fails: true},
		{value: "-0x10", fails: true},
	} {
		t.Run(row.value, func(t *testing.T) {
			t.Setenv(name, row.value)
			got, err := workerLifecycleDelay(name, 99)
			if row.fails {
				if err == nil || err.Error() != name+" must be a non-negative safe integer" {
					t.Fatalf("workerLifecycleDelay(%q) = %d, %v; want Pi's safe-integer error", row.value, got, err)
				}
				return
			}
			if err != nil || got != row.want {
				t.Fatalf("workerLifecycleDelay(%q) = %d, %v; want %d", row.value, got, err, row.want)
			}
		})
	}
	t.Run("absent", func(t *testing.T) {
		if got, err := workerLifecycleDelay(name+"_ABSENT", 99); err != nil || got != 99 {
			t.Fatalf("absent = %d, %v; want fallback 99", got, err)
		}
	})
}
