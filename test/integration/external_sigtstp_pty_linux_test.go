//go:build integration && linux

package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// An external `kill -TSTP <pig>` stops pig by the kernel's default action, and the `fg` that continues it arms the terminal again (Pi e4f50d76 and
// 3107a58d; D661). Pi at the pin, and PiG until this change, continued with the terminal as the shell left it: the shell's prompt took the tty, and `fg`
// did not enter the TUI again. Each case runs a real pig under a job-control bash on a pseudo-terminal, as a terminal window does, and reads the
// terminal's bytes and the process states: nothing waits for a fixed time.

const (
	paste       = "\x1b[?2004h"
	pasteOff    = "\x1b[?2004l"
	cursorShow  = "\x1b[?25h"
	kittyPush   = "\x1b[>7u"
	altScreenOn = "\x1b[?1049h"
)

// suspendStatFields are the fields of /proc/<pid>/stat after the command name: state, parent, process group, session, ..., and the start time at index
// 19; nil when the process is gone.
func suspendStatFields(pid int) []string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil
	}
	return strings.Fields(string(b[bytes.LastIndexByte(b, ')')+2:]))
}

// suspendDescendants are the processes below root in the process tree, by their parent links, in pid order. A process that has been reparented away
// (its parent died) is no longer below root.
func suspendDescendants(root int) []int {
	parent := map[int]int{}
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if fields := suspendStatFields(pid); len(fields) > 1 {
			parent[pid], _ = strconv.Atoi(fields[1])
		}
	}
	var found []int
	for pid, p := range parent {
		for depth := 0; p > 1 && depth < 64; p, depth = parent[p], depth+1 {
			if p == root {
				found = append(found, pid)
				break
			}
		}
	}
	slices.Sort(found)
	return found
}

// suspendProc is one process, told from a later process that gets its pid by its start time.
type suspendProc struct {
	pid   int
	start string
}

// suspendFindTool is the bash tool's `sleep 45` below root. The command line alone does not tell it: every test and every lane on a host runs its own
// `sleep 45`, and the leftovers of earlier runs outlive them by up to 45 seconds.
func suspendFindTool(root int) (suspendProc, bool) {
	for _, pid := range suspendDescendants(root) {
		if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil && string(raw) == "sleep\x0045\x00" {
			if fields := suspendStatFields(pid); len(fields) > 19 {
				return suspendProc{pid, fields[19]}, true
			}
		}
	}
	return suspendProc{}, false
}

// running is whether this process still exists and is not a zombie: an ended tool is reaped, or waits for it, and a later process may have its pid.
func (p suspendProc) running() bool {
	fields := suspendStatFields(p.pid)
	return len(fields) > 19 && fields[19] == p.start && fields[0] != "Z" && fields[0] != "X"
}

func (p suspendProc) describe() string {
	fields := suspendStatFields(p.pid)
	if len(fields) <= 19 {
		return "gone"
	}
	return "state " + fields[0] + ", start " + fields[19] + " (was " + p.start + ")"
}

func suspendRuns() int {
	if n, err := strconv.Atoi(os.Getenv("PIG_SUSPEND_PTY_RUNS")); err == nil && n > 0 {
		return n
	}
	return 2
}

type suspendRig struct {
	t      *testing.T
	master *os.File
	out    *syncBuffer
	shell  *exec.Cmd
	dir    string
	pig    int
}

func (r *suspendRig) text() string {
	r.out.mu.Lock()
	defer r.out.mu.Unlock()
	return r.out.buf.String()
}

func (r *suspendRig) mark() int { return len(r.text()) }

func (r *suspendRig) since(mark int) string { return r.text()[mark:] }

func (r *suspendRig) line(l string) { _, _ = r.master.WriteString(l + "\r") }

func (r *suspendRig) fail(format string, args ...any) {
	r.t.Helper()
	r.t.Fatalf("%s\nterminal tail: %q", fmt.Sprintf(format, args...), tail(r.text(), 600))
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func (r *suspendRig) waitBytes(mark int, why string, needles ...string) {
	r.t.Helper()
	if !waitFor(startupReadyWait, func() bool {
		got := r.since(mark)
		for _, n := range needles {
			if !strings.Contains(got, n) {
				return false
			}
		}
		return true
	}) {
		r.fail("%s: %q not in the terminal bytes written since the mark; since the mark: %q; process state %s", why, needles, tail(r.since(mark), 1200), suspendProcState(r.pig))
	}
}

func suspendProcState(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "gone"
	}
	return string(b[bytes.LastIndexByte(b, ')')+2])
}

