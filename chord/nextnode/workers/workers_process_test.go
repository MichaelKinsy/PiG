package workers

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Ports packages/chord/test/next-node/workers/process.test.ts: a worker process is started with its output piped (the
// stderr tail kept), the link made at the fork (an early frame queued), control messages told, and the whole tree killed.

func TestAWorkerProcessPipesItsOutputKeepsItsStderrTailQueuesAnEarlyFrameOnItsLinkTellsControlMessages(t *testing.T) {
	var mu sync.Mutex
	var output []string
	var control []FromWorker
	child, err := NewWorkerProcess(
		ForkOptions{
			Entry: fixtureEntry(t),
			Env:   fixtureEnvironment("child"),
			OnOutput: func(stream string, chunk []byte) {
				mu.Lock()
				defer mu.Unlock()
				output = append(output, stream+": "+strings.TrimSpace(string(chunk)))
			},
		},
		ProcessEvents{
			Control: func(message FromWorker) {
				mu.Lock()
				defer mu.Unlock()
				control = append(control, message)
			},
			LinkLost:   func() {},
			LinkClosed: func() {},
			Exited:     func(Exit) {},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() ([]string, []FromWorker) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), output...), append([]FromWorker(nil), control...)
	}
	until(t, func() bool { out, ctl := snapshot(); return len(ctl) > 0 && len(out) >= 2 })
	var frames [][]byte
	child.Link.OnMessage(func(message []byte) { frames = append(frames, append([]byte(nil), message...)) })
	if !reflect.DeepEqual(frames, [][]byte{{1, 2, 3}}) {
		t.Fatalf("frames = %v, want the early frame [1 2 3] queued on the link", frames)
	}
	out, ctl := snapshot()
	if !reflect.DeepEqual(ctl, []FromWorker{{Kind: FromWorkerStarted}}) {
		t.Fatalf("control = %+v, want one started", ctl)
	}
	if got := child.StderrTail(); got != "an error line\n" {
		t.Fatalf("stderrTail = %q", got)
	}
	grandchild, err := strconv.Atoi(regexp.MustCompile(`grandchild (\d+)`).FindStringSubmatch(strings.Join(out, "\n"))[1])
	if err != nil {
		t.Fatal(err)
	}
	if !alive(grandchild) {
		t.Fatal("the grandchild is not alive")
	}
	child.Kill()
	<-child.Exited()
	exit := child.Exit()
	if exit.Code != nil || exit.Signal == nil || *exit.Signal != "SIGKILL" {
		t.Fatalf("exit = %+v, want {code: null, signal: SIGKILL}", exit)
	}
	until(t, func() bool { return !alive(grandchild) }, 2_000)
}
