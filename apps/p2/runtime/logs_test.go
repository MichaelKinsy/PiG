package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
)

// Ports apps/p2/test/p2/logs.test.ts (Pi main c23b9ec1). Forced Go forms: Pi wraps `process.stdout.write`, Go swaps the *os.File
// for a pipe whose reader tees (see CaptureProcessLogs); the child of the forked-host case is a shell. Not ported here:
// the `node:test` reporter case (Node's test runner transport has no Go counterpart: `go test` reads the test binary's stdout
// as text, and CaptureProcessLogs captures only when asked, which the first case covers), the detached preload case
// (`plugins/terminals/log-preload.ts`: the terminals plugin is not ported) and the owner-only Logs case (the logs plugin is
// not ported).

func texts(lines []LogLine) []string {
	out := []string{}
	for _, line := range lines {
		out = append(out, line.Text)
	}
	return out
}

func newStore(t *testing.T, limits LogLimits) *LogStore {
	t.Helper()
	store, err := NewLogStore(limits)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTestProcessesDoNotInstallStreamCaptureUnlessExplicitlyRequested(t *testing.T) {
	stdout, stderr := os.Stdout, os.Stderr
	stop := CaptureProcessLogs("not-installed", false)
	if os.Stdout != stdout || os.Stderr != stderr {
		t.Fatal("a test process's streams were replaced")
	}
	stop()
}

func TestCaptureTeesARedactedCopyWhilePartialWritesBinaryBytesEncodingsAndBackpressureRemainIntact(t *testing.T) {
	forward := func() (*os.File, func() []byte) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		var mu = make(chan []byte, 1)
		go func() {
			all, _ := io.ReadAll(reader)
			mu <- all
		}()
		return writer, func() []byte { _ = writer.Close(); return <-mu }
	}
	outWriter, outBytes := forward()
	errWriter, errBytes := forward()
	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWriter, errWriter
	t.Cleanup(func() { os.Stdout, os.Stderr = realOut, realErr })

	secret := "fixture-tee-secret"
	previous := ProcessLogs.Redactor
	ProcessLogs.Redactor = NewLogRedactor()
	ProcessLogs.Redactor.AddSecret(secret)
	t.Cleanup(func() { ProcessLogs.Redactor = previous })
	source := "tee-" + t.Name()
	stop := CaptureProcessLogs(source, true)
	// The writes go to the real streams as they are, partial lines included, and never wait for the copy.
	mustWrite := func(file *os.File, data []byte) {
		t.Helper()
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(os.Stdout, []byte("first-"))
	mustWrite(os.Stdout, []byte(secret+"\n"))
	binary := []byte{0, 255, 15, 128, 97, 10}
	mustWrite(os.Stdout, binary)
	mustWrite(os.Stderr, []byte{0xce, 0xa9, 0x0a})
	large := bytes.Repeat([]byte{'x'}, 512*1024)
	mustWrite(os.Stdout, large)
	stop()
	os.Stdout, os.Stderr = realOut, realErr
	if got, want := outBytes(), slices.Concat([]byte("first-"+secret+"\n"), binary, large); !bytes.Equal(got, want) {
		t.Errorf("stdout: %d bytes, want %d (the real stream is never changed)", len(got), len(want))
	}
	if got := errBytes(); !bytes.Equal(got, []byte("Ω\n")) {
		t.Errorf("stderr = %q, want Ω", got)
	}
	copied, _ := json.Marshal(ProcessLogs.Lines(source))
	if !strings.Contains(string(copied), "first-[redacted]") || strings.Contains(string(copied), secret) {
		t.Errorf("the copy = %.200s", copied)
	}
}

func TestLogsRedactProviderCredentialsProxySecretsCookiesAndBasicAuthBeforeStorageBuffersAreBounded(t *testing.T) {
	directory := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("auth.json", `{"faux":{"type":"api_key","key":"fixture-provider-key"},"other":{"type":"oauth","access":"fixture-oauth-access","refresh":"fixture-oauth-refresh"}}`)
	write("models.json", `{"providers":{"fixture":{"apiKey":"fixture-model-key"}}}`)
	write("owner-key", "fixture-owner-key")
	store := newStore(t, LogLimits{Lines: 4, Bytes: 500, Sources: 2, LineBytes: 128})
	store.Redactor.AddDirectory(directory)
	store.Redactor.AddSecret("fixture-proxy-secret")
	store.Redactor.AddSecret("http://localhost/callback?code=fixture-url-code&state=fixture-url-state#access_token=fixture-url-access")
	store.Redactor.AddSecret("  fixture-trimmed-key  ")
	leaks := func() bool {
		encoded, _ := json.Marshal(store.Lines("server"))
		return strings.Contains(string(encoded), "fixture-")
	}
	for _, secret := range []string{"fixture-provider-key", "fixture-oauth-access", "fixture-oauth-refresh", "fixture-model-key", "fixture-owner-key",
		"fixture-proxy-secret", "fixture-url-code", "fixture-url-state", "fixture-url-access", "fixture-trimmed-key"} {
		store.Append("server", LogError, "bare value "+secret)
		if leaks() {
			t.Fatalf("%s was stored", secret)
		}
	}
	for _, text := range []string{
		"Authorization: Basic Zml4dHVyZTpzZWNyZXQ=",
		"Cookie: p2-owner=fixture-cookie; session=another-cookie",
		"Set-Cookie: session=fixture-cookie; HttpOnly",
		"https://fixture-user:fixture-password@host.test/path",
		`"apiKey": "fixture-unknown-key"`,
		"token=fixture-unknown-token&next=safe",
		"Bearer fixture-bearer-token",
	} {
		store.Append("server", LogStderr, text)
		if leaks() {
			t.Fatalf("%q was stored: %v", text, texts(store.Lines("server")))
		}
	}
	write("auth.json", `{"faux":{"type":"api_key","key":"fixture-new-key"}}`)
	store.Append("server", LogError, "fixture-new-key fixture-provider-key")
	if leaks() {
		t.Fatal("rotated and prior secrets stay protected")
	}
	for i := range 20 {
		store.Append("server", LogEvent, "line-"+string(rune('a'+i)))
	}
	if got := texts(store.Lines("server")); !slices.Equal(got, []string{"line-q", "line-r", "line-s", "line-t"}) {
		t.Fatalf("the last four lines: %v", got)
	}
	store.Append("server", LogStdout, strings.Repeat("oversized", 100))
	if lines := store.Lines("server"); !strings.Contains(lines[len(lines)-1].Text, "omitted") {
		t.Fatalf("an oversized line is replaced: %v", texts(lines))
	}
	copied := store.Lines("server")
	copied[0].Text = "mutated"
	if slices.Contains(texts(store.Lines("server")), "mutated") {
		t.Fatal("Lines returns copies")
	}
	store.Append("worker:a", LogEvent, "one")
	store.Append("worker:b", LogEvent, "two")
	if got := len(store.Sources()); got != 2 {
		t.Fatalf("sources = %d, want 2", got)
	}
	if got := store.Lines("server"); len(got) != 0 {
		t.Fatalf("the least recent source is dropped: %v", texts(got))
	}
	bounded := newStore(t, LogLimits{Bytes: 10})
	for _, text := range []string{"12345", "67890", "abcde"} {
		bounded.Append("server", LogStdout, text)
	}
	if got := texts(bounded.Lines("server")); !slices.Equal(got, []string{"67890", "abcde"}) {
		t.Fatalf("bytes bound the lines: %v", got)
	}
}

func TestNoticesRedactAndBoundMessagesReplaceThePriorSourceNoticeAndTolerateMalformedUnicodeSecrets(t *testing.T) {
	store := newStore(t, LogLimits{LineBytes: 128})
	store.Redactor.AddSecret("fixture-notice-value")
	// A lone surrogate: Go has no string for it but invalid UTF-8 bytes.
	store.Redactor.AddSecret("\xed\xa0\x80")
	for i := range 60 {
		store.Notice("worker:"+string(rune('A'+i)), "fixture-notice-value")
	}
	if got := len(store.Notices()); got != 50 {
		t.Fatalf("notices = %d, want 50", got)
	}
	for _, notice := range store.Notices() {
		if notice.Message != "[redacted]" {
			t.Fatalf("notice %q", notice.Message)
		}
	}
	store.Notice("worker:"+string(rune('A'+59)), strings.Repeat("x", 1000))
	notices := store.Notices()
	if len(notices) != 50 || notices[len(notices)-1].Message != "[oversized error message omitted]" {
		t.Fatalf("the notice of a source is replaced: %d, %q", len(notices), notices[len(notices)-1].Message)
	}
	store.DismissNotice(notices[len(notices)-1].ID)
	if got := len(store.Notices()); got != 49 {
		t.Fatalf("notices after dismissing = %d, want 49", got)
	}
}

func TestLogWritersJoinSplitSecretsAndUTF8BoundPartialLinesAndFlushOnExit(t *testing.T) {
	store := newStore(t, LogLimits{})
	store.Redactor.AddSecret("fixture-split-secret")
	var forwarded []string
	writer := NewLogWriter("worker", LogStderr, store, func(line string) { forwarded = append(forwarded, line) })
	writer.WriteString("start fixture-split-")
	writer.WriteString("secret ")
	data := []byte("Ω\nlast")
	_, _ = writer.Write(data[:1])
	_, _ = writer.Write(data[1:])
	if got := texts(store.Lines("worker")); !slices.Equal(got, []string{"start [redacted] Ω"}) {
		t.Fatalf("lines = %v", got)
	}
	if !slices.Equal(forwarded, []string{"start [redacted] Ω"}) {
		t.Fatalf("the parent receives already-redacted worker lines: %v", forwarded)
	}
	writer.End()
	if lines := store.Lines("worker"); lines[len(lines)-1].Text != "last" {
		t.Fatalf("flushed on exit: %v", texts(lines))
	}
	writer.WriteString(strings.Repeat("x", 100_000))
	writer.WriteString("\nnext\n")
	lines := texts(store.Lines("worker"))
	if got := lines[len(lines)-2:]; !slices.Equal(got, []string{"[oversized log line omitted]", "next"}) {
		t.Fatalf("last two lines: %v", got)
	}
}

func TestForkedHostsCaptureStdoutAndStderrIncludingFailedStartupDiagnostics(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	store := newStore(t, LogLimits{})
	stdout, stderr := NewLogWriter("worker", LogStdout, store, nil), NewLogWriter("worker", LogStderr, store, nil)
	// As the session workers feed a worker's output to its log (Chord's createWorkers `onOutput`).
	command := exec.Command(shell, "-c", `printf 'fixture child stdout\n'; printf 'fixture child stderr\n' >&2; exit 1`)
	command.Stdout, command.Stderr = stdout, stderr
	err = command.Run()
	stdout.End()
	stderr.End()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("the child exits 1: %v", err)
	}
	got := map[LogLevel]string{}
	for _, line := range store.Lines("worker") {
		got[line.Level] = line.Text
	}
	if got[LogStdout] != "fixture child stdout" || got[LogStderr] != "fixture child stderr" || len(store.Lines("worker")) != 2 {
		t.Fatalf("lines = %+v", store.Lines("worker"))
	}
}