func (r *suspendRig) waitState(pid int, want string, why string) {
	r.t.Helper()
	if !waitFor(startupReadyWait, func() bool {
		state := suspendProcState(pid)
		return state == "T" == (want == "T") && state != "gone"
	}) {
		r.fail("%s: process %d is %s, want %s", why, pid, suspendProcState(pid), want)
	}
}

// shellOwnsTerminal reports whether the terminal is no longer in pig's raw mode (for example at bash's readline prompt where ISIG is on, or in cooked
// mode where ICANON/ECHO/ISIG are on). Comparing against bash's readline prompt alone would treat a cooked terminal as false.
func (r *suspendRig) shellOwnsTerminal() bool {
	termios, err := unix.IoctlGetTermios(int(r.master.Fd()), unix.TCGETS)
	if err != nil {
		r.t.Fatalf("read the terminal modes: %v", err)
	}
	return termios.Lflag&unix.ISIG != 0
}

// terminalIsRaw reports whether the terminal is in pig's libuv raw mode: canonical input, echo, signal generation, extended input and CR-to-NL are all off.
func (r *suspendRig) terminalIsRaw() bool {
	termios, err := unix.IoctlGetTermios(int(r.master.Fd()), unix.TCGETS)
	if err != nil {
		r.t.Fatalf("read the terminal modes: %v", err)
	}
	const rawOffL = unix.ICANON | unix.ECHO | unix.ISIG | unix.IEXTEN
	const rawOffI = unix.ICRNL | unix.IXON
	return termios.Lflag&rawOffL == 0 && termios.Iflag&rawOffI == 0
}

// groupMembers are the processes of pig's process group but pig itself.
func suspendGroupMembers(pgid int) []int {
	var members []int
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == pgid {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+2:]))
		if len(fields) > 2 && fields[2] == strconv.Itoa(pgid) {
			members = append(members, pid)
		}
	}
	return members
}

func newSuspendRig(t *testing.T, bin string, env ...string) *suspendRig {
	t.Helper()
	dir := t.TempDir()
	seedIntegrationSettings(t, dir)
	trustIntegrationProjectAt(t, dir, dir)
	master, slave, err := openHangupPTY()
	if err != nil {
		t.Fatalf("open pseudo-terminal: %v", err)
	}
	r := &suspendRig{t: t, master: master, out: &syncBuffer{}, dir: dir}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			_, _ = r.out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	r.shell = exec.Command("bash", "--norc", "--noprofile", "-i")
	r.shell.Dir = dir
	r.shell.Env = append(os.Environ(),
		"HOME="+dir, "PIG_HOME="+dir, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_QUIET_STARTUP=1", "PIG_STARTUP_TRACE=1", "TERM=xterm-256color", "PS1=PROMPT> ", "PIG="+bin,
		"PIG_CODING_AGENT_DIR=", "PIG_CODING_AGENT_SESSION_DIR=", "PI_CODING_AGENT_DIR=", "PI_CODING_AGENT_SESSION_DIR=")
	r.shell.Env = append(r.shell.Env, env...)
	r.shell.Stdin, r.shell.Stdout, r.shell.Stderr = slave, slave, slave
	r.shell.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := r.shell.Start(); err != nil {
		_ = slave.Close()
		t.Fatalf("start the shell: %v", err)
	}
	_ = slave.Close()
	waited := make(chan struct{})
	go func() { _ = r.shell.Wait(); close(waited) }()
	t.Cleanup(func() {
		for _, pid := range slices.Concat(suspendDescendants(r.pig), suspendGroupMembers(r.pig), []int{r.pig}) {
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGCONT)
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		_ = r.shell.Process.Signal(syscall.SIGKILL)
		<-waited
		_ = master.Close()
		<-readerDone
	})
	r.waitBytes(0, "the shell prompt", "PROMPT> ")
	return r
}

// launch runs pig as the shell's job and waits for the footer of its first frame; pig is the leader of its process group, as the shell makes every job.
func (r *suspendRig) launch(args ...string) {
	r.t.Helper()
	mark := r.mark()
	r.line(`"$PIG" --model test-faux/faux-1 ` + strings.Join(args, " "))
	if !waitFor(startupReadyWait, func() bool {
		for _, child := range loginChildren(r.shell.Process.Pid) {
			if pgid, err := syscall.Getpgid(child); err == nil && pgid == child {
				r.pig = child
				return true
			}
		}
		return false
	}) {
		r.fail("pig did not start")
	}
	r.waitBytes(mark, "the end of startup", paste, "faux-1", "interactive-ready")
}

