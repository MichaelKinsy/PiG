// SPDX-License-Identifier: MIT

//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/bench/sessionscale"
)

// point is one measurement of a running arm.
type point struct {
	Arm     string `json:"arm"`
	Mode    string `json:"mode"`
	Fixture string `json:"fixture"`
	Label   string `json:"label"`
	Prompts int    `json:"prompts"`
	// Memory of the process tree, from /proc/<pid>/smaps_rollup, in KiB.
	RssKiB          int64 `json:"rssKiB"`
	PssKiB          int64 `json:"pssKiB"`
	AnonKiB         int64 `json:"anonKiB"`
	PrivateDirtyKiB int64 `json:"privateDirtyKiB"`
	HWMKiB          int64 `json:"hwmKiB"`
	// CPUms is the tree's user+system CPU since start; WallMs the time since start.
	CPUms  int64 `json:"cpuMs"`
	WallMs int64 `json:"wallMs"`
	// PromptCPUms and PromptWallMs are the per-prompt costs of the prompts since the previous point.
	//portlint:allow emptydrop benchmark tool output, not Pi-facing
	PromptCPUms []int64 `json:"promptCpuMs,omitempty"`
	//portlint:allow emptydrop benchmark tool output, not Pi-facing
	PromptWallMs []int64 `json:"promptWallMs,omitempty"`
	Heap         string  `json:"heap,omitempty"`
	//portlint:allow emptydrop benchmark tool output, not Pi-facing
	Requests []requestRecord `json:"requests,omitempty"`
}

type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

func sizeOf(info os.FileInfo) any {
	if info == nil {
		return "missing"
	}
	return info.Size()
}

