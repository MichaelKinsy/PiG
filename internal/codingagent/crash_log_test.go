package codingagent

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
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestFindExtensionStackMatches(t *testing.T) {
	packageExtension := func(source, baseDir, entry string) ExtensionStackMetadata {
		resolvedPath := strings.TrimRight(strings.ReplaceAll(baseDir, `\`, "/"), "/") + "/" + entry
		return ExtensionStackMetadata{
			Path:         resolvedPath,
			ResolvedPath: resolvedPath,
			SourceInfo: ResourceSourceInfo{
				Path: resolvedPath, Source: source, Scope: "user", Origin: "package", BaseDir: baseDir,
			},
		}
	}

	t.Run("matches stack frames beneath loaded package roots", func(t *testing.T) {
		memory := packageExtension("npm:pi-observational-memory", "/home/fedora/.pi/agent/npm/node_modules/pi-observational-memory", "extensions/index.ts")
		unrelated := packageExtension("npm:unrelated", "/home/fedora/.pi/agent/npm/node_modules/unrelated", "extensions/index.ts")
		stack := "TypeError: Cannot read properties of undefined (reading 'runtime')\n" +
			"    at streamSimple (file:///home/fedora/.local/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/chunks/chunk-CMRUVXTE.js:1093:16944)\n" +
			"    at /home/fedora/.pi/agent/npm/node_modules/pi-observational-memory/src/agents/worker-stream.ts:43:45"
		if got := FindExtensionStackMatches(stack, []ExtensionStackMetadata{memory, unrelated}); !slices.Equal(got, []string{"npm:pi-observational-memory"}) {
			t.Fatalf("matches = %q", got)
		}
	})

	t.Run("normalizes Windows paths and deduplicates package extensions", func(t *testing.T) {
		root := `C:\Users\reporter\.pi\agent\npm\node_modules\@scope\memory`
		first := packageExtension("npm:@scope/memory", root, "extensions/first.ts")
		second := packageExtension("npm:@scope/memory", root, "extensions/second.ts")
		stack := "Error: broken\n    at run (c:\\users\\reporter\\.pi\\agent\\npm\\node_modules\\@scope\\memory\\src\\worker.ts:4:2)"
		if got := FindExtensionStackMatches(stack, []ExtensionStackMetadata{first, second}); !slices.Equal(got, []string{"npm:@scope/memory"}) {
			t.Fatalf("matches = %q", got)
		}
	})

	t.Run("does not attribute sibling single-file packages", func(t *testing.T) {
		extension := func(name string) ExtensionStackMetadata {
			path := "/plugins/" + name + ".ts"
			return ExtensionStackMetadata{Path: path, ResolvedPath: path, SourceInfo: ResourceSourceInfo{Path: path, Source: path, Scope: "user", Origin: "package", BaseDir: "/plugins"}}
		}
		stack := "Error: broken\n    at run (file:///plugins/b.ts:4:2)"
		if got := FindExtensionStackMatches(stack, []ExtensionStackMetadata{extension("a"), extension("b")}); !slices.Equal(got, []string{"/plugins/b.ts"}) {
			t.Fatalf("matches = %q", got)
		}
	})

	t.Run("ignores extension paths in the error message", func(t *testing.T) {
		extension := packageExtension("npm:memory", "/tmp/node_modules/memory", "extensions/index.ts")
		stack := "Error: Failed to read " + extension.ResolvedPath + "\n    at run (file:///opt/pi/dist/core.js:4:2)"
		if got := FindExtensionStackMatches(stack, []ExtensionStackMetadata{extension}); len(got) != 0 {
			t.Fatalf("matches = %q", got)
		}
	})

	t.Run("decodes frame paths independently from malformed error text", func(t *testing.T) {
		path := "/Users/reporter/.pi/agent/extensions/local memory/index.ts"
		extension := ExtensionStackMetadata{Path: path, ResolvedPath: path, SourceInfo: ResourceSourceInfo{Path: path, Source: "local", Scope: "user", Origin: "top-level", BaseDir: "/Users/reporter/.pi/agent/extensions"}}
		stack := "Error: progress 100%\n    at run (file:///Users/reporter/.pi/agent/extensions/local%20memory/worker.ts:4:2)"
		if got := FindExtensionStackMatches(stack, []ExtensionStackMetadata{extension}); !slices.Equal(got, []string{path}) {
			t.Fatalf("matches = %q", got)
		}
	})
}

// TestCrashAttributionFromSacrificialProcess proves the matcher accepts real Go
// panic source frames. It does not claim recover intercepts runtime.throw or
// other unrecoverable Go runtime fatals.
func TestCrashAttributionFromSacrificialProcess(t *testing.T) {
	const helperEnv = "PIG_TEST_SACRIFICIAL_CRASH"
	if os.Getenv(helperEnv) == "1" {
		panic("sacrificial crash")
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCrashAttributionFromSacrificialProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("sacrificial process did not crash")
	}
	stack := stderr.String()
	if !strings.Contains(stack, "panic: sacrificial crash") {
		t.Fatalf("child stderr is not a panic stack:\n%s", stack)
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not report this test file")
	}
	root := filepath.Dir(file)
	entry := filepath.Join(root, "index.go")
	metadata := ExtensionStackMetadata{
		Path:         entry,
		ResolvedPath: entry,
		SourceInfo: ResourceSourceInfo{
			Path: entry, ResourceType: "extensions", Enabled: true, Scope: "user", Origin: "package", Source: "test:internal/codingagent", BaseDir: root,
		},
	}
	if got := FindExtensionStackMatches(stack, []ExtensionStackMetadata{metadata}); !slices.Equal(got, []string{"test:internal/codingagent"}) {
		t.Fatalf("matches = %q\nstack:\n%s", got, stack)
	}
}

func TestFormatCrashExtensionHint(t *testing.T) {
	tests := []struct {
		matches []string
		want    string
	}{
		{nil, ""},
		{[]string{""}, ""},
		{[]string{"npm:memory"}, "A stack frame came from loaded extension `npm:memory`, which may be involved. Try disabling it with `pig config`, or run `pig -ne` to confirm."},
		{[]string{"one", "two"}, "A stack frame came from loaded extensions `one` and `two`, which may be involved. Try disabling them with `pig config`, or run `pig -ne` to confirm."},
		{[]string{"one", "two", "three"}, "A stack frame came from loaded extensions `one`, `two`, and `three`, which may be involved. Try disabling them with `pig config`, or run `pig -ne` to confirm."},
	}
	for _, test := range tests {
		if got := FormatCrashExtensionHint(test.matches); got != test.want {
			t.Errorf("FormatCrashExtensionHint(%q) = %q, want %q", test.matches, got, test.want)
		}
	}
}

func crashInput(message string) CrashInput {
	return CrashInput{Kind: "uncaught_exception", Message: message, Stack: "stack of " + message, SessionFile: "/s/session.jsonl", CWD: "/work", Version: "1.2.3"}
}

// Mirrors upstream recordCrash: pretty JSON with a trailing newline, null for
// a missing stack or session file, and only the newest five records kept.
func TestRecordCrashKeepsTheNewestFive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent", "crashes.json")
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	for i := range 6 {
		if _, ok := RecordCrash(crashInput(string(rune('a'+i))), path, now.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("record %d failed", i)
		}
	}
	records := ReadCrashLog(path)
	if len(records) != 5 || records[0].Message != "b" || records[4].Message != "f" {
		t.Fatalf("records = %+v", records)
	}
	first := records[0]
	if first.Timestamp != "2026-09-23T08:00:01.000Z" || first.Version != "1.2.3" || first.Kind != "uncaught_exception" || first.CWD != "/work" || first.Stack == nil || *first.Stack != "stack of b" || first.SessionFile == nil {
		t.Fatalf("record = %+v", first)
	}
	record, ok := RecordCrash(CrashInput{Kind: "fatal_error", Message: "bare", CWD: "/w"}, path, now)
	if !ok || record.Stack != nil || record.SessionFile != nil {
		t.Fatalf("bare record = %+v %v", record, ok)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "[\n  {\n    \"timestamp\": ") || !strings.HasSuffix(string(data), "]\n") || !strings.Contains(string(data), `"stack": null,`) || !strings.Contains(string(data), `"sessionFile": null,`) {
		t.Fatalf("crash log format:\n%s", data)
	}
}

// Mirrors upstream readCrashLog: a missing or malformed file reads as empty,
// and records without a string timestamp and message are dropped.
func TestReadCrashLogFiltersInvalidRecords(t *testing.T) {
	dir := t.TempDir()
	if got := ReadCrashLog(filepath.Join(dir, "missing.json")); len(got) != 0 {
		t.Fatalf("missing = %v", got)
	}
	path := filepath.Join(dir, "crashes.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadCrashLog(path); len(got) != 0 {
		t.Fatalf("malformed = %v", got)
	}
	if err := os.WriteFile(path, []byte(`[1, null, {"timestamp": 5, "message": "x"}, {"timestamp": "2026-01-01T00:00:00.000Z"}, {"timestamp": "2026-01-01T00:00:00.000Z", "message": "ok", "cwd": "/"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadCrashLog(path); len(got) != 1 || got[0].Message != "ok" {
		t.Fatalf("filtered = %+v", got)
	}
}

// Mirrors upstream takeUnnotifiedCrash: the newest pending crash from the last
// seven days is returned once, and every record is marked announced.
func TestTakeUnnotifiedCrashAnnouncesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crashes.json")
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	RecordCrash(crashInput("stale"), path, now.Add(-8*24*time.Hour))
	RecordCrash(crashInput("older"), path, now.Add(-2*time.Hour))
	RecordCrash(crashInput("newest"), path, now.Add(-time.Hour))
	crash, ok := TakeUnnotifiedCrash(path, now)
	if !ok || crash.Message != "newest" {
		t.Fatalf("crash = %+v %v", crash, ok)
	}
	for _, record := range ReadCrashLog(path) {
		if !record.Notified {
			t.Fatalf("record %q not marked notified", record.Message)
		}
	}
	if crash, ok := TakeUnnotifiedCrash(path, now); ok {
		t.Fatalf("announced twice: %+v", crash)
	}
	onlyStale := filepath.Join(t.TempDir(), "crashes.json")
	RecordCrash(crashInput("stale"), onlyStale, now.Add(-8*24*time.Hour))
	if _, ok := TakeUnnotifiedCrash(onlyStale, now); ok {
		t.Fatal("a crash older than seven days was announced")
	}
	ClearCrashLog(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("crash log not cleared: %v", err)
	}
}

func TestCrashMessagesMatchUpstream(t *testing.T) {
	at := time.Date(2026, 9, 23, 15, 4, 5, 0, time.Local)
	notice := crashNotice(CrashRecord{Timestamp: isoTimestamp(at), Message: "boom"})
	if want := "pig crashed on 9/23/2026, 3:04:05 PM (boom). Run /bug to report it; the crash details are attached automatically."; notice != want {
		t.Fatalf("notice = %q, want %q", notice, want)
	}
	if got := crashReportInstructions("/s.jsonl"); got != "To report this crash: run `pig -r` to resume the session, then run /bug. The crash details are attached automatically." {
		t.Fatalf("instructions = %q", got)
	}
	if got := crashReportInstructions(""); got != "To report this crash: start pig and run /bug. The crash details are attached automatically." {
		t.Fatalf("instructions = %q", got)
	}
	if crashMessage(errors.New("bad")) != "bad" || crashMessage("text") != "text" || crashMessage(errors.New("")) != "*errors.errorString" {
		t.Fatal("crashMessage does not mirror error.message || error.name")
	}
}

// Upstream uncaughtCrash prints the crash, records it, and prints the /bug
// instructions only when the record was written.
func TestUncaughtCrashPrintsRecordsAndInstructs(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	agentDir := t.TempDir()
	m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9"})
	var stderr strings.Builder
	m.uncaughtCrash(errors.New("kaboom"), []byte("goroutine 1 [running]:\n"), &stderr)
	out := stderr.String()
	if !strings.HasPrefix(out, "pig exiting due to uncaughtException:\nkaboom\ngoroutine 1 [running]:\n") || !strings.HasSuffix(out, "start pig and run /bug. The crash details are attached automatically.\n") {
		t.Fatalf("stderr = %q", out)
	}
	records := ReadCrashLog(CrashLogPath(agentDir))
	if len(records) != 1 || records[0].Message != "kaboom" || records[0].Version != "9.9.9" || records[0].CWD != "/work" || records[0].Kind != "uncaught_exception" {
		t.Fatalf("records = %+v", records)
	}

	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	unwritable := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: filepath.Join(blocker, "agent")})
	stderr.Reset()
	unwritable.uncaughtCrash("x", nil, &stderr)
	if strings.Contains(stderr.String(), "To report this crash") {
		t.Fatalf("instructions printed without a record: %q", stderr.String())
	}
}

func TestUncaughtCrashAttributesLoadedExtension(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "memory")
	entry := filepath.Join(root, "index.ts")
	m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: t.TempDir(), AppVersion: "9.9.9"})
	m.resourceSourceInfo[entry] = ResourceSourceInfo{
		Path: entry, ResourceType: "extensions", Enabled: true, Scope: "user", Origin: "package", Source: "npm:memory", BaseDir: root,
	}
	stack := []byte("goroutine 1 [running]:\nexample.com/memory.run()\n\t" + filepath.Join(root, "src", "worker.ts") + ":43")
	var stderr strings.Builder
	m.uncaughtCrash(errors.New("kaboom"), stack, &stderr)
	if want := "A stack frame came from loaded extension `npm:memory`, which may be involved."; !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
	}
}

