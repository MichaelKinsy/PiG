package subprocess

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

func TestPythonStageCellHoldsSDKLeaseThroughStartup(t *testing.T) {
	configRoot := t.TempDir()
	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	host := NewHostWithConfigRoot(t.TempDir(), configRoot)
	_, err = host.stageCell(ctx, CellSpec{Language: "python", Strategy: "invalid"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stage cell error = %v, want SDK lease wait", err)
	}
}

func TestGoStageCellDoesNotHoldSDKLeaseThroughStartup(t *testing.T) {
	configRoot := t.TempDir()
	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	host := NewHostWithConfigRoot(t.TempDir(), configRoot)
	_, err = host.stageCell(context.Background(), CellSpec{Language: "go", Strategy: "invalid"}, nil)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stage cell error = %v, want immediate strategy error", err)
	}
}
