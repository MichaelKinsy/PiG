// Ports test/p2/subagent-reporter.test.ts: how the reporter's wait on a subagent's submission ends.

package subagents

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

// reporterFixture is a submission and its subagent conversation, driven by the test.
type reporterFixture struct {
	mu      sync.Mutex
	status  durable.SubmissionStatus
	idleNow bool
	idle    chan struct{}
	settled chan durable.SettledSubmissionRecord
	aborts  int
}

type fakeSubmission struct {
	durable.Submission
	f *reporterFixture
}

func (s fakeSubmission) Wait(ctx context.Context) (durable.SettledSubmissionRecord, error) {
	select {
	case record := <-s.f.settled:
		return record, nil
	case <-ctx.Done():
		return durable.SettledSubmissionRecord{}, ctx.Err()
	}
}

func (s fakeSubmission) Status(context.Context) (durable.SubmissionRecord, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	return durable.SubmissionRecord{Status: s.f.status}, nil
}

func (s fakeSubmission) Abort(context.Context) (durable.SubmissionAbortResult, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if s.f.status != durable.SubmissionQueued {
		return durable.SubmissionAlreadyPlaced, nil
	}
	s.f.aborts++
	return durable.SubmissionAborted, nil
}

type fakeSubagent struct {
	durable.ConversationHandle
	f *reporterFixture
}

func (s fakeSubagent) WaitForIdle(ctx context.Context) error {
	s.f.mu.Lock()
	idleNow := s.f.idleNow
	s.f.mu.Unlock()
	if idleNow {
		return nil
	}
	select {
	case <-s.f.idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newReporterFixture(status durable.SubmissionStatus, idle bool) *reporterFixture {
	return &reporterFixture{status: status, idleNow: idle, idle: make(chan struct{}), settled: make(chan durable.SettledSubmissionRecord, 1)}
}

func (f *reporterFixture) run(pause func() error) (*durable.SettledSubmissionRecord, error) {
	return settledOrWithdrawn(context.Background(), fakeSubmission{f: f}, fakeSubagent{f: f}, pause)
}

var answered = durable.SettledSubmissionRecord{Status: durable.SubmissionStatus("answered")}

type reporterResult struct {
	record *durable.SettledSubmissionRecord
	err    error
}

func (f *reporterFixture) start(pause func() error) chan reporterResult {
	done := make(chan reporterResult, 1)
	go func() {
		record, err := f.run(pause)
		done <- reporterResult{record, err}
	}()
	return done
}

func TestSubagentReporterAnAnswerSettlesTheWait(t *testing.T) {
	f := newReporterFixture(durable.SubmissionStatus("placed"), false)
	done := f.start(func() error { return nil })
	f.settled <- answered
	result := <-done
	if result.err != nil || result.record == nil || result.record.Status != answered.Status || f.aborts != 0 {
		t.Fatalf("result = %+v, aborts = %d", result, f.aborts)
	}
}

// The subagent's run failed with this message queued behind it: Durable leaves it queued and nothing places it.
func TestSubagentReporterAMessageStillQueuedOnceIdleIsWithdrawn(t *testing.T) {
	f := newReporterFixture(durable.SubmissionQueued, false)
	done := f.start(func() error { return nil })
	close(f.idle)
	select {
	case result := <-done:
		if result.err != nil || result.record != nil || f.aborts != 1 {
			t.Fatalf("result = %+v, aborts = %d", result, f.aborts)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait never ended")
	}
}

func TestSubagentReporterAMessagePlacedMeanwhileIsAwaited(t *testing.T) {
	f := newReporterFixture(durable.SubmissionQueued, true)
	// The pause lets an admission in flight place the message.
	done := f.start(func() error {
		f.mu.Lock()
		f.status = durable.SubmissionStatus("placed")
		f.mu.Unlock()
		return nil
	})
	time.AfterFunc(10*time.Millisecond, func() { f.settled <- answered })
	result := <-done
	if result.err != nil || result.record == nil || result.record.Status != answered.Status || f.aborts != 0 {
		t.Fatalf("result = %+v, aborts = %d", result, f.aborts)
	}
}