// suspendBy stops pig with how ("kill" is `kill -TSTP`, "key" is Ctrl+Z at the TUI) and checks the stop: the process is stopped, the shell reports the job
// and has the terminal in cooked mode. The key hands the terminal back before the stop; the external signal cannot, the kernel's default action stops the
// process at once.
func (r *suspendRig) suspendBy(how string) int {
	r.t.Helper()
	mark := r.mark()
	needles := []string{"Stopped", "PROMPT> "}
	switch how {
	case "kill":
		if err := syscall.Kill(r.pig, syscall.SIGTSTP); err != nil {
			r.t.Fatal(err)
		}
	case "key":
		_, _ = r.master.WriteString("\x1a")
		needles = append(needles, pasteOff, cursorShow)
	}
	r.waitState(r.pig, "T", how+": the process did not stop")
	r.waitBytes(mark, how+": the shell did not report the stop", needles...)
	if !r.shellOwnsTerminal() {
		r.fail("%s: the terminal is still raw after the stop", how)
	}
	return mark
}

// waitFrameAfterSequence waits until needle appears AFTER the last occurrence of seq in bytes written since mark.
// Unlike waitBytes(mark, seq, needle), this cannot be satisfied by bash printing the job command line on fg before pig writes seq.
func (r *suspendRig) waitFrameAfterSequence(mark int, seq, needle, why string) {
	r.t.Helper()
	if !waitFor(startupReadyWait, func() bool {
		got := r.since(mark)
		idx := strings.LastIndex(got, seq)
		if idx < 0 {
			return false
		}
		return strings.Contains(got[idx+len(seq):], needle)
	}) {
		r.fail("%s: %q not written after %q; since mark: %q; process state %s", why, needle, seq, tail(r.since(mark), 1200), suspendProcState(r.pig))
	}
}

// resume continues the job with fg and checks that pig takes the terminal again: raw mode, bracketed-paste enable, a real rendered frame
// containing frameNeedles written AFTER pig re-arms the terminal (not met by bash echoing the fg command line), and live editor input.
func (r *suspendRig) resume(frameNeedles ...string) {
	r.t.Helper()
	mark := r.mark()
	r.line("fg")
	r.waitState(r.pig, "S", "fg: the process stays stopped")
	r.waitBytes(mark, "fg: the TUI did not re-arm bracketed paste", paste)
	if !waitFor(startupReadyWait, r.terminalIsRaw) {
		r.fail("fg: terminal did not return to raw TUI mode")
	}
	for _, needle := range frameNeedles {
		r.waitFrameAfterSequence(mark, paste, needle, "fg: the TUI did not redraw its frame")
	}
	// The TUI answers keys again (in raw mode, so tty echo cannot fake this).
	typed := r.mark()
	word := fmt.Sprintf("zq%d", typed)
	_, _ = r.master.WriteString(word)
	r.waitBytes(typed, "fg: the editor does not take keys", word)
	_, _ = r.master.WriteString(strings.Repeat("\x7f", len(word)))
}

func TestExternalSIGTSTPSuspendsLikeCtrlZ(t *testing.T) {
	bin := buildBinary(t)
	for _, tc := range []struct {
		name string
		how  string
		args []string
		alt  bool
	}{
		{"key", "key", nil, true},
		{"kill", "kill", nil, true},
		{"kill twice", "kill twice", nil, true},
		{"kill regular", "kill", []string{"--tui-mode", "regular"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range suspendRuns() {
				r := newSuspendRig(t, bin)
				r.launch(tc.args...)
				// Put LIVE-STREAM-24 in the rendered transcript and let the turn settle idle before suspending so resume() proves rearmTerminal's own redraw (not an in-flight turn-end render) paints LIVE-STREAM-24 after ?2004h/?1049h.
				turnMark := r.mark()
				_, _ = r.master.WriteString("TUI_LIVE_STREAM\r")
				r.waitBytes(turnMark, "the turn", "LIVE-STREAM-24")
				idleMark := r.mark()
				_, _ = r.master.WriteString("idleok")
				r.waitBytes(idleMark, "idle editor echo", "idleok")
				_, _ = r.master.WriteString(strings.Repeat("\x7f", 6))
				var firstFgMark int
				cycles := strings.Count(tc.how, "twice") + 1
				for i := range cycles {
					r.suspendBy(strings.Fields(tc.how)[0])
					fgMark := r.mark()
					if i == 0 {
						firstFgMark = fgMark
					}
					r.resume("LIVE-STREAM-24")
					if tc.alt {
						r.waitFrameAfterSequence(fgMark, altScreenOn, "LIVE-STREAM-24", "fg: fullscreen redraw did not paint after ?1049h")
					}
				}
				if strings.HasPrefix(tc.how, "kill") {
					// Clean exit after external SIGTSTP+fg cycles must pop the Kitty keyboard stack as many times as it pushed
					// between fg and exit (1 pop + 1 push per re-arm cycle, plus 1 pop on exit).
					exitMark := r.mark()
					if err := syscall.Kill(r.pig, syscall.SIGTERM); err != nil {
						t.Fatal(err)
					}
					r.waitBytes(exitMark, "clean exit prompt after SIGTERM", "PROMPT> ")
					if !waitFor(startupReadyWait, func() bool { return suspendProcState(r.pig) == "gone" }) {
						r.fail("pig did not exit after SIGTERM (state %s)", suspendProcState(r.pig))
					}
					seg := r.since(firstFgMark)
					pushes := len(regexp.MustCompile(`\x1b\[>[0-9]+u`).FindAllString(seg, -1))
					pops := len(regexp.MustCompile(`\x1b\[<u`).FindAllString(seg, -1))
					if pushes != cycles || pops != cycles+1 {
						r.fail("unbalanced Kitty keyboard stack from fg to exit across %d cycle(s): pushes=%d pops=%d (want %d/%d)",
							cycles, pushes, pops, cycles, cycles+1)
					}
				}
			}
		})
	}
}

