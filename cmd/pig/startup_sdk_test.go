package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

func TestStartupSDKStageLockWaitIsBoundedAndVisible(t *testing.T) {
	configRoot := t.TempDir()
	release, err := pigsdklock.AcquireBuild(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	var stderr bytes.Buffer
	start := time.Now()
	stageExtensionSDKsAtStartup(context.Background(), configRoot, &stderr, 25*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("startup SDK staging returned after %s, want bounded wait", elapsed)
	}
	output := stderr.String()
	lockPath := filepath.Join(configRoot, "state", "pigsdk", ".transaction.lock")
	for _, want := range []string{"extension SDK staging failed", lockPath, "startup will continue"} {
		if !strings.Contains(output, want) {
			t.Fatalf("startup warning %q does not contain %q", output, want)
		}
	}
}

func BenchmarkStartupSDKStageWarm(b *testing.B) {
	configRoot := b.TempDir()
	if err := pigsdk.EnsureSynced(configRoot); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		stageExtensionSDKsAtStartup(context.Background(), configRoot, io.Discard, startupSDKLockTimeout)
	}
}
