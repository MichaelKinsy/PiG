package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Pi main.ts:251-255 resolves a --session argument that contains a path
// separator or ends in .jsonl with resolvePath(arg, cwd) (utils/paths.ts:77-106),
// whether or not the file exists: Git Bash drive paths are converted on
// Windows, a leading ~ expands to the home directory, file URLs become paths,
// and Node path.resolve handles rooted and drive-relative Windows paths.
// SessionManager.open (session-manager.ts:1763-1782) then keeps the launch cwd
// and the file's directory, and _setSessionFile (session-manager.ts:1050-1054)
// starts a new session that persists at the explicit path.
func TestResolveStartupSessionSelectionOpensMissingSessionPath(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	launchCWD := t.TempDir()
	base := canonicalStartupDir(launchCWD)
	absolute := filepath.Join(t.TempDir(), "absolute.jsonl")
	fileURLTarget := filepath.Join(t.TempDir(), "file url.jsonl")
	type pathCase struct {
		arg  string
		want string
	}
	cases := []pathCase{
		{arg: absolute, want: absolute},
		{arg: filepath.Join("nested", "relative.jsonl"), want: filepath.Join(base, "nested", "relative.jsonl")},
		{arg: "bare.jsonl", want: filepath.Join(base, "bare.jsonl")},
		{arg: "~/tilde.jsonl", want: filepath.Join(home, "tilde.jsonl")},
		{arg: "file:///" + strings.TrimPrefix(strings.ReplaceAll(filepath.ToSlash(fileURLTarget), " ", "%20"), "/"), want: fileURLTarget},
	}
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(base)
		processCWD, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		msysTarget := filepath.Join(t.TempDir(), "msys.jsonl")
		msysVolume := filepath.VolumeName(msysTarget)
		cases = append(cases,
			pathCase{arg: `~\tilde-backslash.jsonl`, want: filepath.Join(home, "tilde-backslash.jsonl")},
			// Node resolves a rooted path without a drive on the process cwd's drive.
			pathCase{arg: `\pig-rooted-session.jsonl`, want: filepath.VolumeName(processCWD) + `\pig-rooted-session.jsonl`},
			// A drive-relative path on the launch drive resolves against the launch cwd.
			pathCase{arg: volume + "drive-relative.jsonl", want: filepath.Join(base, "drive-relative.jsonl")},
			pathCase{arg: "/" + strings.ToLower(msysVolume[:1]) + filepath.ToSlash(msysTarget[len(msysVolume):]), want: msysTarget},
		)
	}
	for _, tc := range cases {
		got, err := resolveStartupSessionSelection(CLIFlags{Session: tc.arg}, launchCWD, "")
		if err != nil {
			t.Fatalf("--session %s: %v", tc.arg, err)
		}
		if got.resumePath != tc.want {
			t.Fatalf("--session %s: resumePath = %q, want %q", tc.arg, got.resumePath, tc.want)
		}
		if got.runtimeCWD != base {
			t.Fatalf("--session %s: runtimeCWD = %q, want %q", tc.arg, got.runtimeCWD, base)
		}
		if got.sessionDir != filepath.Dir(tc.want) {
			t.Fatalf("--session %s: sessionDir = %q, want %q", tc.arg, got.sessionDir, filepath.Dir(tc.want))
		}
		if _, err := os.Stat(tc.want); !os.IsNotExist(err) {
			t.Fatalf("--session %s: selection created the file early: %v", tc.arg, err)
		}
	}
}

// Pi's resolveSessionPath lets fileURLToPath throw, and main does not catch it:
// Node reports the uncaught error headed by its name, code and message, and
// exits 1. --fork resolves its argument the same way.
func TestResolveStartupSessionSelectionReportsFileURLError(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	want := "TypeError [ERR_INVALID_FILE_URL_PATH]: File URL path must not include encoded / characters"
	if runtime.GOOS == "windows" {
		want = `TypeError [ERR_INVALID_FILE_URL_PATH]: File URL path must not include encoded \ or / characters`
	}
	for _, flags := range []CLIFlags{{Session: "file:///a%2Fb.jsonl"}, {Fork: "file:///a%2Fb.jsonl"}} {
		_, err := resolveStartupSessionSelection(flags, t.TempDir(), t.TempDir())
		if err == nil {
			t.Fatalf("flags=%+v: no error", flags)
		}
		if got := formatStartupSessionError(err, true); got != want {
			t.Fatalf("flags=%+v: error = %q, want %q", flags, got, want)
		}
	}
}

