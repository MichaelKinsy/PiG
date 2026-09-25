// tests for the bash executor.
package codingagent

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func defaultShell(t *testing.T) tools.ShellConfig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash executor tests assume POSIX shell")
	}
	cfg, err := tools.GetShellConfig(nil)
	if err != nil {
		t.Fatalf("resolve shell: %v", err)
	}
	return cfg
}

func TestExecuteBash_HappyPath(t *testing.T) {
	shell := defaultShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ExecuteBash(ctx, "echo hello", t.TempDir(), shell, BashExecOptions{})
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Errorf("Output = %q, want contains `hello`", res.Output)
	}
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Errorf("ExitCode = %v, want 0", res.ExitCode)
	}
	if res.Cancelled {
		t.Error("Cancelled = true, want false")
	}
}

func TestExecuteBash_NonZeroExit(t *testing.T) {
	shell := defaultShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ExecuteBash(ctx, "exit 7", t.TempDir(), shell, BashExecOptions{})
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if res.ExitCode == nil || *res.ExitCode != 7 {
		t.Errorf("ExitCode = %v, want 7", res.ExitCode)
	}
}

func TestExecuteBash_StreamingChunks(t *testing.T) {
	shell := defaultShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var chunks []string
	res, err := ExecuteBash(ctx, "printf 'a'; printf 'b'", t.TempDir(), shell, BashExecOptions{
		OnChunk: func(c string) { chunks = append(chunks, c) },
	})
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if !strings.Contains(res.Output, "ab") {
		t.Errorf("Output = %q, want contains `ab`", res.Output)
	}
	if len(chunks) == 0 {
		t.Error("expected at least one streamed chunk")
	}
}

func TestExecuteBash_CancelledByContext(t *testing.T) {
	shell := defaultShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	res, err := ExecuteBash(ctx, "sleep 5", t.TempDir(), shell, BashExecOptions{})
	if err != nil {
		t.Fatalf("ExecuteBash returned error after cancel: %v", err)
	}
	if !res.Cancelled {
		t.Error("expected Cancelled=true when context cancelled mid-run")
	}
	if res.ExitCode != nil {
		t.Errorf("expected ExitCode=nil after cancel, got %v", *res.ExitCode)
	}
}

func TestExecuteBash_StripsANSI(t *testing.T) {
	shell := defaultShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ExecuteBash(ctx, "printf '\\033[31mred\\033[0m'", t.TempDir(), shell, BashExecOptions{})
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if strings.Contains(res.Output, "\033[") {
		t.Errorf("Output should have ANSI stripped, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "red") {
		t.Errorf("Output = %q, want contains `red`", res.Output)
	}
}

func TestExecuteBash_TruncationPersistsFullOutputFile(t *testing.T) {
	shell := defaultShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := `i=0; while [ $i -lt 2505 ]; do printf "line-%04d\n" $i; i=$((i+1)); done`
	res, err := ExecuteBash(ctx, cmd, t.TempDir(), shell, BashExecOptions{})
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated output")
	}
	if res.FullOutputPath == "" {
		t.Fatal("expected FullOutputPath for truncated output")
	}
	data, err := os.ReadFile(res.FullOutputPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", res.FullOutputPath, err)
	}
	full := string(data)
	if !strings.Contains(full, "line-0000") {
		t.Fatalf("full output missing first line")
	}
	if !strings.Contains(full, "line-2504") {
		t.Fatalf("full output missing last line")
	}
	if strings.Contains(res.Output, "line-0000") {
		t.Fatalf("truncated output unexpectedly contains first line")
	}
}
