package tools

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
	builtin "github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func fakeShellOutput(text string, options *harness.ShellExecOptions, spill string) harness.ShellExecResult {
	limits := harness.ShellOutputLimits{MaxBytes: builtin.DefaultMaxBytesUpstream, MaxLines: builtin.DefaultMaxLinesUpstream}
	if options.Capture != nil {
		limits = options.Capture.Limits
	}
	tr := builtin.TruncateTail(text, limits.MaxBytes, limits.MaxLines)
	var by *string
	if tr.TruncatedBy != "" {
		by = new(tr.TruncatedBy)
	}
	metadata := harness.ShellOutputMetadata{SpillPath: spill, Truncation: harness.ShellOutputTruncation{Truncated: tr.Truncated, TruncatedBy: by, TotalLines: tr.TotalLines, TotalBytes: tr.TotalBytes, OutputLines: tr.OutputLines, OutputBytes: tr.OutputBytes, LastLinePartial: tr.LastLinePartial, FirstLineExceedsLimit: tr.FirstLineExceedsLimit, MaxLines: tr.MaxLines, MaxBytes: tr.MaxBytes}}
	if options.OnUpdate != nil {
		if err := options.OnUpdate(context.Background(), harness.ShellOutputUpdate{Kind: harness.ShellOutputUpdateReplace, Output: harness.ShellOutputView{Text: tr.Content, ShellOutputMetadata: metadata}}); err != nil {
			panic(err)
		}
	}
	return harness.ShellExecResult{ExitCode: 0, ShellOutputMetadata: metadata}
}

type timeoutOutputEnv struct{ *envpkg.NodeExecutionEnv }

func (env *timeoutOutputEnv) Exec(ctx context.Context, _ string, options *harness.ShellExecOptions) (harness.ShellExecResult, error) {
	output := lines(builtin.DefaultMaxLinesUpstream+1, "line-%d") + "\n"
	path := filepath.Join(env.Cwd(), "timeout.log")
	if err := env.WriteFile(ctx, path, []byte(output)); err != nil {
		return harness.ShellExecResult{}, err
	}
	fakeShellOutput(output, options, path)
	return harness.ShellExecResult{}, &harness.ExecutionError{Code: harness.ExecutionErrorTimeout, Message: "timeout:" + builtin.FormatJSNumber(*options.Timeout)}
}

type lateOutputEnv struct {
	*envpkg.NodeExecutionEnv
	late func()
}

func (env *lateOutputEnv) Exec(_ context.Context, _ string, options *harness.ShellExecOptions) (harness.ShellExecResult, error) {
	result := fakeShellOutput("before\n", options, "")
	env.late = func() { fakeShellOutput("before\nlate\n", options, "") }
	return result, nil
}

type checkpointOutputEnv struct{ *envpkg.NodeExecutionEnv }

func (env *checkpointOutputEnv) Exec(_ context.Context, _ string, options *harness.ShellExecOptions) (harness.ShellExecResult, error) {
	fakeShellOutput("one\n", options, "")
	time.Sleep(2100 * time.Millisecond)
	fakeShellOutput("one\ntwo\n", options, "")
	time.Sleep(100 * time.Millisecond)
	fakeShellOutput("one\ntwo\nthree\n", options, "")
	time.Sleep(2000 * time.Millisecond)
	return fakeShellOutput("one\ntwo\nthree\nfour\n", options, ""), nil
}

