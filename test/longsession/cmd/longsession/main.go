// SPDX-License-Identifier: MIT

// Command longsession writes long Pi session fixtures and measures a harness's memory with one loaded.
//
//	longsession gen -tokens 1000000 -window 5000000 -out DIR
//	longsession measure -bin pig -dir DIR [-mode rpc|interactive] [-prompts 5] [-profile heap,metrics]
//	longsession curve -bin pig -out DIR [-from 100000 -to 1000000 -step 100000] [-extra 850000]
//
// gen writes DIR/session.jsonl, DIR/agent (models.json declaring the scripted model with the window, settings.json selecting it) and DIR/meta.json.
//
// measure starts the binary with the fixture's agent directory as PIG_CODING_AGENT_DIR and PI_CODING_AGENT_DIR, a private HOME, and --session on a fresh copy of DIR/session.jsonl (the harness appends its prompts to the session it opens; the copy is removed afterwards), in RPC mode over pipes or in interactive mode in a tmux session. It serves the scripted model itself (an OpenAI-compatible stream that answers "ok" and reports the request's bytes/4 as input tokens) and points the fixture at it. It prints one JSON line per stage to stdout: "loaded" once the harness answers (RPC get_state, or the interactive footer naming the model), then "prompts" after the prompts. Memory is the process tree's Rss, Pss and Private_Dirty from /proc/<pid>/smaps_rollup, in MiB. With -profile, PIG_PROFILE names the kinds and the files land in DIR/profile; the heap profile is written at exit after a forced collection, while the session is still open.
//
// curve runs gen and measure for each size from -from to -to by -step, plus each -extra size, and prints every line.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/test/longsession"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: longsession gen|measure|curve [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = gen(os.Args[2:])
	case "measure":
		err = measureCmd(os.Args[2:])
	case "curve":
		err = curve(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "longsession:", err)
		os.Exit(1)
	}
}

func fixtureFlags(fs *flag.FlagSet) *longsession.Options {
	opts := &longsession.Options{}
	fs.IntVar(&opts.Window, "window", 1_000_000, "declared context window in tokens")
	fs.IntVar(&opts.ResultBytes, "result-bytes", 16384, "bytes per tool result")
	fs.Uint64Var(&opts.Seed, "seed", 1, "content seed")
	return opts
}

func gen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	opts := fixtureFlags(fs)
	fs.IntVar(&opts.Tokens, "tokens", 1_000_000, "context tokens to reach")
	out := fs.String("out", "", "fixture directory")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("gen: -out is required")
	}
	meta, err := longsession.Write(*out, *opts)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(meta)
}

type measureOptions struct {
	bin, dir, mode, profile string
	prompts                 int
	timeout                 time.Duration
}

func measureFlags(fs *flag.FlagSet) *measureOptions {
	m := &measureOptions{}
	fs.StringVar(&m.bin, "bin", "pig", "harness binary")
	fs.StringVar(&m.mode, "mode", "rpc", "rpc or interactive")
	fs.StringVar(&m.profile, "profile", "", "PIG_PROFILE kinds, for example heap,metrics")
	fs.IntVar(&m.prompts, "prompts", 5, "prompts to send after loading")
	fs.DurationVar(&m.timeout, "timeout", 10*time.Minute, "limit for each stage")
	return m
}

func measureCmd(args []string) error {
	fs := flag.NewFlagSet("measure", flag.ExitOnError)
	m := measureFlags(fs)
	fs.StringVar(&m.dir, "dir", "", "fixture directory written by gen")
	_ = fs.Parse(args)
	if m.dir == "" {
		return errors.New("measure: -dir is required")
	}
	return measure(*m, json.NewEncoder(os.Stdout))
}