func TestHandleFatalRuntimeErrorRecordsAndRequestsFailureExit(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	m, _ := newExtensionDialogProbe(t)
	m.opts.AgentDir = t.TempDir()
	m.opts.AppVersion = "fatal-test"
	m.opts.CWD = "/work"
	if err := m.handleFatalRuntimeError("Failed to create session", errors.New("disk exploded")); !errors.Is(err, ErrInteractiveCrashed) {
		t.Fatalf("handleFatalRuntimeError = %v, want ErrInteractiveCrashed", err)
	}
	if !m.requestExit.Load() || !m.fatalRuntime.Load() {
		t.Fatalf("exit state request=%v fatal=%v", m.requestExit.Load(), m.fatalRuntime.Load())
	}
	rendered := stripANSITest(strings.Join(m.chatContainer.Render(200), "\n"))
	for _, want := range []string{"Error: Failed to create session: disk exploded", "To report this crash:", "run /bug"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("chat missing %q:\n%s", want, rendered)
		}
	}
	records := ReadCrashLog(CrashLogPath(m.opts.AgentDir))
	if len(records) != 1 || records[0].Kind != "fatal_error" || records[0].Message != "disk exploded" || records[0].Stack == nil {
		t.Fatalf("records = %+v", records)
	}
	if err := m.requestedExitError(); !errors.Is(err, ErrInteractiveCrashed) {
		t.Fatalf("requestedExitError = %v", err)
	}
}

