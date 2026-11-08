package p2test

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Go-only: what a failed test shows of its session workers. A worker is a child process, so a test whose call ended with
// `the link closed` (a worker that answered nothing for the heartbeat, which its server then killed, or one that crashed)
// used to show nothing of why: the worker's stderr went to the server's log store and was dropped with the test. When a
// test with a Fixture has failed, the fixture's cleanup (before it closes the server) asks each worker still alive for the
// stacks of its goroutines (SIGUSR1, see dumpStacksOnSignal) and logs what each session worker wrote: its stderr, with the
// stacks, its stdout and the server's events of it. The log is what the store retains of the source (1000 lines).

// stacksWait bounds the wait for the workers asked for their stacks to write them.
const stacksWait = 10 * time.Second

// trackSessions records the sessions whose worker started during the fixture, for its failure report.
func (f *Fixture) trackSessions() {
	f.Server.Workers().Subscribe(func(event p2runtime.HostEvent) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !slices.Contains(f.sessions, event.Session) {
			f.sessions = append(f.sessions, event.Session)
		}
	})
}

// livePids are the processes of the workers that run now, by session.
func (f *Fixture) livePids() map[string]int {
	value, err := f.Server.Workers().Workers().State().Value()
	if err != nil {
		return nil
	}
	states, _ := value.(*delta.JsonObject)
	live := map[string]int{}
	if states == nil {
		return live
	}
	for _, key := range states.Keys() {
		entry, _ := states.Value(key).(*delta.JsonObject)
		if entry == nil {
			continue
		}
		if pid, ok := entry.Value("pid").(float64); ok && pid > 0 {
			live[strings.TrimPrefix(key, "session|")] = int(pid)
		}
	}
	return live
}

// stacksWritten is whether the session's worker wrote its stacks: its end line is in its output.
func stacksWritten(session string) bool {
	return slices.ContainsFunc(p2runtime.ProcessLogs.Lines(hosts.SessionPrefix+session), func(line p2runtime.LogLine) bool {
		return line.Level == p2runtime.LogStderr && line.Text == stacksEnd
	})
}

// reportWorkers is the fixture's cleanup, run before the server closes: on a failed test it asks each live worker for its
// stacks and logs the output of every session worker.
func (f *Fixture) reportWorkers() {
	if !f.t.Failed() || f.Server == nil {
		return
	}
	var asked []string
	for session, pid := range f.livePids() {
		if askForStacks(pid) {
			asked = append(asked, session)
		}
	}
	waited := Poll(func() (bool, error) {
		return !slices.ContainsFunc(asked, func(session string) bool { return !stacksWritten(session) }), nil
	}, stacksWait, "the workers to write their stacks")
	f.mu.Lock()
	sessions := slices.Clone(f.sessions)
	f.mu.Unlock()
	for _, session := range sessions {
		var report strings.Builder
		for _, line := range p2runtime.ProcessLogs.Lines(hosts.SessionPrefix + session) {
			report.WriteString(string(line.Level))
			report.WriteString(": ")
			report.WriteString(line.Text)
			report.WriteByte('\n')
		}
		f.t.Logf("session worker %s:\n%s", session, report.String())
	}
	if waited != nil {
		f.t.Logf("%v: %s", waited, fmt.Sprint(asked))
	}
}
