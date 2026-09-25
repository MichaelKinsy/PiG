package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func TestExtensionsCacheStatsAndPruneUseManagedRootsOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	entry := filepath.Join(home, "cache", "ext", "fixture-hash")
	published, err := runtimecell.PublishArtifact(context.Background(), entry, "bin", "hash", "go", func(scratch string) (string, error) {
		artifact := filepath.Join(scratch, "bin")
		return artifact, os.WriteFile(artifact, []byte("artifact"), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	usagePath := filepath.Join(published.Dir, "usage.json")
	usageBefore, err := os.ReadFile(usagePath)
	if err != nil {
		t.Fatal(err)
	}
	usageInfoBefore, err := os.Stat(usagePath)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runExtensionsCommand([]string{"extensions", "cache", "stats", "--json"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("stats code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var stats runtimecell.CacheReport
	if err := json.Unmarshal([]byte(stdout), &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.Entries) != 1 || stats.Entries[0].Path != published.Dir {
		t.Fatalf("stats = %#v", stats)
	}
	usageAfter, _ := os.ReadFile(usagePath)
	usageInfoAfter, _ := os.Stat(usagePath)
	if string(usageAfter) != string(usageBefore) || !usageInfoAfter.ModTime().Equal(usageInfoBefore.ModTime()) {
		t.Fatal("cache inspection updated successful-use metadata")
	}

	stdout, stderr, code = captureStdoutStderr(t, func() int {
		return runExtensionsCommand([]string{"extensions", "cache", "prune", "--retention", "1ns", "--dry-run", "--json"})
	})
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"removed":1`) {
		t.Fatalf("dry-run code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("dry-run removed entry: %v", err)
	}

	stdout, stderr, code = captureStdoutStderr(t, func() int {
		return runExtensionsCommand([]string{"extensions", "cache", "prune", "--retention", "1ns", "--json"})
	})
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"removed":1`) {
		t.Fatalf("prune code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("wet prune retained entry: %v", err)
	}
}

func TestParseCacheSize(t *testing.T) {
	for input, want := range map[string]int64{"1KiB": 1024, "2MB": 2_000_000, "17": 17} {
		got, err := parseCacheSize(input)
		if err != nil || got != want {
			t.Errorf("parseCacheSize(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"-1", "nope", "999999999999999999999GB"} {
		if _, err := parseCacheSize(input); err == nil {
			t.Errorf("parseCacheSize(%q) succeeded", input)
		}
	}
}

func TestTouchUsageThrottlesWritesAndFutureTimestamps(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(2_000_000_000, 0)
	if err := runtimecell.TouchUsage(root, now); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "usage.json")
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimecell.TouchUsage(root, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	second, _ := os.Stat(path)
	if !second.ModTime().Equal(first.ModTime()) {
		t.Fatal("usage write was not throttled")
	}
	if err := runtimecell.TouchUsage(root, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	third, _ := os.Stat(path)
	if !third.ModTime().Equal(first.ModTime()) {
		t.Fatal("future usage timestamp was overwritten")
	}
}

func TestCachePruneFailsClosedWhenCurrentRootsDoNotResolve(t *testing.T) {
	err := requireCompleteCurrentCacheRoots([]error{errors.New("source hash failed")})
	if err == nil || !strings.Contains(err.Error(), "cache was not pruned") {
		t.Fatalf("guard error = %v", err)
	}
}

// Startup's daily cache collection never waits for another process's
// collection, and a fresh collection marker costs no lock at all. The old code
// took the lock with a blocking wait before reading the marker, on both
// startup extension passes.
func TestAutomaticExtensionCacheGCNeverWaitsForAnotherCollector(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	cacheRoot := filepath.Join(home, "cache")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	holder := flock.New(filepath.Join(cacheRoot, ".auto-gc.lock"))
	if locked, err := holder.TryLock(); err != nil || !locked {
		t.Fatalf("hold collector lock: %v %v", locked, err)
	}
	defer func() { _ = holder.Unlock() }()

	marker := filepath.Join(cacheRoot, ".last-auto-gc")
	collect := func() error {
		done := make(chan error, 1)
		go func() { done <- runAutomaticExtensionCacheGC(nil) }()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("automatic cache collection waited for another collector's lock")
			return nil
		}
	}

	// Due: another process holds the lock, so it is collecting; skip.
	if err := collect(); err != nil {
		t.Fatalf("collection while another collector runs: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("skipped collection wrote the marker: %v", err)
	}

	// Not due: return without touching the lock.
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := collect(); err != nil {
		t.Fatalf("collection with a fresh marker: %v", err)
	}
}