func TestFatalSessionReplacementHandlersUseCrashPath(t *testing.T) {
	boom := errors.New("replacement failed")
	var prefixes []string
	fatal := func(prefix string, err error) error {
		if !errors.Is(err, boom) {
			t.Fatalf("fatal error = %v", err)
		}
		prefixes = append(prefixes, prefix)
		return ErrInteractiveCrashed
	}
	noop := func(string) {}
	tests := []struct {
		name string
		want string
		run  func(*SlashContext) error
	}{
		{name: "create", want: "Failed to create session", run: func(sc *SlashContext) error {
			sc.NewSession = func() error { return boom }
			return newHandler(sc)
		}},
		{name: "resume", want: "Failed to resume session", run: func(sc *SlashContext) error {
			sc.PickSession = func() (string, bool) { return "/session.jsonl", true }
			sc.LoadSessionPath = func(string) error { return boom }
			return resumeHandler(sc)
		}},
		{name: "import", want: "Failed to import session", run: func(sc *SlashContext) error {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			sc.Args = path
			sc.LoadSessionPath = func(string) error { return boom }
			return importHandler(sc)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefixes = nil
			sc := &SlashContext{Append: noop, FatalRuntimeError: fatal}
			if err := test.run(sc); !errors.Is(err, ErrInteractiveCrashed) {
				t.Fatalf("handler = %v", err)
			}
			if !slices.Equal(prefixes, []string{test.want}) {
				t.Fatalf("prefixes = %q", prefixes)
			}
		})
	}
}

