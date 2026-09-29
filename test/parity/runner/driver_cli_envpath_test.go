//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// /usr/bin/env resolves its program with the child's PATH, so the Windows
// emulation must use the scenario's effective PATH, not the runner's.
func TestEnvLauncherProgramUsesScenarioPath(t *testing.T) {
	dir := t.TempDir()
	file := "scenario-tool"
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	environ := []string{"PATH=" + t.TempDir(), "Path=" + dir}
	got, err := envLauncherProgram(t, "scenario-tool", effectivePath(environ))
	if err != nil {
		t.Fatalf("scenario PATH lookup: %v", err)
	}
	if filepath.Dir(got) != dir {
		t.Fatalf("resolved %q, want a program in %q", got, dir)
	}
	if _, err := envLauncherProgram(t, "scenario-tool", t.TempDir()); err == nil {
		t.Fatal("a program outside the scenario PATH must not resolve")
	}
}

// env runs a program named by a path as given; joining it onto each PATH
// directory reported an existing program as not found.
func TestEnvLauncherProgramRunsPathsAsGiven(t *testing.T) {
	dir := t.TempDir()
	file := "scenario-tool"
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	program := filepath.Join(dir, file)
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := envLauncherProgram(t, program, t.TempDir())
	if err != nil {
		t.Fatalf("absolute program: %v", err)
	}
	if got != program {
		t.Fatalf("resolved %q, want %q", got, program)
	}
}

// A script bin's #!/usr/bin/env interpreter comes from the scenario's PATH:
// a POSIX kernel runs env with the child's environment, and the Windows
// emulation must do the same.
func TestCLIModeDriverRunsShebangInterpreterFromScenarioPath(t *testing.T) {
	cwd := t.TempDir()
	script := filepath.Join(cwd, "fake-bin.mjs")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env node\nprocess.stdout.write('real node\\n');\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeDir := t.TempDir()
	fake, content := filepath.Join(fakeDir, "node"), "#!/bin/sh\necho scenario node\n"
	if runtime.GOOS == "windows" {
		fake, content = fake+".cmd", "@echo off\r\necho scenario node\r\n"
	}
	if err := os.WriteFile(fake, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{
		Name: "shebang-scenario-path", SourcePath: filepath.Join(cwd, "scenario.toml"),
		Env: EnvOverrides{OverrideBaseArgs: true, PigBin: script, Pig: []string{"PATH=" + fakeDir}},
		CLI: CLIDriverConfig{CWD: cwd, TimeoutSeconds: 10},
	}
	result := (cliModeDriver{}).Run(t.Context(), t, BinaryRef{Label: "pig", Path: "unused"}, scenario)
	if result.Err != nil || strings.TrimSpace(result.Output) != "scenario node" {
		t.Fatalf("output=%q exit code=%d err=%v, want the scenario PATH's node", result.Output, result.ExitCode, result.Err)
	}
}

// A UNC temp directory gives a longer snapshot root, which would make the two
// system prompts differ in length; the runner reports it instead.
func TestCheckPromptPathRootRejectsOtherLengths(t *testing.T) {
	if err := checkPromptPathRoot(); err != nil {
		t.Fatalf("default root: %v", err)
	}
	saved := promptPathRoot
	t.Cleanup(func() { promptPathRoot = saved })
	promptPathRoot = `\\server\share\t`
	if err := checkPromptPathRoot(); err == nil || !strings.Contains(err.Error(), "UNC") {
		t.Fatalf("UNC root: err=%v, want an error naming UNC paths", err)
	}
}
