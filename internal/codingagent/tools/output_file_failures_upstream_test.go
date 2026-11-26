package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/outputfiles"
)

// Ports packages/coding-agent/test/output-file-failures.test.ts from Pi main
// 943a99e3c2e1f119fcd633741363a200e0677999 and 8209929fb553e7a8c91dc0894569f9ec43dd201f.

func setTempDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
}

func setMissingTempDir(t *testing.T) {
	t.Helper()
	setTempDir(t, filepath.Join(t.TempDir(), "missing-temp-dir"))
}

// failingWriteCloser wraps a real *os.File and injects ENOSPC on Write or Close.
type failingWriteCloser struct {
	file      *os.File
	failWrite error
	failClose error
}

func (f *failingWriteCloser) Write(p []byte) (int, error) {
	if f.failWrite != nil {
		return 0, f.failWrite
	}
	return f.file.Write(p)
}

func (f *failingWriteCloser) Close() error {
	err := f.file.Close()
	if f.failClose != nil {
		return f.failClose
	}
	return err
}

func injectOutputFileFailure(t *testing.T, mode string) (code string, createdPaths *[]string) {
	t.Helper()
	var paths []string
	var mu sync.Mutex
	switch mode {
	case "open":
		setMissingTempDir(t)
		return "ENOENT", &paths
	case "write":
		dir := t.TempDir()
		setTempDir(t, dir)
		restore := outputfiles.SetStreamOpenerForTest(func(path string) (io.WriteCloser, bool, error) {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, outputfiles.Mode)
			if err != nil {
				return nil, false, err
			}
			mu.Lock()
			paths = append(paths, path)
			mu.Unlock()
			return &failingWriteCloser{
				file:      f,
				failWrite: &fs.PathError{Op: "write", Path: path, Err: syscall.ENOSPC},
			}, true, nil
		})
		t.Cleanup(restore)
		return "ENOSPC", &paths
	case "close":
		dir := t.TempDir()
		setTempDir(t, dir)
		restore := outputfiles.SetStreamOpenerForTest(func(path string) (io.WriteCloser, bool, error) {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, outputfiles.Mode)
			if err != nil {
				return nil, false, err
			}
			mu.Lock()
			paths = append(paths, path)
			mu.Unlock()
			return &failingWriteCloser{
				file:      f,
				failClose: &fs.PathError{Op: "close", Path: path, Err: syscall.ENOSPC},
			}, true, nil
		})
		t.Cleanup(restore)
		return "ENOSPC", &paths
	default:
		t.Fatalf("unknown failure mode %q", mode)
		return "", &paths
	}
}