// Upstream's built-in /fork catches runtimeHost.fork errors with showError and
// keeps running; only extension commandContextActions.fork is process-fatal.
func TestReviewBuiltinForkFailureIsNotFatal(t *testing.T) {
	boom := errors.New("fork failed")
	called := false
	sc := &SlashContext{
		Append: func(string) {},
		Args:   "entry",
		FatalRuntimeError: func(string, error) error {
			called = true
			return ErrInteractiveCrashed
		},
		ForkToNewSession: func(string) error { return boom },
	}
	err := forkHandler(sc)
	if called || errors.Is(err, ErrInteractiveCrashed) {
		t.Fatalf("built-in /fork failure took the fatal crash path (err=%v); upstream shows a non-fatal error", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("forkHandler = %v, want original error", err)
	}
}

func TestShowCrashNoticeAnnouncesPendingCrash(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	m, _ := newExtensionDialogProbe(t)
	m.opts.AgentDir = t.TempDir()
	RecordCrash(crashInput("startup-boom"), CrashLogPath(m.opts.AgentDir), time.Now())
	m.showCrashNotice()
	rendered := stripANSITest(strings.Join(m.chatContainer.Render(200), "\n"))
	if !strings.Contains(rendered, "pig crashed on ") || !strings.Contains(rendered, "(startup-boom). Run /bug to report it") {
		t.Fatalf("chat = %q", rendered)
	}
}

// Upstream /bug attaches the crash log without the notified flag and clears
// it once the report is recorded.
func TestBugReportAttachesAndClearsTheCrashLog(t *testing.T) {
	dir := chdirTemp(t)
	agentDir := t.TempDir()
	logPath := CrashLogPath(agentDir)
	RecordCrash(crashInput("reported"), logPath, time.Now())
	TakeUnnotifiedCrash(logPath, time.Now())
	script := &bugDialogScript{t: t, editor: "", choices: []string{"No", "No", bugReportExport}}
	sc := script.context(bugReportSession(t))
	sc.AgentDir = agentDir
	if err := bugHandler(sc); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "pig-bug-report-*.zip"))
	if len(matches) != 1 {
		t.Fatalf("archives = %v", matches)
	}
	raw := readZip(t, matches[0])["diagnostics.json"]
	var diagnostics struct {
		Crashes []map[string]any `json:"crashes"`
	}
	if err := json.Unmarshal(raw, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if len(diagnostics.Crashes) != 1 || diagnostics.Crashes[0]["message"] != "reported" {
		t.Fatalf("crashes = %v", diagnostics.Crashes)
	}
	if _, present := diagnostics.Crashes[0]["notified"]; present {
		t.Fatalf("crash carries notified: %v", diagnostics.Crashes[0])
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("crash log survives the report: %v", err)
	}
}

