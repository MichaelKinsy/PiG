// SPDX-License-Identifier: MIT

// The ceilings are measured on linux; other platforms allocate differently on their file, terminal and process paths.

//go:build linux

package perfgate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/profiling"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The memory rows of Pi-Bolt's benchmark suite (bolt-v0.8.5 bench/long_session.py and bench/tmux_check.py) as ratchet guards: pig on a pseudo-terminal against Pi-Bolt's scripted model, the retained heap read at the end. They pass at the base; each change that lowers what pig retains lowers their ceilings.

// boltPrompt is the prompt Pi-Bolt's harness types (bench/harness.py PROMPT).
const boltPrompt = "Read the four fixture files"

// boltDone is the end of the model's answer to the nth prompt of a session (bench/harness.py done): the harness waits for it, not for an earlier answer that a redraw writes again.
func boltDone(prompt int) string {
	return fmt.Sprintf("Done: read all four files (prompt %d).", prompt)
}

// boltModel answers like Pi-Bolt's bench/fake_model.py: each turn streams 120 words; the first four turns after a prompt then call read on the next of readToolFiles, arguments streamed 8 bytes per record, and the fifth answers boltDone of the prompt's number. Usage stays at a few thousand tokens against a 200,000-token window, so nothing compacts and the context is the whole history. With distinct, prompt n reads its own copies under fixture/p<n>/ (research/memory-vs-bolt's distinct-contents variant), so no two read results carry the same text. Each response waits latency before its first byte, as a model's time to the first token.
type boltModel struct {
	*httptest.Server
	distinct bool
	latency  time.Duration
	mu       sync.Mutex
	// lastBody is the size of the last request body: the context the process last sent.
	lastBody int
}

func newBoltModel(t *testing.T, distinct bool, latency time.Duration) *boltModel {
	t.Helper()
	model := &boltModel{distinct: distinct, latency: latency}
	model.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { model.answer(t, w, r) }))
	t.Cleanup(model.Close)
	return model
}

// lastRequestBytes is the size of the last request body.
func (m *boltModel) lastRequestBytes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastBody
}

func (m *boltModel) answer(t *testing.T, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("request body: %v", err)
		return
	}
	m.mu.Lock()
	m.lastBody = len(body)
	m.mu.Unlock()
	var request struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Errorf("request body: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	turn, prompt := 0, 0
	for _, message := range request.Messages {
		switch message.Role {
		case "user":
			turn = 0
			prompt++
		case "tool":
			turn++
		}
	}
	var out bytes.Buffer
	chunk := func(delta map[string]any, finish any, usage map[string]any) {
		choices := []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}
		if usage != nil {
			choices = []any{}
		}
		record := map[string]any{"id": "chatcmpl-bench", "object": "chat.completion.chunk", "created": 0, "model": "fake-model", "choices": choices}
		if usage != nil {
			record["usage"] = usage
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Error(err)
		}
		out.WriteString("data: ")
		out.Write(encoded)
		out.WriteString("\n\n")
	}
	chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil)
	for i := range 120 {
		chunk(map[string]any{"content": readToolsWords[i%len(readToolsWords)] + " "}, nil, nil)
	}
	if turn < len(readToolFiles) {
		path := readToolFiles[turn].path
		if m.distinct {
			path = strings.Replace(path, "fixture/", fmt.Sprintf("fixture/p%d/", prompt), 1)
		}
		arguments, _ := json.Marshal(map[string]string{"path": path})
		chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call_%d", turn), "type": "function", "function": map[string]any{"name": "read", "arguments": ""}}}}, nil, nil)
		for i := 0; i < len(arguments); i += 8 {
			chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": string(arguments[i:min(i+8, len(arguments))])}}}}, nil, nil)
		}
		chunk(map[string]any{}, "tool_calls", nil)
	} else {
		chunk(map[string]any{"content": "\n\n" + boltDone(prompt)}, nil, nil)
		chunk(map[string]any{}, "stop", nil)
	}
	chunk(nil, nil, map[string]any{"prompt_tokens": 1000 * (turn + 1), "completion_tokens": 150, "total_tokens": 1000*(turn+1) + 150})
	out.WriteString("data: [DONE]\n\n")
	time.Sleep(m.latency)
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write(out.Bytes())
}

