// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"testing"
	"time"
)

// lineFrameInputs are the inputs and the frame delays of the scheduler's own test of Pi's interval
// (TestThrottledFrameWaitsTheWholeMillisecondsLeftOfTheInterval): a frame asked for elapsed after the last one began waits want.
var lineFrameInputs = []struct{ elapsed, want time.Duration }{
	{1400 * time.Microsecond, 14 * time.Millisecond},
	{5 * time.Millisecond, 11 * time.Millisecond},
	{15300 * time.Microsecond, 0},
	{16 * time.Millisecond, 0},
}

// A renderer whose run did not select the adaptive policy draws its frames at the times Pi's scheduler gives, on the inputs of the scheduler's
// own tests: one that was never given a policy, and one in which everything else the policy asks for is there (a trusted terminal, an open gate,
// an active screen writer) and only the policy was taken away. With the policy the same inputs give earlier frames, so the inputs tell the two apart.
func TestFrameTimesWithoutThePolicyArePisOnTheSchedulersOwnInputs(t *testing.T) {
	for _, tc := range lineFrameInputs {
		// Never given a policy: the scheduler's test as it is written.
		var out bytes.Buffer
		plain := NewWithOutput(&out, 20, 5)
		plain.Add(&countingRenderComponent{})
		clock := newManualRenderClock(t, plain)
		plain.requestRender(false)
		clock.fire()
		clock.at = clock.at.Add(tc.elapsed)
		plain.requestRender(false)
		if got := clock.delays[len(clock.delays)-1]; got != tc.want {
			t.Errorf("no policy given, a frame asked for %v after the last one began waits %v, want %v", tc.elapsed, got, tc.want)
		}

		// The policy taken away, all of its conditions met.
		removed := newPacingScreen(t)
		removed.ui.SetFramePacing(nil)
		removed.frame(0, 0)
		removed.at = removed.at.Add(tc.elapsed)
		asked := removed.at
		removed.ui.requestRender(false)
		if got := removed.lastDelay(); got != tc.want {
			t.Errorf("policy removed, a frame asked for %v after the last one began waits %v, want %v", tc.elapsed, got, tc.want)
		}
		if want := asked.Add(tc.want); !removed.due.Equal(want) {
			t.Errorf("policy removed, a frame asked for %v after the last one began is due %v, want %v", tc.elapsed, removed.due, want)
		}

		// The policy in force: the frame is due sooner wherever Pi's interval makes it wait longer than the floor allows.
		paced := newPacingScreen(t)
		paced.frame(0, 0)
		paced.at = paced.at.Add(tc.elapsed)
		paced.ui.requestRender(false)
		got := paced.lastDelay()
		if tc.elapsed < frameIntervalFloor && got >= tc.want {
			t.Errorf("policy in force, a frame asked for %v after the last one began waits %v, want less than Pi's %v", tc.elapsed, got, tc.want)
		}
		if got > tc.want {
			t.Errorf("policy in force, a frame asked for %v after the last one began waits %v, longer than Pi's %v", tc.elapsed, got, tc.want)
		}
	}
}

// The loop that keeps the frame deadline itself is handed Pi's deadline when the run did not select the adaptive policy, on the input of the
// scheduler's own test (TestOwnerDeadlineOfAThrottledFrameIsTheWholeMillisecondsLeftOfTheInterval): 14 ms after a request made 1.4 ms after the
// last frame began; with the policy in force the deadline is sooner.
func TestOwnerDeadlineWithoutThePolicyIsPisOnTheSchedulersOwnInput(t *testing.T) {
	for _, policy := range []string{"removed", "in force"} {
		s := newPacingScreen(t)
		if policy == "removed" {
			s.ui.SetFramePacing(nil)
		}
		var due []time.Time
		s.ui.SetRenderDeadline(func(d time.Time) { due = append(due, d) })
		s.at = s.at.Add(time.Second)
		s.ui.requestRender(false)
		s.ui.RunDueRender()
		s.at = s.at.Add(1400 * time.Microsecond)
		s.ui.requestRender(false)
		if len(due) == 0 {
			t.Fatalf("policy %s: the owner was handed no deadline", policy)
		}
		last, pis := due[len(due)-1], s.at.Add(14*time.Millisecond)
		if policy == "removed" && !last.Equal(pis) {
			t.Errorf("policy removed: the owner's deadline is %v, want Pi's %v", last, pis)
		}
		if policy == "in force" && !last.Before(pis) {
			t.Errorf("policy in force: the owner's deadline is %v, want one before Pi's %v", last, pis)
		}
	}
}