// Run recovers a crash on its own goroutine, records it, and reports
// ErrInteractiveCrashed so the caller exits 1 without a second message.
func TestRunRecordsACrashAndReportsErrInteractiveCrashed(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	agentDir := t.TempDir()
	m := NewInteractiveMode(InteractiveOptions{
		CWD:         t.TempDir(),
		AgentDir:    agentDir,
		AppVersion:  "run-crash",
		NoThemes:    true,
		StartupMark: func(string) { panic("startup mark exploded") },
	})
	if err := m.Run(t.Context()); !errors.Is(err, ErrInteractiveCrashed) {
		t.Fatalf("Run = %v, want ErrInteractiveCrashed", err)
	}
	records := ReadCrashLog(CrashLogPath(agentDir))
	if len(records) != 1 || records[0].Message != "startup mark exploded" || records[0].Stack == nil || !strings.Contains(*records[0].Stack, "goroutine") {
		t.Fatalf("records = %+v", records)
	}
}

// A renderer overflow is upstream's thrown Error: Node prints it as
// "Error: <message>", and the crash record keeps the bare message.
func TestUncaughtCrashPrintsRenderOverflowAsError(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	agentDir := t.TempDir()
	m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9"})
	overflow := &tui.RenderOverflowError{Line: 3, LineWidth: 126, TerminalWidth: 100, LogPath: filepath.Join(agentDir, tui.TUICrashLogName)}
	var stderr strings.Builder
	m.uncaughtCrash(overflow, []byte("goroutine 1 [running]:\n"), &stderr)
	want := "pig exiting due to uncaughtException:\nError: Rendered line 3 exceeds terminal width (126 > 100).\n\n" +
		"This is likely caused by a custom TUI component not truncating its output.\n" +
		"Use visibleWidth() to measure and truncateToWidth() to truncate lines.\n\n" +
		"Debug log written to: " + overflow.LogPath + "\ngoroutine 1 [running]:\n"
	if out := stderr.String(); !strings.HasPrefix(out, want) {
		t.Fatalf("stderr = %q, want prefix %q", out, want)
	}
	records := ReadCrashLog(CrashLogPath(agentDir))
	if len(records) != 1 || records[0].Message != overflow.Error() || records[0].Kind != "uncaught_exception" {
		t.Fatalf("records = %+v", records)
	}
}

// An overflow recovered off the owner loop is re-raised on it, where Run's
// uncaughtException handler reports it. Other panics propagate unchanged.
func TestForwardRenderCrashReraisesOverflowOnOwnerLoop(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: t.TempDir()})
	overflow := &tui.RenderOverflowError{Line: 1, LineWidth: 30, TerminalWidth: 20, LogPath: "/x"}
	m.forwardRenderCrash(overflow)
	var task func()
	select {
	case task = <-m.uiTaskCh:
	case <-time.After(5 * time.Second):
		t.Fatal("overflow was not posted to the owner loop")
	}
	raised := func() (value any) {
		defer func() { value = recover() }()
		task()
		return nil
	}()
	if raised != overflow {
		t.Fatalf("owner loop raised %v, want the overflow", raised)
	}
	other := errors.New("other")
	propagated := func() (value any) {
		defer func() { value = recover() }()
		m.forwardRenderCrash(other)
		return nil
	}()
	if propagated != other { //nolint:errorlint // identity: the recovered panic value itself, not an error chain
		t.Fatalf("non-overflow panic = %v, want it re-panicked", propagated)
	}
}