var modeSequence = regexp.MustCompile(`\x1b\[(\?(?:2004|1000|1002|1003|1004|1006|1049|2031)[hl]|<u|>[0-9]+u|>4;[0-9]m)`)

// ctrlZStopModeSequences and ctrlZResumeModeSequences are what pig itself writes around a Ctrl+Z suspend: the stop leg (up to leaving the alternate
// screen, ?1049l) and the resume leg (from re-entering the alternate screen, ?1049h, onward). The bytes bash writes at its own prompt in between
// (such as bash 5's ?2004h/?2004l) are excluded so the golden is not bound to a bash version. Recorded before D661 and kept unchanged.
const (
	ctrlZStopModeSequences   = "\x1b[?2031l \x1b[?1006l \x1b[?1004l \x1b[?1003l \x1b[?1002l \x1b[?1000l \x1b[?2004l \x1b[<u \x1b[?1049l"
	ctrlZResumeModeSequences = "\x1b[?1049h \x1b[?1000h \x1b[?1002h \x1b[?1003h \x1b[?1004h \x1b[?1006h \x1b[?2004h \x1b[>7u \x1b[?2031h"
)

func TestCtrlZKeyKeepsItsModeSequences(t *testing.T) {
	bin := buildBinary(t)
	r := newSuspendRig(t, bin)
	r.launch()
	mark := r.suspendBy("key")
	r.resume()
	all := modeSequence.FindAllString(r.since(mark), -1)
	stopEnd := slices.Index(all, "\x1b[?1049l")
	resumeStart := slices.Index(all, altScreenOn)
	if stopEnd < 0 || resumeStart < 0 || resumeStart <= stopEnd {
		t.Fatalf("missing alt-screen leave/enter pair in Ctrl+Z mode sequences: %q", strings.Join(all, " "))
	}
	gotStop := strings.Join(all[:stopEnd+1], " ")
	if gotStop != ctrlZStopModeSequences {
		t.Fatalf("pig's stop mode sequences around Ctrl+Z changed:\ngot  %q\nwant %q", gotStop, ctrlZStopModeSequences)
	}
	gotResume := strings.Join(all[resumeStart:], " ")
	if gotResume != ctrlZResumeModeSequences {
		t.Fatalf("pig's resume mode sequences around Ctrl+Z changed:\ngot  %q\nwant %q", gotResume, ctrlZResumeModeSequences)
	}
}

