//go:build !pig_strip_extension_sdk_go || !pig_strip_extension_sdk_python || !pig_strip_extension_sdk_rust || !pig_strip_extension_sdk_lisp

package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// These two tests need an extension SDK to stage: a Binary built with all four of the Go, Python, Rust and Lisp SDKs stripped has none, so no SDK is
// unstaged for diagnose to name and no staging fails for the startup to warn about. Any one of the four brings them back.

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

// `pig diagnose` names when unstaged SDKs are staged.
func TestDiagnoseSaysWhenSDKsStage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	stages, err := pigsdk.Status(home)
	if err != nil || len(stages) == 0 || stages[0].Current {
		t.Fatalf("a fresh config root reports %+v (err %v)", stages, err)
	}
	var out strings.Builder
	runDiagnose(&out, "")
	if !strings.Contains(out.String(), "not staged: stages when an extension loads") {
		t.Fatalf("diagnose output:\n%s", out.String())
	}
}
