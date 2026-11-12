// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/profiling"
)

// pig additive (D627): a process that runs the pig command samples no heap allocations unless it asked for a heap profile. The cli package
// imports internal/memprofrate for this; the test fails if the import goes.
func TestCommandProcessSamplesNoAllocations(t *testing.T) {
	if profile := flag.Lookup("test.memprofile"); profile != nil && profile.Value.String() != "" {
		t.Skip("go test -memprofile sets the rate itself")
	}
	if strings.Contains(os.Getenv("GODEBUG"), "memprofilerate") || strings.Contains(os.Getenv("PIG_PROFILE"), "heap") || strings.Contains(os.Getenv("PIG_PROFILE"), "allocs") {
		t.Skip("the environment asks for heap-profile sampling")
	}
	if runtime.MemProfileRate != 0 {
		t.Fatalf("runtime.MemProfileRate = %d, want 0", runtime.MemProfileRate)
	}
}

const memProfChildEnv = "PIG_MEMPROFRATE_CLI_CHILD"

//go:noinline
func allocateMemProfWorkload() (kept [][]byte) {
	for range 512 {
		kept = append(kept, make([]byte, 128<<10))
	}
	return kept
}

// TestMemProfChild runs as the child of TestMemProfRateFollowsTheSwitches: the cli package's own startup (its imports' inits, the same ones the
// pig binary runs) has set the rate; it reports the rate, takes a profile through the PIG_PROFILE path and reports what the heap profile holds.
func TestMemProfChild(t *testing.T) {
	if os.Getenv(memProfChildEnv) == "" {
		t.Skip("runs as the child of TestMemProfRateFollowsTheSwitches")
	}
	rate := runtime.MemProfileRate
	stop := profiling.Start()
	kept := allocateMemProfWorkload()
	stop()
	runtime.KeepAlive(kept)
	var size, named int
	matches, _ := filepath.Glob(filepath.Join(os.Getenv("PIG_PROFILE_DIR"), "*-heap.pprof"))
	for _, path := range matches {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		zr, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(zr)
		_ = file.Close()
		size = len(raw)
		if bytes.Contains(raw, []byte("allocateMemProfWorkload")) {
			named = 1
		}
	}
	fmt.Printf("REPORT rate=%d files=%d size=%d named=%d\n", rate, len(matches), size, named)
}

// pig additive (D627): the startup path of the command's process sets the rate from PIG_PROFILE and GODEBUG, and a heap profile taken with
// PIG_PROFILE=heap holds the samples of the allocations made.
func TestMemProfRateFollowsTheSwitches(t *testing.T) {
	if flag.Lookup("test.memprofile").Value.String() != "" {
		t.Skip("go test -memprofile sets the rate itself")
	}
	if strings.Contains(os.Getenv("GODEBUG"), "memprofilerate") {
		t.Skip("the environment sets the rate")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, profile, godebug string
		wantRate               int
		wantProfile            bool
	}{
		{name: "default", wantRate: 0},
		{name: "cpu", profile: "cpu", wantRate: 0},
		{name: "heap", profile: "heap", wantRate: 512 * 1024, wantProfile: true},
		{name: "allocs", profile: "allocs", wantRate: 512 * 1024},
		{name: "GODEBUG", godebug: "memprofilerate=65536", wantRate: 65536},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestMemProfChild$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), memProfChildEnv+"=1", "PIG_PROFILE="+tt.profile, "GODEBUG="+tt.godebug, "PIG_PROFILE_DIR="+t.TempDir(), "GOMAXPROCS=1")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child: %v\n%s", err, output)
			}
			var rate, files, size, named int
			_, line, _ := bytes.Cut(output, []byte("REPORT "))
			if _, err := fmt.Sscanf(string(line), "rate=%d files=%d size=%d named=%d", &rate, &files, &size, &named); err != nil {
				t.Fatalf("child report: %v\n%s", err, output)
			}
			if rate != tt.wantRate {
				t.Errorf("MemProfileRate = %d, want %d", rate, tt.wantRate)
			}
			if tt.wantProfile && (files != 1 || named != 1) {
				t.Errorf("heap profile: %d files, %d bytes, workload frame present %d; want one file naming the workload", files, size, named)
			}
		})
	}
}