func curve(args []string) error {
	fs := flag.NewFlagSet("curve", flag.ExitOnError)
	opts := fixtureFlags(fs)
	m := measureFlags(fs)
	out := fs.String("out", "", "directory for the fixtures")
	from := fs.Int("from", 100_000, "first size")
	to := fs.Int("to", 1_000_000, "last size")
	step := fs.Int("step", 100_000, "size step")
	extra := fs.String("extra", "850000", "comma-separated extra sizes")
	_ = fs.Parse(args)
	if *out == "" || *step <= 0 {
		return errors.New("curve: -out and a positive -step are required")
	}
	var sizes []int
	for size := *from; size <= *to; size += *step {
		sizes = append(sizes, size)
	}
	for field := range strings.SplitSeq(*extra, ",") {
		if field = strings.TrimSpace(field); field != "" {
			size, err := strconv.Atoi(field)
			if err != nil {
				return fmt.Errorf("curve: -extra %q: %w", field, err)
			}
			sizes = append(sizes, size)
		}
	}
	slices.Sort(sizes)
	sizes = slices.Compact(sizes)
	enc := json.NewEncoder(os.Stdout)
	for _, size := range sizes {
		dir := filepath.Join(*out, strconv.Itoa(size))
		o := *opts
		o.Tokens = size
		if _, err := longsession.Write(dir, o); err != nil {
			return err
		}
		mo := *m
		mo.dir = dir
		if err := measure(mo, enc); err != nil {
			return fmt.Errorf("size %d: %w", size, err)
		}
	}
	return nil
}

// Line is one measurement.
type Line struct {
	Bin         string             `json:"bin"`
	Mode        string             `json:"mode"`
	Stage       string             `json:"stage"`
	Tokens      int                `json:"tokens"`
	Window      int                `json:"window"`
	Bytes       int64              `json:"sessionBytes"`
	SHA256      string             `json:"sessionSha256"`
	Prompts     int                `json:"prompts"`
	Requests    int64              `json:"modelRequests"`
	Seconds     float64            `json:"seconds"`
	Memory      map[string]float64 `json:"memoryMiB"`
	Profiles    []string           `json:"profiles,omitempty"`
	MetricsFile string             `json:"metricsFile,omitempty"`
}

func measure(m measureOptions, enc *json.Encoder) error {
	var meta longsession.Meta
	data, err := os.ReadFile(filepath.Join(m.dir, "meta.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}
	model, err := startModel()
	if err != nil {
		return err
	}
	defer model.close()
	// The fixture's agent directory points at the endpoint this run serves.
	if err := longsession.WriteAgentDir(meta.AgentDir, meta.Window, model.url); err != nil {
		return err
	}
	// The harness appends the prompts to the session it opens, so it opens a copy: the fixture stays the file meta.json describes, and every harness and run loads the same bytes.
	runDir, err := os.MkdirTemp(m.dir, "run-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(runDir) }()
	session, err := meta.Stage(runDir)
	if err != nil {
		return err
	}
	home := filepath.Join(m.dir, "home")
	work := meta.Cwd
	profileDir := filepath.Join(m.dir, "profile")
	for _, d := range []string{home, work, profileDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	env := []string{
		"HOME=" + home, "PIG_HOME=" + home, "PI_HOME=" + home,
		"PIG_CODING_AGENT_DIR=" + meta.AgentDir, "PI_CODING_AGENT_DIR=" + meta.AgentDir,
		"PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "LANG=C.UTF-8",
		"PI_SKIP_VERSION_CHECK=1", "PI_OFFLINE=1",
	}
	// GODEBUG=memprofilerate=1 makes the heap profile exact instead of sampled every 512 KiB; GOGC and GOMEMLIMIT pass through for the same reason.
	for _, key := range []string{"GODEBUG", "GOGC", "GOMEMLIMIT"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	if m.profile != "" {
		env = append(env, "PIG_PROFILE="+m.profile, "PIG_PROFILE_DIR="+profileDir)
	}
	base := Line{Bin: m.bin, Mode: m.mode, Tokens: meta.Tokens, Window: meta.Window, Bytes: meta.Bytes, SHA256: meta.SHA256}
	var h harness
	switch m.mode {
	case "rpc":
		h, err = startRPC(m.bin, session, work, env)
	case "interactive":
		h, err = startTmux(m.bin, session, work, env)
	default:
		return fmt.Errorf("unknown mode %q", m.mode)
	}
	if err != nil {
		return err
	}
	started := time.Now()
	emit := func(stage string, prompts int) error {
		line := base
		line.Stage, line.Prompts, line.Requests = stage, prompts, model.requests.Load()
		line.Seconds = time.Since(started).Seconds()
		line.Memory = treeMemory(h.pid())
		return enc.Encode(line)
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	err = h.ready(ctx)
	cancel()
	if err == nil {
		err = emit("loaded", 0)
	}
	for i := 1; err == nil && i <= m.prompts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		err = h.prompt(ctx, fmt.Sprintf("Prompt %d after the long session: answer ok.", i), model, int64(i))
		cancel()
	}
	if err == nil && m.prompts > 0 {
		err = emit("prompts", m.prompts)
	}
	exitErr := h.exit(m.timeout)
	if err != nil {
		return err
	}
	if exitErr != nil {
		return exitErr
	}
	if m.profile != "" {
		line := base
		line.Stage = "exit"
		files, _ := filepath.Glob(filepath.Join(profileDir, fmt.Sprintf("pig-%d-*", h.pid())))
		line.Profiles = files
		return enc.Encode(line)
	}
	return nil
}

type harness interface {
	pid() int
	ready(ctx context.Context) error
	prompt(ctx context.Context, text string, model *scriptedModel, want int64) error
	exit(timeout time.Duration) error
}

type rpcHarness struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan map[string]any
	done  chan error
}

func startRPC(bin, session, work string, env []string) (*rpcHarness, error) {
	cmd := exec.Command(bin, "--mode", "rpc", "--session", session)
	cmd.Dir, cmd.Env, cmd.Stderr = work, env, os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &rpcHarness{cmd: cmd, stdin: stdin, lines: make(chan map[string]any, 1024), done: make(chan error, 1)}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1<<20), 1<<30)
		for scanner.Scan() {
			var v map[string]any
			if json.Unmarshal(scanner.Bytes(), &v) == nil {
				h.lines <- v
			}
		}
		close(h.lines)
	}()
	go func() { h.done <- cmd.Wait() }()
	return h, nil
}

func (h *rpcHarness) pid() int { return h.cmd.Process.Pid }

func (h *rpcHarness) send(v map[string]any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = h.stdin.Write(append(data, '\n'))
	return err
}

func (h *rpcHarness) await(ctx context.Context, match func(map[string]any) bool) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("rpc: %w", ctx.Err())
		case line, ok := <-h.lines:
			if !ok {
				return errors.New("rpc: the harness closed stdout")
			}
			if line["type"] == "response" && line["success"] == false {
				return fmt.Errorf("rpc: %v", line["error"])
			}
			if match(line) {
				return nil
			}
		}
	}
}

