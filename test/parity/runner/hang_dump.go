//go:build parity

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// hangProbeBudget bounds the whole probe: it runs on a failure path whose session is torn down right after.
	hangProbeBudget = 20 * time.Second
	// hangErrCap bounds the copy of the dump carried in Result.Err, and so in the scenario's failure output. The whole dump goes to Result.HangDump and the failure artifacts.
	hangErrCap = 32 << 10
	// hangPaneLines is how many trailing lines of the pane a dump shows.
	hangPaneLines = 60
	// hangCommandCap bounds the command column of a process row.
	hangCommandCap = 160
	// hangExitWait bounds the wait for pig to die after SIGQUIT; hangTranscriptWait bounds the wait for its dump to reach the transcript.
	hangExitWait       = 5 * time.Second
	hangTranscriptWait = 3 * time.Second
	hangPoll           = 50 * time.Millisecond
)

// waitTimeoutError marks an error as a wait that timed out, with the same text as the error it wraps. runTmuxStep returns it for its waits so that Run can tell a hang from a failed key send.
type waitTimeoutError struct{ error }

func (e waitTimeoutError) Unwrap() error { return e.error }

// signalHungPig delivers SIGQUIT to one pid. It is a variable so a test can count the signals the runner sends.
var signalHungPig = func(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGQUIT)
}

// hangProbe records the state of a program whose wait timed out. The tmux driver defers it at each wait failure, so it runs after the final pane capture and before the session is killed.
//
// The program is found through the session this run created: the pane's pid is the launch shell tmux started, and the program is a descendant of it. SIGQUIT goes to a descendant whose argv[0] is the path the runner launched (bin.Path), and to no other process: the session name is unique to this run and another run's programs are not below its pane. A pig that is a Go program prints every goroutine and exits with status 2; Pi gets the tree and the pane only.
type hangProbe struct {
	session, transcript string
	bin                 BinaryRef
}

// record sets result.HangDump to the whole dump and joins a capped copy of it to result.Err.
func (p hangProbe) record(result *Result) {
	ctx, cancel := context.WithTimeout(context.Background(), hangProbeBudget)
	defer cancel()
	dump := p.collect(ctx)
	result.HangDump = dump
	result.Err = errors.Join(result.Err, errors.New(capDump(dump, hangErrCap)))
}

func (p hangProbe) collect(ctx context.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "hang dump for %s (%s), tmux session %s\n", p.bin.Label, p.bin.Path, p.session)
	fmt.Fprintf(&b, "-- pane, last capture --\n%s\n", paneTail(ctx, p.session))
	b.WriteString("-- process tree under the pane's launch shell (pid ppid state elapsed command) --\n")
	rows, err := sessionProcessRows(ctx, p.session)
	if err != nil {
		fmt.Fprintf(&b, "unavailable: %v\n", err)
		return b.String()
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "%d %d %s %s %s\n", row.pid, row.parent, row.state, row.elapsed, shortCommand(row.command))
		if extra := procDetail(row.pid); extra != "" {
			fmt.Fprintf(&b, "    %s\n", extra)
		}
	}
	if p.bin.Label != "pig" {
		return b.String()
	}
	pid, err := hungProgramPID(rows, p.bin.Path)
	if err != nil {
		fmt.Fprintf(&b, "-- goroutine dump -- not requested: %v\n", err)
		return b.String()
	}
	if err := signalHungPig(pid); err != nil {
		fmt.Fprintf(&b, "-- goroutine dump -- SIGQUIT to %d failed: %v\n", pid, err)
		return b.String()
	}
	fmt.Fprintf(&b, "-- goroutine dump -- SIGQUIT sent to pid %d --\n", pid)
	waitProcessGone(ctx, pid)
	b.WriteString(readGoroutineDump(ctx, p.transcript))
	fmt.Fprintf(&b, "-- pane, after SIGQUIT --\n%s\n", paneTail(ctx, p.session))
	return b.String()
}

type hangProcessRow struct {
	pid, parent int
	state       string
	elapsed     string
	command     string
}

// sessionProcessRows lists the pane's launch shell and everything below it, by pid.
func sessionProcessRows(ctx context.Context, session string) ([]hangProcessRow, error) {
	//nolint:gosec // G204 (class a): program is literal tmux; args are a fixed list-panes with a harness-generated session.
	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs("list-panes", "-s", "-t", session, "-F", "#{pane_pid}")...).Output()
	if err != nil {
		return nil, fmt.Errorf("pane pid of %s: %w", session, err)
	}
	owned := make(map[int]bool)
	for value := range strings.FieldsSeq(string(out)) {
		pid, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("pane pid %q: %w", value, err)
		}
		owned[pid] = true
	}
	table, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,stat=,etime=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var all []hangProcessRow
	var links []processParent
	for line := range strings.SplitSeq(string(table), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil {
			continue
		}
		// args is the rest of the line after the four columns: keep its spacing.
		command := line
		for range 4 {
			_, command, _ = strings.Cut(strings.TrimLeft(command, " "), " ")
		}
		all = append(all, hangProcessRow{pid: pid, parent: parent, state: fields[2], elapsed: fields[3], command: strings.TrimSpace(command)})
		links = append(links, processParent{pid, parent})
	}
	retainDescendants(owned, links)
	var rows []hangProcessRow
	for _, row := range all {
		if owned[row.pid] {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(a, b hangProcessRow) int { return a.pid - b.pid })
	if len(rows) == 0 {
		return nil, fmt.Errorf("no process of %s is running", session)
	}
	return rows, nil
}

