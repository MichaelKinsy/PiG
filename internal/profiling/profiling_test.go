// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package profiling

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartWithoutProfileIsNoop(t *testing.T) {
	t.Setenv("PIG_PROFILE", "")
	dir := t.TempDir()
	t.Chdir(dir)
	Start()()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("PIG_PROFILE unset wrote %d files", len(entries))
	}
}

func TestStartWithWritesEveryRequestedKind(t *testing.T) {
	dir := t.TempDir()
	var diag bytes.Buffer
	stop := StartWith("cpu,heap,allocs,block,mutex,goroutine,trace,bogus", dir, &diag)
	sink := 0
	for i := range 200000 {
		sink += len(strings.Repeat("x", i%7))
	}
	stop()
	stop() // A second call must not write again or panic.
	if !strings.Contains(diag.String(), `unknown PIG_PROFILE kind "bogus"`) {
		t.Errorf("unknown kind was not reported: %q", diag.String())
	}
	for _, suffix := range []string{"-cpu.pprof", "-heap.pprof", "-allocs.pprof", "-block.pprof", "-mutex.pprof", "-goroutine.pprof", ".trace"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "pig-*"+suffix))
		if len(matches) != 1 {
			t.Errorf("want one %s file, got %v", suffix, matches)
			continue
		}
		if info, err := os.Stat(matches[0]); err != nil || info.Size() == 0 {
			t.Errorf("%s is empty or missing: %v", matches[0], err)
		}
	}
	if got := strings.Count(diag.String(), "pig: wrote "); got != 7 {
		t.Errorf("reported %d written files, want 7: %s", got, diag.String())
	}
	_ = sink
}

func BenchmarkStartDisabled(b *testing.B) {
	b.Setenv("PIG_PROFILE", "")
	for b.Loop() {
		Start()()
	}
}