// A tool and a provider stream in flight go on after fg, as after the key.
func TestExternalSIGTSTPKeepsARunGoing(t *testing.T) {
	bin := buildBinary(t)
	for _, how := range []string{"key", "kill"} {
		t.Run("tool "+how, func(t *testing.T) {
			for range suspendRuns() {
				r := newSuspendRig(t, bin)
				r.launch()
				_, _ = r.master.WriteString("Run: sleep for a while\r")
				marker := filepath.Join(r.dir, ".pig-faux-tool-started")
				if !waitFor(startupReadyWait, func() bool { _, err := os.Stat(marker); return err == nil }) {
					r.fail("the tool did not start")
				}
				r.suspendBy(how)
				// The bash tool runs in a process group of its own, which neither the key's SIGTSTP to pig's group nor an external signal reaches. Its shell
				// writes the marker before it starts the sleep, so the sleep may not exist yet.
				var tool suspendProc
				if !waitFor(startupReadyWait, func() bool { var ok bool; tool, ok = suspendFindTool(r.pig); return ok }) {
					r.fail("no tool process below pig")
				}
				if state := suspendProcState(tool.pid); state == "T" {
					r.fail("the tool process is stopped with pig")
				}
				r.resume()
				if !tool.running() {
					r.fail("the tool process %d ended with the suspend: %s", tool.pid, tool.describe())
				}
			}
		})
		t.Run("stream "+how, func(t *testing.T) {
			for range suspendRuns() {
				r := newSuspendRig(t, bin)
				r.launch()
				mark := r.mark()
				_, _ = r.master.WriteString("TUI_LIVE_STREAM_PAUSE\r")
				r.waitBytes(mark, "the stream", "LIVE-STREAM-08")
				r.suspendBy(how)
				if strings.Contains(r.since(mark), "LIVE-STREAM-24") {
					t.Log("the stream ended before the suspend; the run went on without it")
				}
				resumed := r.mark()
				r.line("fg")
				r.waitBytes(resumed, "the stream does not finish after fg", "LIVE-STREAM-24")
			}
		})
	}
}

// While an external editor owns the terminal the TUI does not: the editor's own job control keeps working, and an external TSTP stops pig alone, as the
// default action does.
func TestSIGTSTPWhileAnExternalEditorOwnsTheTerminal(t *testing.T) {
	bin := buildBinary(t)
	for _, how := range []string{"key", "kill"} {
		t.Run(how, func(t *testing.T) {
			for range suspendRuns() {
				dir := t.TempDir()
				fifo := filepath.Join(dir, "release")
				started := filepath.Join(dir, "started")
				editor := filepath.Join(dir, "editor.sh")
				script := "#!/bin/sh\n: > \"" + started + "\"\nread line < \"" + fifo + "\"\necho edited-by-the-fake-editor >> \"$1\"\n"
				if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					t.Fatal(err)
				}
				r := newSuspendRig(t, bin, "EDITOR="+editor, "VISUAL=")
				r.launch()
				_, _ = r.master.WriteString("\x07")
				if !waitFor(startupReadyWait, func() bool { _, err := os.Stat(started); return err == nil }) {
					r.fail("the editor did not start")
				}
				var editorPid int
				if !waitFor(startupReadyWait, func() bool {
					members := suspendGroupMembers(r.pig)
					for _, pid := range members {
						if cmdline, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); bytes.Contains(cmdline, []byte("editor.sh")) {
							editorPid = pid
						}
					}
					return editorPid != 0
				}) {
					r.fail("no editor process in pig's group")
				}
				mark := r.mark()
				switch how {
				case "key":
					// The terminal is cooked while the editor runs: the key is the terminal's SIGTSTP to the job.
					_, _ = r.master.WriteString("\x1a")
					r.waitState(editorPid, "T", "the editor does not stop with the job")
				case "kill":
					_ = syscall.Kill(r.pig, syscall.SIGTSTP)
				}
				r.waitState(r.pig, "T", "pig does not stop")
				r.waitBytes(mark, "the shell does not report the stop", "Stopped")
				if how == "kill" && suspendProcState(editorPid) == "T" {
					r.fail("an external TSTP stopped the editor too")
				}
				if got := r.since(mark); strings.Contains(got, kittyPush) || strings.Contains(got, altScreenOn) {
					r.fail("pig armed the terminal while the editor owns it: %q", got)
				}
				fgMark := r.mark()
				r.line("fg")
				r.waitState(r.pig, "S", "fg: pig stays stopped")
				r.waitState(editorPid, "S", "fg: the editor stays stopped")
				back := r.mark()
				var release *os.File
				var openErr error
				if !waitFor(startupReadyWait, func() bool {
					f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
					if err == nil {
						release = f
						return true
					}
					openErr = err
					return false
				}) {
					r.fail("open editor release FIFO: %v", openErr)
				}
				_, _ = release.WriteString("done\n")
				_ = release.Close()
				r.waitBytes(back, "the TUI does not take the terminal back from the editor", altScreenOn, "edited-by-the-fake-editor")
				// The continue while the editor owned the terminal armed nothing: the one alternate-screen enter since fg is the TUI's start after the editor.
				if n := strings.Count(r.since(fgMark), altScreenOn); n != 1 {
					r.fail("%d alternate-screen enters since fg, want 1 (the TUI took the terminal while the editor owned it)", n)
				}
			}
		})
	}
}