func TestHarnessBash(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:576
	t.Run("executes commands and combines stdout and stderr", func(t *testing.T) {
		result := execute(t, CreateBashTool(nil), newEnv(t), "bash-1", map[string]any{"command": "printf out; printf err >&2"})
		out := textOutput(result)
		if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
			t.Fatal(out)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:591
	t.Run("reports nonzero exits and timeouts", func(t *testing.T) {
		env := newEnv(t)
		tool := CreateBashTool(nil)
		_, err := tool.Execute(t.Context(), "bash-2", map[string]any{"command": "printf failed; exit 7"}, noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
		if err == nil || !regexp.MustCompile(`failed[\s\S]*Command exited with code 7`).MatchString(err.Error()) {
			t.Fatalf("exit error=%v", err)
		}
		_, err = tool.Execute(t.Context(), "bash-3", map[string]any{"command": "sleep 2", "timeout": 0.01}, noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
		if err == nil || !strings.Contains(err.Error(), "Command timed out after 0.01 seconds") {
			t.Fatalf("timeout error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:617
	t.Run("preserves truncated output when a command times out", func(t *testing.T) {
		env := &timeoutOutputEnv{newEnv(t)}
		_, err := CreateBashTool(nil).Execute(t.Context(), "bash-timeout-output", map[string]any{"command": "emit-output-then-time-out", "timeout": 0.05}, noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
		if err == nil || !strings.Contains(err.Error(), "Command timed out after 0.05 seconds") {
			t.Fatalf("error=%v", err)
		}
		match := regexp.MustCompile(`Full output: ([^\]\n]+)`).FindStringSubmatch(err.Error())
		if len(match) != 2 {
			t.Fatalf("spill path missing: %v", err)
		}
		full := readFile(t, env, match[1])
		if !strings.Contains(full, "line-1\nline-2") || !strings.Contains(full, "line-2000\nline-2001") {
			t.Fatal("spill lacks first or last lines")
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:643
	t.Run("ignores output callbacks after execution settles", func(t *testing.T) {
		env := &lateOutputEnv{NodeExecutionEnv: newEnv(t)}
		var updates []string
		result, err := CreateBashTool(nil).Execute(t.Context(), "bash-late", map[string]any{"command": "late"}, func(update harness.AgentToolResult, _ harness.AgentHarnessToolUpdateOptions) {
			updates = append(updates, textOutput(update))
		}, ExecutionToolContext{Env: env}, testInvocation{})
		if err != nil {
			t.Fatal(err)
		}
		env.late()
		if textOutput(result) != "before\n" {
			t.Fatal(textOutput(result))
		}
		for _, update := range updates {
			if strings.Contains(update, "late") {
				t.Fatalf("late update=%q", update)
			}
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:660
	t.Run("reports the total size of an oversized final line", func(t *testing.T) {
		result := execute(t, CreateBashTool(nil), newEnv(t), "bash-long-line", map[string]any{"command": "printf '%060000d' 0"})
		if !regexp.MustCompile(`Showing last 50\.0KB of line 1 \(line is 58\.6KB\)\. Full output:`).MatchString(textOutput(result)) {
			t.Fatal("incorrect last-line notice")
		}
		removeSpill(t, result)
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:712
	t.Run("supports command prefixes", func(t *testing.T) {
		result := execute(t, CreateBashTool(&BashToolOptions{CommandPrefix: "value=hello"}), newEnv(t), "bash-4", map[string]any{"command": "printf $value"})
		if textOutput(result) != "hello" {
			t.Fatal(textOutput(result))
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:750
	t.Run("coalesces updates and persists truncated full output", func(t *testing.T) {
		env := newEnv(t)
		var updates []harness.AgentToolResult
		result, err := CreateBashTool(nil).Execute(t.Context(), "bash-5", map[string]any{"command": "i=1; while [ $i -le 3000 ]; do echo line-$i; i=$((i + 1)); done"}, func(update harness.AgentToolResult, _ harness.AgentHarnessToolUpdateOptions) {
			updates = append(updates, update)
		}, ExecutionToolContext{Env: env}, testInvocation{})
		if err != nil {
			t.Fatal(err)
		}
		if len(updates) >= 25 || len(updates) == 0 {
			t.Fatalf("updates=%d", len(updates))
		}
		details := result.Details.(*BashToolDetails)
		tr := details.Truncation
		if tr == nil || !tr.Truncated || tr.TruncatedBy == nil || *tr.TruncatedBy != "lines" || tr.TotalLines != 3000 || tr.OutputLines != 2000 {
			t.Fatalf("truncation=%+v", tr)
		}
		if !strings.Contains(textOutput(result), "line-3000") || details.FullOutputPath == "" {
			t.Fatal("final output or spill path missing")
		}
		final := updates[len(updates)-1]
		fd := final.Details.(*BashToolDetails)
		if !strings.Contains(textOutput(final), "line-3000") || fd.Truncation.TotalLines != 3000 || fd.Truncation.TotalBytes != len(lines(3000, "line-%d")+"\n") || fd.FullOutputPath != details.FullOutputPath {
			t.Fatalf("final update=%+v", final)
		}
		full := readFile(t, env, details.FullOutputPath)
		if !strings.Contains(full, "line-1\nline-2") || !strings.Contains(full, "line-2999\nline-3000") {
			t.Fatal("spill lacks first or last lines")
		}
		removeSpill(t, result)
	})
}
func removeSpill(t *testing.T, result harness.AgentToolResult) {
	t.Helper()
	if details, ok := result.Details.(*BashToolDetails); ok && details.FullOutputPath != "" {
		if err := os.Remove(details.FullOutputPath); err != nil {
			t.Fatal(err)
		}
	}
}

type workspaceContext struct {
	ExecutionToolContext
	workspace string
}

// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:674
func TestHarnessBashPreparesExecutionWithTurnContext(t *testing.T) {
	env := envpkg.NewNodeExecutionEnv(envpkg.NodeExecutionEnvOptions{Cwd: t.TempDir(), ShellEnv: map[string]string{"PI_BASH_PREPARE_INHERITED": "inherited"}})
	t.Cleanup(func() { env.Cleanup(context.Background()) })
	if err := env.CreateDir(t.Context(), "workspace", nil); err != nil {
		t.Fatal(err)
	}
	turn := &workspaceContext{ExecutionToolContext: ExecutionToolContext{Env: env}, workspace: filepath.Join(env.Cwd(), "workspace")}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var received any
	var signal context.Context
	// Git Bash's $PWD is an MSYS path (/tmp/...); pwd -W prints the Windows path.
	pwd := `"$PWD"`
	if runtime.GOOS == "windows" {
		pwd = `"$(pwd -W)"`
	}
	tool := CreateBashTool(&BashToolOptions{CommandPrefix: "prefix=ready", Prepare: func(callContext context.Context, execution *BashExecution, toolContext any) error {
		received = toolContext
		signal = callContext
		execution.Cwd = toolContext.(*workspaceContext).workspace
		execution.Env = map[string]string{"PI_BASH_PREPARE_EXPLICIT": "explicit"}
		execution.InheritEnv = false
		execution.Command += "\nprintf '%s:%s:%s:%s' \"$prefix\" \"${PI_BASH_PREPARE_INHERITED-}\" \"$PI_BASH_PREPARE_EXPLICIT\" " + pwd
		return nil
	}})
	result, err := tool.Execute(ctx, "bash-prepare", map[string]any{"command": ":"}, noUpdate, turn, testInvocation{})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := env.CanonicalPath(t.Context(), turn.workspace)
	if err != nil {
		t.Fatal(err)
	}
	got := textOutput(result)
	if prefix, directory, ok := strings.Cut(got, "explicit:"); ok && runtime.GOOS == "windows" {
		// pwd -W spells the directory with / and may keep 8.3 short names; canonicalize it as CanonicalPath does.
		if resolved, err := filepath.EvalSymlinks(directory); err == nil {
			got = prefix + "explicit:" + resolved
		}
	}
	if received != turn || signal != ctx || got != "ready::explicit:"+canonical {
		t.Fatalf("context=%v signal=%v result=%q", received, signal, textOutput(result))
	}
}

// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:726
func TestHarnessBashRequestsDistinctBoundedCheckpoints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		env := &checkpointOutputEnv{newEnv(t)}
		var checkpoints []string
		_, err := CreateBashTool(nil).Execute(t.Context(), "bash-checkpoints", map[string]any{"command": "controlled"}, func(update harness.AgentToolResult, options harness.AgentHarnessToolUpdateOptions) {
			if options.Checkpoint {
				checkpoints = append(checkpoints, textOutput(update))
			}
		}, ExecutionToolContext{Env: env}, testInvocation{})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"one\ntwo\n", "one\ntwo\nthree\nfour\n"}; !reflect.DeepEqual(checkpoints, want) {
			t.Fatalf("checkpoints=%q want=%q", checkpoints, want)
		}
	})
}