// writeBoltHome configures the fake model as Pi-Bolt's bench/harness.py pi_home does, offline, and writes the fixtures it reads into the working directory: the four of readToolFiles, and with distinct a copy of them per prompt under fixture/p<n>/ that starts with its own line.
func writeBoltHome(t *testing.T, home *pigHome, model *boltModel, prompts int) {
	t.Helper()
	home.env = append(home.env, "PI_OFFLINE=1", "PIG_OFFLINE=1")
	agent := strings.TrimPrefix(envValue(home.env, "PIG_CODING_AGENT_DIR"), "PIG_CODING_AGENT_DIR=")
	models := `{"providers":{"fake":{"baseUrl":"` + model.URL + `/v1","api":"openai-completions","apiKey":"fake","models":[{"id":"fake-model","contextWindow":200000,"maxTokens":8192}]}}}`
	files := map[string]string{
		filepath.Join(agent, "models.json"):   models,
		filepath.Join(agent, "settings.json"): `{"lastChangelogVersion":"9999.0.0","theme":"dark"}`,
		filepath.Join(agent, "auth.json"):     "{}",
	}
	for _, file := range readToolFiles {
		source := writeSource(file.size)
		if !model.distinct {
			files[filepath.Join(home.cwd, file.path)] = source
			continue
		}
		for n := 1; n <= prompts; n++ {
			path := strings.Replace(file.path, "fixture/", fmt.Sprintf("fixture/p%d/", n), 1)
			files[filepath.Join(home.cwd, path)] = fmt.Sprintf("// copy %d\n", n) + source
		}
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// boltArgs is how Pi-Bolt's harness starts the runtime under test.
var boltArgs = []string{"--no-session", "--model", "fake/fake-model"}

// ptyKeep bounds the terminal output a ptyPig keeps: waits look only at the output since a recent mark.
const ptyKeep = 4 << 20

// ptyPig is pig on a pseudo-terminal with PIG_PROFILE=metrics. It keeps what pig writes and answers the cursor position and device attribute queries as a terminal does.
type ptyPig struct {
	t          *testing.T
	command    *exec.Cmd
	master     *os.File
	profileDir string

	mu sync.Mutex
	// out is the tail of the output; written counts every byte pig has written, so written-len(out) is the offset of out[0].
	out     []byte
	written int
	last    time.Time

	exited  chan struct{}
	waitErr error
}

// startPTY starts binary with args in home's working directory and environment on a cols x rows pseudo-terminal, with extra appended to the environment.
func startPTY(t *testing.T, home *pigHome, binary string, args []string, cols, rows uint16, extra ...string) *ptyPig {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pseudo-terminal number: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	p := &ptyPig{t: t, master: master, profileDir: t.TempDir(), last: time.Now(), exited: make(chan struct{})}
	p.command = exec.CommandContext(ctx, binary, args...)
	p.command.Dir = home.cwd
	var env []string
	for _, entry := range home.env {
		if name, _, _ := strings.Cut(entry, "="); name != "TMUX" && name != "TMUX_PANE" {
			env = append(env, entry)
		}
	}
	p.command.Env = append(append(env, "TERM=xterm-256color", "PIG_PROFILE=metrics", "PIG_PROFILE_DIR="+p.profileDir), extra...)
	p.command.Stdin, p.command.Stdout, p.command.Stderr = slave, slave, slave
	p.command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := p.command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	_ = slave.Close()
	t.Cleanup(func() {
		cancel()
		<-p.exited
		_ = master.Close()
	})
	go func() {
		p.waitErr = p.command.Wait()
		close(p.exited)
	}()
	go func() {
		buffer := make([]byte, 64<<10)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
				p.mu.Lock()
				p.out = append(p.out, chunk...)
				p.written += n
				if len(p.out) > ptyKeep {
					p.out = append([]byte(nil), p.out[len(p.out)-ptyKeep/4:]...)
				}
				p.last = time.Now()
				p.mu.Unlock()
				if bytes.Contains(chunk, []byte("\x1b[6n")) {
					_, _ = master.WriteString("\x1b[1;1R")
				}
				if bytes.Contains(chunk, []byte("\x1b[c")) {
					_, _ = master.WriteString("\x1b[?62;c")
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return p
}

// mark is the offset of the next byte pig writes.
func (p *ptyPig) mark() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.written
}

func (p *ptyPig) send(text string) {
	p.t.Helper()
	if _, err := p.master.WriteString(text); err != nil {
		p.t.Fatalf("write to pig: %v", err)
	}
}

// tail is the last of the output, for failure messages.
func (p *ptyPig) tail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.out[max(0, len(p.out)-1500):])
}

// waitFor waits until pig has written marker since the offset since, then until it has written nothing for quiet.
func (p *ptyPig) waitFor(since int, marker string, quiet time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(testbudget.Wait(p.t))
	found := false
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			p.t.Fatalf("pig exited (%v) before %q:\n%s", p.waitErr, marker, p.tail())
		default:
		}
		p.mu.Lock()
		if !found {
			start := max(0, since-(p.written-len(p.out)))
			found = start <= len(p.out) && bytes.Contains(p.out[start:], []byte(marker))
		}
		settled := found && time.Since(p.last) >= quiet
		p.mu.Unlock()
		if settled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatalf("pig did not write %q:\n%s", marker, p.tail())
}