// A SIGTSTP and fg that land inside the startup window after tui-started (and before interactive-ready) must re-arm the terminal in raw mode and write
// bracketed-paste enable again, instead of leaving pig in the shell's cooked mode.
// A session_start barrier extension (precedent: test/integration/ext_test.go TestExt_SessionStartPrecedesLoadedResources) deterministically holds Init
// between tui-started and interactive-ready so the external stop never races startup completion.
func TestSIGTSTPInStartupWindowRearmsTerminal(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	ext := filepath.Join(dir, "startup-pause.mjs")
	source := `import { existsSync, writeFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_start", async () => {
    writeFileSync(process.env.PIG_STARTUP_STARTED, "started");
    while (!existsSync(process.env.PIG_STARTUP_RELEASE)) {
      await new Promise(resolve => setTimeout(resolve, 10));
    }
  });
}
`
	if err := os.WriteFile(ext, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })

	r := newSuspendRig(t, bin, "PIG_STARTUP_STARTED="+started, "PIG_STARTUP_RELEASE="+release)
	mark := r.mark()
	r.line(`"$PIG" --model test-faux/faux-1 -e ` + ext)
	if !waitFor(startupReadyWait, func() bool {
		for _, child := range loginChildren(r.shell.Process.Pid) {
			if pgid, err := syscall.Getpgid(child); err == nil && pgid == child {
				r.pig = child
				return true
			}
		}
		return false
	}) {
		r.fail("pig did not start")
	}
	// Wait for the barrier: TUI has started (tui-started), session_start is held, and interactive-ready cannot fire until we write release.
	r.waitBytes(mark, "startup did not reach tui-started", "tui-started")
	if !waitFor(startupReadyWait, func() bool { _, err := os.Stat(started); return err == nil }) {
		r.fail("startup pause extension never entered session_start")
	}
	if strings.Contains(r.since(mark), "interactive-ready") {
		r.fail("interactive-ready fired before the startup barrier was released")
	}
	r.suspendBy("kill")
	resumed := r.mark()
	r.line("fg")
	r.waitState(r.pig, "S", "fg: pig stays stopped")
	// Wait for the SIGCONT re-arm and redraw inside the startup window itself (before releasing the barrier): paste must be written and a rendered frame
	// (the horizontal rule / status line, or "faux-1" written AFTER ?1049h) must follow it, not bash's fg command-line echo.
	r.waitFrameAfterSequence(resumed, altScreenOn, paste, "fg during startup: pig did not re-arm paste after ?1049h inside the startup window")
	r.waitFrameAfterSequence(resumed, paste, "───", "fg during startup: pig did not redraw the editor frame after re-arm inside the startup window")
	if !r.terminalIsRaw() {
		r.fail("fg during startup: terminal was not re-armed in raw TUI mode while still in the startup window")
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.waitBytes(resumed, "fg during startup: pig did not reach interactive-ready after barrier release", "interactive-ready")
	if !r.terminalIsRaw() {
		r.fail("fg during startup: terminal was not in raw TUI mode after startup completed")
	}
}

// assertNoScrollbackDumpBetweenAltScreenExitAndEnter fails if pig dumped rendered transcript/footer rows onto the main screen between ?1049l and ?1049h.
func (r *suspendRig) assertNoScrollbackDumpBetweenAltScreenExitAndEnter(why, got string) {
	r.t.Helper()
	exitIdx := strings.Index(got, "\x1b[?1049l")
	if exitIdx < 0 {
		r.fail("%s: expected ?1049l during fullscreen re-arm, got %q", why, tail(got, 600))
	}
	afterExit := got[exitIdx:]
	enterIdx := strings.Index(afterExit, altScreenOn)
	if enterIdx < 0 {
		r.fail("%s: expected ?1049h after ?1049l during fullscreen re-arm, got %q", why, tail(afterExit, 600))
	}
	segment := afterExit[:enterIdx]
	if strings.Contains(segment, "faux-1") || strings.Contains(segment, "LIVE-STREAM-24") || strings.Contains(segment, "\x1b[2K") {
		r.fail("%s: dumped rendered rows onto main screen scrollback between ?1049l and ?1049h: %q", why, tail(segment, 600))
	}
}

