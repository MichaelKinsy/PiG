// SPDX-License-Identifier: MIT

package memprofrate

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/metrics"
	"strings"
	"testing"
)

func TestRateFor(t *testing.T) {
	tests := []struct {
		name    string
		current int
		profile string
		want    int
	}{
		{"no profile", DefaultRate, "", 0},
		{"profiles that read no samples", DefaultRate, "cpu,trace,metrics,goroutine,block,mutex,flight", 0},
		{"heap", DefaultRate, "heap", DefaultRate},
		{"allocs", DefaultRate, "allocs", DefaultRate},
		{"heap among others with spaces", DefaultRate, "cpu, heap ,metrics", DefaultRate},
		{"unknown kind", DefaultRate, "heaps,Heap", 0},
		{"GODEBUG rate stays", 4096, "", 4096},
		{"GODEBUG rate of one stays", 1, "cpu", 1},
		{"GODEBUG zero stays", 0, "heap", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rateFor(tt.current, tt.profile); got != tt.want {
				t.Fatalf("rateFor(%d, %q) = %d, want %d", tt.current, tt.profile, got, tt.want)
			}
		})
	}
}

const childEnv = "PIG_MEMPROFRATE_CHILD"

//go:noinline
func allocateSampledWorkload() (kept [][]byte) {
	for range 16 << 10 {
		kept = append(kept, make([]byte, 4096))
		if len(kept) > 64 {
			kept = kept[:0]
		}
	}
	return kept
}

// The profiling-buckets class is the runtime's stack records of sampled allocations; its first record allocates a 1.4 MB table.
func bucketsBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/profiling/buckets:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// sampledAllocBytes is what the runtime's allocation profile attributes to allocateSampledWorkload after the collections that publish it.
func sampledAllocBytes() int64 {
	runtime.GC()
	runtime.GC()
	records := make([]runtime.MemProfileRecord, 64)
	n, ok := runtime.MemProfile(records, true)
	for !ok {
		records = make([]runtime.MemProfileRecord, n+64)
		n, ok = runtime.MemProfile(records, true)
	}
	var total int64
	for _, record := range records[:n] {
		frames := runtime.CallersFrames(record.Stack())
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".allocateSampledWorkload") {
				total += record.AllocBytes
				break
			}
			if !more {
				break
			}
		}
	}
	return total
}

// TestChildReport runs in the child process of TestSamplingFollowsTheRequest: 64 MiB of allocations from one function, then what the runtime
// sampled of them.
func TestChildReport(t *testing.T) {
	if os.Getenv(childEnv) == "" {
		t.Skip("runs as the child of TestSamplingFollowsTheRequest")
	}
	before := bucketsBytes()
	allocateSampledWorkload()
	sampled := sampledAllocBytes()
	fmt.Printf("REPORT rate=%d sampled=%d bucketsGrowth=%d\n", runtime.MemProfileRate, sampled, int64(bucketsBytes())-int64(before))
}

// Without a profile request the process samples nothing: the workload leaves no allocation record and the profiling-buckets class does not
// grow. PIG_PROFILE=heap or allocs, or the user's GODEBUG=memprofilerate, keeps the sampling and its records. The child runs on one P: a
// runtime P whose allocation cache saw the rate change takes one more sample (the cache of the P that allocated before the init, and every other
// P's armed countdown), so with several Ps a few samples of the workload are legitimate; with one, the test binary's own start-up has used it.
func TestSamplingFollowsTheRequest(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		profile     string
		godebug     string
		wantRate    int
		wantSamples bool
	}{
		{name: "no request", wantRate: 0},
		{name: "cpu profile", profile: "cpu,metrics", wantRate: 0},
		{name: "heap profile", profile: "heap", wantRate: DefaultRate, wantSamples: true},
		{name: "allocs profile", profile: "metrics,allocs", wantRate: DefaultRate, wantSamples: true},
		{name: "GODEBUG rate", godebug: "memprofilerate=65536", wantRate: 65536, wantSamples: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestChildReport$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), childEnv+"=1", "PIG_PROFILE="+tt.profile, "GODEBUG="+tt.godebug, "GOMAXPROCS=1")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child: %v\n%s", err, output)
			}
			var rate int
			var sampled, growth int64
			_, line, _ := bytes.Cut(output, []byte("REPORT "))
			if _, err := fmt.Sscanf(string(line), "rate=%d sampled=%d bucketsGrowth=%d", &rate, &sampled, &growth); err != nil {
				t.Fatalf("child report: %v\n%s", err, output)
			}
			if rate != tt.wantRate {
				t.Errorf("MemProfileRate = %d, want %d", rate, tt.wantRate)
			}
			if (sampled > 0) != tt.wantSamples {
				t.Errorf("sampled bytes of a 64 MiB workload = %d, want samples: %v", sampled, tt.wantSamples)
			}
			if !tt.wantSamples && growth > 64<<10 {
				t.Errorf("profiling-buckets class grew by %d bytes without sampling", growth)
			}
		})
	}
}