// settle waits until pig has written nothing for quiet.
func (p *ptyPig) settle(quiet time.Duration) { p.waitFor(p.mark(), "", quiet) }

// ready waits for the first ready frame: the footer names the model.
func (p *ptyPig) ready() { p.waitFor(0, "fake-model", 300*time.Millisecond) }

// prompt types boltPrompt, sends it, and waits for the answer to the nth prompt and then for quiet.
func (p *ptyPig) prompt(n int, quiet time.Duration) {
	p.t.Helper()
	since := p.mark()
	p.send(boltPrompt)
	time.Sleep(50 * time.Millisecond)
	p.send("\r")
	p.waitFor(since, boltDone(n), quiet)
}

// exit sends Ctrl+D to the empty editor and returns the exit record PIG_PROFILE=metrics wrote.
func (p *ptyPig) exit() profiling.ProcessMetrics {
	p.t.Helper()
	p.send("\x04")
	select {
	case <-p.exited:
	case <-time.After(testbudget.Wait(p.t)):
		p.t.Fatalf("pig did not exit after Ctrl+D:\n%s", p.tail())
	}
	if p.waitErr != nil {
		p.t.Fatalf("pig: %v\n%s", p.waitErr, p.tail())
	}
	matches, _ := filepath.Glob(filepath.Join(p.profileDir, "pig-*-metrics.json"))
	if len(matches) != 1 {
		p.t.Fatalf("pig wrote no metrics record (%v)\n%s", matches, p.tail())
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		p.t.Fatal(err)
	}
	var record profiling.ProcessMetrics
	if err := json.Unmarshal(data, &record); err != nil {
		p.t.Fatalf("%s: %v", matches[0], err)
	}
	return record
}

// longSessionPrompts is the length of the long row's session: Pi-Bolt runs 75 prompts and samples every 25; the guard stops at the first sample.
const longSessionPrompts = 25

// Pi-Bolt's long row (bench/long_session.py, "memory after a 4.2M-token session") at its first sample point: one interactive --no-session process, 25 prompts of four read tool calls on 47-239 KB files and an answer, then Ctrl+D. Gated: the live heap after two collections at exit, and that heap less the last request body, which is what pig retains beyond the context it sends (memory bounded by the context). "same contents" is Pi-Bolt's script, four distinct texts read 25 times each; "distinct contents" reads 100 distinct texts, as a real session does, which the request splice cache's quoted copies show.
func TestLongSessionMemoryGate(t *testing.T) {
	for _, variant := range []struct {
		name, heap, minusContext string
		distinct                 bool
	}{
		{"same contents", "process.mem_long25.heap_live_bytes", "process.mem_long.heap_live_minus_context_bytes", false},
		{"distinct contents", "process.mem_long25_distinct.heap_live_bytes", "process.mem_long_distinct.heap_live_minus_context_bytes", true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			binary, err := pigBinary()
			if err != nil {
				t.Fatal(err)
			}
			home := newPigHome(t)
			model := newBoltModel(t, variant.distinct, 0)
			writeBoltHome(t, home, model, longSessionPrompts)
			warm := startPTY(t, home, binary, boltArgs, 120, 40) // The first start in a new home materializes caches; gate the next one.
			warm.ready()
			warm.exit()
			pig := startPTY(t, home, binary, boltArgs, 120, 40)
			pig.ready()
			for n := 1; n <= longSessionPrompts; n++ {
				pig.prompt(n, 100*time.Millisecond)
			}
			record := pig.exit()
			sent := model.lastRequestBytes()
			t.Logf("perfgate: %s record %+v; last request body %d B", variant.heap, record, sent)
			perfgate.Check(t, variant.heap, float64(record.HeapLiveBytes))
			perfgate.Check(t, variant.minusContext, float64(record.HeapLiveBytes)-float64(sent))
		})
	}
}