// A worker's stacks are redacted line by line: a redaction of a line that holds no secret must not pay for the secrets or the
// environment again (it matched every environment name, and sorted and percent-encoded every secret, per line: 230 allocations,
// which starved the report of a failed test of its worker's stacks when the machine was loaded).
func TestRedactingALineWithNoSecretDoesNotRebuildTheSecretsEachTime(t *testing.T) {
	for index := range 30 {
		t.Setenv("P2_REDACT_TOKEN_"+strings.Repeat("x", index), "environment-secret-value-"+strings.Repeat("y", index))
	}
	redactor := NewLogRedactor()
	for index := range 40 {
		redactor.AddSecret(strings.Repeat("s", 10+index) + " x")
	}
	line := "\t/home/user/p2/plugins/prompt/worker.go:192 +0x829"
	if got := redactor.Redact(line); got != line {
		t.Fatalf("Redact changed a line without secrets: %q", got)
	}
	allocations := testing.AllocsPerRun(50, func() { redactor.Redact(line) })
	if allocations > 60 {
		t.Fatalf("Redact allocated %v times for a line without secrets (the secrets and the environment are rebuilt per call)", allocations)
	}
}

func TestRedactionSeesAnEnvironmentSecretSetAfterTheFirstRedaction(t *testing.T) {
	redactor := NewLogRedactor()
	redactor.Redact("warm")
	t.Setenv("P2_LATE_TOKEN", "late secret value")
	if got := redactor.Redact("a late secret value b"); strings.Contains(got, "late secret value") {
		t.Fatalf("an environment secret set after the first redaction was printed: %q", got)
	}
	redactor.AddSecret("a b&c")
	if got := redactor.Redact("x a%20b%26c y"); strings.Contains(got, "a%20b%26c") {
		t.Fatalf("the percent-encoded form of a secret was printed: %q", got)
	}
}

