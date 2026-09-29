package coding

// Ports packages/coding-agent/src/core/agent-session.ts (_prepareRetry, abortRetry).

import (
	"context"
	"sync"
	"time"
)

// SetRetryContinuationScheduler binds the host's Promise-reaction queue for retry prefix and completion steps. A host that supplies a queue must drain it while awaiting Session operations and keep consuming Events until Close finishes. Without a queue, the run's worker owns these steps.
func (s *Session) SetRetryContinuationScheduler(schedule func(func())) {
	s.retryMu.Lock()
	s.retrySchedule = schedule
	s.retryMu.Unlock()
}

type retryWaitResult struct {
	retry bool
	err   error
}

type sessionRetryWait struct {
	session  *Session
	ctx      context.Context
	cancel   context.CancelFunc
	schedule func(func())
	timer    *time.Timer
	done     chan retryWaitResult
	once     sync.Once
}

func (wait *sessionRetryWait) abort() {
	wait.cancel()
	if wait.schedule != nil {
		// AbortController.abort rejects sleep synchronously, enqueueing its catch before the command's resolved response continuation.
		wait.schedule(func() { wait.resolve(false) })
	}
}

func (wait *sessionRetryWait) resolve(retry bool) {
	wait.once.Do(func() {
		wait.session.retryMu.Lock()
		if wait.session.retryCancel == wait {
			wait.session.retryCancel = nil
		}
		wait.session.retryMu.Unlock()
		result := retryWaitResult{retry: retry && wait.ctx.Err() == nil}
		if !result.retry {
			wait.session.finishCancelledRetry()
			if wait.schedule != nil {
				result.err = wait.session.FlushEvents(context.WithoutCancel(wait.ctx))
			}
		}
		complete := func() { wait.done <- result }
		if wait.schedule == nil {
			complete()
		} else {
			// Returning from _prepareRetry resolves the Promise awaited by _handlePostAgentRun; its continuation follows already queued command replies.
			wait.schedule(complete)
		}
	})
}

func (wait *sessionRetryWait) await() (bool, error) {
	retry := false
	select {
	case <-wait.ctx.Done():
	case <-wait.timer.C:
		retry = true
	}
	if wait.schedule == nil {
		wait.resolve(retry)
	} else {
		wait.schedule(func() { wait.resolve(retry) })
	}
	result := <-wait.done
	return result.retry, result.err
}

// retryPrefix keeps the synchronous prefix atomic with host input callbacks, including the persisted omission preceding the sleep. Slow run work never holds the Session mutex while waiting for its host queue.
func (s *Session) retryPrefix(schedule func(func()), prefix func() (*sessionRetryWait, error)) (*sessionRetryWait, error) {
	if schedule == nil {
		return prefix()
	}
	type result struct {
		wait *sessionRetryWait
		err  error
	}
	done := make(chan result, 1)
	s.mu.Unlock()
	defer s.mu.Lock()
	schedule(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		wait, err := prefix()
		done <- result{wait, err}
	})
	value := <-done
	return value.wait, value.err
}