func (h *rpcHarness) ready(ctx context.Context) error {
	if err := h.send(map[string]any{"id": "ready", "type": "get_state"}); err != nil {
		return err
	}
	return h.await(ctx, func(line map[string]any) bool { return line["id"] == "ready" })
}

func (h *rpcHarness) prompt(ctx context.Context, text string, _ *scriptedModel, _ int64) error {
	if err := h.send(map[string]any{"type": "prompt", "message": text}); err != nil {
		return err
	}
	return h.await(ctx, func(line map[string]any) bool { return line["type"] == "agent_end" })
}

func (h *rpcHarness) exit(timeout time.Duration) error {
	_ = h.stdin.Close()
	select {
	case err := <-h.done:
		return err
	case <-time.After(timeout):
		_ = h.cmd.Process.Kill()
		return errors.New("rpc: the harness did not exit after stdin closed")
	}
}

type tmuxHarness struct {
	name    string
	panePID int
}

func startTmux(bin, session, work string, env []string) (*tmuxHarness, error) {
	name := fmt.Sprintf("longsession-%d-%d", os.Getpid(), time.Now().UnixNano())
	quoted := []string{"exec", "env", "-i"}
	for _, kv := range env {
		quoted = append(quoted, shellQuote(kv))
	}
	quoted = append(quoted, shellQuote(bin), "--session", shellQuote(session))
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-x", "200", "-y", "50", "-c", work, strings.Join(quoted, " "))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux: %w: %s", err, out)
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", name, "#{pane_pid}").Output()
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, err
	}
	return &tmuxHarness{name: name, panePID: pid}, nil
}

func (h *tmuxHarness) pid() int { return h.panePID }

func (h *tmuxHarness) screen() string {
	out, _ := exec.Command("tmux", "capture-pane", "-p", "-t", h.name).Output()
	return string(out)
}

func (h *tmuxHarness) poll(ctx context.Context, done func(string) bool) error {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if done(h.screen()) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("interactive: %w; screen:\n%s", ctx.Err(), h.screen())
		case <-tick.C:
		}
	}
}

