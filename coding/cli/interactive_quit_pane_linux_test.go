//go:build linux

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Quitting interactive mode with ctrl+c twice, ctrl+d or /quit while a turn streams. Pi's quit stops the TUI and then awaits runtimeHost.dispose(), whose first step is
// `await this.session.abort()` (interactive-mode.ts shutdown, agent-session-runtime.ts dispose; Pi 7a53c310a, #9126): the interrupted turn is settled, saved in the session
// file, shown to the extensions (message_end, turn_end, agent_end), and then session_shutdown follows. The process exits 0.

// qsPane is one pig in interactive mode in the pane of a tmux server of its own. The pane's shell writes pig's exit status to a file as a user's shell sees it (128 plus the
// signal's number when a signal ended pig): tmux marks a pane dead when its terminal closes, which can be long before it has collected the status.
type qsPane struct {
	t      *testing.T
	socket string
	stderr string
	exit   string
	pig    int
}

func startQSPane(t *testing.T, cwd, stderr string, env []string, argv ...string) *qsPane {
	t.Helper()
	dir := testenv.ShortTempDir(t, "pig-qs-")
	p := &qsPane{t: t, socket: filepath.Join(dir, "tmux"), stderr: stderr, exit: filepath.Join(dir, "exit-status")}
	script := "trap : INT\nTERM=xterm-256color"
	for _, arg := range argv {
		script += " " + qsShellQuote(arg)
	}
	script += " 2>" + qsShellQuote(stderr) + "\necho $? > " + qsShellQuote(p.exit+".tmp") + "\nmv " + qsShellQuote(p.exit+".tmp") + " " + qsShellQuote(p.exit) + "\n"
	runner := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(runner, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"new-session", "-d", "-s", "keeper", "sleep 100000", ";", "set-option", "-g", "remain-on-exit", "on", ";", "new-session", "-d", "-s", "pig", "-x", "100", "-y", "32", "-c", cwd}
	for _, assignment := range env {
		args = append(args, "-e", assignment)
	}
	if out, err := p.tmux(append(args, "exec sh "+qsShellQuote(runner))...); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := p.tmux("list-panes", "-t", "pig", "-F", "#{pane_dead} #{pane_pid}"); err == nil {
			if fields := strings.Fields(out); len(fields) == 2 && fields[0] == "0" {
				if pid, err := strconv.Atoi(fields[1]); err == nil {
					qsKillTree(pid)
				}
			}
		}
		_, _ = p.tmux("kill-server")
	})
	return p
}

func qsShellQuote(arg string) string { return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'" }

func qsKillTree(pid int) {
	for _, child := range qsChildren(pid) {
		qsKillTree(child)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func (p *qsPane) tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", append([]string{"-S", p.socket, "-f", "/dev/null"}, args...)...).CombinedOutput()
	return string(out), err
}

func (p *qsPane) must(args ...string) string {
	p.t.Helper()
	out, err := p.tmux(args...)
	if err != nil {
		p.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return out
}

func (p *qsPane) screen() string { return p.must("capture-pane", "-p", "-t", "pig") }

func (p *qsPane) keys(names ...string) {
	p.must(append([]string{"send-keys", "-t", "pig"}, names...)...)
}
func (p *qsPane) text(literal string) { p.must("send-keys", "-t", "pig", "-l", literal) }

// pigPID is the process ID of pig: the child of the pane's shell.
func (p *qsPane) pigPID() int {
	p.t.Helper()
	shell, err := strconv.Atoi(strings.TrimSpace(p.must("list-panes", "-t", "pig", "-F", "#{pane_pid}")))
	if err != nil {
		p.t.Fatal(err)
	}
	p.await("pig under the pane's shell", func() bool {
		if children := qsChildren(shell); len(children) > 0 {
			p.pig = children[0]
		}
		return p.pig != 0
	})
	return p.pig
}

// status reports whether pig has exited and the status its shell saw.
func (p *qsPane) status() (int, bool) {
	p.t.Helper()
	data, err := os.ReadFile(p.exit)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		p.t.Fatal(err)
	}
	return code, true
}

// await polls until done reports true; a hang fails after the test budget with the screen and pig's stderr.
func (p *qsPane) await(what string, done func() bool) {
	p.t.Helper()
	budget := testbudget.Wait(p.t)
	deadline := time.Now().Add(budget)
	for !done() {
		if time.Now().After(deadline) {
			stderr, _ := os.ReadFile(p.stderr)
			p.t.Fatalf("%s: not within %s\n--- screen ---\n%s\n--- stderr ---\n%s", what, budget, p.screen(), stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func qsChildren(pid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var children []int
	for _, entry := range entries {
		child, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) > 1 && fields[1] == strconv.Itoa(pid) {
			children = append(children, child)
		}
	}
	return children
}

// qsAwaitGone fails the test for each pid still alive after the test budget.
func qsAwaitGone(t *testing.T, pids []int, what string) {
	t.Helper()
	budget := testbudget.Wait(t)
	deadline := time.Now().Add(budget)
	for _, pid := range pids {
		for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			if time.Now().After(deadline) {
				t.Errorf("process %d (%s) is alive %s after pig exited", pid, what, budget)
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// qsScratch writes the agent directory of a pig that serves the model localhold/hold-model from baseURL, and returns the directories and the environment of the run.
func qsScratch(t *testing.T, baseURL string) (root, agent, project, sessions string, env []string) {
	t.Helper()
	root = t.TempDir()
	agent, project, sessions = filepath.Join(root, "agent"), filepath.Join(root, "project"), filepath.Join(root, "sessions")
	for _, dir := range []string{agent, project} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seedFirstRunDone(t, agent)
	models := fmt.Sprintf(`{"providers":{"localhold":{"baseUrl":%q,"api":"openai-completions","apiKey":"dummy","models":[{"id":"hold-model"}]}}}`, baseURL)
	if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	env = []string{"HOME=" + root, "PIG_HOME=" + root, "PI_HOME=" + root, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "PIG_OFFLINE=1", "PI_OFFLINE=1"}
	return root, agent, project, sessions, env
}
