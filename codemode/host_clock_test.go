package codemode_test

import (
	"encoding/json"
	"testing"
	"time"
)

// The script reaches the host through quickjs-wasi's WASI shim, which upstream's worker keeps apart from fd_write
// (packages/codemode/src/runtime/worker.ts): clock_time_get is the host's Date.now() for the realtime and the
// monotonic clock, random_get is crypto.getRandomValues, and the timezone is the host's (quickjs-wasi 3.6.2
// dist/wasi-shim.js and dist/index.js, timezoneOffset "host").

func TestDateNowIsTheHostsWallClockInMilliseconds(t *testing.T) {
	sandbox := newSandbox(t, 0)
	before := time.Now().UnixMilli()
	result := run(t, sandbox, "return [Date.now(), new Date().getTime()]")
	after := time.Now().UnixMilli()
	wantOK(t, result, string(result.Value))
	var now []int64
	if err := json.Unmarshal(result.Value, &now); err != nil {
		t.Fatal(err)
	}
	for _, ms := range now {
		if ms < before || ms > after {
			t.Errorf("script clock %d outside the host's [%d, %d]", ms, before, after)
		}
	}
}

func TestMathRandomIsSeededFreshForEveryExecution(t *testing.T) {
	sandbox := newSandbox(t, 0)
	seen := map[string]bool{}
	for range 3 {
		result := run(t, sandbox, "return [Math.random(), Math.random()]")
		wantOK(t, result, string(result.Value))
		if seen[string(result.Value)] {
			t.Fatalf("Math.random() repeated %s across executions: the VM's random source is not the host's", result.Value)
		}
		seen[string(result.Value)] = true
	}
}

func TestDatesUseTheHostsTimezone(t *testing.T) {
	local := time.Local
	t.Cleanup(func() { time.Local = local })
	time.Local = time.FixedZone("UTC+05:30", 5*3600+30*60)
	sandbox := newSandbox(t, 0)
	result := run(t, sandbox, "const d = new Date(0); return [d.getTimezoneOffset(), d.getHours(), d.getMinutes()]")
	wantOK(t, result, "[-330,5,30]")
}