// ready waits for the footer to name the model and the screen to hold still for a second.
func (h *tmuxHarness) ready(ctx context.Context) error {
	if err := h.poll(ctx, func(screen string) bool { return strings.Contains(screen, longsession.Model) }); err != nil {
		return err
	}
	return h.settle(ctx)
}

func (h *tmuxHarness) settle(ctx context.Context) error {
	last, since := "", time.Now()
	return h.poll(ctx, func(screen string) bool {
		if screen != last {
			last, since = screen, time.Now()
		}
		return time.Since(since) >= time.Second
	})
}

// prompt types the text and waits until the model has answered it and the screen has not changed for a second.
func (h *tmuxHarness) prompt(ctx context.Context, text string, model *scriptedModel, want int64) error {
	if err := exec.Command("tmux", "send-keys", "-t", h.name, "-l", text).Run(); err != nil {
		return err
	}
	if err := exec.Command("tmux", "send-keys", "-t", h.name, "Enter").Run(); err != nil {
		return err
	}
	if err := h.poll(ctx, func(string) bool { return model.answered.Load() >= want }); err != nil {
		return err
	}
	return h.settle(ctx)
}

func (h *tmuxHarness) exit(timeout time.Duration) error {
	// The session ends with the harness; killing it covers a harness that did not exit, and fails when it is already gone.
	defer func() { _ = exec.Command("tmux", "kill-session", "-t", h.name).Run() }()
	if err := exec.Command("tmux", "send-keys", "-t", h.name, "-l", "/quit").Run(); err != nil {
		return err
	}
	if err := exec.Command("tmux", "send-keys", "-t", h.name, "Enter").Run(); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", h.panePID)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("interactive: the harness did not exit after /quit")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// treeMemory sums smaps_rollup over the process and its descendants, in MiB.
func treeMemory(pid int) map[string]float64 {
	total := map[string]float64{}
	var walk func(int)
	walk = func(pid int) {
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid)); err == nil {
			for line := range strings.SplitSeq(string(data), "\n") {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				switch key {
				case "Rss", "Pss", "Private_Dirty", "Swap":
					fields := strings.Fields(value)
					if kb, err := strconv.ParseFloat(fields[0], 64); err == nil {
						total[strings.ToLower(key)] += kb / 1024
					}
				}
			}
		}
		tasks, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		for _, task := range tasks {
			children, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, task.Name()))
			for field := range strings.FieldsSeq(string(children)) {
				if child, err := strconv.Atoi(field); err == nil {
					walk(child)
				}
			}
		}
	}
	walk(pid)
	for key, value := range total {
		total[key] = float64(int(value*10+0.5)) / 10
	}
	return total
}

// scriptedModel is an OpenAI-compatible chat completions endpoint that streams "ok" and reports the request's bytes/4 as input tokens.
type scriptedModel struct {
	server   *http.Server
	url      string
	requests atomic.Int64
	answered atomic.Int64
}

func startModel() (*scriptedModel, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	m := &scriptedModel{url: "http://" + listener.Addr().String() + "/v1"}
	m.server = &http.Server{Handler: http.HandlerFunc(m.serve), ReadHeaderTimeout: time.Minute}
	go func() { _ = m.server.Serve(listener) }()
	return m, nil
}

func (m *scriptedModel) serve(w http.ResponseWriter, r *http.Request) {
	m.requests.Add(1)
	body, _ := io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "text/event-stream")
	var writeErr error
	chunk := func(v any) {
		data, _ := json.Marshal(v)
		if writeErr == nil {
			_, writeErr = fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}
	id := fmt.Sprintf("chatcmpl-%d", m.requests.Load())
	chunk(map[string]any{"id": id, "object": "chat.completion.chunk", "model": longsession.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": nil}}})
	chunk(map[string]any{"id": id, "object": "chat.completion.chunk", "model": longsession.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	chunk(map[string]any{"id": id, "object": "chat.completion.chunk", "model": longsession.Model, "choices": []any{}, "usage": map[string]any{"prompt_tokens": body / 4, "completion_tokens": 1, "total_tokens": body/4 + 1}})
	if writeErr == nil {
		_, writeErr = fmt.Fprint(w, "data: [DONE]\n\n")
	}
	// A write error means the harness hung up; that request is not answered.
	if writeErr == nil {
		m.answered.Add(1)
	}
}

func (m *scriptedModel) close() { _ = m.server.Close() }
