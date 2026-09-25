package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports upstream test/powershell-tool.test.ts "uses process-local execution
// policy bypass".
func TestPowerShellArgsUseProcessLocalExecutionPolicyBypass(t *testing.T) {
	want := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}
	if !slices.Equal(PowerShellArgs, want) {
		t.Fatalf("PowerShellArgs = %q, want %q", PowerShellArgs, want)
	}
}

// Upstream getPowerShellConfig throws on every platform but win32, and on
// win32 resolves pwsh.exe, then powershell.exe, with POWERSHELL_ARGS.
func TestGetPowerShellConfigPlatformGate(t *testing.T) {
	cfg, err := GetPowerShellConfig()
	if runtime.GOOS != "windows" {
		if err == nil || err.Error() != "The powershell tool is only available on Windows." {
			t.Fatalf("GetPowerShellConfig on %s = %+v, %v; want the Windows-only error", runtime.GOOS, cfg, err)
		}
		return
	}
	if err != nil {
		if err.Error() != "No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH." {
			t.Fatalf("unexpected resolution error: %v", err)
		}
		return
	}
	if base := strings.ToLower(filepath.Base(cfg.Path)); base != "pwsh.exe" && base != "powershell.exe" {
		t.Errorf("resolved shell %q is not PowerShell", cfg.Path)
	}
	if !slices.Equal(cfg.Args, PowerShellArgs) {
		t.Errorf("args = %q, want %q", cfg.Args, PowerShellArgs)
	}
}

// The powershell definition is createShellToolDefinition with the PowerShell
// config: its description names PowerShell, its schema and constrained
// sampling request match bash, and its guideline follows the session
// environment exposure.
func TestPowerShellToolSchema(t *testing.T) {
	tool := &PowerShellTool{}
	s := tool.Schema()
	if tool.Name() != "powershell" || s.Name != "powershell" {
		t.Fatalf("name = %q/%q, want powershell", tool.Name(), s.Name)
	}
	const wantDescription = "Execute a PowerShell command in the current working directory. Returns stdout and stderr. " +
		"Output is truncated to last 2000 lines or 50KB (whichever is hit first). If truncated, full output is saved to a temp file. " +
		"Optionally provide a timeout in seconds."
	if s.Description != wantDescription {
		t.Errorf("description = %q\nwant %q", s.Description, wantDescription)
	}
	if want := (&BashTool{}).Schema(); !jsonEqual(t, s.Parameters, want.Parameters) {
		t.Errorf("parameters = %v, want bash's %v", s.Parameters, want.Parameters)
	}
	if cs := s.ConstrainedSampling; cs == nil || cs.Type != "json_schema" || cs.Strict != "prefer" || !jsonEqual(t, cs, &ai.ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}) {
		t.Errorf("constrained sampling = %+v, want json_schema/prefer", s.ConstrainedSampling)
	}
	if !slices.Equal(s.PromptGuidelines, []string{sessionGuideline}) {
		t.Errorf("guidelines = %q", s.PromptGuidelines)
	}
	if got := (&PowerShellTool{HideSessionEnvironment: true}).Schema().PromptGuidelines; got != nil {
		t.Errorf("hidden session environment still has guidelines %q", got)
	}
	if PowerShellPromptSnippet != "Execute PowerShell commands" {
		t.Errorf("prompt snippet = %q", PowerShellPromptSnippet)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(ja) == string(jb)
}

// Upstream emits the initial empty update before exec runs, then surfaces
// getPowerShellConfig's error as the tool error. On Windows the same call runs
// PowerShell (ported from the win32-only upstream execution case).
func TestPowerShellToolExecute(t *testing.T) {
	var updates []string
	tool := &PowerShellTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: "Write-Output 'héllo €'; Get-ExecutionPolicy -Scope Process"})
	res, err := tool.Execute(t.Context(), "", args, func(content string, details any) {
		updates = append(updates, content)
	})
	if err != nil {
		t.Fatalf("Execute returned a hard error: %v", err)
	}
	if len(updates) == 0 || updates[0] != "" {
		t.Fatalf("first update = %q, want the initial empty update", updates)
	}
	if runtime.GOOS != "windows" {
		if !res.IsError || res.Content != "The powershell tool is only available on Windows." || res.Details != nil {
			t.Fatalf("result = %+v, want the Windows-only tool error", res)
		}
		return
	}
	if res.IsError {
		t.Fatalf("PowerShell execution failed: %s", res.Content)
	}
	for _, want := range []string{"héllo €", "Bypass"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("output missing %q: %q", want, res.Content)
		}
	}
}