// memProbeSource is added to cmd/pig's package main (go build -overlay) for the memory snapshot of an idle process: with PIG_PERFGATE_SNAPSHOT_DIR set, SIGUSR1 reads runtime/metrics without a collection and writes the values as snapshot-<n>.json there, and SIGUSR2 writes the cumulative allocation counters, flushed as the exit record flushes them, as allocs-<n>.json. Without it the binary is cmd/pig.
const memProbeSource = `// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"syscall"
)

func init() {
	dir := os.Getenv("PIG_PERFGATE_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for n := 1; ; n++ {
			<-signals
			samples := []metrics.Sample{
				{Name: "/gc/heap/live:bytes"},
				{Name: "/memory/classes/total:bytes"},
				{Name: "/memory/classes/heap/released:bytes"},
				{Name: "/gc/cycles/total:gc-cycles"},
				{Name: "/gc/cycles/forced:gc-cycles"},
			}
			metrics.Read(samples)
			values := map[string]uint64{}
			for _, sample := range samples {
				values[sample.Name] = sample.Value.Uint64()
			}
			data, _ := json.Marshal(values)
			path := filepath.Join(dir, fmt.Sprintf("snapshot-%d.json", n))
			_ = os.WriteFile(path+".tmp", data, 0o600)
			_ = os.Rename(path+".tmp", path)
		}
	}()
	allocSignals := make(chan os.Signal, 1)
	signal.Notify(allocSignals, syscall.SIGUSR2)
	go func() {
		for n := 1; ; n++ {
			<-allocSignals
			// The cumulative allocation counters, read as the exit record reads them (profiling.CollectMetrics): ReadMemStats flushes the allocation caches
			// without a collection, which a runtime/metrics read does not.
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			samples := []metrics.Sample{{Name: "/gc/heap/allocs:objects"}, {Name: "/gc/heap/allocs:bytes"}}
			metrics.Read(samples)
			values := map[string]uint64{}
			for _, sample := range samples {
				values[sample.Name] = sample.Value.Uint64()
			}
			data, _ := json.Marshal(values)
			path := filepath.Join(dir, fmt.Sprintf("allocs-%d.json", n))
			_ = os.WriteFile(path+".tmp", data, 0o600)
			_ = os.Rename(path+".tmp", path)
		}
	}()
}
`

// memProbeBinary is cmd/pig with memProbeSource, built beside pigBinary so TestMain removes it.
var memProbeBinary = sync.OnceValues(func() (string, error) {
	pig, err := pigBinary()
	if err != nil {
		return "", err
	}
	root, err := testenv.ModuleRootPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(pig), "memprobe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	source := filepath.Join(dir, "memprobe.go")
	if err := os.WriteFile(source, []byte(memProbeSource), 0o600); err != nil {
		return "", err
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(root, "cmd", "pig", "zz_perfgate_memprobe.go"): source}})
	if err != nil {
		return "", err
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "pig")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-overlay", overlayPath, "-o", out, "./cmd/pig")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/pig with the memory probe: %w\n%s", err, data)
	}
	return out, nil
})

// snapshot signals the probe and returns the runtime/metrics values it wrote.
func (p *ptyPig) snapshot(dir string, n int) map[string]uint64 {
	p.t.Helper()
	if err := p.command.Process.Signal(syscall.SIGUSR1); err != nil {
		p.t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("snapshot-%d.json", n))
	deadline := time.Now().Add(testbudget.Wait(p.t))
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			values := map[string]uint64{}
			if err := json.Unmarshal(data, &values); err != nil {
				p.t.Fatalf("%s: %v", path, err)
			}
			return values
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatalf("pig wrote no %s", path)
	return nil
}

