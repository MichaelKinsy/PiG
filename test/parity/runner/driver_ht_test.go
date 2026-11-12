//go:build parity

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWaitForHTReadyUsesSharedUpstreamFooterMatcher(t *testing.T) {
	calls := 0
	view := func() (string, error) {
		calls++
		if calls == 1 {
			return "pi starting", nil
		}
		return "~/repo\n0.0%/0 (auto) unknown\n", nil
	}
	got, err := waitForHTReady(context.Background(), "$0.000", time.Second, view)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !paneMatchesReadyPattern(got, "$0.000") {
		t.Fatalf("calls=%d output=%q", calls, got)
	}
}

func TestWaitForHTReadyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForHTReady(ctx, "Ready", time.Second, func() (string, error) { return "", nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestHTFinalResult_KeepsTheWholeCapturesWhenTheCropFails(t *testing.T) {
	cfg := TmuxDriverConfig{CaptureStart: "BEGIN"}
	use := captureUse{plain: true, escaped: true}
	got := htFinalResult("scn", "pig", cfg, use, "banner\nbody\ncursor: 3,1", "esc banner\nbody\ncursor: 3,1", 42)
	if got.Err == nil || !strings.Contains(got.Err.Error(), `"BEGIN" not found`) || !strings.Contains(got.Err.Error(), "scn (pig)") {
		t.Fatalf("got %v, want the crop failure naming the scenario and the binary", got.Err)
	}
	if got.Output != "banner\nbody" || got.Escaped != "esc banner\nbody" {
		t.Fatalf("captures %q, %q: the uncropped captures must be kept for the artifacts", got.Output, got.Escaped)
	}
	if got.RuntimeMs != 42 || !got.ReadyOK {
		t.Fatalf("runtime %d ready %v: the run's measurements must survive the crop failure", got.RuntimeMs, got.ReadyOK)
	}
	root := t.TempDir()
	source := filepath.Join(root, "scenario.toml")
	if err := os.WriteFile(source, []byte("name = \"scn\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome := &ScenarioOutcome{
		Scenario: &Scenario{Name: "scn", Driver: "ht", SourcePath: source},
		Pig:      SystemResults{Runs: []Result{got}},
		Pi:       SystemResults{Runs: []Result{{Output: "pi", Escaped: "pi"}}},
		Failures: []string{"pig run error: " + got.Err.Error()},
	}
	dir, err := WriteFailureArtifacts(t, filepath.Join(root, "artifacts"), outcome, BinaryRef{Label: "pig", Path: "/tmp/pig"}, BinaryRef{Label: "pi", Path: "/tmp/pi"}, FreshnessReport{Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"pig.stdout": "banner\nbody", "pig.escaped": "esc banner\nbody"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != want {
			t.Fatalf("artifact %s = %q, %v; want the uncropped capture %q", name, data, err, want)
		}
	}
	ctxJSON, err := os.ReadFile(filepath.Join(dir, "context.json"))
	if err != nil || !strings.Contains(string(ctxJSON), "not found") {
		t.Fatalf("context.json lacks the crop error: %q, %v", ctxJSON, err)
	}
}

func TestHTFinalResult_CropsWhenTheMarkerIsThere(t *testing.T) {
	cfg := TmuxDriverConfig{CaptureStart: "BEGIN"}
	got := htFinalResult("scn", "pi", cfg, captureUse{plain: true}, "banner\nBEGIN\nbody\ncursor: 3,1", "esc", 1)
	if got.Err != nil || got.Output != "BEGIN\nbody" {
		t.Fatalf("output %q err %v, want the capture cropped to its start", got.Output, got.Err)
	}
	if !got.EscapedUncropped || got.PlainUncropped {
		t.Fatalf("labels plain=%v escaped=%v, want only the unasserted escaped capture labelled", got.PlainUncropped, got.EscapedUncropped)
	}
}
