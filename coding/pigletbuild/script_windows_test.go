//go:build windows

package pigletbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A cmd.exe started with delayed expansion (/v:on) expands !NAME! in the
// lines of a batch file it runs, even inside quotes. The launcher turns it off
// for itself, so a legal path such as team!TOKEN! reaches pig unchanged.
func TestWindowsScriptKeepsBangsUnderDelayedExpansion(t *testing.T) {
	root := t.TempDir()
	pigletDir := filepath.Join(root, "team!TOKEN!")
	if err := os.MkdirAll(pigletDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(pigletDir, "review.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalPiglet, err := filepath.EvalSymlinks(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	output := filepath.Join(root, "bin", "review.cmd")
	var stdout, stderr strings.Builder
	if code := runBuild([]string{pigletPath, "--format", "script", "--out", output}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	fakePig := filepath.Join(root, "fake-bin", "pig.exe")
	copyTestBinary(t, fakePig)

	command := exec.Command("cmd.exe")
	// cmd.exe strips the outer quotes of the /c string and runs the rest.
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /v:on /c ""` + output + `" plain"`}
	command.Dir = root
	command.Env = append(os.Environ(), scriptEchoEnv+"=1", "TOKEN=EXPANDED",
		"PATH="+filepath.Dir(fakePig)+string(os.PathListSeparator)+os.Getenv("PATH"))
	executed, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(executed), "\r\n", "\n")), "\n")
	if want := []string{"--piglet", canonicalPiglet, "plain"}; len(lines) != 4 || lines[1] != want[0] || lines[2] != want[1] || lines[3] != want[2] {
		t.Fatalf("pig received %#v, want working directory then %#v", lines, want)
	}
}
