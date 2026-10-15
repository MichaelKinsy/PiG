package host

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// Node's own timer code decides each delay: lib/internal/timers.js Timeout runs a delay outside [1, TIMEOUT_MAX] ms, or
// NaN, after 1 ms, and insert files the timer under MathTrunc(msecs). The script reads the list each timer joined.
const nodeTimerListScript = `
const { timerListMap } = require("internal/timers");
process.emitWarning = () => {};
const out = JSON.parse(process.argv[1]).map((ms) => {
	const timer = setTimeout(() => {}, ms);
	const lists = Object.keys(timerListMap);
	clearTimeout(timer);
	return Number(lists[0]);
});
console.log(JSON.stringify(out));
`

func TestNodeDelayMatchesNodeTimers(t *testing.T) {
	delays := []time.Duration{
		-time.Second, 0, 1, time.Millisecond / 2, time.Millisecond - 1, time.Millisecond, time.Millisecond + 1,
		1500 * time.Microsecond, 2*time.Millisecond - 1, 2 * time.Millisecond, 1000700 * time.Microsecond,
		nodeTimeoutMax - 1, nodeTimeoutMax, nodeTimeoutMax + 1, nodeTimeoutMax + time.Millisecond, 1<<63 - 1,
	}
	ms := make([]float64, len(delays))
	for i, d := range delays {
		ms[i] = float64(d) / float64(time.Millisecond)
	}
	input, err := json.Marshal(ms)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "node", "--expose-internals", "-e", nodeTimerListScript, string(input)).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []float64
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(delays) {
		t.Fatalf("node output %q: %v", output, err)
	}
	for i, d := range delays {
		if got := nodeDelay(d); got != time.Duration(want[i])*time.Millisecond {
			t.Errorf("nodeDelay(%s ms) = %s, Node waits %s ms", strconv.FormatFloat(ms[i], 'f', -1, 64), got, strconv.FormatFloat(want[i], 'f', -1, 64))
		}
	}
}

// cluster.ts:26-37 with a deadline above TIMEOUT_MAX: Node's setTimeout runs it after 1 ms, so the vote rejects at once,
// with the deadline as given in the message.
func TestAwaitVoteDeadlineAboveNodeTimeoutMaxRejectsAfterOneMillisecond(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	connection, _ := childConnection(t, host, "w")
	var vote *Promise[*WireReloadReport]
	host.run(func() { vote = awaitVote(host, connection, 9, nodeTimeoutMax+time.Millisecond) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := vote.Await(ctx)
	if !IsChordError(err, CodeAborted) || messageOf(err) != "aborted/vote_deadline: votes missing after 2147483648 ms" {
		t.Fatalf("vote: %v", err)
	}
}