func seqLines(count int) string {
	var b strings.Builder
	for i := 1; i <= count; i++ {
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
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

	t.Run("write failure removes partial file (pi-943a99e3c:887)", func(t *testing.T) {
		code, created := injectOutputFileFailure(t, "write")
		stream := outputfiles.OpenStream("pi-test", ".log")
		stream.WriteString("partial\n")
		stream.Flush()
		stream.WriteString("ignored after error\n")
		res := stream.Close()
		if !res.Failed() || res.Path != "" || !strings.Contains(res.Error, code) {
			t.Fatalf("Close() = %+v, want failed %s without path", res, code)
		}
		if len(*created) != 1 {
			t.Fatalf("created paths = %v, want 1", *created)
		}
		if _, err := os.Stat((*created)[0]); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial file %s still exists: err=%v", (*created)[0], err)
		}
	})

	t.Run("EEXIST leaves an existing file untouched (pi-943a99e3c:897)", func(t *testing.T) {
		dir := t.TempDir()
		setTempDir(t, dir)
		existing := filepath.Join(dir, "preexisting.log")
		if err := os.WriteFile(existing, []byte("keep-me"), 0o600); err != nil {
			t.Fatal(err)
		}
		restore := outputfiles.SetStreamOpenerForTest(func(_ string) (io.WriteCloser, bool, error) {
			f, err := os.OpenFile(existing, os.O_WRONLY|os.O_CREATE|os.O_EXCL, outputfiles.Mode)
			return f, err == nil, err
		})
		t.Cleanup(restore)

		stream := outputfiles.OpenStream("pi-test", ".log")
		stream.WriteString("overwrite attempt")
		res := stream.Close()
		if !res.Failed() || res.Path != "" || !strings.Contains(res.Error, "EEXIST") {
			t.Fatalf("Close() = %+v, want EEXIST failure", res)
		}
		data, err := os.ReadFile(existing)
		if err != nil || string(data) != "keep-me" {
			t.Fatalf("existing file = %q, %v; want untouched 'keep-me'", data, err)
		}
	})

	t.Run("close failure removes partial file and reports error (pi-8209929fb:475)", func(t *testing.T) {
		code, created := injectOutputFileFailure(t, "close")
		stream := outputfiles.OpenStream("pi-test", ".log")
		stream.WriteString("buffered\n")
		res := stream.Close()
		if !res.Failed() || res.Path != "" || !strings.Contains(res.Error, code) {
			t.Fatalf("Close() = %+v, want failed %s without path", res, code)
		}
		if len(*created) != 1 {
			t.Fatalf("created paths = %v, want 1", *created)
		}
		if _, err := os.Stat((*created)[0]); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file %s still exists after close failure: err=%v", (*created)[0], err)
		}
	})

	t.Run("ignores writes after close", func(t *testing.T) {
		setTempDir(t, t.TempDir())
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
	short3000 := seqLines(3000)
	longOutput := strings.Repeat("0123456789abcdef\n", 20000) // ~340 KB

	for _, mode := range []string{"open", "write"} {
		t.Run("keeps tail and reports full_output_error when temp file "+mode+" fails", func(t *testing.T) {
			code, created := injectOutputFileFailure(t, mode)
			tool := CreateBashTool(t.TempDir(), &BashToolOptions{
				Operations: fakeBashOps{fn: func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
					opts.OnData([]byte(longOutput))
					return BashOperationsResult{ExitCode: new(0)}, nil
				}},
			})
			res, err := tool.Execute(t.Context(), "call-1", json.RawMessage(`{"command":"seq 20000"}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError {
				t.Fatalf("expected non-error tool result, got IsError=true")
			}
			text := res.Content[0].(ai.TextContent).Text
			footerPattern := regexp.MustCompile(`\[Showing lines 18001-20000 of 20000\. Could not save the full output: .*` + code + `.*\]$`)
			if !footerPattern.MatchString(text) {
				t.Fatalf("text tail = %q, want match %s", text[max(0, len(text)-200):], footerPattern)
			}
			if !strings.Contains(text, "0123456789abcdef\n") {
				t.Fatalf("text did not retain output tail")
			}
			details, ok := res.Details.(*BashDetails)
			if !ok || details == nil {
				t.Fatalf("details = %T, want *BashDetails", res.Details)
			}
			if details.FullOutputPath != "" {
				t.Fatalf("FullOutputPath = %q, want empty", details.FullOutputPath)
			}
			if !strings.Contains(details.FullOutputError, code) {
				t.Fatalf("FullOutputError = %q, want %s", details.FullOutputError, code)
			}
			// H3: ToolResultDetails() maps FullOutputError onto *extension.BashToolDetails.
			extDetails, ok := details.ToolResultDetails().(*extension.BashToolDetails)
			if !ok || extDetails == nil || extDetails.FullOutputPath != "" || !strings.Contains(extDetails.FullOutputError, code) {
				t.Fatalf("ToolResultDetails() = %#v, want *extension.BashToolDetails with FullOutputError containing %s", details.ToolResultDetails(), code)
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
			if errStr, _ := structured["full_output_error"].(string); !strings.Contains(errStr, code) {
				t.Fatalf("structured full_output_error = %q, want %s", errStr, code)
			}
			if outStr, _ := structured["output"].(string); !strings.Contains(outStr, "bytes omitted") {
				t.Fatalf("structured output missing omission marker: %q", outStr[:min(len(outStr), 120)])
			}
			for _, p := range *created {
				if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial output file %s not removed", p)
				}
			}
		})
	}

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
	short3000 := seqLines(3000)

	for _, mode := range []string{"open", "write"} {
		t.Run("keeps output tail and reports fullOutputError when temp file "+mode+" fails", func(t *testing.T) {
			code, created := injectOutputFileFailure(t, mode)
			res, err := ExecuteBashWithOperations(t.Context(), "seq 3000", t.TempDir(), fakeBashOps{
				fn: func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
					opts.OnData([]byte(short3000))
					return BashOperationsResult{ExitCode: new(0)}, nil
				},
			}, BashExecOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Truncated || res.FullOutputPath != "" || !strings.Contains(res.FullOutputError, code) {
				t.Fatalf("result = %+v, want truncated with empty path and %s error", res, code)
			}
			if !strings.HasSuffix(res.Output, "\n2999\n3000\n") && !strings.HasSuffix(res.Output, "\n2999\n3000") {
				t.Fatalf("output tail = %q, want ending with 2999/3000", res.Output[max(0, len(res.Output)-30):])
			}
			for _, p := range *created {
				if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial file %s not removed", p)
				}
			}
		})
	}

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

	// Pi 8209929fb:618: cancellation and late output arriving while closing the temp file must not alter the result.
	t.Run("decides the result when the command ends, not after the file is closed", func(t *testing.T) {
		dir := t.TempDir()
		setTempDir(t, dir)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		longData := strings.Repeat("line\n", 15000) // >50KB so temp file opens
		var lateOnData func([]byte)
		// Hook stream Close so cancel() and lateOnData() fire during file close.
		restore := outputfiles.SetStreamOpenerForTest(func(path string) (io.WriteCloser, bool, error) {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, outputfiles.Mode)
			if err != nil {
				return nil, false, err
			}
			return &closeHookFile{
				File: f,
				onClose: func() {
					cancel()
					if lateOnData != nil {
						lateOnData([]byte("late output\n"))
					}
				},
			}, true, nil
		})
		t.Cleanup(restore)

		res, err := ExecuteBashWithOperations(ctx, "seq 15000", t.TempDir(), fakeBashOps{
			fn: func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
				opts.OnData([]byte(longData))
				lateOnData = opts.OnData
				return BashOperationsResult{ExitCode: new(7)}, nil
			},
		}, BashExecOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if res.FullOutputPath != "" {
			t.Cleanup(func() { _ = os.Remove(res.FullOutputPath) })
		}
		if res.Cancelled || res.ExitCode == nil || *res.ExitCode != 7 || !res.Truncated {
			t.Fatalf("result = %+v, want exitCode=7 cancelled=false truncated=true", res)
		}
		if strings.Contains(res.Output, "late output") {
			t.Fatalf("late output leaked into result.Output")
		}
		saved, err := os.ReadFile(res.FullOutputPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(saved) != longData {
			t.Fatalf("saved file len = %d, want %d (no late output)", len(saved), len(longData))
		}
	})
}

type closeHookFile struct {
	*os.File
	onClose func()
}

func (c *closeHookFile) Close() error {
	if c.onClose != nil {
		c.onClose()
	}
	return c.File.Close()
}