func run(args []string) (err error) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	arm := fs.String("arm", "", "arm name")
	command := fs.String("cmd", "", "command line of the runtime (space separated)")
	fixture := fs.String("fixture", "", "fixture directory written by gen")
	mode := fs.String("mode", "interactive", "interactive or rpc")
	promptsFlag := fs.String("prompts", "5,50", "prompt counts at which to measure (cumulative)")
	soak := fs.Duration("soak", 0, "after the prompts, stream slowly for this long, measuring every -soak-every")
	soakEvery := fs.Duration("soak-every", time.Minute, "soak measurement interval")
	soakChunk := fs.Duration("soak-chunk-delay", 250*time.Millisecond, "delay between streamed chunks during the soak")
	soakIdle := fs.Duration("soak-idle", 5*time.Second, "idle time between soak prompts")
	heapSignal := fs.Bool("heap", false, "send SIGUSR1 at each point and wait for a heap profile (pigprobe)")
	cpuSignal := fs.Bool("cpu", false, "profile the CPU of each checkpoint's prompts with SIGUSR2 (pigprobe)")
	out := fs.String("out", "", "JSON lines output (default stdout)")
	dump := fs.String("dump", "", "directory that receives each model request body")
	inPlace := fs.String("session", "", "resume this session file in place instead of a copy of the fixture's (no prompts are allowed)")
	private := fs.Bool("private", false, "never print terminal output or keep request bodies (for a private session file)")
	work := fs.String("work", "", "scratch directory (default a temp dir)")
	timeout := fs.Duration("timeout", 30*time.Minute, "overall timeout")
	fs.DurationVar(&quitTimeout, "quit-timeout", quitTimeout, "how long the runtime may take to exit after the quit key")
	var extraEnv stringsFlag
	fs.Var(&extraEnv, "env", "KEY=VALUE for the runtime (repeatable)")
	_ = fs.Parse(args)
	if *arm == "" || *command == "" || (*fixture == "" && *inPlace == "") {
		return errors.New("run: -arm, -cmd and -fixture or -session are required")
	}
	if *inPlace != "" && (*promptsFlag != "" || *soak > 0) {
		return errors.New("run: -session resumes a file in place and sends no prompts; pass -prompts \"\"")
	}
	if *private {
		*dump = ""
	}
	var checkpoints []int
	for f := range strings.SplitSeq(*promptsFlag, ",") {
		if f == "" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			return err
		}
		checkpoints = append(checkpoints, n)
	}
	slices.Sort(checkpoints)

	scratch := *work
	if scratch == "" {
		d, err := os.MkdirTemp("", "sessionscale-run-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(d) }()
		scratch = d
	}
	var meta struct {
		Shape struct {
			Cwd string `json:"Cwd"`
		} `json:"shape"`
		Window int `json:"window"`
	}
	if *fixture == "" {
		*fixture = scratch
	}
	if data, err := os.ReadFile(filepath.Join(*fixture, "meta.json")); err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	server := newScripted()
	server.dumpDir = *dump
	baseURL, stopServer, err := server.start("127.0.0.1:0")
	if err != nil {
		return err
	}
	defer stopServer()
	home := filepath.Join(scratch, "home")
	agentDir := filepath.Join(scratch, "agent")
	heapDir := filepath.Join(scratch, "heap")
	for _, d := range []string{home, agentDir, heapDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	window := meta.Window
	if window == 0 {
		window = 272_000
	}
	if err := sessionscale.WriteAgentDir(agentDir, window, baseURL); err != nil {
		return err
	}
	session := filepath.Join(scratch, "session.jsonl")
	if *inPlace != "" {
		session = *inPlace
		// A resume appends to the file it opens when an entry is missing (Pi's sdk.ts writes a thinking_level_change for an older session), and a file
		// resumed in place is an input that must not change: the run fails when its size or modification time moved.
		before, beforeErr := os.Stat(session)
		if beforeErr != nil {
			return beforeErr
		}
		defer func() {
			after, statErr := os.Stat(session)
			if err == nil && (statErr != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime())) {
				err = fmt.Errorf("run: the runtime changed %s in place (size %d -> %v)", session, before.Size(), sizeOf(after))
			}
		}()
	} else if err := copyFile(filepath.Join(*fixture, "session.jsonl"), session); err != nil {
		return err
	}
	cwd := meta.Shape.Cwd
	if cwd == "" {
		cwd = filepath.Join(*fixture, "work")
	}
	tail := func() string {
		if *private {
			return "(private)"
		}
		return drvTail()
	}
	_ = os.MkdirAll(cwd, 0o700)

	argv := strings.Fields(*command)
	argv = append(argv, "--session", session)
	if *mode == "rpc" {
		argv = append(argv, "--mode", "rpc")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = runtimeEnv(home, agentDir, heapDir, extraEnv)

	w := os.Stdout
	if *out != "" {
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	//portlint:allow jsonescape benchmark tool output, not Pi-facing
	enc := json.NewEncoder(w)

	var drv driver
	if *mode == "rpc" {
		drv, err = startRPC(cmd)
	} else {
		drv, err = startPTY(cmd)
	}
	if err != nil {
		return err
	}
	drvTail = drv.tail
	defer drv.kill()
	pid := cmd.Process.Pid
	deadline := time.Now().Add(*timeout)
	start := time.Now()
	if err := drv.ready(deadline); err != nil {
		return fmt.Errorf("%s: not ready: %w (output tail %q)", *arm, err, tail())
	}
	heapN := 0
	measure := func(label string, prompts int, promptCPU, promptWall []int64) error {
		settle(pid, deadline)
		p := point{Arm: *arm, Mode: *mode, Fixture: filepath.Base(*fixture), Label: label, Prompts: prompts, PromptCPUms: promptCPU, PromptWallMs: promptWall}
		if *heapSignal {
			heapN++
			name := filepath.Join(heapDir, fmt.Sprintf("heap-%d.pprof", heapN))
			if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
				return err
			}
			for time.Now().Before(deadline) {
				if st, err := os.Stat(name + ".done"); err == nil && st != nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			keep := filepath.Join(filepath.Dir(*out), fmt.Sprintf("%s-%s-%s.pprof", *arm, filepath.Base(*fixture), label))
			if *out == "" {
				keep = name
			} else if err := copyFile(name, keep); err != nil {
				return err
			}
			p.Heap = keep
			settle(pid, deadline)
		}
		mem := treeMemory(pid)
		p.RssKiB, p.PssKiB, p.AnonKiB, p.PrivateDirtyKiB, p.HWMKiB = mem.rss, mem.pss, mem.anon, mem.privateDirty, mem.hwm
		p.CPUms = treeCPU(pid)
		p.WallMs = time.Since(start).Milliseconds()
		p.Requests = server.records()
		return enc.Encode(p)
	}
	if err := measure("resume", 0, nil, nil); err != nil {
		return err
	}
	done, cpuN := 0, 0
	for _, target := range checkpoints {
		var cpus, walls []int64
		if *cpuSignal {
			_ = syscall.Kill(pid, syscall.SIGUSR2)
		}
		for done < target {
			done++
			c0, t0 := treeCPU(pid), time.Now()
			if err := drv.prompt(fmt.Sprintf("Prompt %d: summarize the last change in one sentence.", done), server, deadline); err != nil {
				return fmt.Errorf("%s: prompt %d: %w (output tail %q)", *arm, done, err, tail())
			}
			settle(pid, deadline)
			cpus = append(cpus, treeCPU(pid)-c0)
			walls = append(walls, time.Since(t0).Milliseconds())
		}
		if *cpuSignal {
			cpuN++
			_ = syscall.Kill(pid, syscall.SIGUSR2)
			name := filepath.Join(heapDir, fmt.Sprintf("cpu-%d.pprof", cpuN))
			for time.Now().Before(deadline) {
				if _, err := os.Stat(name + ".done"); err == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if *out != "" {
				_ = copyFile(name, filepath.Join(filepath.Dir(*out), fmt.Sprintf("%s-%s-cpu-prompts-%d.pprof", *arm, filepath.Base(*fixture), target)))
			}
		}
		if err := measure(fmt.Sprintf("prompts-%d", target), done, cpus, walls); err != nil {
			return err
		}
	}
	if *soak > 0 {
		server.mu.Lock()
		server.chunkDelay, server.chunks = *soakChunk, 40
		server.mu.Unlock()
		soakStart := time.Now()
		next := soakStart.Add(*soakEvery)
		for time.Since(soakStart) < *soak {
			done++
			if err := drv.prompt(fmt.Sprintf("Soak prompt %d: stream a long answer.", done), server, deadline); err != nil {
				return fmt.Errorf("%s: soak prompt %d: %w", *arm, done, err)
			}
			time.Sleep(*soakIdle)
			if time.Now().After(next) {
				if err := measure(fmt.Sprintf("soak-%s", time.Since(soakStart).Round(time.Second)), done, nil, nil); err != nil {
					return err
				}
				next = next.Add(*soakEvery)
			}
		}
		if err := measure("soak-end", done, nil, nil); err != nil {
			return err
		}
	}
	q0 := time.Now()
	err = drv.quit()
	//portlint:allow mapkeyorder benchmark tool output, not Pi-facing
	_ = enc.Encode(map[string]any{"arm": *arm, "label": "quit", "quitMs": time.Since(q0).Milliseconds(), "error": fmt.Sprint(err)})
	return err
}

// quitTimeout bounds how long a runtime may take to exit after the quit key.
var quitTimeout = 20 * time.Second

// drvTail is the running driver's output tail.
var drvTail = func() string { return "" }

func runtimeEnv(home, agentDir, heapDir string, extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "GOMEMLIMIT", k == "GOGC", k == "GODEBUG", strings.HasPrefix(k, "PIG_"), strings.HasPrefix(k, "PI_"), k == "HOME", k == "XDG_CONFIG_HOME", k == "XDG_DATA_HOME", k == "XDG_CACHE_HOME", k == "XDG_STATE_HOME":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+home, "TERM=xterm-256color", "COLUMNS=120", "LINES=40",
		"PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
		"PIG_HOME="+home, "PI_HOME="+home,
		"PI_OFFLINE=1", "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PIG_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0", "PIG_TELEMETRY=0",
		"SESSIONSCALE_HEAP_DIR="+heapDir,
	)
	return append(env, extra...)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// driver is one way of talking to a runtime.
type driver interface {
	ready(deadline time.Time) error
	prompt(text string, server *scripted, deadline time.Time) error
	tail() string
	quit() error
	kill()
}

// ── pty driver ──────────────────────────────────────────────────────────────

type ptyDriver struct {
	cmd    *exec.Cmd
	master *os.File
	mu     sync.Mutex
	buf    []byte
	last   time.Time
	// exited closes when the runtime exits; waitErr is its exit status then.
	exited  chan struct{}
	waitErr error
}

func (d *ptyDriver) hasExited() bool {
	select {
	case <-d.exited:
		return true
	default:
		return false
	}
}

func startPTY(cmd *exec.Cmd) (*ptyDriver, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return nil, err
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		return nil, err
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120}); err != nil {
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = slave.Close()
	d := &ptyDriver{cmd: cmd, master: master, last: time.Now(), exited: make(chan struct{})}
	go func() {
		d.waitErr = cmd.Wait()
		close(d.exited)
	}()
	//portlint:allow golifetime ends when master.Read fails, which ptyDriver.quit causes by closing master
	go func() {
		b := make([]byte, 64<<10)
		for {
			n, err := master.Read(b)
			if n > 0 {
				d.mu.Lock()
				d.buf = append(d.buf, b[:n]...)
				if len(d.buf) > 4<<20 {
					d.buf = append([]byte(nil), d.buf[len(d.buf)-(1<<20):]...)
				}
				d.last = time.Now()
				d.mu.Unlock()
				// Answer terminal queries (cursor position, device attributes) so a TUI that waits for them proceeds.
				if bytes.Contains(b[:n], []byte("\x1b[6n")) {
					_, _ = master.WriteString("\x1b[1;1R")
				}
				if bytes.Contains(b[:n], []byte("\x1b[c")) {
					_, _ = master.WriteString("\x1b[?62;c")
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return d, nil
}

func (d *ptyDriver) quietFor(dur time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.last) >= dur
}

func (d *ptyDriver) contains(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return bytes.Contains(d.buf, []byte(s))
}

func (d *ptyDriver) ready(deadline time.Time) error {
	answered := 0
	for time.Now().Before(deadline) {
		if d.hasExited() {
			return fmt.Errorf("exited: %w", d.waitErr)
		}
		if d.contains("scripted") && d.quietFor(time.Second) {
			return nil
		}
		// A startup selector (a session whose cwd is missing) waits for a choice; Enter takes the first, Continue.
		if answered < 3 && d.quietFor(time.Second) && d.contains("Continue") && d.contains("Cancel") {
			answered++
			_, _ = d.master.WriteString("\r")
			d.mu.Lock()
			d.last = time.Now()
			d.mu.Unlock()
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("timeout")
}

func (d *ptyDriver) prompt(text string, server *scripted, deadline time.Time) error {
	before := len(server.records())
	if _, err := d.master.WriteString(text); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := d.master.WriteString("\r"); err != nil {
		return err
	}
	for time.Now().Before(deadline) {
		if d.hasExited() {
			return fmt.Errorf("exited: %w", d.waitErr)
		}
		recs := server.records()
		if len(recs) > before && recs[len(recs)-1].End != 0 && d.contains(marker(recs[len(recs)-1].N)) && d.quietFor(300*time.Millisecond) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("timeout")
}

func (d *ptyDriver) tail() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.buf[max(0, len(d.buf)-600):])
}

func (d *ptyDriver) quit() error {
	_, _ = d.master.Write([]byte{4}) // Ctrl+D on an empty editor exits Pi and PiG.
	select {
	case <-d.exited:
		return d.waitErr
	case <-time.After(quitTimeout):
		d.kill()
		return errors.New("quit: timeout")
	}
}

func (d *ptyDriver) kill() {
	if d.cmd.Process != nil {
		_ = syscall.Kill(-d.cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = d.master.Close()
}

// ── rpc driver ──────────────────────────────────────────────────────────────

type rpcDriver struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan map[string]any
	mu     sync.Mutex
	stderr bytes.Buffer
}

func startRPC(cmd *exec.Cmd) (*rpcDriver, error) {
	d := &rpcDriver{cmd: cmd, events: make(chan map[string]any, 4096)}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = &lockedWriter{mu: &d.mu, w: &d.stderr}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	d.stdin = in
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			var ev map[string]any
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				select {
				case d.events <- ev:
				default:
				}
			}
		}
		//portlint:allow doubleclose only this reader goroutine closes events, once, after its scan loop ends
		close(d.events)
	}()
	return d, nil
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (d *rpcDriver) send(v any) error {
	data, _ := json.Marshal(v)
	_, err := d.stdin.Write(append(data, '\n'))
	return err
}

func (d *rpcDriver) await(pred func(map[string]any) bool, deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		//portlint:allow golifetime the timer case returns at the deadline
		select {
		case ev, ok := <-d.events:
			if !ok {
				return errors.New("runtime closed stdout")
			}
			if pred(ev) {
				return nil
			}
		case <-timer.C:
			return errors.New("timeout")
		}
	}
}

func (d *rpcDriver) ready(deadline time.Time) error {
	if err := d.send(map[string]any{"id": "ready", "type": "get_state"}); err != nil {
		return err
	}
	return d.await(func(ev map[string]any) bool { return ev["type"] == "response" && ev["id"] == "ready" }, deadline)
}

func (d *rpcDriver) prompt(text string, _ *scripted, deadline time.Time) error {
	if err := d.send(map[string]any{"type": "prompt", "message": text}); err != nil {
		return err
	}
	return d.await(func(ev map[string]any) bool { return ev["type"] == "agent_end" }, deadline)
}

func (d *rpcDriver) tail() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.stderr.String()
	return s[max(0, len(s)-600):]
}

func (d *rpcDriver) quit() error {
	_ = d.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		d.kill()
		return errors.New("quit: timeout")
	}
}

func (d *rpcDriver) kill() {
	if d.cmd.Process != nil {
		_ = syscall.Kill(-d.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// ── /proc readings ──────────────────────────────────────────────────────────

func treePIDs(root int) []int {
	children := map[int][]int{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
		if len(fields) > 1 {
			ppid, _ := strconv.Atoi(fields[1])
			children[ppid] = append(children[ppid], pid)
		}
	}
	out := []int{root}
	for i := 0; i < len(out); i++ {
		out = append(out, children[out[i]]...)
	}
	return out
}

type memory struct{ rss, pss, anon, privateDirty, hwm int64 }

func treeMemory(root int) memory {
	var m memory
	for _, pid := range treePIDs(root) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
		if err == nil {
			//portlint:allow pathseparators the kernel writes /proc/<pid>/smaps_rollup with \n line ends only; benchmark tool, not Pi-facing
			for line := range strings.SplitSeq(string(data), "\n") {
				f := strings.Fields(line)
				if len(f) < 2 {
					continue
				}
				v, _ := strconv.ParseInt(f[1], 10, 64)
				switch f[0] {
				case "Rss:":
					m.rss += v
				case "Pss:":
					m.pss += v
				case "Anonymous:":
					m.anon += v
				case "Private_Dirty:":
					m.privateDirty += v
				}
			}
		}
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
			//portlint:allow pathseparators the kernel writes /proc/<pid>/status with \n line ends only; benchmark tool, not Pi-facing
			for line := range strings.SplitSeq(string(data), "\n") {
				if v, ok := strings.CutPrefix(line, "VmHWM:"); ok {
					n, _ := strconv.ParseInt(strings.Fields(v)[0], 10, 64)
					m.hwm += n
				}
			}
		}
	}
	return m
}

var clockTicks = int64(100)

func treeCPU(root int) int64 {
	var ticks int64
	for _, pid := range treePIDs(root) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		f := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
		if len(f) > 14 {
			u, _ := strconv.ParseInt(f[11], 10, 64)
			s, _ := strconv.ParseInt(f[12], 10, 64)
			ticks += u + s
		}
	}
	return ticks * 1000 / clockTicks
}

// settle waits until the tree used less than 20 ms of CPU in 500 ms, so a reading is not taken mid-render or mid-collection.
func settle(pid int, deadline time.Time) {
	prev := treeCPU(pid)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		cur := treeCPU(pid)
		if cur-prev < 20 {
			return
		}
		prev = cur
	}
}
