package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

type portBashOperations func(context.Context, string, string, BashOperationsExecOptions) (BashOperationsResult, error)

func (f portBashOperations) Exec(ctx context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	return f(ctx, command, cwd, opts)
}

func bashPort(t *testing.T, tool *BashTool, command string) agent.AgentToolResult {
	t.Helper()
	return runFileTool(t, tool, t.Context(), map[string]any{"command": command})
}
func assertBashPortError(t *testing.T, result agent.AgentToolResult, pattern string) {
	t.Helper()
	if !result.IsError || !regexp.MustCompile(pattern).MatchString(result.Text()) {
		t.Fatalf("result = %+v; want error %q", result, pattern)
	}
}
func assertFullBashOutput(t *testing.T, path string) {
	t.Helper()
	if path == "" {
		t.Fatal("missing full output path")
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"1\n2\n3", "2998\n2999\n3000"} {
		if !strings.Contains(string(data), part) {
			t.Fatalf("full output lacks %q", part)
		}
	}
}

func TestToolsBashPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:486
	t.Run("should execute simple commands", func(t *testing.T) {
		result := bashPort(t, &BashTool{CWD: t.TempDir()}, "echo 'test output'")
		requireTextParts(t, result, []string{"test output"}, nil)
		if result.Details != nil {
			t.Fatalf("details = %#v", result.Details)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:493
	t.Run("should handle command errors", func(t *testing.T) {
		assertBashPortError(t, bashPort(t, &BashTool{CWD: t.TempDir()}, "exit 1"), `(Command failed|code 1)`)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:531
	t.Run("should reject a null exit code from custom operations", func(t *testing.T) {
		tool := &BashTool{CWD: t.TempDir(), Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			opts.OnData([]byte("partial\n"))
			return BashOperationsResult{}, nil
		})}
		assertBashPortError(t, bashPort(t, tool, "remote"), `partial\s+Command terminated without an exit code$`)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:545
	t.Run("should respect timeout", func(t *testing.T) {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Fatal(err)
		}
		result := runFileTool(t, &BashTool{CWD: t.TempDir()}, t.Context(), map[string]any{"command": fmt.Sprintf("%q -e %q", node, "setInterval(() => {}, 1000)"), "timeout": 0.05})
		assertBashPortError(t, result, `(?i)timed out`)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:550
	t.Run("should include full output path for truncated timeout and abort errors", func(t *testing.T) {
		for _, tc := range []struct{ error, want string }{{"timeout:5", "Command timed out after 5 seconds"}, {"aborted", "Command aborted"}} {
			t.Run(tc.error, func(t *testing.T) {
				tool := &BashTool{CWD: t.TempDir(), Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
					for i := 1; i <= 3000; i++ {
						opts.OnData(fmt.Appendf(nil, "%d\n", i))
					}
					return BashOperationsResult{}, errors.New(tc.error)
				})}
				result := bashPort(t, tool, "chatty-fail")
				assertBashPortError(t, result, regexp.QuoteMeta(tc.want))
				if !regexp.MustCompile(`\[Showing lines \d+-\d+ of \d+\. Full output: `).MatchString(result.Text()) || strings.Contains(result.Text(), "Full output: undefined") {
					t.Fatal(result.Text())
				}
				match := regexp.MustCompile(`Full output: ([^\]\n]+)`).FindStringSubmatch(result.Text())
				if len(match) != 2 {
					t.Fatalf("no path in %q", result.Text())
				}
				assertFullBashOutput(t, match[1])
			})
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:586
	t.Run("should throw error when cwd does not exist", func(t *testing.T) {
		assertBashPortError(t, bashPort(t, &BashTool{CWD: "/this/directory/definitely/does/not/exist/12345"}, "echo test"), `Working directory does not exist`)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:596
	t.Run("should handle process spawn errors", func(t *testing.T) {
		cfg := posixShellConfig(t, "bash", "bash")
		cfg.operations = &LocalShellOperations{ShellName: "bash", ResolveShell: func() (ShellConfig, error) {
			return ShellConfig{Path: "/nonexistent-shell-path-xyz123", Args: []string{"-c"}}, nil
		}}
		result := runShell(t, t.Context(), t.TempDir(), cfg, bashParams{Command: "echo test"})
		assertBashPortError(t, result, `ENOENT`)
		fmt.Printf("BASH_SPAWN %s\n", result.Text())
		_, err := cfg.operations.Exec(t.Context(), "echo test", t.TempDir(), BashOperationsExecOptions{})
		if !errors.Is(err, os.ErrNotExist) || !result.IsError {
			t.Fatalf("result = %+v, spawn = %v", result, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:607
	t.Run("should pass shellPath through to shell resolution", func(t *testing.T) {
		settings := fakeSettings{path: "/custom/bash"}
		tool := &BashTool{CWD: t.TempDir(), Settings: settings, Operations: portBashOperations(func(context.Context, string, string, BashOperationsExecOptions) (BashOperationsResult, error) {
			return BashOperationsResult{ExitCode: new(0)}, nil
		})}
		result := bashPort(t, tool, "echo test")
		if result.IsError {
			t.Fatal(result.Text())
		}
		fmt.Printf("BASH_DELEGATE %s\n", result.Text())
		_, err := NewLocalBashOperations(settings, "").Exec(t.Context(), "echo test", t.TempDir(), BashOperationsExecOptions{})
		if err == nil || err.Error() != "Custom shell path not found: /custom/bash" {
			t.Fatalf("local resolution = %v", err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:629
	t.Run("should send commands over stdin when shell resolution requires it", func(t *testing.T) {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Fatal(err)
		}
		ops := &LocalShellOperations{ShellName: "bash", ResolveShell: func() (ShellConfig, error) {
			return ShellConfig{Path: node, Args: []string{"-e", `let input = ""; process.stdin.setEncoding("utf8"); process.stdin.on("data", (chunk) => { input += chunk; }); process.stdin.on("end", () => { process.stdout.write(input); });`}, CommandTransport: "stdin"}, nil
		}}
		command := `name='World'; echo "Hello, ${name}!"; count=3; for i in $(seq 1 ${count}); do echo "Iteration ${i} of ${count}"; done`
		var output strings.Builder
		result, err := ops.Exec(t.Context(), command, t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
		if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || output.String() != command {
			t.Fatalf("result %+v, %v, output %q", result, err, output.String())
		}
	})
	for _, tc := range []struct{ name, prefix, command, want string }{
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:679
		{"should prepend command prefix when configured", "export TEST_VAR=hello", "echo $TEST_VAR", "hello"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:688
		{"should include output from both prefix and command", "echo prefix-output", "echo command-output", "prefix-output\ncommand-output"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:697
		{"should work without command prefix", "", "echo no-prefix", "no-prefix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := bashPort(t, &BashTool{CWD: t.TempDir(), CommandPrefix: tc.prefix}, tc.command)
			if result.IsError || strings.TrimSpace(result.Text()) != tc.want {
				t.Fatal(result)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:704
	t.Run("should coalesce streaming updates for chatty output", func(t *testing.T) {
		// Node runs the 5000 synchronous onData calls before any throttle
		// timer can fire. Hold the scheduler's clock still for the burst so
		// the count reflects coalescing, not how fast this host appends.
		frozen := time.Unix(1_700_000_000, 0)
		previous := shellUpdateTime
		shellUpdateTime = shellUpdateClock{now: func() time.Time { return frozen }, after: func(time.Duration) (<-chan time.Time, func()) { return nil, func() {} }}
		t.Cleanup(func() { shellUpdateTime = previous })
		tool := &BashTool{CWD: t.TempDir(), Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			for i := range 5000 {
				opts.OnData(fmt.Appendf(nil, "line %d\n", i))
			}
			return BashOperationsResult{ExitCode: new(0)}, nil
		})}
		var mu sync.Mutex
		var updates []string
		result, err := tool.Execute(t.Context(), "test-call-chatty-updates", json.RawMessage(`{"command":"chatty"}`), func(content string, _ any) { mu.Lock(); updates = append(updates, content); mu.Unlock() })
		if err != nil {
			t.Fatal(err)
		}
		if len(updates) >= 25 {
			t.Fatalf("%d updates, want <25", len(updates))
		}
		requireTextParts(t, result, []string{"line 4999"}, nil)
		if details, ok := result.Details.(*BashDetails); ok {
			t.Cleanup(func() { _ = os.Remove(details.FullOutputPath) })
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:724
	t.Run("should not count a trailing newline as an extra truncated bash output line", func(t *testing.T) {
		tool := &BashTool{CWD: t.TempDir(), Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			for i := 1; i <= 4000; i++ {
				opts.OnData(fmt.Appendf(nil, "line-%04d\n", i))
			}
			return BashOperationsResult{ExitCode: new(0)}, nil
		})}
		result := bashPort(t, tool, "many-lines")
		details, ok := result.Details.(*BashDetails)
		if !ok || details.Truncation == nil {
			t.Fatalf("details = %+v", result.Details)
		}
		t.Cleanup(func() { _ = os.Remove(details.FullOutputPath) })
		if details.Truncation.TotalLines != 4000 || details.Truncation.OutputLines != 2000 {
			t.Fatal(details.Truncation)
		}
		requireTextParts(t, result, []string{"line-2001", "line-4000", "[Showing lines 2001-4000 of 4000. Full output: "}, []string{"4001"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:746
	t.Run("should decode UTF-8 characters split across output chunks", func(t *testing.T) {
		tool := &BashTool{CWD: t.TempDir(), Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			euro := []byte("€\n")
			opts.OnData(euro[:1])
			opts.OnData(euro[1:])
			return BashOperationsResult{ExitCode: new(0)}, nil
		})}
		result := bashPort(t, tool, "split-utf8")
		if result.IsError || strings.TrimSpace(result.Text()) != "€" {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:762
	t.Run("should expose local bash operations for extension reuse", func(t *testing.T) {
		t.Setenv("TEST_LOCAL_BASH_OPS", "from-local-ops")
		var output strings.Builder
		result, err := NewLocalBashOperations(nil, "").Exec(t.Context(), "echo $TEST_LOCAL_BASH_OPS", t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }, Env: os.Environ()})
		if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || strings.TrimSpace(output.String()) != "from-local-ops" {
			t.Fatalf("result %+v, %v, output %q", result, err, output.String())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:775
	t.Run("should preserve executeBash sanitization when using local bash operations", func(t *testing.T) {
		result, err := ExecuteBashWithOperations(t.Context(), `printf '\033[31mred\033[0m\r\n'`, t.TempDir(), NewLocalBashOperations(nil, ""), BashExecOptions{})
		if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || result.Output != "red\n" {
			t.Fatalf("result %+v, %v", result, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:786
	t.Run("should persist full output when truncation happens by line count only", func(t *testing.T) {
		result := bashPort(t, &BashTool{CWD: t.TempDir()}, "seq 3000")
		details, ok := result.Details.(*BashDetails)
		if !ok || details.Truncation == nil || !details.Truncation.Truncated || details.Truncation.TruncatedBy != "lines" {
			t.Fatalf("details = %+v", result.Details)
		}
		if !regexp.MustCompile(`\[Showing lines \d+-\d+ of \d+\. Full output: `).MatchString(result.Text()) || strings.Contains(result.Text(), "Full output: undefined") {
			t.Fatal(result.Text())
		}
		assertFullBashOutput(t, details.FullOutputPath)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:809
	t.Run("executeBash should persist full output when truncation happens by line count only", func(t *testing.T) {
		result, err := ExecuteBashWithOperations(t.Context(), "seq 3000", t.TempDir(), NewLocalBashOperations(nil, ""), BashExecOptions{})
		if err != nil || !result.Truncated {
			t.Fatalf("result %+v, %v", result, err)
		}
		assertFullBashOutput(t, result.FullOutputPath)
	})
}

func TestToolsLsPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:937
	t.Run("should list dotfiles and directories", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".hidden-file"), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dir, ".hidden-dir"), 0o700); err != nil {
			t.Fatal(err)
		}
		result := runFileTool(t, &LsTool{CWD: dir}, t.Context(), map[string]any{"path": dir})
		requireTextParts(t, result, []string{".hidden-file", ".hidden-dir/"}, nil)
	})
}