// posixShellConfig runs the shared shell execution through /bin/sh so the
// PowerShell-specific wiring can be exercised on every platform with a shell.
func posixShellConfig(t *testing.T, name, shellName string) shellToolConfig {
	t.Helper()
	return shellToolConfig{
		name:           name,
		shellName:      shellName,
		tempFilePrefix: "pi-" + name,
		operations:     &LocalShellOperations{ShellName: shellName, ResolveShell: defaultShellConfig},
	}
}

func runShell(t *testing.T, ctx context.Context, cwd string, cfg shellToolConfig, p bashParams) agent.AgentToolResult {
	t.Helper()
	args, _ := json.Marshal(p)
	res, err := executeShellTool(ctx, cwd, cfg, args, nil)
	if err != nil {
		t.Fatalf("executeShellTool: %v", err)
	}
	return res
}

// createLocalPowerShellOperations prefixes the command after the spawn
// context is resolved; the command prefix setting is bash-only.
func TestShellToolWrapsCommandBeforeSpawn(t *testing.T) {
	cfg := posixShellConfig(t, "powershell", "PowerShell")
	cfg.operations.(*LocalShellOperations).WrapCommand = func(command string) string { return "echo wrapped\n" + command }
	res := runShell(t, t.Context(), t.TempDir(), cfg, bashParams{Command: "echo body"})
	if res.IsError || res.Content != "wrapped\nbody\n" {
		t.Fatalf("result = %+v, want wrapped output", res)
	}
}

func TestShellToolTimeoutValidationMirrorsResolveTimeoutMs(t *testing.T) {
	cfg := posixShellConfig(t, "bash", "bash")
	for _, raw := range []string{`{"command":"echo x","timeout":0}`, `{"command":"echo x","timeout":-2}`} {
		res, err := executeShellTool(t.Context(), t.TempDir(), cfg, json.RawMessage(raw), nil)
		if err != nil || !res.IsError || res.Content != "Invalid timeout: must be a finite number of seconds" {
			t.Errorf("%s: result = %+v, %v", raw, res, err)
		}
	}
	res := runShell(t, t.Context(), t.TempDir(), cfg, bashParams{Command: "echo x", Timeout: maxBashTimeoutSeconds + 1})
	if !res.IsError || res.Content != "Invalid timeout: maximum is 2147483.647 seconds" {
		t.Errorf("over-large timeout result = %+v", res)
	}
}

