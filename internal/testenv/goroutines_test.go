// SPDX-License-Identifier: MIT

package testenv

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeT struct {
	testing.TB
	failed  bool
	message string
}

func (f *fakeT) Helper() {}
func (f *fakeT) Fatalf(format string, args ...any) {
	f.failed = true
	f.message = fmt.Sprintf(format, args...)
	panic(f)
}

// ownerGoroutine is a goroutine that waits on a channel a live owner still references, the case the goroutineleak profile cannot see.
func ownerGoroutine() (release func()) {
	stop := make(chan struct{})
	started := make(chan struct{})
	go func() {
		close(started)
		<-stop
	}()
	<-started
	return func() { close(stop) }
}

func TestGoroutineBaselineSeesAGoroutineThatOutlivesItsOwner(t *testing.T) {
	baseline := BaselineGoroutines()
	release := ownerGoroutine()
	survivors := baseline.Outliving()
	if len(survivors) != 1 || !strings.Contains(survivors[0], "ownerGoroutine") {
		t.Fatalf("Outliving = %v, want the one owner goroutine", survivors)
	}
	release()
	baseline.RequireEnded(t, 5*time.Second)
	if survivors := baseline.Outliving(); len(survivors) != 0 {
		t.Fatalf("Outliving after release = %v, want none", survivors)
	}
}

func TestRequireEndedFailsNamingTheSurvivor(t *testing.T) {
	baseline := BaselineGoroutines()
	release := ownerGoroutine()
	defer release()
	probe := &fakeT{TB: t}
	func() {
		defer func() {
			if recovered := recover(); recovered != probe {
				panic(recovered)
			}
		}()
		baseline.RequireEnded(probe, 20*time.Millisecond)
	}()
	if !probe.failed {
		t.Fatal("RequireEnded passed with a goroutine still alive")
	}
	if !strings.Contains(probe.message, "ownerGoroutine") || !strings.Contains(probe.message, "outlived their owner") {
		t.Fatalf("failure message does not name the survivor: %s", probe.message)
	}
}

func TestRequireAtMostAllowsTheLimitAndFailsAboveIt(t *testing.T) {
	baseline := BaselineGoroutines()
	first, second := ownerGoroutine(), ownerGoroutine()
	defer first()
	defer second()
	baseline.RequireAtMost(t, 2, time.Second)
	probe := &fakeT{TB: t}
	func() {
		defer func() {
			if recovered := recover(); recovered != probe {
				panic(recovered)
			}
		}()
		baseline.RequireAtMost(probe, 1, 20*time.Millisecond)
	}()
	if !probe.failed || !strings.Contains(probe.message, "limit 1") {
		t.Fatalf("RequireAtMost(1) with two survivors: failed=%v message=%q", probe.failed, probe.message)
	}
}

// requireEndedFails runs RequireEnded against a fake test and returns its failure message, or "" when it passed.
func requireEndedFails(t *testing.T, baseline GoroutineBaseline, grace time.Duration) string {
	t.Helper()
	probe := &fakeT{TB: t}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil && recovered != probe {
				panic(recovered)
			}
		}()
		baseline.RequireEnded(probe, grace)
	}()
	return probe.message
}

// An owner's HTTP transport that keeps an idle connection keeps its connection goroutines: they outlive the owner until
// the owner closes its idle connections, so the gate reports them like any other survivor.
func TestRequireEndedSeesAnOwnersIdleHTTPConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer server.Close()
	baseline := BaselineGoroutines()
	transport := &http.Transport{}
	response, err := (&http.Client{Transport: transport}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if message := requireEndedFails(t, baseline, 20*time.Millisecond); !strings.Contains(message, "persistConn") {
		t.Fatalf("RequireEnded with an idle keep-alive connection open = %q, want a failure naming its persistConn goroutines", message)
	}
	transport.CloseIdleConnections()
	baseline.RequireEnded(t, 5*time.Second)
}
