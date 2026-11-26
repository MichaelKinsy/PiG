package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/outputfiles"
)

// Ports packages/coding-agent/test/output-file-failures.test.ts from Pi main
// 943a99e3c2e1f119fcd633741363a200e0677999 and 8209929fb553e7a8c91dc0894569f9ec43dd201f.

func setMissingTempDir(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing-temp-dir")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
}

func TestOutputFileStreamFailuresUpstream(t *testing.T) {
	t.Run("reports open failure without panicking and ignores later writes", func(t *testing.T) {
		setMissingTempDir(t)
		stream := outputfiles.OpenStream("pi-test", ".log")
		stream.Write([]byte("hello\n"))
		stream.WriteString("world\n")
		res := stream.Close()
		if !res.Failed() || res.Path != "" || !strings.Contains(res.Error, "ENOENT") {
			t.Fatalf("Close() = %+v, want failed ENOENT without path", res)
		}
		if got := stream.Result(); got != res {
			t.Fatalf("Result() = %+v, want %+v", got, res)
		}
	})

	t.Run("ignores writes after close", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("TMPDIR", dir)
		t.Setenv("TMP", dir)
		t.Setenv("TEMP", dir)
		stream := outputfiles.OpenStream("pi-test", ".log")
		stream.WriteString("before\n")
		res := stream.Close()
		if !res.Saved() {
			t.Fatalf("Close() = %+v, want saved", res)
		}
		t.Cleanup(func() { _ = os.Remove(res.Path) })
		stream.WriteString("after\n")
		data, err := os.ReadFile(res.Path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "before\n" {
			t.Fatalf("file content = %q, want %q", data, "before\n")
		}
	})
}

type fakeBashOps struct {
	fn func(ctx context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error)
}

func (f fakeBashOps) Exec(ctx context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	return f.fn(ctx, command, cwd, opts)
}

func TestBashToolOutputFileFailuresUpstream(t *testing.T) {
	var shortBuilder strings.Builder
	for i := 1; i <= 3000; i++ {
		shortBuilder.WriteString("line\n")
	}
	short3000 := shortBuilder.String()
	longOutput := strings.Repeat("0123456789abcdef\n", 20000) // ~170 KB

	t.Run("keeps in-memory tail and reports full_output_error when temp file cannot open", func(t *testing.T) {
		setMissingTempDir(t)
		tool := CreateBashTool(t.TempDir(), &BashToolOptions{
			Operations: fakeBashOps{fn: func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
				opts.OnData([]byte(longOutput))
				return BashOperationsResult{ExitCode: new(0)}, nil
			}},
		})
		res, err := tool.Execute(t.Context(), "call-1", json.RawMessage(`{"command":"seq 10000"}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("expected non-error tool result, got IsError=true")
		}
		text := res.Content[0].(ai.TextContent).Text
		if !strings.Contains(text, "Could not save the full output:") || !strings.Contains(text, "ENOENT") {
			t.Fatalf("text = %q, want Could not save the full output with ENOENT", text)
		}
		details, ok := res.Details.(*BashDetails)
		if !ok || details == nil {
			t.Fatalf("details = %T, want *BashDetails", res.Details)
		}
		if details.FullOutputPath != "" {
			t.Fatalf("FullOutputPath = %q, want empty", details.FullOutputPath)
		}
		if !strings.Contains(details.FullOutputError, "ENOENT") {
			t.Fatalf("FullOutputError = %q, want ENOENT", details.FullOutputError)
		}
		var structured map[string]any
		if err := json.Unmarshal(res.StructuredContent, &structured); err != nil {
			t.Fatal(err)
		}
		if structured["truncated"] != true {
			t.Fatalf("structured truncated = %v, want true", structured["truncated"])
		}
		if _, hasPath := structured["full_output_path"]; hasPath {
			t.Fatalf("structuredContent unexpectedly has full_output_path: %v", structured)
		}
		if errStr, _ := structured["full_output_error"].(string); !strings.Contains(errStr, "ENOENT") {
			t.Fatalf("structured full_output_error = %q, want ENOENT", errStr)
		}
		if outStr, _ := structured["output"].(string); !strings.Contains(outStr, "bytes omitted") {
			t.Fatalf("structured output missing omission marker: %q", outStr[:min(len(outStr), 120)])
		}
	})

	t.Run("codemode fallback returns plain output without omission marker when all 3000 short lines fit in memory", func(t *testing.T) {
		setMissingTempDir(t)
		acc := NewOutputAccumulator("pi-bash")
		if err := acc.Append([]byte(short3000)); err != nil {
			t.Fatal(err)
		}
		acc.Finish()
		_ = acc.CloseTempFile()
		full, err := acc.ReadFullOutput(structuredOutputMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		if full.Truncated {
			t.Fatalf("ReadFullOutput.Truncated = true, want false when all bytes retained")
		}
		if full.Content != short3000 {
			t.Fatalf("ReadFullOutput.Content len = %d, want %d", len(full.Content), len(short3000))
		}
	})

	t.Run("codemode fallback keeps tail of one 300000-byte line when temp file fails", func(t *testing.T) {
		setMissingTempDir(t)
		oneLongLine := strings.Repeat("a", 300_000) + "END\n"
		acc := NewOutputAccumulator("pi-bash")
		if err := acc.Append([]byte(oneLongLine)); err != nil {
			t.Fatal(err)
		}
		acc.Finish()
		_ = acc.CloseTempFile()
		full, err := acc.ReadFullOutput(structuredOutputMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		if !full.Truncated {
			t.Fatal("ReadFullOutput.Truncated = false, want true")
		}
		if !strings.Contains(full.Content, "bytes omitted") || !strings.HasSuffix(full.Content, "END\n") {
			t.Fatalf("ReadFullOutput.Content did not retain end of long line: tail=%q", full.Content[max(0, len(full.Content)-40):])
		}
	})
}

func TestUserBashExecutorOutputFileFailuresUpstream(t *testing.T) {
	t.Run("keeps output tail and reports fullOutputError when temp file fails to open", func(t *testing.T) {
		setMissingTempDir(t)
		short3000 := strings.Repeat("line\n", 3000)
		res, err := ExecuteBashWithOperations(t.Context(), "seq 3000", t.TempDir(), fakeBashOps{
			fn: func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
				opts.OnData([]byte(short3000))
				return BashOperationsResult{ExitCode: new(0)}, nil
			},
		}, BashExecOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Truncated || res.FullOutputPath != "" || !strings.Contains(res.FullOutputError, "ENOENT") {
			t.Fatalf("result = %+v, want truncated with empty path and ENOENT error", res)
		}
	})

	t.Run("reports no failure for ANSI-only output that is not truncated after stripping", func(t *testing.T) {
		setMissingTempDir(t)
		colored := strings.Repeat("\x1b[31m\x1b[0m", 7000) + "done\n"
		res, err := ExecuteBashWithOperations(t.Context(), "build", t.TempDir(), fakeBashOps{
			fn: func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
				opts.OnData([]byte(colored))
				return BashOperationsResult{ExitCode: new(0)}, nil
			},
		}, BashExecOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Truncated || res.FullOutputError != "" {
			t.Fatalf("result = %+v, want not truncated and empty FullOutputError", res)
		}
		if res.Output != "done\n" {
			t.Fatalf("output = %q, want %q", res.Output, "done\n")
		}
	})
}
