// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import "testing"

// The host's commit leaves its job's end as Pi's does: the caller resumes there, hostAnswerTicks after the core's answer, the publication goes out with
// the Storage's adoption (pubBeforeEnd + adoptTail hops before the job's run settles, as a core line job's: Pi e7a0df27 F-ticks-runtime-drift, adoption at
// pass 114 = 83 + total 36 - 5) and the line is free a hop before the end. A job shorter than the facade's chain
// answers at once.
func TestAHostCommitIsAnsweredEarlyEnoughForItsCallerToResumeAtThePiJobEnd(t *testing.T) {
	for _, c := range []struct {
		total int
		want  hostCommitHops
	}{
		{137, hostCommitHops{publish: 132, answer: 134, release: 136}},
		{34, hostCommitHops{publish: 29, answer: 31, release: 33}},
		{3, hostCommitHops{publish: 0, answer: 0, release: 2}},
		{2, hostCommitHops{publish: 0, answer: 0, release: 1}},
		{0, hostCommitHops{}},
	} {
		if got := hostCommitSchedule(c.total, 3); got != c.want {
			t.Errorf("total %d: schedule %+v, want %+v", c.total, got, c.want)
		}
	}
}
