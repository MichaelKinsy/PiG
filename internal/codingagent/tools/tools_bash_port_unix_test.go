//go:build !windows

package tools

import (
	"os"
	"reflect"
	"testing"
)

func TestToolsBashSignalsPort(t *testing.T) {
	for _, tc := range []struct {
		signal string
		code   int
	}{{"KILL", 137}, {"TERM", 143}} {
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:500
		t.Run("should map signal-killed commands to 128 plus the signal number/"+tc.signal, func(t *testing.T) {
			result, err := CreateLocalBashOperations(nil).Exec(t.Context(), "kill -"+tc.signal+" $$", t.TempDir(), BashOperationsExecOptions{OnData: func([]byte) {}})
			if err != nil || result.ExitCode == nil || *result.ExitCode != tc.code {
				t.Fatalf("result %+v, %v; want %d", result, err, tc.code)
			}
		})
		// .upstream/v0.99.1/packages/coding-agent/test/tools.test.ts:554
		t.Run("should report signal-killed commands as errors while preserving partial output/"+tc.signal, func(t *testing.T) {
			result := bashPort(t, &BashTool{CWD: t.TempDir()}, "printf 'before-kill\\n'; kill -"+tc.signal+" $$")
			assertBashPortError(t, result, `before-kill\s+Command exited with code `+jsNumber(float64(tc.code))+`$`)
		})
	}
}

func TestToolsLegacyWSLTransportPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:653 deliberately runs off Windows with a Windows-path fixture.
	t.Chdir(t.TempDir())
	const path = `C:\Windows\System32\bash.exe`
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := GetShellConfig(path)
	want := ShellConfig{Path: path, Args: []string{"-s"}, CommandTransport: "stdin"}
	if err != nil || !reflect.DeepEqual(cfg, want) {
		t.Fatalf("shell = %+v, %v; want %+v", cfg, err, want)
	}
}
