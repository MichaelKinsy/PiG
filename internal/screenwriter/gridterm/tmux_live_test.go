// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build unix

package gridterm

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"
)

// TestMain runs the tests, or, started by tmux as the program of a pane (TestLinesAreWhatTmuxShows), that program.
func TestMain(m *testing.M) {
	if in := os.Getenv("PIG_GRIDTERM_PANE_IN"); in != "" {
		os.Exit(paneMain(in, os.Getenv("PIG_GRIDTERM_PANE_ANSWERS")))
	}
	os.Exit(m.Run())
}

// paneMain is the program of a pane: it puts the pane's terminal into raw mode, writes to it what arrives on the FIFO in, and
// writes to the FIFO answers what the terminal answers. It is this test binary, and it copies with read and write themselves, so
// that the test needs no stty or cat and behaves the same whichever ones a system has.
func paneMain(in, answers string) int {
	if _, err := term.MakeRaw(0); err != nil {
		fmt.Fprintln(os.Stderr, "gridterm pane: raw mode:", err)
		return 2
	}
	feed, err := syscall.Open(in, syscall.O_RDONLY, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gridterm pane:", in, err)
		return 2
	}
	answer, err := syscall.Open(answers, syscall.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gridterm pane:", answers, err)
		return 2
	}
	go copyBytes(answer, 0)
	copyBytes(1, feed)
	return 0
}

// copyBytes writes to the descriptor to what it reads from the descriptor from, until either fails or from ends.
func copyBytes(to, from int) {
	buf := make([]byte, 4096)
	for {
		n, err := syscall.Read(from, buf)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			return
		}
		for done := 0; done < n; {
			m, err := syscall.Write(to, buf[done:n])
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				return
			}
			done += m
		}
	}
}