// The timeout status repeats the requested seconds as JavaScript prints them,
// and an abort or timeout with no output reports only the status.
func TestShellToolTimeoutAndAbortStatus(t *testing.T) {
	cfg := posixShellConfig(t, "bash", "bash")
	res := runShell(t, t.Context(), t.TempDir(), cfg, bashParams{Command: "sleep 5", Timeout: 0.5})
	if !res.IsError || res.Content != "Command timed out after 0.5 seconds" || res.Details != nil {
		t.Errorf("timeout result = %+v", res)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var once sync.Once
	done := make(chan agent.AgentToolResult, 1)
	go func() {
		args, _ := json.Marshal(bashParams{Command: "sleep 30"})
		r, _ := executeShellTool(ctx, t.TempDir(), cfg, args, func(string, any) {
			once.Do(func() { time.AfterFunc(200*time.Millisecond, cancel) })
		})
		done <- r
	}()
	select {
	case r := <-done:
		if !r.IsError || r.Content != "Command aborted" {
			t.Errorf("abort result = %+v, want only the status", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not return")
	}
	cancel()
}

func TestShellToolMissingWorkingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	res := runShell(t, t.Context(), missing, posixShellConfig(t, "powershell", "PowerShell"), bashParams{Command: "echo x"})
	want := "Working directory does not exist: " + missing + "\nCannot execute PowerShell commands."
	if !res.IsError || res.Content != want {
		t.Fatalf("result = %+v, want %q", res, want)
	}
}

// Full output of an overflowing PowerShell run lands in a pig-powershell temp
// file (upstream tempFilePrefix "pi-powershell").
func TestShellToolTempFilePrefix(t *testing.T) {
	cfg := posixShellConfig(t, "powershell", "PowerShell")
	res := runShell(t, t.Context(), t.TempDir(), cfg, bashParams{Command: `awk 'BEGIN{for(i=1;i<=3000;i++) printf "%020d\n", i}'`})
	d, ok := res.Details.(*BashDetails)
	if !ok || d == nil || d.FullOutputPath == "" {
		t.Fatalf("details = %#v", res.Details)
	}
	t.Cleanup(func() { _ = os.Remove(d.FullOutputPath) })
	if !strings.HasPrefix(filepath.Base(d.FullOutputPath), "pi-powershell-") {
		t.Errorf("full output path %q lacks the powershell prefix", d.FullOutputPath)
	}
}

// Mirrors upstream allToolNames / createAllTools: powershell sits between bash
// and edit in the registry, which CreateCodingTools (PiG's default set) omits.
func TestCreateAllToolsMirrorsUpstreamRegistry(t *testing.T) {
	want := []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"}
	if got := BuiltinToolNames(); !slices.Equal(got, want) {
		t.Fatalf("BuiltinToolNames() = %v, want %v", got, want)
	}
	var got []string
	for _, tool := range CreateAllTools(t.TempDir(), nil, "") {
		got = append(got, tool.Name())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("CreateAllTools() = %v, want %v", got, want)
	}
	if BuiltinToolDescription("powershell") == "" {
		t.Error("powershell has no host-action description")
	}
}

func TestBuiltinToolActive(t *testing.T) {
	allow := map[string]struct{}{"powershell": {}}
	for _, tc := range []struct {
		name            string
		active, allowed map[string]struct{}
		want            bool
	}{
		{"bash", nil, nil, true},
		{"powershell", nil, nil, false},
		{"powershell", nil, map[string]struct{}{"bash": {}}, false},
		{"powershell", nil, allow, true},
		{"powershell", map[string]struct{}{"read": {}}, allow, false},
		{"powershell", allow, nil, true},
		{"grep", map[string]struct{}{"read": {}}, nil, false},
	} {
		if got := BuiltinToolActive(tc.name, tc.active, tc.allowed); got != tc.want {
			t.Errorf("BuiltinToolActive(%q, %v, %v) = %v, want %v", tc.name, tc.active, tc.allowed, got, tc.want)
		}
	}
}

// OutputAccumulator snapshots retain whole-stream counts after the rolling
// tail is trimmed; createShellToolDefinition passes that snapshot to callers.
func TestShellToolPreservesTruncationSnapshot(t *testing.T) {
	cfg := posixShellConfig(t, "bash", "bash")
	res := runShell(t, t.Context(), t.TempDir(), cfg, bashParams{Command: `awk 'BEGIN{for(i=1;i<=12000;i++) printf "%020d\n", i}'`})
	d, ok := res.Details.(*BashDetails)
	if !ok || d == nil || d.Truncation == nil {
		t.Fatalf("details = %#v", res.Details)
	}
	t.Cleanup(func() { _ = os.Remove(d.FullOutputPath) })
	tr := d.Truncation
	if !tr.Truncated || tr.TotalLines != 12000 || tr.TotalBytes != 252000 || tr.OutputLines != 2000 || tr.TruncatedBy != "lines" {
		t.Fatalf("snapshot = %+v", *tr)
	}
	if !strings.Contains(res.Content, "[Showing lines 10001-12000 of 12000. Full output:") {
		t.Fatalf("missing whole-stream footer: %q", res.Content[len(res.Content)-200:])
	}
}
