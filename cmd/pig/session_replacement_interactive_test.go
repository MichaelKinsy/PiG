//go:build linux

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// waitReplaceLog polls the fixture log until it holds at least n lines.
func waitReplaceLog(t *testing.T, path string, n int, what string) []string {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if data, err := os.ReadFile(path); err == nil && len(strings.Fields(string(data))) > 0 {
			if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) >= n {
				return replaceLogLines(t, path)
			}
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(path)
			t.Fatalf("interactive mode never logged %s (%d lines wanted); log:\n%s", what, n, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Pi's interactive mode runs /new, /resume, /clone and /fork through runtimeHost, so each builds the replacement Session and its extensions through the Session factory and rebinds once (setRebindSession). The extension processes are fresh instances: A to E below.
// interactivePig is one interactive pig process on a pseudo-terminal, started with the session-replace fixture.
type interactivePig struct {
	t      *testing.T
	master *os.File
	exited chan struct{}
	output *ptyOutput
	log    string
	// home is PIG_HOME and dir the startup working directory.
	home, dir string
}

func startInteractivePig(t *testing.T) *interactivePig {
	t.Helper()
	return startInteractivePigWith(t, sessionReplaceFixture(t))
}

func startInteractivePigWith(t *testing.T, fixture string) *interactivePig {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	// The home holds the Session cwds the resume picker prints; a t.TempDir name repeats the test name and, under a nested TMPDIR, pushes the Session name out of the 140-column picker.
	p := &interactivePig{t: t, log: filepath.Join(t.TempDir(), "replace.log"), output: &ptyOutput{}, home: shortTempDir(t), dir: t.TempDir()}
	home := p.home
	seedFirstRunDone(t, filepath.Join(home, "agent"))
	master, slave := openPTY(t, 40, 140)
	p.master = master
	t.Cleanup(func() { _ = master.Close() })
	cmd := exec.Command(binary, "--model", "test-faux/faux-1", "-e", fixture, "--session-dir", filepath.Join(home, "sessions"))
	cmd.Dir = p.dir
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_TEST_REPLACE_LOG="+p.log, "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	p.exited = make(chan struct{})
	go func() { _ = cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-p.exited
	})
	go func() { _, _ = io.Copy(p.output, master) }()
	waitReplaceLog(t, p.log, 1, "session_start")
	return p
}

func (p *interactivePig) send(text string) {
	p.t.Helper()
	if _, err := p.master.Write([]byte(text)); err != nil {
		p.t.Fatal(err)
	}
}

// command types a slash command and submits it once its autocomplete settled.
func (p *interactivePig) command(text string) {
	p.t.Helper()
	p.send(text)
	time.Sleep(200 * time.Millisecond)
	p.send("\r")
}

func (p *interactivePig) quit() {
	p.t.Helper()
	// A fork prefills the editor with the selected message once the replacement returned, a moment after its last extension event.
	time.Sleep(700 * time.Millisecond)
	p.command("\x15/quit")
	select {
	case <-p.exited:
	case <-time.After(testbudget.Wait(p.t)):
		p.t.Fatalf("/quit did not exit; screen tail: %q", tail(p.output.since(0), 1500))
	}
}

// Pi's interactive mode runs /new, /resume, /clone and /fork through runtimeHost, so each builds the replacement Session and its extensions through the Session factory and rebinds once (setRebindSession). The extension processes are fresh instances: A to E below.
func TestInteractiveSessionCommandsReplaceThroughTheRuntimeFactory(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	t.Parallel()
	p := startInteractivePig(t)
	// Persist the first Session: only a Session with an assistant reply exists on disk, so only it can be resumed, cloned or forked.
	p.send("reply with exactly: first\r")
	p.output.waitQuiet(0, []byte("first"), 300*time.Millisecond, testbudget.Wait(t))
	time.Sleep(500 * time.Millisecond)

	p.command("/new")
	waitReplaceLog(t, p.log, 3, "/new")
	// /resume and /fork open a picker; Enter selects the first entry, which is the first Session and its user message.
	p.command("/resume")
	time.Sleep(700 * time.Millisecond)
	p.send("\r")
	waitReplaceLog(t, p.log, 5, "/resume")
	time.Sleep(500 * time.Millisecond)
	p.command("/clone")
	waitReplaceLog(t, p.log, 7, "/clone")
	time.Sleep(500 * time.Millisecond)
	p.command("/fork")
	time.Sleep(700 * time.Millisecond)
	p.send("\r")
	waitReplaceLog(t, p.log, 9, "/fork")
	p.quit()

	got := replaceLogLines(t, p.log)
	want := []string{
		"session_start reason=startup instance=A previous=no",
		"session_shutdown reason=new instance=A target=yes",
		"session_start reason=new instance=B previous=yes",
		"session_shutdown reason=resume instance=B target=yes",
		"session_start reason=resume instance=C previous=yes",
		"session_shutdown reason=fork instance=C target=yes",
		"session_start reason=fork instance=D previous=yes",
		"session_shutdown reason=fork instance=D target=yes",
		"session_start reason=fork instance=E previous=yes",
		"session_shutdown reason=quit instance=E target=no",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("interactive lifecycle:\n got %q\nwant %q\nscreen tail: %q", got, want, tail(p.output.since(0), 600))
	}
}

// An extension command that awaits ctx.newSession() is still running while the replacement retires its Session. Its extension process must stay up until the command returns, then exit; the replacement runs in a fresh instance.
func TestInteractiveExtensionNewSessionKeepsTheCallerAliveUntilItReturns(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	t.Parallel()
	p := startInteractivePig(t)
	p.command("/replace-new")
	waitReplaceLog(t, p.log, 4, "/replace-new")
	p.quit()

	got := replaceLogLines(t, p.log)
	want := []string{
		"session_start reason=startup instance=A previous=no",
		"session_shutdown reason=new instance=A target=yes",
		"session_start reason=new instance=B previous=yes",
		"replace-new cancelled=false instance=A",
		"session_shutdown reason=quit instance=B target=no",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("interactive extension replacement:\n got %q\nwant %q\nscreen tail: %q", got, want, tail(p.output.since(0), 600))
	}
}

// Pi agent-session.ts:2941 discovers resources with reason "startup" for every Session start that is not a reload, so /new reports "startup" to resources_discover handlers.
func TestInteractiveReplacementDiscoversResourcesWithStartupReason(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-resources.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	p := startInteractivePigWith(t, fixture)
	waitReplaceLog(t, p.log, 2, "startup resources_discover")
	p.command("/new")
	waitReplaceLog(t, p.log, 4, "/new")
	p.quit()

	got := replaceLogLines(t, p.log)
	want := []string{
		"session_start reason=startup",
		"resources_discover reason=startup",
		"session_start reason=new",
		"resources_discover reason=startup",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("resources_discover reasons:\n got %q\nwant %q\nscreen tail: %q", got, want, tail(p.output.since(0), 600))
	}
}

func tail(data []byte, n int) []byte {
	return data[max(0, len(data)-n):]
}
