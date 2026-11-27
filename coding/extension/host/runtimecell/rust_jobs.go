// SPDX-License-Identifier: MIT

package runtimecell

import (
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/pbnjay/memory"
)

// rustJobMemoryAllowance is the physical memory one concurrent cargo job may
// claim in the Rust job budget. A cold build of a pig cell (pig-sdk, serde and
// serde_json) on a 16-CPU, 16 GiB Windows machine peaked at about 1.1 GB for
// its largest single rustc or linker process, so a job is budgeted at 1 GiB.
const rustJobMemoryAllowance = 1 << 30

// rustJobMemoryShare divides physical memory into the part the Rust job budget
// may claim and the part that stays with the host, the sessions and the OS.
const rustJobMemoryShare = 2

var (
	// rustJobCores and rustJobPhysicalMemory size the budget; tests replace them.
	rustJobCores = runtime.NumCPU
	// TotalMemory is the memory accessible to the kernel, 0 on a platform that
	// cannot report it.
	rustJobPhysicalMemory = func() int64 { return int64(memory.TotalMemory()) }
	// pig additive (D20): PiG compiles extensions, which Pi does not, so the
	// Rust job budget of its builds has no upstream equivalent.
	rustBuildJobs = newRustJobPool(rustJobBudget())
)

// rustJobBudget is the number of concurrent cargo jobs the Rust builds of this
// process share: one job per GiB of the half of physical memory the budget
// claims, never more than the machine's CPUs. Memory the platform cannot report
// (TotalMemory's 0) leaves the budget at the CPU count, which is cargo's own
// default.
func rustJobBudget() int {
	return rustJobBudgetFor(rustJobCores(), rustJobPhysicalMemory())
}

func rustJobBudgetFor(cores int, memory int64) int {
	if cores < 1 {
		cores = 1
	}
	if memory <= 0 {
		return cores
	}
	memoryJobs := int(memory / rustJobMemoryShare / rustJobMemoryAllowance)
	if memoryJobs < 1 {
		return 1
	}
	if memoryJobs < cores {
		return memoryJobs
	}
	return cores
}

// rustJobPool hands the budget to concurrent cargo builds. A build takes the
// whole free budget, so the one cold build a quiet machine usually runs at the
// budget's full width; a build that starts while the budget is spent still
// takes one job, so it never waits on another build, and concurrent builds can
// therefore claim one job each beyond the budget. take reports how many of the
// jobs it returned came from the budget, and end returns only those: a job the
// pool never gave out must not come back as free.
type rustJobPool struct {
	mu    sync.Mutex
	total int
	free  int
}

func newRustJobPool(jobs int) *rustJobPool {
	if jobs < 1 {
		jobs = 1
	}
	return &rustJobPool{total: jobs, free: jobs}
}

// take returns the jobs one build may hand cargo and how many of them came
// from the budget. A build that starts while the budget is spent runs on one
// job outside it and reserves none.
func (p *rustJobPool) take() (jobs int, reserved int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.free < 1 {
		return 1, 0
	}
	jobs = p.free
	p.free = 0
	return jobs, jobs
}

// end returns a finished build's reservation to the budget. A build that ran
// on the unbudgeted minimum reserves nothing, so end(0) is right for it: the
// jobs it ran on were never taken from the pool and are not free now.
func (p *rustJobPool) end(reserved int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.free += reserved
	if p.free > p.total {
		p.free = p.total
	}
}

// RustBuildEnvironment returns the environment for one pig-driven cargo build
// and the function that ends the build. An environment that already names
// CARGO_BUILD_JOBS keeps it and takes no budget; otherwise the build takes a
// share of this process's Rust job budget and names it.
func RustBuildEnvironment(env []string) ([]string, func()) {
	return rustBuildEnvironment(env, rustBuildJobs)
}

func rustBuildEnvironment(env []string, jobs *rustJobPool) ([]string, func()) {
	if cargoBuildJobs(env) != "" {
		return env, func() {}
	}
	taken, reserved := jobs.take()
	return append(env, "CARGO_BUILD_JOBS="+strconv.Itoa(taken)), func() { jobs.end(reserved) }
}

// cargoBuildJobs returns the last CARGO_BUILD_JOBS value of env, or "" when env
// does not name one. An empty value counts as unset.
func cargoBuildJobs(env []string) string {
	const prefix = "CARGO_BUILD_JOBS="
	value := ""
	for _, entry := range env {
		if named, ok := strings.CutPrefix(entry, prefix); ok {
			value = named
		}
	}
	return value
}
