package pigletbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The script launcher is a POSIX shell script, and on Windows a cmd.exe
// batch file with CRLF line endings (D69). The Piglet path is quoted for each
// shell, including characters a batch file would otherwise expand or split on.
func TestAC2AC7SourceScriptFileAndStdoutCreateNoPigState(t *testing.T) {
	root := t.TempDir()
	pigletDir := filepath.Join(root, "team's 100% & piglets")
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
	pigHome := filepath.Join(root, "pig-home")
	t.Setenv("PIG_HOME", pigHome)
	output := filepath.Join(root, "bin", "review")
	want := "#!/bin/sh\nexec pig --piglet " + quotePOSIX(canonicalPiglet) + " \"$@\"\n"
	fakePig := filepath.Join(root, "fake-bin", "pig")
	if runtime.GOOS == "windows" {
		output += ".cmd"
		want = "@setlocal DisableDelayedExpansion\r\n@pig --piglet \"" + strings.ReplaceAll(canonicalPiglet, "%", "%%") + "\" %*\r\n"
		fakePig += ".exe"
	}
	var stdout, stderr strings.Builder
	code := runBuild([]string{pigletPath, "--format", "script", "--out", output}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Piglet script written:") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("script = %q, want %q", data, want)
	}
	info, err := os.Stat(output)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("script mode = %v, %v", info, err)
	}
	if _, err := os.Stat(pigHome); !os.IsNotExist(err) {
		t.Fatalf("script build created Pig state: %v", err)
	}

	copyTestBinary(t, fakePig)
	cwd := filepath.Join(root, "work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(output, "--model", "provider/model with space")
	command.Dir = cwd
	command.Env = append(os.Environ(), scriptEchoEnv+"=1", "PATH="+filepath.Dir(fakePig)+string(os.PathListSeparator)+os.Getenv("PATH"))
	executed, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(executed)), "\n")
	canonicalCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{canonicalCwd, "--piglet", canonicalPiglet, "--model", "provider/model with space"}
	if len(lines) != len(wantArgs) {
		t.Fatalf("executed lines = %#v", lines)
	}
	for i := range wantArgs {
		if lines[i] != wantArgs[i] {
			t.Fatalf("executed lines = %#v, want %#v", lines, wantArgs)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = runBuild([]string{pigletPath, "--format=script", "--out", "-"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || stdout.String() != want {
		t.Fatalf("stdout script: code=%d stdout=%q stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(pigHome); !os.IsNotExist(err) {
		t.Fatalf("stdout script build created Pig state: %v", err)
	}

	jsonOutput := filepath.Join(root, "bin", "review-json")
	stdout.Reset()
	stderr.Reset()
	code = runBuild([]string{pigletPath, "--format", "script", "--out", jsonOutput, "--json"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"format":"script"`) || !strings.Contains(stdout.String(), `"output":`) || strings.Contains(stdout.String(), `"artifact":`) || strings.Contains(stdout.String(), `"record":`) {
		t.Fatalf("JSON script output: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runBuild([]string{pigletPath, "--format", "script", "--out", output}, &stdout, &stderr); code == 0 {
		t.Fatal("script build overwrote an existing output")
	}
	if _, err := os.Stat(pigHome); !os.IsNotExist(err) {
		t.Fatalf("file/stdout/JSON script builds created Pig state: %v", err)
	}
	unchanged, err := os.ReadFile(output)
	if err != nil || string(unchanged) != want {
		t.Fatalf("existing script changed: %q, %v", unchanged, err)
	}
}

// Every platform's launcher bytes, on any host (D69).
func TestSourceScriptPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos, source, want string
	}{
		{"linux", "/home/me/team's piglets/review.yaml", "#!/bin/sh\nexec pig --piglet '/home/me/team'\"'\"'s piglets/review.yaml' \"$@\"\n"},
		{"darwin", "/Users/me/100% & piglets/review.yaml", "#!/bin/sh\nexec pig --piglet '/Users/me/100% & piglets/review.yaml' \"$@\"\n"},
		{"windows", `C:\Users\me\team's 100% & piglets\review.yaml`, "@setlocal DisableDelayedExpansion\r\n@pig --piglet \"C:\\Users\\me\\team's 100%% & piglets\\review.yaml\" %*\r\n"},
	} {
		if got := string(sourceScript(tc.goos, tc.source)); got != tc.want {
			t.Errorf("sourceScript(%s, %q) = %q, want %q", tc.goos, tc.source, got, tc.want)
		}
	}
}

func TestAC3ScriptBoundaries(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "review.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		json bool
	}{
		{name: "missing output", args: nil},
		{name: "targets", args: []string{"--targets", "linux/amd64"}},
		{name: "builder", args: []string{"--builder", "native"}},
		{name: "verification", args: []string{"--verification", "basic"}},
		{name: "locked", args: []string{"--locked"}},
		{name: "record", args: []string{"--record", filepath.Join(root, "record.json")}},
		{name: "json stdout conflict", args: []string{"--out", "-", "--json"}, json: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pigHome := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-"), "pig-home")
			t.Setenv("PIG_HOME", pigHome)
			output := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-")+".sh")
			args := []string{pigletPath, "--format", "script"}
			args = append(args, test.args...)
			if test.name != "missing output" && !hasBuildFlag(args, "--out") {
				args = append(args, "--out", output)
			}
			var stdout, stderr strings.Builder
			code := runBuild(args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if test.json {
				if stderr.Len() != 0 || !strings.Contains(stdout.String(), `"success":false`) {
					t.Fatalf("JSON separation: stdout=%s stderr=%s", stdout.String(), stderr.String())
				}
			} else if !strings.Contains(stderr.String(), "--format script") {
				t.Fatalf("stderr=%s", stderr.String())
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("boundary created output: %v", err)
			}
			if _, err := os.Stat(pigHome); !os.IsNotExist(err) {
				t.Fatalf("boundary created Pig state: %v", err)
			}
		})
	}
}

func TestBuildHelpDescribesReservedArtifactOptions(t *testing.T) {
	var stdout, stderr strings.Builder
	code := runBuild([]string{"--help"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"Usage:", "pig piglet build", "image is reserved and currently fails", "--locked", "currently fails", "--record <path>"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestImageFormatFailsBeforeOutput(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "review.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "review-image")
	var stdout, stderr strings.Builder
	if code := runBuild([]string{pigletPath, "--format", "image", "--out", output}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "not implemented") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("image boundary created output: %v", err)
	}
}
