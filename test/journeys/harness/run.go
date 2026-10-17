package harness

import (
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// Config is shared by every run of one test process.
type Config struct {
	PigBin string
	PiBin  string
	// Instrumented marks the PiG binary as built with the race detector, the lock-order wrappers and the request-validity tag; its memory bound is scaled.
	Instrumented bool
	// ArtifactDir receives the evidence of failing runs.
	ArtifactDir string
	// CoverDir, when set, is GOCOVERDIR for PiG runs (the binary must be built with -cover).
	CoverDir string
	// Scale multiplies every wait deadline (JOURNEY_TIMEOUT_SCALE) for loaded hosts.
	Scale float64
}

// Failure is one checker or comparison failure.
type Failure struct {
	Kind string // hang, request, race, lockorder, memory, leak, exit, stderr, frame, session, journey
	Msg  string
}

func (f Failure) String() string { return f.Kind + ": " + f.Msg }

// Snap is one recorded frame.
type Snap struct {
	Index int
	Name  string
	Plain string
}

// MemSample is a resident-set sample of the product's process tree.
type MemSample struct {
	At    string
	Total int64
	HWM   int64
}

type abortRun struct{}

// Run is one product instance executing a journey plan; it is the Ctx every step receives.
type Run struct {
	Cfg  *Config
	J    *Journey
	Kind Kind
	Plan Plan
	Env  Env
	SB   *Sandbox
	P    *Provider
	T    Driver

	id        string
	artifacts string
	launches  int
	stepName  string
	stepStart time.Time
	rng       *rand.Rand

	Failures []Failure
	Snaps    []Snap
	Mem      []MemSample
	Timings  []StepTiming
	Started  time.Time
	Elapsed  time.Duration

	finalScreen string
	snapOff     int // pipe journeys: the part of the output already framed
}

var runCounter atomic.Int64

func (c *Run) scaled(d time.Duration) time.Duration {
	if c.Cfg.Scale > 0 {
		return time.Duration(float64(d) * c.Cfg.Scale)
	}
	return d
}

// Timeouts for event waits; each is scaled by Config.Scale.
const (
	hangQuiet = 25 * time.Second // no terminal or provider event for this long while a condition is unmet
	stepMax   = 90 * time.Second // upper bound for one wait even when events keep arriving
	quietGap  = 120 * time.Millisecond
)

// NewRun prepares the sandbox and provider for one product.
func NewRun(cfg *Config, j *Journey, kind Kind, plan Plan, root string) (*Run, error) {
	c := &Run{Cfg: cfg, J: j, Kind: kind, Plan: plan, Started: time.Now()}
	c.id = fmt.Sprintf("j%d-%d", os.Getpid(), runCounter.Add(1))
	if j.Env != nil {
		c.Env = j.Env(kind)
	}
	var err error
	c.P, err = NewProvider(j.Script)
	if err != nil {
		return nil, err
	}
	c.P.OnRequest = func(r *Request) error {
		if v := CheckChatRequest(r); len(v) > 0 {
			return errors.New(strings.Join(v, "; "))
		}
		return nil
	}
	extra := append([]string{"PIG_JOURNEY_ID=" + c.id}, c.Env.ExtraEnv...)
	if kind == PiG {
		if cfg.Instrumented {
			extra = append(extra,
				"GORACE=log_path="+filepath.Join(root, "race")+" exitcode=66 halt_on_error=0",
				"PIG_LOCKORDER_REPORT="+filepath.Join(root, "lockorder.txt"),
				"PIG_REQUEST_VALIDITY_REPORT="+filepath.Join(root, "requestvalidity.txt"))
		}
		if cfg.CoverDir != "" {
			extra = append(extra, "GOCOVERDIR="+cfg.CoverDir)
		}
	} else {
		extra = append(extra, "PI_SKIP_VERSION_CHECK=1")
	}
	settings := map[string]any{}
	if !c.Env.KeepHeader {
		settings["quietStartup"] = true
	}
	maps.Copy(settings, c.Env.Settings)
	c.SB, err = NewSandbox(SandboxOpts{Kind: kind, Root: root, Provider: c.P, Settings: settings, Files: c.Env.Files,
		ExtraEnv: extra, Reasoning: c.Env.Reasoning, Images: c.Env.Images, Bin: c.Env.Bin, AgentFiles: c.Env.AgentDirFiles, ContextWindow: c.Env.ContextWindow})
	if err != nil {
		c.P.Close()
		return nil, err
	}
	c.artifacts = filepath.Join(cfg.ArtifactDir, fmt.Sprintf("%s-seed%d", j.Name, plan.Seed), string(kind))
	return c, nil
}

// Intn returns a seeded value in [0,n) from the current step's random source; both products draw the same sequence, and removing other steps never changes it.
func (c *Run) Intn(n int) int { return c.rng.IntN(n) }

// Pick returns a seeded choice among options for the current step.
func Pick[T any](c *Run, options ...T) T { return options[c.rng.IntN(len(options))] }

func (c *Run) bin() string {
	if c.Kind == PiG {
		return c.Cfg.PigBin
	}
	return c.Cfg.PiBin
}

// Launch starts the product with the base arguments plus extra. It waits for the interactive prompt.
func (c *Run) Launch(extra ...string) {
	c.launchRaw(extra...)
	c.WaitReady()
	c.Quiet(c.scaled(quietGap))
}

func (c *Run) launchRaw(extra ...string) {
	argv := []string{c.bin()}
	if !c.Env.Extensions {
		argv = append(argv, "--no-extensions")
	}
	if !c.Env.NoModelArg {
		argv = append(argv, "--model", ModelRef)
	}
	argv = append(argv, "--session-dir", c.SB.Sessions)
	argv = append(argv, c.Env.Args...)
	argv = append(argv, extra...)
	c.launches++
	c.snapOff = 0
	var t Driver
	var err error
	if c.Env.Pipe {
		t, err = StartPipe(PipeOpts{Dir: c.SB.Cwd, Env: c.SB.Env, Argv: argv})
	} else {
		w, h := c.Env.Width, c.Env.Height
		if w == 0 {
			w = 120
		}
		if h == 0 {
			h = 40
		}
		t, err = StartTerminal(TermOpts{Dir: c.SB.Cwd, Env: c.SB.Env, Argv: argv, Width: w, Height: h,
			Root: filepath.Join(c.SB.Root, fmt.Sprintf("term%d", c.launches))})
	}
	if err != nil {
		c.fail("journey", "start process: %v", err)
	}
	c.T = t
	c.sampleMem("launch")
}

// WaitReady waits for the footer's model name, which both products draw once the prompt is live.
func (c *Run) WaitReady() {
	if c.Env.Pipe {
		c.Send(`{"id":"ready","type":"get_state"}`)
		c.WaitFor(`"id":"ready"`)
		return
	}
	c.Await("interactive prompt", func(s string) bool {
		lines := strings.Split(s, "\n")
		for i := max(0, len(lines)-4); i < len(lines); i++ {
			if strings.Contains(lines[i], "strict") {
				return true
			}
		}
		return false
	})
}

func (c *Run) fail(kind, format string, args ...any) {
	c.Failures = append(c.Failures, Failure{Kind: kind, Msg: fmt.Sprintf(format, args...)})
	panic(abortRun{})
}

func (c *Run) note(kind, format string, args ...any) {
	c.Failures = append(c.Failures, Failure{Kind: kind, Msg: fmt.Sprintf(format, args...)})
}

// Fail aborts the run with a journey assertion failure.
func (c *Run) Fail(format string, args ...any) { c.fail("assert", format, args...) }

// Expect records an assertion failure and continues.
func (c *Run) Expect(ok bool, format string, args ...any) {
	if !ok {
		c.note("assert", "step %s: "+format, append([]any{c.stepName}, args...)...)
	}
}

// Await blocks, event by event, until pred holds for the visible screen. It never sleeps: it wakes on terminal output, provider activity or process exit. If neither source produces an event for hangQuiet while the condition stays false, or the wait exceeds stepMax, the run fails as a hang with a goroutine dump.
func (c *Run) Await(desc string, pred func(screen string) bool) string {
	start := time.Now()
	quiet := time.NewTimer(c.scaled(hangQuiet))
	defer quiet.Stop()
	hard := time.NewTimer(c.scaled(stepMax))
	defer hard.Stop()
	for {
		tch := c.T.Changed()
		pch := c.P.Changed()
		screen, err := c.T.Screen()
		if err != nil {
			c.fail("journey", "capture screen while waiting for %s: %v", desc, err)
		}
		if pred(screen) {
			return screen
		}
		select {
		case <-c.T.Exited():
			screen, _ = c.T.Screen()
			if pred(screen) {
				return screen
			}
			c.fail("exit", "process exited while waiting for %s (exit %s)\n%s", desc, c.exitText(), screen)
		default:
		}
		select {
		case <-tch:
			resetTimer(quiet, c.scaled(hangQuiet))
		case <-pch:
			resetTimer(quiet, c.scaled(hangQuiet))
		case <-c.T.Exited():
		case <-quiet.C:
			c.hang(desc, fmt.Sprintf("no terminal or provider event for %s", c.scaled(hangQuiet)), time.Since(start))
		case <-hard.C:
			c.hang(desc, fmt.Sprintf("condition unmet after %s of activity", c.scaled(stepMax)), time.Since(start))
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func (c *Run) exitText() string {
	if code, err := c.T.ExitCode(); err == nil {
		return fmt.Sprint(code)
	}
	return "?"
}

// hang records a hang with every available diagnostic and aborts the run.
func (c *Run) hang(desc, why string, waited time.Duration) {
	screen, _ := c.T.Screen()
	var b strings.Builder
	fmt.Fprintf(&b, "step %q waited %s for %s: %s\n", c.stepName, waited.Round(time.Millisecond), desc, why)
	fmt.Fprintf(&b, "--- screen ---\n%s\n--- provider events ---\n", screen)
	for _, e := range c.P.Events() {
		fmt.Fprintf(&b, "%s %s #%d %s\n", e.At.Format("15:04:05.000"), e.Kind, e.N, e.Name)
	}
	if pid := c.T.PID(); pid > 0 && c.Kind == PiG {
		// SIGQUIT makes the Go runtime write every goroutine's stack to stderr and exit.
		_ = signalPID(pid, 3)
		select {
		case <-c.T.Exited():
		case <-time.After(15 * time.Second):
		}
		dump := c.T.Stderr()
		_ = os.MkdirAll(c.artifacts, 0o755)
		dumpPath := filepath.Join(c.artifacts, "goroutine-dump.txt")
		_ = os.WriteFile(dumpPath, []byte(dump), 0o644)
		// Goroutines blocked on the terminal's own input are noise; the report keeps the head of the dump and the file has the rest.
		head := strings.Join(strings.SplitN(dump, "\n", 121)[:min(120, strings.Count(dump, "\n")+1)], "\n")
		fmt.Fprintf(&b, "--- goroutine dump (SIGQUIT), first 120 lines; full dump in %s ---\n%s\n", dumpPath, head)
	}
	c.Failures = append(c.Failures, Failure{Kind: "hang", Msg: b.String()})
	panic(abortRun{})
}

// quietMax bounds Quiet: a running loader repaints continuously, so the terminal may never go silent.
const quietMax = 2 * time.Second

// Quiet waits until the terminal has been silent for d (event-driven; the timer restarts on every output), or until quietMax passes while output keeps arriving. A frame taken after Quiet therefore shows a settled screen, or a screen with a running loader whose animation phase the comparison normalizes.
func (c *Run) Quiet(d time.Duration) {
	limit := time.NewTimer(c.scaled(quietMax))
	defer limit.Stop()
	for {
		ch := c.T.Changed()
		t := time.NewTimer(d)
		select {
		case <-ch:
			t.Stop()
		case <-c.T.Exited():
			t.Stop()
			return
		case <-limit.C:
			t.Stop()
			return
		case <-t.C:
			return
		}
	}
}

// Idle waits until the provider has received at least requests requests, every request has finished (or its client disconnected), no loader row is visible, and the terminal is quiet.
func (c *Run) Idle(requests int) {
	c.Await(fmt.Sprintf("idle after %d provider requests", requests), func(s string) bool {
		if len(c.P.Requests()) < requests {
			return false
		}
		if c.P.Count(EvDone)+c.P.Count(EvDisconnect) < len(c.P.Requests()) {
			return false
		}
		return !hasLoader(s)
	})
	c.Quiet(c.scaled(quietGap))
	// A frame may have restarted work during the quiet window (a queued message, a follow-up request).
	s, _ := c.T.Screen()
	if hasLoader(s) || c.P.Count(EvDone)+c.P.Count(EvDisconnect) < len(c.P.Requests()) {
		c.Idle(requests)
	}
}

// loaderPattern matches a running loader on its own row and the working status embedded in the editor's top border ("── ⠋ Working ──",
// custom-editor.ts embedWorkingStatus), which a row-start pattern misses.
var loaderPattern = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]\s+\S|Working\.\.\.|Working…`)

func hasLoader(screen string) bool { return loaderPattern.MatchString(screen) }

// Send writes one line to the process's standard input (an RPC command).
func (c *Run) Send(line string) {
	if err := c.T.SendText(line + "\n"); err != nil {
		c.fail("journey", "send %q: %v", truncate(line, 80), err)
	}
}

// WaitCount waits until sub occurs at least n times on the screen.
func (c *Run) WaitCount(sub string, n int) string {
	return c.Await(fmt.Sprintf("%d x %q visible", n, sub), func(s string) bool { return strings.Count(s, sub) >= n })
}

// WaitFor waits until every substring is visible.
func (c *Run) WaitFor(subs ...string) string {
	return c.Await(fmt.Sprintf("%q visible", subs), func(s string) bool {
		for _, sub := range subs {
			if !strings.Contains(s, sub) {
				return false
			}
		}
		return true
	})
}

// WaitGone waits until sub is no longer visible.
func (c *Run) WaitGone(sub string) {
	c.Await(fmt.Sprintf("%q gone", sub), func(s string) bool { return !strings.Contains(s, sub) })
}

// Say types text and submits it.
func (c *Run) Say(text string) {
	c.Type(text)
	c.Key("Enter")
}

// Type types literal text.
func (c *Run) Type(text string) {
	if err := c.T.SendText(text); err != nil {
		c.fail("journey", "send text: %v", err)
	}
}

// Key sends named keys (Enter, Escape, C-c, Up, ...).
func (c *Run) Key(keys ...string) {
	if err := c.T.SendKeys(keys...); err != nil {
		c.fail("journey", "send keys %v: %v", keys, err)
	}
}

// Bytes writes raw bytes to the terminal input.
func (c *Run) Bytes(b []byte) {
	if err := c.T.SendBytes(b); err != nil {
		c.fail("journey", "send bytes: %v", err)
	}
}

// Resize changes the terminal size and waits until the product writes to the terminal again, which is its repaint for the new size. A screen predicate cannot tell the repaint from the old screen: tmux reflows what was drawn, which leaves rules and footers of plausible length behind.
func (c *Run) Resize(w, h int) {
	before := c.T.Outputs()
	if err := c.T.Resize(w, h); err != nil {
		c.fail("journey", "resize: %v", err)
	}
	c.Await(fmt.Sprintf("the repaint after resizing to %dx%d", w, h), func(string) bool { return c.T.Outputs() > before })
}

// Screen returns the visible screen.
func (c *Run) Screen() string {
	s, err := c.T.Screen()
	if err != nil {
		c.fail("journey", "screen: %v", err)
	}
	return s
}

// Snap records the visible screen as frame name for comparison with the other product.
func (c *Run) Snap(name string) {
	plain := c.Screen()
	if c.Env.Pipe {
		// A pipe has no screen: a frame is what the process wrote since the previous frame.
		plain, c.snapOff = plain[c.snapOff:], len(plain)
	}
	c.Snaps = append(c.Snaps, Snap{Index: len(c.Snaps), Name: fmt.Sprintf("%s/%s", c.stepName, name), Plain: plain})
	c.sampleMem("snap " + name)
}

func (c *Run) sampleMem(at string) {
	if c.T == nil || c.Kind != PiG {
		return
	}
	pid := c.T.PID()
	if pid == 0 {
		return
	}
	total, hwm := ProcTreeRSS(pid)
	c.Mem = append(c.Mem, MemSample{At: at, Total: total, HWM: hwm})
}

// WaitExit waits for the process to exit and returns its status.
func (c *Run) WaitExit() int {
	t := time.NewTimer(c.scaled(stepMax))
	defer t.Stop()
	select {
	case <-c.T.Exited():
	case <-t.C:
		c.hang("process exit", "process did not exit", c.scaled(stepMax))
	}
	code, err := c.T.ExitCode()
	if err != nil {
		c.fail("exit", "exit status: %v", err)
	}
	return code
}

// Quit leaves the product (Ctrl+D on an empty prompt, end of input for RPC) and checks the exit status.
func (c *Run) Quit() {
	if err := c.T.Quit(); err != nil {
		c.fail("journey", "quit: %v", err)
	}
	code := c.WaitExit()
	c.checkExit(code)
}

func (c *Run) checkExit(code int) {
	if code != c.J.ExitCode {
		c.note("exit", "%s exited %d, want %d\nstderr:\n%s", c.Kind, code, c.J.ExitCode, c.T.Stderr())
	}
	c.finalScreen, _ = c.T.History()
}

// Restart quits the running process and starts a new one in the same sandbox.
func (c *Run) Restart(extra ...string) {
	c.Quit()
	c.sweepLeaks()
	c.Launch(extra...)
}

// Execute runs the plan. It never panics: failures are recorded in c.Failures.
func (c *Run) Execute() {
	defer func() { c.Elapsed = time.Since(c.Started) }()
	steps := []Step{}
	steps = append(steps, c.J.Backbone...)
	for _, name := range c.Plan.Pool {
		a, ok := c.J.action(name)
		if !ok {
			c.note("journey", "plan names unknown action %q", name)
			continue
		}
		steps = append(steps, a.Step)
	}
	steps = append(steps, c.J.Tail...)
	aborted := false
	for _, s := range steps {
		if !c.runStep(s) {
			aborted = true
			break
		}
	}
	c.finish(aborted)
}

func (c *Run) runStep(s Step) (ok bool) {
	c.stepName = s.Name
	c.stepStart = time.Now()
	c.rng = stepRand(c.Plan.Seed, s.Name)
	defer func() {
		c.Timings = append(c.Timings, StepTiming{Name: s.Name, Dur: time.Since(c.stepStart)})
		if r := recover(); r != nil {
			ok = false
			if _, isAbort := r.(abortRun); !isAbort {
				c.Failures = append(c.Failures, Failure{Kind: "journey", Msg: fmt.Sprintf("step %s panicked: %v\n%s", s.Name, r, debug.Stack())})
			}
		}
	}()
	s.Run(c)
	return true
}

// finish stops what is left and runs the always-on post-run checkers.
func (c *Run) finish(aborted bool) {
	if c.T != nil {
		if !aborted {
			select {
			case <-c.T.Exited():
			default:
				c.note("exit", "journey ended with %s still running (the Tail must quit)", c.Kind)
			}
		}
		c.sampleMem("end")
		if c.finalScreen == "" {
			c.finalScreen, _ = c.T.History()
		}
		c.T.Close()
		c.sweepLeaks()
	}
	c.P.Close()
	for _, f := range c.P.Failures() {
		c.note("request", "%s", f)
	}
	if c.Kind == PiG {
		c.checkStderr()
		c.checkMemory()
	}
}

// sweepLeaks fails the run if a process of this run outlives its terminal: it scans for the run's unique environment marker and kills any survivor.
func (c *Run) sweepLeaks() {
	for _, pid := range findMarked("PIG_JOURNEY_ID=" + c.id) {
		if c.T != nil && pid == c.T.PID() {
			select {
			case <-c.T.Exited():
			default:
				continue
			}
		}
		c.note("leak", "process %d (%s) outlived the product it belonged to", pid, procCmdline(pid))
		_ = signalPID(pid, 9)
	}
}

func (c *Run) checkStderr() {
	root := c.SB.Root
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(root, name)
		switch {
		case strings.HasPrefix(name, "race."):
			b, _ := os.ReadFile(path)
			c.note("race", "data race reported by the race detector:\n%s", truncate(string(b), 6000))
		case name == "lockorder.txt":
			if b, _ := os.ReadFile(path); len(b) > 0 {
				c.note("lockorder", "lock-order cycle:\n%s", truncate(string(b), 6000))
			}
		case name == "requestvalidity.txt":
			if b, _ := os.ReadFile(path); len(b) > 0 {
				c.note("request", "in-process request-validity violation:\n%s", truncate(string(b), 6000))
			}
		case strings.HasPrefix(name, "term"):
			b, _ := os.ReadFile(filepath.Join(path, "stderr.log"))
			s := string(b)
			for _, bad := range []string{"panic:", "fatal error:", "DATA RACE", "REQUESTVALIDITY", "goroutine "} {
				if strings.Contains(s, bad) {
					c.note("stderr", "%s wrote %q to stderr:\n%s", name, bad, truncate(s, 6000))
					break
				}
			}
		}
	}
}

func (c *Run) checkMemory() {
	limit := int64(c.J.MemMaxMB) << 20
	// The 14 journeys peak at: plain peaks 62-73 MiB, instrumented 165-185 MiB. The default leaves about 2.7x headroom
	// over the plain peak, so a leak of the size of the session shows up instead of disappearing under a bound no journey approaches.
	if limit == 0 {
		limit = 200 << 20
	}
	if c.Cfg.Instrumented {
		limit *= 4 // race-detector shadow memory and lock-order bookkeeping
	}
	var peak int64
	var at string
	for _, m := range c.Mem {
		if v := m.peak(); v > peak {
			peak, at = v, m.At
		}
	}
	if peak > limit {
		c.note("memory", "process tree peaked at %d MiB (at %s), above the %d MiB bound", peak>>20, at, limit>>20)
	}
}

// PeakMem returns the largest resident set seen, in bytes: the process tree at a checkpoint or the main process's high-water mark.
func (c *Run) PeakMem() int64 {
	var peak int64
	for _, m := range c.Mem {
		peak = max(peak, m.peak())
	}
	return peak
}

// peak is the larger of the sampled tree resident set and the main process's high-water mark, which also covers a transient peak between two checkpoints.
func (m MemSample) peak() int64 { return max(m.Total, m.HWM) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[truncated]"
}

// WaitGate waits, hang-checked, until the provider's stream reached the named gate.
func (c *Run) WaitGate(name string) {
	reached := c.P.Reached(name)
	c.Await("provider stream gate "+name, func(string) bool {
		select {
		case <-reached:
			return true
		default:
			return false
		}
	})
}

// GatePath returns the path of the named gate file under the working directory.
func (c *Run) GatePath(name string) string { return filepath.Join(c.SB.Cwd, ".gates", name) }

// GateWait is a shell snippet that blocks until Open(name) was called. The tool polls; the harness never does.
func GateWait(name string) string {
	return fmt.Sprintf("until [ -e .gates/%s ]; do sleep 0.02; done", name)
}

// Open releases the named gate.
func (c *Run) Open(name string) {
	if err := os.MkdirAll(filepath.Dir(c.GatePath(name)), 0o755); err != nil {
		c.fail("journey", "gate dir: %v", err)
	}
	if err := os.WriteFile(c.GatePath(name), nil, 0o644); err != nil {
		c.fail("journey", "open gate %s: %v", name, err)
	}
}

// WriteFile creates a file under the working directory (for example state read by an Env.Bin fake program).
func (c *Run) WriteFile(rel, content string) {
	p := filepath.Join(c.SB.Cwd, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		c.fail("journey", "write %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		c.fail("journey", "write %s: %v", rel, err)
	}
}

// WriteAgentFile creates a file under the agent directory while the product runs (for example a prompt template that /reload picks up).
func (c *Run) WriteAgentFile(rel, content string) {
	p := filepath.Join(c.SB.Agent, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		c.fail("journey", "write %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		c.fail("journey", "write %s: %v", rel, err)
	}
}

// Settle is the quiet gap Quiet(Settle()) waits for before a frame is taken: scaled for loaded hosts.
func (c *Run) Settle() time.Duration { return c.scaled(quietGap) }

// Esc sends Escape and waits for the terminal to settle, so a following key is not decoded together with the Escape as an Alt-modified key.
func (c *Run) Esc() {
	c.Key("Escape")
	c.Quiet(c.Settle())
}

// FindText returns the 1-based terminal row and column where sub starts on the visible screen.
func (c *Run) FindText(sub string) (row, col int, ok bool) {
	for i, line := range strings.Split(c.Screen(), "\n") {
		if before, _, ok := strings.Cut(line, sub); ok {
			return i + 1, len([]rune(before)) + 1, true
		}
	}
	return 0, 0, false
}

// Click presses and releases the left mouse button at a 1-based cell, as an SGR mouse report.
func (c *Run) Click(col, row int) {
	c.Bytes([]byte(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", col, row, col, row)))
}

// Wheel scrolls the mouse wheel at a 1-based cell.
func (c *Run) Wheel(col, row int, up bool) {
	b := 65
	if up {
		b = 64
	}
	c.Bytes([]byte(fmt.Sprintf("\x1b[<%d;%d;%dM", b, col, row)))
}

// Paste sends text as a bracketed paste.
func (c *Run) Paste(text string) {
	c.Bytes([]byte("\x1b[200~" + text + "\x1b[201~"))
}

// ClickText clicks the first cell of the lowest occurrence of sub on the visible screen and fails the journey when it is not shown.
func (c *Run) ClickText(sub string) {
	row, col, ok := c.FindLastText(sub)
	if !ok {
		c.Fail("click: %q is not on the screen:\n%s", sub, c.Screen())
	}
	c.Click(col, row)
}

// FindLastText is FindText for the lowest occurrence on the screen.
func (c *Run) FindLastText(sub string) (row, col int, ok bool) {
	lines := strings.Split(c.Screen(), "\n")
	for i, line := range slices.Backward(lines) {
		if j := strings.LastIndex(line, sub); j >= 0 {
			return i + 1, len([]rune(line[:j])) + 1, true
		}
	}
	return 0, 0, false
}