// tmuxCommand runs tmux on the test's own server, the one whose socket is the file socket.
func tmuxCommand(socket string, args ...string) (string, error) {
	//nolint:gosec // G204 (class a): program is literal tmux; the arguments are the test's own, on a server of its own.
	cmd := exec.Command("tmux", append([]string{"-S", socket, "-f", "/dev/null"}, args...)...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "TMUX=") && !strings.HasPrefix(v, "SHELL=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "SHELL=/bin/sh")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// cellsShown is how many cells each row of a capture has.
func cellsShown(capture string) []int {
	var cells []int
	for _, row := range strings.Split(strings.TrimSuffix(capture, "\n"), "\n") {
		n := 0
		for row != "" {
			cluster, width := segmentRepo(row)
			n, row = n+width, row[len(cluster):]
		}
		cells = append(cells, n)
	}
	return cells
}

// The grid's lines are the ones the tmux in PATH shows: every case is written to a pane of a tmux server of the test's own, and
// after every step capture-pane -N and -J are compared with the grid. A pane's program is this test binary (paneMain): it copies a
// FIFO to the terminal and what the terminal answers to another; a step ends with a request for the cursor's position, so tmux has
// read the step when the answer comes. With PIG_GRIDTERM_RECORD set the lines are written to testdata as that version's record. Without tmux the test is
// skipped: TestLinesAreWhatTmuxRecorded compares the records.
func TestLinesAreWhatTmuxShows(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux in PATH")
	}
	//nolint:gosec // G204 (class a): program is literal tmux.
	versionOut, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		t.Skipf("tmux -V: %v", err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(versionOut)), "tmux ")
	// The server's socket is in a directory of its own that ends with the test, so that nothing of the server is left then, whatever
	// tmux does with the file. The directory is in /tmp and not in t.TempDir(): a socket's path has to fit in about a hundred bytes.
	socketDir, err := os.MkdirTemp("/tmp", "pig-gridterm-")
	if err != nil {
		t.Skipf("no directory for the tmux server's socket: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "tmux")
	// The server ends with its last session: this one keeps it up between two cases, and its end is the server's.
	if out, err := tmuxCommand(socket, "new-session", "-d", "-s", "hold", "-x", "80", "-y", "24", "sleep 900"); err != nil {
		t.Skipf("tmux %s starts no server here: %v: %s", version, err, out)
	}
	t.Cleanup(func() { _, _ = tmuxCommand(socket, "kill-session", "-t", "hold") })
	must := func(args ...string) string {
		t.Helper()
		out, err := tmuxCommand(socket, args...)
		if err != nil {
			t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return out
	}
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := tmuxCases()
	shown := map[string][]tmuxLines{}
	// show writes a case to a pane of its own and returns the lines tmux shows after every step. The pane's session and the FIFOs
	// end with it, also when a step fails.
	show := func(i int, c tmuxCase) []tmuxLines {
		in, answers := filepath.Join(dir, fmt.Sprintf("in%d", i)), filepath.Join(dir, fmt.Sprintf("answers%d", i))
		for _, fifo := range []string{in, answers} {
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// Both ends of each FIFO are held open here, so neither open waits for the pane's program.
		feed, err := os.OpenFile(in, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer feed.Close()
		answered, err := os.OpenFile(answers, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer answered.Close()
		session := fmt.Sprintf("case%d", i)
		must("new-session", "-d", "-s", session, "-x", fmt.Sprint(c.cols), "-y", fmt.Sprint(c.rows), fmt.Sprintf("PIG_GRIDTERM_PANE_IN='%s' PIG_GRIDTERM_PANE_ANSWERS='%s' exec '%s'", in, answers, self))
		defer func() { _, _ = tmuxCommand(socket, "kill-session", "-t", session) }()
		must("set-option", "-t", session, "status", "off")
		must("resize-window", "-t", session, "-x", fmt.Sprint(c.cols), "-y", fmt.Sprint(c.rows))
		reports, readFailed := make(chan struct{}, 64), make(chan error, 1)
		go func() {
			buf := make([]byte, 256)
			for {
				n, err := answered.Read(buf)
				if err != nil {
					readFailed <- err
					return
				}
				for _, b := range buf[:n] {
					if b == 'R' {
						reports <- struct{}{}
					}
				}
			}
		}()
		var lines []tmuxLines
		for k, step := range c.steps {
			if step.cols != 0 {
				must("resize-window", "-t", session, "-x", fmt.Sprint(step.cols), "-y", fmt.Sprint(step.rows))
			}
			if _, err := feed.WriteString(step.write + "\x1b[6n"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-reports:
			case err := <-readFailed:
				t.Fatalf("%s, step %d: reading the pane's answers: %v", c.name, k, err)
			case <-time.After(30 * time.Second):
				// Where the step stopped: whether the pane's program runs, and whether tmux read the step (the cursor, the screen).
				pane, _ := tmuxCommand(socket, "list-panes", "-t", session, "-F", "dead=#{pane_dead} command=#{pane_current_command}")
				state, _ := tmuxCommand(socket, "display-message", "-p", "-t", session, "cursor=#{cursor_x},#{cursor_y} alternate=#{alternate_on}")
				screen, _ := tmuxCommand(socket, "capture-pane", "-p", "-t", session)
				t.Fatalf("%s, step %d: tmux %s did not report the cursor within 30 s; pane %s, %s, first row %q", c.name, k, version,
					strings.TrimSpace(pane), strings.TrimSpace(state), strings.SplitN(screen, "\n", 2)[0])
			}
			lines = append(lines, tmuxLines{N: cellsShown(must("capture-pane", "-p", "-N", "-t", session)), J: cellsShown(must("capture-pane", "-p", "-J", "-t", session))})
		}
		return lines
	}
	for i, c := range cases {
		shown[c.name] = show(i, c)
	}
	compareTmuxLines(t, version, cases, shown)
	if os.Getenv("PIG_GRIDTERM_RECORD") != "" && !t.Failed() {
		data, err := json.Marshal(tmuxRecord{Tmux: version, Digest: tmuxDigest(cases), Lines: shown})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("testdata", "tmux-"+version+".json"), append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
