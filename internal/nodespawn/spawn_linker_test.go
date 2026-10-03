//go:build !windows

package nodespawn

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// On Android, where the app data directory is not executable, SetProgram starts the program through the linker, and
// starts the trampoline that preserves a duplicate environment key through the linker as well. The trampoline execs the
// linker with the program, since the Go runtime's execve is not the libc call termux-exec redirects.
func TestSetProgramStartsProgramsAndTheTrampolineThroughTheLinker(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Dir(self)
	program := filepath.Join(dataDir, "termux-prog")
	const linker = "/system/bin/linker64"
	t.Cleanup(linkerexec.SetStarterForTest(linkerexec.Starter{
		Linker:   linker,
		DataDir:  dataDir,
		ReadHead: func(string) ([]byte, error) { return []byte("\x7fELF"), nil },
	}))

	plain := &exec.Cmd{Path: program, Args: []string{program, "a"}, Env: []string{"A=1"}}
	SetProgram(plain)
	if plain.Path != linker || !slices.Equal(plain.Args, []string{linker, program, "a"}) || plain.Err != nil {
		t.Fatalf("plain: Path=%q Args=%q Err=%v", plain.Path, plain.Args, plain.Err)
	}

	// Equal keys in cmd.Env make os/exec drop one, so SetProgram wraps the start in a trampoline.
	env := []string{"K=1", "K=2"}
	wrapped := &exec.Cmd{Path: program, Args: []string{program, "a"}, Env: env}
	SetProgram(wrapped)
	want := []string{linker, self, linker, linker, program, "a", "K=1", "K=2"}
	if wrapped.Path != linker || !slices.Equal(wrapped.Args, want) || wrapped.Err != nil {
		t.Fatalf("trampoline: Path=%q Args=%q Err=%v\nwant %q", wrapped.Path, wrapped.Args, wrapped.Err, want)
	}
	if !slices.Equal(wrapped.Env, []string{trampolineEnv + "=3"}) {
		t.Fatalf("trampoline Env = %q", wrapped.Env)
	}
}
