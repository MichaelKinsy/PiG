// Go-only: the fixture's report of a failed test's session workers (p2test/workerlogs.go). A worker is a child process: a
// call that ends with `the link closed` (a worker that answered nothing, a crash) showed nothing of why, because its stderr
// went to the server's log store and was dropped with the test. A failed test's fixture now logs what each session worker
// wrote, after asking each worker still alive for its goroutines' stacks.

package e2e_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// reportedTB is the test a fixture belongs to, with the verdict and the log in the hands of the case: its cleanups run when
// the case says so.
type reportedTB struct {
	testing.TB
	failed   bool
	mu       sync.Mutex
	cleanups []func()
	logs     strings.Builder
}

func (r *reportedTB) Failed() bool { return r.failed }

func (r *reportedTB) Cleanup(cleanup func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, cleanup)
}

func (r *reportedTB) Logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(&r.logs, format, args...)
}

// finish runs the cleanups last-in first-out, as testing does, and returns what was logged.
func (r *reportedTB) finish() string {
	r.mu.Lock()
	cleanups := r.cleanups
	r.cleanups = nil
	r.mu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logs.String()
}

// answeredSession starts a session on fixture and has its worker answer a message: the worker runs, and its id.
func answeredSession(t *testing.T, fixture *p2test.Fixture) string {
	t.Helper()
	browser := fixture.Browser()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "report"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id")
	chat, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "hello", "requestId", "r1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool { return view != nil && len(assistants(view)) > 0 }, 0); err != nil {
		t.Fatal(err)
	}
	return session.(string)
}

func TestAFailedTestLogsItsSessionWorkersStacks(t *testing.T) {
	failing := &reportedTB{TB: t, failed: true}
	fixture := p2test.StartFixture(failing, hosts.ServerOptions{})
	session := answeredSession(t, fixture)
	logs := failing.finish()
	// The report names the session's worker, holds the stacks of its goroutines (the main one is in the worker's entry) up to
	// their end line, and the worker went on to answer for them (it did not crash for the signal).
	for _, want := range []string{
		"session worker " + session + ":", "stderr: goroutine 1 [", "p2/hosts.RunWorker", "stderr: p2test: the stacks of the goroutines end",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the failure report lacks %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "Worker crashed") {
		t.Errorf("the worker crashed for the request of its stacks:\n%s", logs)
	}
}

func TestAPassingTestLogsNothingOfItsWorkers(t *testing.T) {
	passing := &reportedTB{TB: t}
	fixture := p2test.StartFixture(passing, hosts.ServerOptions{})
	answeredSession(t, fixture)
	if logs := passing.finish(); logs != "" {
		t.Fatalf("a passing test logged:\n%s", logs)
	}
}