// `pi -p --session <new file>` runs and persists that session at the given
// path. Compare the persisted record sequence with the pinned Pi 0.87.1.
func TestPrintSessionPathCreatesSessionLikePi(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	piRoot, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "1.0.3" {
		t.Fatalf("Pi version=%q", pkg.Version)
	}
	fauxProvider, err := filepath.Abs("../../test/parity/testdata/test-faux-provider.ts")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	run := func(command string, argv []string, env []string) []string {
		t.Helper()
		home, cwd := t.TempDir(), t.TempDir()
		sessionPath := filepath.Join(t.TempDir(), "explicit.jsonl")
		argv = append(argv, "--model", "test-faux/faux-1", "--no-context-files", "--no-skills", "--session", sessionPath, "-p", "What is 20+22?")
		cmd := exec.CommandContext(t.Context(), command, argv...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), append([]string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "pig"), "PI_CODING_AGENT_DIR=" + filepath.Join(home, "pi")}, env...)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\nstdout=%s\nstderr=%s", filepath.Base(command), err, stdout.String(), stderr.String())
		}
		if stdout.String() != "42\n" {
			t.Fatalf("%s stdout = %q, want %q", filepath.Base(command), stdout.String(), "42\n")
		}
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			t.Fatalf("%s did not persist --session %s: %v", filepath.Base(command), sessionPath, err)
		}
		var records []string
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			var record struct {
				Type    string `json:"type"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatalf("%s session line %q: %v", filepath.Base(command), line, err)
			}
			if record.Type == "message" {
				record.Type += ":" + record.Message.Role
			}
			records = append(records, record.Type)
		}
		return records
	}
	pi := run(node, []string{filepath.Join(piRoot, "dist", "cli.js"), "-e", fauxProvider}, nil)
	if len(pi) == 0 || pi[0] != "session" || !slices.Contains(pi, "message:assistant") {
		t.Fatalf("Pi session records = %v", pi)
	}
	pig := run(binary, nil, []string{"PIG_TEST_FAUX=1"})
	if !slices.Equal(pig, pi) {
		t.Fatalf("pig session records = %v, Pi = %v", pig, pi)
	}
}

// The CLI reports the uncaught file URL error with the line Node prints for
// Pi, and exits 1 before any session starts.
func TestSessionFileURLErrorComparedWithPi(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	piCLI, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/cli.js")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	run := func(command string, argv []string) (string, string, int) {
		t.Helper()
		home := t.TempDir()
		cmd := exec.CommandContext(t.Context(), command, append(argv, "--session", "file:///a%2Fb.jsonl", "-p", "hi")...)
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "pig"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "pi"), "PIG_OFFLINE=1", "PI_OFFLINE=1")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		return stdout.String(), stderr.String(), code
	}
	piOut, piErr, piCode := run(node, []string{piCLI})
	// Node writes its uncaught-error report with CRLF line ends on Windows.
	piErr = strings.ReplaceAll(piErr, "\r\n", "\n")
	pigOut, pigErr, pigCode := run(binary, nil)
	line := strings.TrimSuffix(pigErr, "\n")
	if pigOut != piOut || pigCode != piCode || piCode != 1 || strings.Contains(line, "\n") || !strings.Contains(piErr, "\n"+line+"\n") {
		t.Fatalf("pig stdout=%q stderr=%q exit=%d; Pi stdout=%q stderr=%q exit=%d", pigOut, pigErr, pigCode, piOut, piErr, piCode)
	}
}

// Pi main.ts createSessionManager hands a --fork path argument to
// SessionManager.forkFrom without requiring the file (main.ts:374-383).
// forkFrom reads the source with loadEntriesFromFile, which reads a missing,
// empty, or headerless file as no entries, and throws "Cannot fork: source
// session file is empty or invalid: <path>" (session-manager.ts:1818-1823);
// forkSessionOrExit prints it as "Error: <message>" and exits 1.
func TestForkMissingOrInvalidSessionPathComparedWithPi(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	piCLI, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/cli.js")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	run := func(command string, argv []string, arg string, setup func(cwd string)) (string, string, int) {
		t.Helper()
		home, cwd := t.TempDir(), t.TempDir()
		setup(cwd)
		cmd := exec.CommandContext(t.Context(), command, append(argv, "--fork", arg, "-p", "hi")...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "pig"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "pi"), "PIG_OFFLINE=1", "PI_OFFLINE=1")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		// pig names the canonical path and Node keeps cwd as given; on Windows they differ when t.TempDir is an 8.3 short path.
		return stdout.String(), strings.ReplaceAll(strings.ReplaceAll(stderr.String(), canonicalTestPath(t, cwd), "<cwd>"), cwd, "<cwd>"), code
	}
	cases := []struct {
		name  string
		setup func(cwd string)
	}{
		{"missing", func(string) {}},
		{"empty", func(cwd string) {
			if err := os.WriteFile(filepath.Join(cwd, "source.jsonl"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"headerless", func(cwd string) {
			if err := os.WriteFile(filepath.Join(cwd, "source.jsonl"), []byte(`{"type":"message","id":"a"}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			piOut, piErr, piCode := run(node, []string{piCLI}, "source.jsonl", tc.setup)
			pigOut, pigErr, pigCode := run(binary, nil, "source.jsonl", tc.setup)
			want := "Error: Cannot fork: source session file is empty or invalid: <cwd>" + string(filepath.Separator) + "source.jsonl\n"
			if piErr != want || piCode != 1 {
				t.Fatalf("Pi stderr=%q exit=%d; want %q exit 1", piErr, piCode, want)
			}
			if pigOut != piOut || pigErr != piErr || pigCode != piCode {
				t.Fatalf("pig stdout=%q stderr=%q exit=%d; Pi stdout=%q stderr=%q exit=%d", pigOut, pigErr, pigCode, piOut, piErr, piCode)
			}
		})
	}
}