// The detached host's last snapshot is the one it exits with: the startup snapshot's writer (a goroutine of its own) must not
// rename its older content over the fatal error's (the host exits right after Fatal, as Pi's exit hook leaves it the last
// writer). Seen as an empty snapshot of a failed host under load (e2e TestDetachedHostStoresFatalStartupErrorsRedacted).
func TestFatalLeavesTheFatalErrorInTheLastSnapshotWhateverTheStartupWriteDoes(t *testing.T) {
	for i := range 100 {
		directory := t.TempDir()
		source := "fatal-order-" + strings.Repeat("x", i%7) + string(rune('a'+i%26))
		published := CapturePublishedLogs(source, directory, true)
		published.Fatal(os.ErrInvalid, nil)
		text, err := os.ReadFile(filepath.Join(directory, source+".logs.json"))
		if err != nil {
			t.Fatal(err)
		}
		// A late writer renames after Fatal returns, as the exiting host would not wait for it.
		for range 3 {
			goruntime.Gosched()
		}
		again, _ := os.ReadFile(filepath.Join(directory, source+".logs.json"))
		if !strings.Contains(string(text), "invalid argument") || !strings.Contains(string(again), "invalid argument") {
			t.Fatalf("round %d: the last snapshot lacks the fatal error: %s then %s", i, text, again)
		}
	}
}