// In fullscreen mode (the default), both (a) an external kill -TSTP + fg and (b) a bare kill -CONT to a running foreground pig
// re-arm with PreserveScreen: true, redrawing the screen without dumping rendered transcript rows onto the main screen between ?1049l and ?1049h.
// Keeping foreground SIGCONT unconditional is required so shells that restore a job's saved raw termios on fg (fish, mksh) still re-arm and redraw.
func TestExternalSIGTSTPAndBareSIGCONTDoNotDumpScrollbackInFullscreen(t *testing.T) {
	bin := buildBinary(t)
	r := newSuspendRig(t, bin)
	r.launch()
	// Produce a distinct transcript line before suspending.
	turnMark := r.mark()
	_, _ = r.master.WriteString("TUI_LIVE_STREAM\r")
	r.waitBytes(turnMark, "the turn", "LIVE-STREAM-24")

	// 1. External kill -TSTP followed by fg: re-arms fullscreen and redraws LIVE-STREAM-24 after ?1049h without dumping rows between ?1049l and ?1049h.
	r.suspendBy("kill")
	fgMark := r.mark()
	r.resume("LIVE-STREAM-24")
	r.waitFrameAfterSequence(fgMark, altScreenOn, "LIVE-STREAM-24", "external SIGTSTP + fg did not redraw LIVE-STREAM-24 after ?1049h")
	r.assertNoScrollbackDumpBetweenAltScreenExitAndEnter("external SIGTSTP + fg", r.since(fgMark))

	// 2. Bare kill -CONT to a running foreground pig: re-arms with PreserveScreen: true (waits deterministically for the re-arm and redraw to complete,
	// no unsynchronized negative assertion) and dumps nothing to main-screen scrollback between ?1049l and ?1049h.
	contMark := r.mark()
	if err := syscall.Kill(r.pig, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	r.waitFrameAfterSequence(contMark, altScreenOn, "LIVE-STREAM-24", "bare SIGCONT did not complete fullscreen re-arm and redraw")
	r.assertNoScrollbackDumpBetweenAltScreenExitAndEnter("bare SIGCONT", r.since(contMark))
	if !r.terminalIsRaw() {
		r.fail("bare SIGCONT left terminal out of raw mode")
	}
}

// D227 + D661: after an external SIGTSTP, `bg` sends SIGCONT while pig is NOT the foreground process group of the terminal.
// Pig must leave the terminal untouched and park itself again with SIGTSTP (state T, matching D227), so:
// (1) pig emits no alt-screen enter or kitty push while in the background, and
// (2) a subsequent `fg` without intervening prompt input (e.g. `bg; sleep 0.5; fg` under bash <= 5.2 or zsh, whose `fg` sends
// SIGCONT only to a stopped job) finds pig stopped, sends SIGCONT, and re-arms raw mode and redraws the TUI.
func TestExternalSIGTSTPBackgroundContinueTouchesNothing(t *testing.T) {
	bin := buildBinary(t)
	r := newSuspendRig(t, bin)
	r.launch()
	turnMark := r.mark()
	_, _ = r.master.WriteString("TUI_LIVE_STREAM\r")
	r.waitBytes(turnMark, "the turn", "LIVE-STREAM-24")
	idleMark := r.mark()
	_, _ = r.master.WriteString("idleok")
	r.waitBytes(idleMark, "idle editor echo", "idleok")
	_, _ = r.master.WriteString(strings.Repeat("\x7f", 6))

	r.suspendBy("kill")
	bgMark := r.mark()
	// Run bg, wait for pig to handle the background SIGCONT and park itself in state T without any prompt key waking SIGTTIN,
	// verify the shell still owns the terminal with no arm sequences written, then run fg on the same command line so bash 5.2
	// only sends SIGCONT if pig actually parked in state T.
	r.line(fmt.Sprintf(`bg; sleep 0.5; echo BG_STATE=$(awk '{print $3}' /proc/%d/stat); echo bg-shell-ok`, r.pig))
	r.waitBytes(bgMark, "shell command after bg", "BG_STATE=", "bg-shell-ok", "PROMPT> ")
	r.waitState(r.pig, "T", "bg: pig did not re-stop itself in background (D227)")
	if !r.shellOwnsTerminal() {
		r.fail("bg caused pig to take raw mode from the shell")
	}
	got := r.since(bgMark)
	if !strings.Contains(got, "BG_STATE=T") {
		r.fail("bg: pig was not parked in state T before prompt input (got %q)", tail(got, 600))
	}
	if strings.Contains(got, altScreenOn) || strings.Contains(got, kittyPush) {
		r.fail("bg caused pig to write terminal arm sequences while in background: %q", tail(got, 600))
	}
	// Now fg must re-arm raw mode and redraw LIVE-STREAM-24 after ?1049h.
	fgMark := r.mark()
	r.resume("LIVE-STREAM-24")
	r.waitFrameAfterSequence(fgMark, altScreenOn, "LIVE-STREAM-24", "fg after bg did not re-arm and redraw LIVE-STREAM-24 after ?1049h")
}

// Under dash (src/jobs.c calls only tcsetpgrp, never tcsetattr), the shell does not save or restore termios on job control, so after an
// external kill -TSTP the dash prompt runs on pig's raw tty (ICANON=false, ECHO=false, ICRNL=false), and sending fg with LF re-arms and redraws the TUI.
func TestExternalSIGTSTPUnderDashJobControl(t *testing.T) {
	if _, err := exec.LookPath("dash"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("dash not installed under CI: %v", err)
		}
		t.Skip("dash not installed")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	seedIntegrationSettings(t, dir)
	trustIntegrationProjectAt(t, dir, dir)
	master, slave, err := openHangupPTY()
	if err != nil {
		t.Fatalf("open pseudo-terminal: %v", err)
	}
	r := &suspendRig{t: t, master: master, out: &syncBuffer{}, dir: dir}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			_, _ = r.out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	r.shell = exec.Command("dash", "-i", "-m")
	r.shell.Dir = dir
	r.shell.Env = append(os.Environ(),
		"HOME="+dir, "PIG_HOME="+dir, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_QUIET_STARTUP=1", "PIG_STARTUP_TRACE=1", "TERM=xterm-256color", "PS1=DASH> ", "PIG="+bin,
		"PIG_CODING_AGENT_DIR=", "PIG_CODING_AGENT_SESSION_DIR=", "PI_CODING_AGENT_DIR=", "PI_CODING_AGENT_SESSION_DIR=")
	r.shell.Stdin, r.shell.Stdout, r.shell.Stderr = slave, slave, slave
	r.shell.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := r.shell.Start(); err != nil {
		_ = slave.Close()
		t.Fatalf("start dash: %v", err)
	}
	_ = slave.Close()
	waited := make(chan struct{})
	go func() { _ = r.shell.Wait(); close(waited) }()
	t.Cleanup(func() {
		for _, pid := range slices.Concat(suspendDescendants(r.pig), suspendGroupMembers(r.pig), []int{r.pig}) {
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGCONT)
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		_ = r.shell.Process.Signal(syscall.SIGKILL)
		<-waited
		_ = master.Close()
		<-readerDone
	})
	r.waitBytes(0, "dash prompt", "DASH> ")
	launchMark := r.mark()
	_, _ = r.master.WriteString(`"$PIG" --model test-faux/faux-1` + "\n")
	if !waitFor(startupReadyWait, func() bool {
		for _, child := range loginChildren(r.shell.Process.Pid) {
			if pgid, err := syscall.Getpgid(child); err == nil && pgid == child {
				r.pig = child
				return true
			}
		}
		return false
	}) {
		r.fail("pig did not start under dash")
	}
	r.waitBytes(launchMark, "pig startup under dash", paste, "interactive-ready")
	turnMark := r.mark()
	_, _ = r.master.WriteString("TUI_LIVE_STREAM\r")
	r.waitBytes(turnMark, "turn under dash", "LIVE-STREAM-24")
	idleMark := r.mark()
	_, _ = r.master.WriteString("idleok")
	r.waitBytes(idleMark, "idle echo under dash", "idleok")
	// Wait for pig to drain the editor clear before SIGTSTP: dash keeps raw termios
	// across job stop, so any unread \x7f bytes left in the PTY input queue would be
	// read literally by dash ahead of "fg".
	clearMark := r.mark()
	_, _ = r.master.WriteString(strings.Repeat("\x7f", 6) + "okdash")
	r.waitBytes(clearMark, "idle clear under dash", "okdash")

	stopMark := r.mark()
	if err := syscall.Kill(r.pig, syscall.SIGTSTP); err != nil {
		t.Fatal(err)
	}
	r.waitState(r.pig, "T", "pig did not stop under dash")
	r.waitBytes(stopMark, "dash prompt after stop", "DASH> ")
	// This checks dash's own behavior (not pig's): dash 0.5.12 does not call tcsetattr on job stop, so termios stay raw
	// between kill -TSTP and fg (a dash that restored cooked termios would fail this check with no fault of pig).
	if !r.terminalIsRaw() {
		r.fail("expected dash prompt to keep raw termios after kill -TSTP")
	}
	fgMark := r.mark()
	// Because ICRNL is off on dash's raw tty, LF (\n) submits fg.
	_, _ = r.master.WriteString("fg\n")
	r.waitState(r.pig, "S", "dash fg: pig stayed stopped")
	r.waitFrameAfterSequence(fgMark, altScreenOn, "LIVE-STREAM-24", "dash fg did not re-arm and redraw LIVE-STREAM-24 after ?1049h")
	if !r.terminalIsRaw() {
		r.fail("dash fg left terminal out of raw mode")
	}
}
