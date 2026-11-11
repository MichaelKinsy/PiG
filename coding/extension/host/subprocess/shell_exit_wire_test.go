package subprocess

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi's ShellTimeoutError.shellExit (bash.ts:100-105) crosses the wire as ErrorInfo.shellExit: the host's error reply of builtinTool.localExec carries it, and the error
// response of an extension's user_bash_exec, which the host's shell tool reads, is the local backend's error again.
func TestShellExitCrossesTheWireAsErrorInfoShellExit(t *testing.T) {
	timeout := &tools.ShellTimeoutError{Timeout: "2", ShellExit: &tools.ShellExit{ExitCode: 7, AfterSeconds: 0.5}}
	encoded, err := json.Marshal(errorInfoOf(timeout))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"message":"timeout:2","shellExit":{"exitCode":7,"afterSeconds":0.5}}`; string(encoded) != want {
		t.Fatalf("the wire error = %s, want %s", encoded, want)
	}
	var info ErrorInfo
	if err := json.Unmarshal(encoded, &info); err != nil {
		t.Fatal(err)
	}
	var back *tools.ShellTimeoutError
	if !errors.As(info.ToError(), &back) || back.Timeout != "2" || back.ShellExit == nil || *back.ShellExit != *timeout.ShellExit {
		t.Fatalf("the error read back = %#v, want the local backend's timeout with the shell's exit", info.ToError())
	}

	// A timeout that never saw the shell exit, and any other error, carry no shellExit.
	for _, plain := range []error{&tools.ShellTimeoutError{Timeout: "2"}, errors.New("timeout:2")} {
		encoded, _ := json.Marshal(errorInfoOf(plain))
		if string(encoded) != `{"message":"timeout:2"}` {
			t.Fatalf("the wire error of %#v = %s, want the message alone", plain, encoded)
		}
		var info ErrorInfo
		if err := json.Unmarshal(encoded, &info); err != nil {
			t.Fatal(err)
		}
		if _, ok := errors.AsType[*tools.ShellTimeoutError](info.ToError()); ok {
			t.Fatalf("%s without a shellExit was read back as the local backend's timeout", encoded)
		}
	}
	// An error wrapped around the backend's keeps nothing of it, as `new Error(error.message)` in Pi.
	wrapped := errorInfoOf(errors.New(timeout.Error()))
	if wrapped.ShellExit != nil {
		t.Fatalf("a new error with the backend's message carries %+v", wrapped.ShellExit)
	}
}

// A fused extension's SDK returns an error of its own package's type, which the host reads through ShellExitReport when it is the exec's own error.
type fusedShellTimeout struct{ message string }

func (e fusedShellTimeout) Error() string { return e.message }
func (fusedShellTimeout) ShellExitReport() (int, float64, bool) {
	return 7, 0.5, true
}

func TestLocalShellTimeoutReadsAFusedSDKsError(t *testing.T) {
	var got *tools.ShellTimeoutError
	if !errors.As(localShellTimeout(fusedShellTimeout{"timeout:2"}), &got) || got.Timeout != "2" || got.ShellExit == nil || got.ShellExit.ExitCode != 7 || got.ShellExit.AfterSeconds != 0.5 {
		t.Fatalf("localShellTimeout = %#v, want the local backend's timeout with the shell's exit", got)
	}
	other := errors.New("boom")
	if !errors.Is(localShellTimeout(other), other) {
		t.Fatal("an error that is not a timeout was replaced")
	}
	if err := localShellTimeout(fusedShellTimeout{"not a timeout"}); !errors.Is(err, fusedShellTimeout{"not a timeout"}) {
		t.Fatalf("an error that is not a timeout became %#v", err)
	}
	if localShellTimeout(nil) != nil {
		t.Fatal("nil became an error")
	}
}
