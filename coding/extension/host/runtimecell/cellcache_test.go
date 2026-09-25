package runtimecell

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// helperEnv keys let a re-exec'd copy of the test binary act as a competing Pig
// process building the same cell. The cross-process shape matters: the bug is a
// shared deterministic path across independent processes, which in-process
// goroutines would not reproduce faithfully.
const (
	envHelper  = "CELLCACHE_HELPER"
	envFinal   = "CELLCACHE_FINAL"
	envDigest  = "CELLCACHE_DIGEST"
	envCounter = "CELLCACHE_COUNTER"
	envResult  = "CELLCACHE_RESULT"
)

// TestCellcacheHelperProcess is not a real test: when CELLCACHE_HELPER is set it
// runs one PublishArtifact call and exits, standing in for a parallel Pig
// process. The build func appends one byte to a shared counter file, so counting
// bytes afterward reveals how many processes actually built (must be exactly 1).
func TestCellcacheHelperProcess(t *testing.T) {
	if os.Getenv(envHelper) != "1" {
		t.Skip("helper process entry point")
	}
	finalDir := os.Getenv(envFinal)
	digest := os.Getenv(envDigest)
	counter := os.Getenv(envCounter)

	entry, err := PublishArtifact(context.Background(), finalDir, "runner", digest, "go", func(scratch string) (string, error) {
		f, ferr := os.OpenFile(counter, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if ferr != nil {
			return "", ferr
		}
		_, _ = f.Write([]byte("x"))
		_ = f.Close()
		time.Sleep(150 * time.Millisecond) // widen the race window
		out := filepath.Join(scratch, "runner")
		if werr := os.WriteFile(out, []byte("BUILT-ARTIFACT-BYTES"), 0o755); werr != nil {
			return "", werr
		}
		return out, nil
	})
	// Report through a private file, not stdout: the test framework writes its
	// own PASS/ok lines to stdout, which would corrupt a stdout-based result.
	if err != nil {
		_ = os.WriteFile(os.Getenv(envResult), []byte("ERR:"+err.Error()), 0o644)
		os.Exit(1)
	}
	_ = os.WriteFile(os.Getenv(envResult), []byte(entry.ArtifactPath), 0o644)
}

func TestPublishArtifactDedupesAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	finalDir := filepath.Join(root, "cells", "go", "deadbeefdeadbeef")
	counter := filepath.Join(root, "build-counter")
	digest := "deadbeefdeadbeef"

	const procs = 8
	var wg sync.WaitGroup
	results := make([]string, procs)
	for i := range procs {
		wg.Go(func() {
			resultFile := filepath.Join(root, "result-"+string(rune('0'+i)))
			cmd := exec.Command(os.Args[0], "-test.run=TestCellcacheHelperProcess")
			cmd.Env = append(os.Environ(),
				envHelper+"=1",
				envFinal+"="+finalDir,
				envDigest+"="+digest,
				envCounter+"="+counter,
				envResult+"="+resultFile,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("helper %d failed: %v\n%s", i, err, out)
				return
			}
			data, err := os.ReadFile(resultFile)
			if err != nil {
				t.Errorf("helper %d wrote no result: %v", i, err)
				return
			}
			results[i] = string(data)
		})
	}
	wg.Wait()

	// Exactly one process built the cell; the rest reused it.
	built, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if got := len(built); got != 1 {
		t.Fatalf("expected exactly 1 build across %d processes, got %d", procs, got)
	}

	// Every process resolved to the same published artifact.
	want := filepath.Join(finalDir, "runner")
	for i, r := range results {
		if r != want {
			t.Errorf("process %d artifact = %q, want %q", i, r, want)
		}
	}

	// The published entry is valid and keeps immutable readiness metadata
	// separate from mutable successful-use metadata.
	if _, ok := validCellEntry(finalDir, EntryIdentity{InputDigest: digest, Artifact: "runner", Language: "go"}); !ok {
		t.Fatal("published entry is not valid")
	}
	names := dirNames(t, finalDir)
	if len(names) != 3 || !contains(names, "runner") || !contains(names, cellReadyFile) || !contains(names, usageFile) {
		t.Fatalf("entry should hold {runner, ready.json, usage.json}, got %v", names)
	}
}

func TestPublishArtifactCopiesExternalCompilerOutput(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "compiled-runner")
	if err := os.WriteFile(external, []byte("external artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := PublishArtifact(context.Background(), filepath.Join(root, "cells", "rust", "external"), "runner", "external", "rust", func(string) (string, error) {
		return external, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external compiler cache artifact was removed: %v", err)
	}
	data, err := os.ReadFile(entry.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external artifact" {
		t.Fatalf("published artifact = %q", data)
	}
}

func TestPublishArtifactRejectsIncompleteEntry(t *testing.T) {
	root := t.TempDir()
	finalDir := filepath.Join(root, "cells", "go", "abc123")
	// Simulate a legacy/crashed entry: a binary with no readiness marker.
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, "runner"), []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := validCellEntry(finalDir, EntryIdentity{InputDigest: "abc123", Artifact: "runner", Language: "go"}); ok {
		t.Fatal("entry without ready.json must be treated as invalid")
	}
	built := false
	entry, err := PublishArtifact(context.Background(), finalDir, "runner", "abc123", "go", func(scratch string) (string, error) {
		built = true
		out := filepath.Join(scratch, "runner")
		return out, os.WriteFile(out, []byte("fresh"), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Fatal("expected a rebuild over the incomplete entry")
	}
	data, _ := os.ReadFile(entry.ArtifactPath)
	if string(data) != "fresh" {
		t.Fatalf("stale artifact was not replaced: %q", data)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

func TestGCStaleScratchRemovesOnlyAbandoned(t *testing.T) {
	parent := t.TempDir()
	stale := filepath.Join(parent, ".build-stale")
	fresh := filepath.Join(parent, ".build-fresh")
	entry := filepath.Join(parent, "name-hash") // a real published entry, not scratch
	for _, d := range []string{stale, fresh, entry} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-staleScratchAge - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	gcStaleScratch(parent)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("aged scratch not reclaimed: err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh scratch wrongly reclaimed: %v", err)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Errorf("published entry wrongly reclaimed: %v", err)
	}
}

func TestPublishArtifactCacheHitUpdatesSuccessfulUse(t *testing.T) {
	root := t.TempDir()
	entryDir := filepath.Join(root, "cells", "go", "use-update")
	_, err := PublishArtifact(context.Background(), entryDir, "runner", "use-update", "go", func(scratch string) (string, error) {
		artifact := filepath.Join(scratch, "runner")
		return artifact, os.WriteFile(artifact, []byte("artifact"), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	data, _ := json.Marshal(UsageMetadata{LastUsed: old.Unix()})
	if err := os.WriteFile(filepath.Join(entryDir, usageFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishArtifact(context.Background(), entryDir, "runner", "use-update", "go", func(string) (string, error) {
		t.Fatal("valid cache hit rebuilt artifact")
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(entryDir, usageFile))
	if err != nil {
		t.Fatal(err)
	}
	var usage UsageMetadata
	if err := json.Unmarshal(data, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.LastUsed <= old.Unix() {
		t.Fatalf("cache hit last use = %d, want after %d", usage.LastUsed, old.Unix())
	}
}