// hungProgramPID picks the process the runner launched: exactly one row whose command begins with the launched path and is not the launch shell, whose own command line merely contains it.
func hungProgramPID(rows []hangProcessRow, path string) (int, error) {
	var found []int
	for _, row := range rows {
		if row.command == path || strings.HasPrefix(row.command, path+" ") {
			if !strings.HasPrefix(row.state, "Z") {
				found = append(found, row.pid)
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return 0, fmt.Errorf("no live process below the pane runs %s", path)
	default:
		return 0, fmt.Errorf("%d processes below the pane run %s: %v", len(found), path, found)
	}
}

// procDetail reports, on Linux, what the kernel says one process is doing: its wait channel and the state of each thread. A runtime that never started cannot print a goroutine dump, and this is then the only evidence.
func procDetail(pid int) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	wchan, _ := os.ReadFile(filepath.Join(dir, "wchan"))
	stats, _ := filepath.Glob(filepath.Join(dir, "task", "*", "stat"))
	counts := map[string]int{}
	for _, stat := range stats {
		raw, err := os.ReadFile(stat)
		if err != nil {
			continue
		}
		// pid (comm) state ...: comm may hold spaces and parentheses, so the state follows the last ")".
		if i := bytes.LastIndexByte(raw, ')'); i >= 0 {
			if fields := strings.Fields(string(raw[i+1:])); len(fields) > 0 {
				counts[fields[0]]++
			}
		}
	}
	states := make([]string, 0, len(counts))
	for state, n := range counts {
		states = append(states, fmt.Sprintf("%s=%d", state, n))
	}
	slices.Sort(states)
	return fmt.Sprintf("wchan=%q threads=%d states: %s", strings.TrimSpace(string(wchan)), len(stats), strings.Join(states, " "))
}

// waitProcessGone waits, bounded, until pid has exited or is a zombie.
func waitProcessGone(ctx context.Context, pid int) {
	deadline := time.Now().Add(hangExitWait)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		//nolint:gosec // G204 (class a): program is literal ps; the argument is a pid the runner found.
		out, _ := exec.CommandContext(ctx, "ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if state := strings.TrimSpace(string(out)); state == "" || strings.HasPrefix(state, "Z") {
			return
		}
		time.Sleep(hangPoll)
	}
}

// readGoroutineDump returns the text of the terminal transcript from pig's "SIGQUIT: quit" line on: the Go runtime writes the dump to pig's stderr, which is the pane's terminal. It waits, bounded, for the transcript to hold the dump and stop growing.
func readGoroutineDump(ctx context.Context, transcript string) string {
	deadline := time.Now().Add(hangTranscriptWait)
	var text string
	size := -1
	for ctx.Err() == nil {
		raw, _ := os.ReadFile(transcript)
		text = strings.ReplaceAll(transcriptText(raw), "\r", "")
		if strings.Contains(text, "SIGQUIT: quit") && strings.Contains(text, "goroutine ") && len(raw) == size {
			break
		}
		size = len(raw)
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(3 * hangPoll)
	}
	if i := strings.Index(text, "SIGQUIT: quit"); i >= 0 {
		return text[i:] + "\n"
	}
	return "no \"SIGQUIT: quit\" in the terminal transcript; its last lines:\n" + lastLines(text, hangPaneLines) + "\n"
}

func paneTail(ctx context.Context, session string) string {
	//nolint:gosec // G204 (class a): program is literal tmux; args are a fixed capture-pane with a harness-generated session.
	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs("capture-pane", "-t", session, "-p")...).Output()
	if err != nil {
		return fmt.Sprintf("capture-pane: %v", err)
	}
	pane := strings.TrimRight(string(out), " \n")
	if strings.TrimSpace(pane) == "" {
		return "(blank)"
	}
	return lastLines(pane, hangPaneLines)
}

// shortCommand keeps the start of a command line: the launch shell's line carries every unset variable of the pane and is not what a reader looks for.
func shortCommand(command string) string {
	if len(command) <= hangCommandCap {
		return command
	}
	return command[:hangCommandCap] + fmt.Sprintf("... (%d more bytes)", len(command)-hangCommandCap)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func capDump(dump string, limit int) string {
	if len(dump) <= limit {
		return dump
	}
	return dump[:limit] + fmt.Sprintf("\n... %d more bytes in the failure artifact (hang-dump.txt)", len(dump)-limit)
}

// writeHangDump keeps one run's dump beside the other failure artifacts; a run with no dump writes nothing.
func writeHangDump(write func(name, data string) error, name, dump string) {
	if dump != "" {
		_ = write(name, dump)
	}
}