// tmuxIdlePrompts is the number of read prompts of the tmux row: Pi-Bolt's four, the one it resizes the window under, the one it aborts and "Say hi", all answered in full here.
const tmuxIdlePrompts = 6

// tmuxModelLatency stands in for the human pace of the tmux row's model (one event every 10 ms, about 1.3 s a turn): each turn's response waits this long, so a run spans spinner ticks as it does in Pi-Bolt's row, and the run's end arms the idle collection.
const tmuxModelLatency = 100 * time.Millisecond

// tmuxIdleSamples is how many times the tmux row runs; its gate reads the minimum, as the fused dispatch gate does
// (545fa6f364). What an idle pig holds moves by tens of kilobytes between runs of one build (a run's spinner ticks and
// renders land differently: CI 39063579995 read 6,334,080 bytes where five runs of the same tree read 6,313,416 to
// 6,318,392), so one run's reading crossed a ceiling no change had moved. The minimum is what the build holds at least.
const tmuxIdleSamples = 3

// idleCollectionWait is how long the tmux row leaves pig idle before it reads its memory: the interactive idle collection (D172) runs 2 s after a run ends.
const idleCollectionWait = 3 * time.Second

// Pi-Bolt's tmux row (bench/tmux_check.py, "memory of a session in tmux") on a 160x48 pseudo-terminal: 40 typed keys, a 400-line bracketed paste, cleared, then six prompts of four read tool calls and an answer, then idle until the idle collection has run. Gated from runtime/metrics in the idle process: the live heap and the memory the Go runtime holds and has not returned to the OS (/memory/classes/total less /memory/classes/heap/released).
func TestTmuxIdleMemoryGate(t *testing.T) {
	binary, err := memProbeBinary()
	if err != nil {
		t.Fatal(err)
	}
	live, nonreleased := math.Inf(1), math.Inf(1)
	for range tmuxIdleSamples {
		l, n := tmuxIdleSample(t, binary)
		live, nonreleased = min(live, l), min(nonreleased, n)
	}
	perfgate.Check(t, "process.mem_tmux_idle.heap_live_bytes", live)
	perfgate.Check(t, "process.mem_tmux_idle.go_nonreleased_bytes", nonreleased)
}

// tmuxIdleSample runs the tmux row once and returns the idle process's live heap and the memory the runtime holds and has
// not returned to the OS.
func tmuxIdleSample(t *testing.T, binary string) (live, nonreleased float64) {
	t.Helper()
	home := newPigHome(t)
	model := newBoltModel(t, false, tmuxModelLatency)
	writeBoltHome(t, home, model, tmuxIdlePrompts)
	warm := startPTY(t, home, binary, boltArgs, 160, 48) // The first start in a new home materializes caches; gate the next one.
	warm.ready()
	warm.exit()
	snapshots := t.TempDir()
	pig := startPTY(t, home, binary, boltArgs, 160, 48, "PIG_PERFGATE_SNAPSHOT_DIR="+snapshots)
	pig.ready()
	for i := range 40 {
		pig.send(string("abcdefghij"[i%10]))
		time.Sleep(5 * time.Millisecond)
	}
	pig.settle(200 * time.Millisecond)
	pig.send("\x15") // Ctrl+U
	pig.settle(200 * time.Millisecond)
	lines := make([]string, 400)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d: lorem ipsum dolor sit amet", i)
	}
	pig.send("\x1b[200~" + strings.Join(lines, "\r") + "\x1b[201~")
	pig.settle(300 * time.Millisecond)
	pig.send("\x15")
	pig.settle(100 * time.Millisecond)
	pig.send("\x1b")
	pig.settle(300 * time.Millisecond)
	for n := 1; n <= tmuxIdlePrompts; n++ {
		pig.prompt(n, 300*time.Millisecond)
	}
	pig.settle(idleCollectionWait)
	values := pig.snapshot(snapshots, 1)
	record := pig.exit()
	live = float64(values["/gc/heap/live:bytes"])
	nonreleased = float64(values["/memory/classes/total:bytes"]) - float64(values["/memory/classes/heap/released:bytes"])
	t.Logf("perfgate: process.mem_tmux_idle idle runtime/metrics %v; exit record %+v", values, record)
	return live, nonreleased
}
