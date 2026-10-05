//go:build !windows

package crossspawn

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// On Android an app cannot execve a file in its data directory, so Command must
// start such a program through the linker.
func TestCommandStartsDataDirProgramsThroughTheLinker(t *testing.T) {
	dataDir := t.TempDir()
	program := filepath.Join(dataDir, "usr", "bin", "npm")
	if err := os.MkdirAll(filepath.Dir(program), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("\x7fELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	const linker = "/system/bin/linker64"
	t.Cleanup(linkerexec.SetStarterForTest(linkerexec.Starter{
		Linker:   linker,
		DataDir:  dataDir,
		ReadHead: func(string) ([]byte, error) { return []byte("\x7fELF"), nil },
	}))
	t.Setenv("PATH", filepath.Dir(program))

	for _, name := range []string{program, "npm"} {
		cmd := Command(t.Context(), dataDir, name, "install", "x")
		if cmd.Path != linker || !slices.Equal(cmd.Args, []string{linker, program, "install", "x"}) {
			t.Errorf("Command(%q): Path=%q Args=%q", name, cmd.Path, cmd.Args)
		}
	}
}

// Off Android the start is unchanged.
func TestCommandLeavesStartsAloneWithoutTheLinker(t *testing.T) {
	t.Cleanup(linkerexec.SetStarterForTest(linkerexec.Starter{}))
	cmd := Command(t.Context(), "", "/bin/sh", "-c", "true")
	if cmd.Path != "/bin/sh" || !slices.Equal(cmd.Args, []string{"/bin/sh", "-c", "true"}) {
		t.Fatalf("Path=%q Args=%q", cmd.Path, cmd.Args)
	}
}
