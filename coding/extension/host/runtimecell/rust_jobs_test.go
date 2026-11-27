// SPDX-License-Identifier: MIT

package runtimecell

import "testing"

// A cargo build pig starts must not be pinned to one job: on a 16-CPU machine a
// cold cell build (pig-sdk, serde, serde_json) took 192.4 s at CARGO_BUILD_JOBS=1
// against 72.9 s at 4 jobs and 47.7 s at 16 jobs, and the packed cell build
// forced 1 job while the standalone Rust build took cargo's own default. Both
// paths now take a share of one process-wide budget instead.
func TestRustBuildEnvironmentTakesTheFreeBudgetOfThePool(t *testing.T) {
	pool := newRustJobPool(8)
	env, end := rustBuildEnvironment([]string{"PATH=/bin"}, pool)
	defer end()
	if got := cargoBuildJobs(env); got != "8" {
		t.Errorf("CARGO_BUILD_JOBS = %q, want the pool's free budget 8", got)
	}
}

func TestRustBuildEnvironmentReturnsTheBudgetWhenTheBuildEnds(t *testing.T) {
	pool := newRustJobPool(4)
	_, end := rustBuildEnvironment(nil, pool)
	end()
	env, endSecond := rustBuildEnvironment(nil, pool)
	defer endSecond()
	if got := cargoBuildJobs(env); got != "4" {
		t.Errorf("CARGO_BUILD_JOBS = %q after the first build ended, want the whole budget 4", got)
	}
}

// A build that starts while the budget is spent must still get a job: waiting
// for another build would hold up the extension load that asked for it.
func TestRustBuildEnvironmentGivesAConcurrentBuildOneJob(t *testing.T) {
	pool := newRustJobPool(4)
	_, endFirst := rustBuildEnvironment(nil, pool)
	env, endSecond := rustBuildEnvironment(nil, pool)
	defer endFirst()
	defer endSecond()
	if got := cargoBuildJobs(env); got != "1" {
		t.Errorf("CARGO_BUILD_JOBS = %q for the second concurrent build, want 1", got)
	}
}

// A build whose environment already names CARGO_BUILD_JOBS keeps it: it is the
// user's own override and takes no budget from the other builds.
func TestRustBuildEnvironmentKeepsTheCallersJobs(t *testing.T) {
	pool := newRustJobPool(8)
	env, end := rustBuildEnvironment([]string{"CARGO_BUILD_JOBS=3"}, pool)
	defer end()
	if got := cargoBuildJobs(env); got != "3" {
		t.Errorf("CARGO_BUILD_JOBS = %q, want the caller's 3", got)
	}
	next, endNext := rustBuildEnvironment(nil, pool)
	defer endNext()
	if got := cargoBuildJobs(next); got != "8" {
		t.Errorf("CARGO_BUILD_JOBS = %q for the next build, want the untouched budget 8", got)
	}
}

// An empty value counts as unset, the way an inherited empty variable does.
func TestRustBuildEnvironmentReplacesAnEmptyCallersJobs(t *testing.T) {
	pool := newRustJobPool(4)
	env, end := rustBuildEnvironment([]string{"CARGO_BUILD_JOBS="}, pool)
	defer end()
	if got := cargoBuildJobs(env); got != "4" {
		t.Errorf("CARGO_BUILD_JOBS = %q, want the pool's budget 4", got)
	}
}

// The budget is one job per GiB of the half of physical memory it claims, never
// more than the machine's CPUs. An unreadable physical memory falls back to the
// CPU count, which is cargo's own default.
func TestRustJobBudgetIsBoundedByCoresAndMemory(t *testing.T) {
	const gib = int64(1) << 30
	cases := []struct {
		name   string
		cores  int
		memory int64
		want   int
	}{
		{name: "memory binds", cores: 64, memory: 16 * gib, want: 8},
		{name: "cores bind", cores: 4, memory: 64 * gib, want: 4},
		{name: "one job floor", cores: 8, memory: 1 * gib, want: 1},
		{name: "unknown memory keeps the cores", cores: 12, memory: 0, want: 12},
		{name: "no cores is one job", cores: 0, memory: 64 * gib, want: 1},
		{name: "this machine's shape", cores: 16, memory: 15_743_000_000, want: 7},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := rustJobBudgetFor(test.cores, test.memory); got != test.want {
				t.Errorf("rustJobBudgetFor(%d, %d GiB) = %d, want %d", test.cores, test.memory>>30, got, test.want)
			}
		})
	}
}

// A build that ran on the unbudgeted minimum must not put its job back: it
// never took one from the budget, which the build that did still holds. With a
// total of 4, A takes all four, two minimum builds finish while A runs, and the
// next build must still get one job rather than the two a spent pool would
// wrongly report as free.
func TestRustBuildEnvironmentDoesNotReturnAnUnbudgetedJobToThePool(t *testing.T) {
	pool := newRustJobPool(4)
	_, endA := rustBuildEnvironment(nil, pool)
	_, endB := rustBuildEnvironment(nil, pool)
	_, endC := rustBuildEnvironment(nil, pool)
	endB()
	endC()
	envD, endD := rustBuildEnvironment(nil, pool)
	defer endA()
	defer endD()
	if got := cargoBuildJobs(envD); got != "1" {
		t.Errorf("CARGO_BUILD_JOBS = %q for the next build while A holds the budget, want 1", got)
	}
}

// The budget a pool starts with is never handed out twice, and a build that
// took the unbudgeted minimum cannot push the pool past its total.
func TestRustJobPoolNeverGivesOutMoreThanItsTotal(t *testing.T) {
	pool := newRustJobPool(2)
	first, firstReserved := pool.take()
	second, secondReserved := pool.take()
	if first != 2 || firstReserved != 2 || second != 1 || secondReserved != 0 {
		t.Fatalf("take() = (%d jobs, %d reserved) then (%d jobs, %d reserved), want (2, 2) then (1, 0)", first, firstReserved, second, secondReserved)
	}
	pool.end(secondReserved)
	pool.end(firstReserved)
	if got, _ := pool.take(); got != 2 {
		t.Errorf("take() after both builds ended = %d, want the full budget 2", got)
	}
}
