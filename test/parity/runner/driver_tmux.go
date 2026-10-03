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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// tmuxDriver launches the binary inside a tmux session, waits for the
// ready pattern, optionally sends keys, then captures the pane.
//
// Wall-clock time is measured from tmux session creation to ready-pattern
// detection (or to capture if no ready pattern is set). This isolates
// binary+runtime startup from shell init overhead since tmux exec's the
// binary directly.
type tmuxDriver struct{}

func (tmuxDriver) Name() string { return "interactive-tmux" }

func (tmuxDriver) Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) (result Result) {
	t.Helper()
	cfg := sc.Tmux

	// Allocate a fresh per-binary tempdir for token substitution.
	// {{TEMP}} in any scenario CLI arg, env value, or cwd resolves
	// to this path. pig and pi get DISTINCT roots so they cannot
	// share state within a single run, and every run gets a fresh
	// root so durability runs (runs = 3) cannot share state across
	// repetitions. See test/parity/runner/tokens.go for rationale.
	tc := newTokenContext(t, "tmux-"+sc.Name+"-"+bin.Label)
	if bin.Label == "pig" && sc.Env.PigBin != "" {
		bin.Path = resolveScenarioCWD(sc.SourcePath, tc.expand(sc.Env.PigBin))
	}
	if bin.Label == "pi" && sc.Env.PiBin != "" {
		bin.Path = resolveScenarioCWD(sc.SourcePath, tc.expand(sc.Env.PiBin))
	}

	// Legacy pre_clear_paths support. New scenarios should not use
	// this: a fresh {{TEMP}} root has nothing to clear. We keep
	// the field for unmigrated scenarios but the scenario lint will
	// flag any new use.
	for _, p := range cfg.PreClearPaths {
		resolved := tc.expand(p)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(sc.SourcePath), resolved)
		}
		_ = os.RemoveAll(resolved)
	}

	session := fmt.Sprintf("parity-%s-%s-%s-%s", parityRunID(), sc.Name, bin.Label, uniqueID())
	registerSession(session)
	started := false
	defer func() {
		// Cleanup must remain observable after the scenario context is cancelled.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), processExitTimeout(t))
		defer cancel()
		var owned map[int]bool
		var err error
		if started {
			owned, err = tmuxSessionProcesses(cleanupCtx, session)
		}
		killSession(session)
		if started && err == nil {
			err = awaitOwnedProcesses(cleanupCtx, owned)
		}
		result.Err = errors.Join(result.Err, err)
	}()

	// Snapshot agent dirs so binary writes (e.g. pi persisting
	// defaultModel into settings.json) never mutate committed testdata.
	preserveAuth, injectAuth := scenarioAuthModes(sc)
	pigEnv, piEnv := snapshotAgentDirs(t, sc.SourcePath, sc.Env.Pig, sc.Env.Pi, preserveAuth, injectAuth)

	// Build the inline shell command: env... bin args...; sleep keeps the
	// pane alive so we can capture after the binary settles.
	parts := snapshotBinaryEnv(t, sc.SourcePath, bin.Env, preserveAuth, injectAuth)
	// Apply per-binary env overrides from the scenario's [env] section.
	switch bin.Label {
	case "pig":
		parts = append(parts, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, pigEnv))...)
	case "pi":
		parts = append(parts, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, piEnv))...)
	}
	var changelogSource string
	if sc.Assert.ChangelogHeadersComplete {
		var err error
		changelogSource, err = readChangelogSource(bin, parts)
		if err != nil {
			return Result{Err: fmt.Errorf("read changelog assertion input: %w", err)}
		}
	}
	parts = append(parts, bin.Path)
	if !sc.Env.OverrideBaseArgs {
		parts = append(parts, bin.Args...)
	}
	// Apply per-binary extra CLI args from the scenario's [env] section.
	// Token expansion happens here so scenarios can use {{TEMP}} in any
	// flag value (e.g. `--session-dir`, `--log-dir`, `--config-path`).
	switch bin.Label {
	case "pig":
		parts = append(parts, tc.expandSlice(sc.Env.PigArgs)...)
	case "pi":
		parts = append(parts, tc.expandSlice(sc.Env.PiArgs)...)
	}
	// Apply per-binary extension overrides for pi.
	if bin.Label == "pi" {
		for _, ext := range sc.Env.PiExtensions {
			resolved := ext
			if !filepath.IsAbs(ext) {
				// Resolve relative to the scenario's source directory.
				resolved = filepath.Join(filepath.Dir(sc.SourcePath), ext)
			}
			snapshot, err := snapshotExtensionPath(t, resolved)
			if err != nil {
				return Result{Err: fmt.Errorf("snapshot extension %s: %w", resolved, err)}
			}
			parts = append(parts, "-e", snapshot)
		}
	}
	// Apply per-binary extension overrides for pig.
	if bin.Label == "pig" {
		for _, ext := range sc.Env.PigExtensions {
			resolved := ext
			if !filepath.IsAbs(ext) {
				resolved = filepath.Join(filepath.Dir(sc.SourcePath), ext)
			}
			snapshot, err := snapshotExtensionPath(t, resolved)
			if err != nil {
				return Result{Err: fmt.Errorf("snapshot extension %s: %w", resolved, err)}
			}
			parts = append(parts, "-e", snapshot)
		}
	}
	if sc.Model != "" {
		parts = append(parts, "--model", sc.Model)
	}
	// Shell-quote every part. The runner builds an inline shell
	// command by joining parts with spaces; any value that contains
	// shell metacharacters (notably PATH={{PATH}} with directories
	// like "Visual Studio Code") would otherwise be split by the
	// shell and break the binary launch.
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = shellQuote(p)
	}
	// Pin COLORTERM and drop ambient proxy vars so a CI runner inside tmux
	// matches local: pi downgrades to 256-color without COLORTERM, and a
	// host HTTP_PROXY otherwise leaks into one binary.
	envPrefix := tmuxEnvPrefix(os.Getenv("GOCOVERDIR"))
	exitStatusPath := filepath.Join(tc.tempRoot, "process-exit-status")
	cmdStr := envPrefix + strings.Join(quoted, " ") + "; exit_code=$?; printf '%s\\n' \"$exit_code\" > " + shellQuote(exitStatusPath+".tmp") + " && mv " + shellQuote(exitStatusPath+".tmp") + " " + shellQuote(exitStatusPath) + "; sleep 999"
	// Working directory: prepend `cd <dir> && …` so the binary launches in
	// the scenario cwd (resolved relative to the scenario source path) or
	// the default cwd fixture. The directory is copied per binary under the
	// temp root, mirroring snapshotAgentDirs, so concurrent pi/pig
	// invocations cannot race on shared fixture files (e.g. an edit-tool
	// diff scenario where both binaries mutate parity-edit-target.txt) and
	// neither binary runs inside the checkout.
	cwd := resolveScenarioCWD(sc.SourcePath, cfg.CWD)
	if cwd == "" {
		cwd = defaultCWDFixture()
	}
	snap, err := snapshotCWD(t, cwd)
	if err != nil {
		return Result{Err: fmt.Errorf("snapshot cwd %s: %w", cwd, err)}
	}
	if err := validateCWDFooterCrop(snap, cfg); err != nil {
		return Result{Err: err}
	}
	if err := initSnapshotGitBranch(ctx, snap, cfg.GitBranch); err != nil {
		return Result{Err: err}
	}
	if cfg.WindowStyle != "" {
		cmdStr = "tmux set-option -w -t \"$TMUX_PANE\" window-style " + shellQuote(cfg.WindowStyle) + " && " + cmdStr
	}
	cmdStr = "cd " + shellQuote(snap) + " && " + cmdStr

	tmuxServerMu.Lock()
	serverErr := ensureTmuxServer()
	// The isolated server is shared by both binaries. Start each runtime clock
	// only after that one-time setup so the first binary does not own it.
	t0 := time.Now()
	var createOutput []byte
	var createErr error
	if serverErr == nil {
		createOutput, createErr = exec.CommandContext(ctx, "tmux", tmuxArgs(createTmuxSessionArgs(session, cmdStr, cfg)...)...).CombinedOutput()
	}
	tmuxServerMu.Unlock()
	if serverErr != nil {
		return Result{Err: serverErr}
	}
	if createErr != nil {
		return Result{Err: fmt.Errorf("tmux new-session: %w: %s", createErr, strings.TrimSpace(string(createOutput)))}
	}
	started = true
	transcriptPath := filepath.Join(tc.tempRoot, "terminal-transcript.log")
	pipeCommand := "cat >> " + shellQuote(transcriptPath)
	if err := exec.CommandContext(ctx, "tmux", tmuxArgs("pipe-pane", "-o", "-t", session, pipeCommand)...).Run(); err != nil {
		return Result{Err: fmt.Errorf("tmux pipe-pane: %w", err)}
	}

	// Pick the right ready pattern for this binary.
	ready := cfg.ReadyPatternPig
	if bin.Label == "pi" {
		ready = cfg.ReadyPatternPi
	}

	readyOK := true
	var operationErr error
	if ready != "" {
		timeout := time.Duration(cfg.ReadyTimeoutSeconds) * time.Second
		readyOK = waitForPaneOrCapture(ctx, session, ready, cfg.CaptureStart, timeout)
		if !readyOK {
			operationErr = fmt.Errorf("ready pattern %q not reached within %s", ready, timeout)
		}
	}
	elapsed := time.Since(t0).Milliseconds()

	// Startup-surface gate. A scenario with no input captures what the binary
	// renders by itself, but Pi mounts the TUI, and so draws the ready footer,
	// before managed-tool setup, extension binding and the resumed transcript
	// (interactive-mode.ts:951-1036); PiG keeps that order. The footer alone
	// therefore does not mean the asserted surface has rendered. Wait, bounded
	// by the ready timeout, for the capture surface itself; a surface that never
	// appears still fails the capture_start check below.
	if readyOK && ready != "" && len(cfg.Keys) == 0 && len(cfg.Steps) == 0 && cfg.CaptureStart != "" {
		waitForCaptureSurface(ctx, session, cfg, time.Duration(cfg.ReadyTimeoutSeconds)*time.Second)
	}

	// Post-ready stability gate (A). The ready pattern can match while pi's TUI
	// is still settling: its startup footer floats mid-screen before the layout
	// finalizes and the input loop begins accepting a submitting Enter. Sending
	// into that window loses the dispatch unrecoverably (text buffers, Enter is
	// dropped). Let the pane stabilize before dispatching keys. Bounded and
	// best-effort, so a scenario with a persistent startup animation still
	// proceeds. Only gates when the scenario actually dispatches keys.
	if readyOK && (len(cfg.Keys) > 0 || len(cfg.Steps) > 0) {
		waitForPaneStable(ctx, session, 150*time.Millisecond, 2*time.Second)
	}

	// Send any post-ready keys. Legacy scenarios use the top-level
	// keys+settle fields; canonical staged scenarios use [[tmux.steps]].
	if operationErr == nil && len(cfg.Steps) > 0 {
		for _, step := range cfg.Steps {
			timeout := time.Duration(step.WaitTimeoutSeconds) * time.Second
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			baseline := capturePaneHistory(ctx, session, transcriptPath)
			if err := sendTmuxKeys(ctx, session, step.Keys); err != nil {
				operationErr = err
				break
			}
			if len(step.WaitContains) > 0 {
				if !waitForPaneAllSince(ctx, session, transcriptPath, step.WaitContains, baseline, true, timeout) {
					operationErr = fmt.Errorf("step output %q not reached within %s", step.WaitContains, timeout)
					break
				}
			}
			visible := step.visibleContains(bin.Label)
			if len(visible) > 0 || len(step.WaitNotContains) > 0 {
				if !waitForPaneState(ctx, session, visible, step.WaitNotContains, timeout) {
					operationErr = fmt.Errorf("visible step state includes=%q excludes=%q not reached within %s", visible, step.WaitNotContains, timeout)
					break
				}
			}
			if step.WaitStableMilliseconds > 0 {
				window := time.Duration(step.WaitStableMilliseconds) * time.Millisecond
				if !waitForPaneQuiescent(ctx, func() string { return capturePaneVisible(ctx, session) }, window, tmuxQuiescencePoll, timeout) {
					operationErr = fmt.Errorf("visible pane did not stay unchanged for %s within %s", window, timeout)
					break
				}
			}
			if step.SettleSeconds > 0 {
				time.Sleep(time.Duration(step.SettleSeconds) * time.Second)
			}
		}
	} else if operationErr == nil {
		if err := sendTmuxKeys(ctx, session, cfg.Keys); err != nil {
			operationErr = err
		}
		if cfg.SettleSeconds > 0 {
			time.Sleep(time.Duration(cfg.SettleSeconds) * time.Second)
		}
	}

	// A scenario that asserts this binary's exit status observes a completed
	// process: await the launch shell's status file (bounded), then capture,
	// so neither the status nor the final output is sampled mid-exit.
	if operationErr == nil && expectedExitCode(sc, bin.Label) != nil {
		if err := awaitProcessExit(ctx, exitStatusPath, processExitTimeout(t)); err != nil {
			operationErr = err
		}
	}

	pane, escaped, err := capturePanePair(ctx, session, cfg)
	if err != nil {
		return Result{
			RuntimeMs: elapsed,
			ReadyOK:   readyOK,
			Err:       fmt.Errorf("capture-pane: %w (%s)", err, tmuxServerReport(ctx, session)),
		}
	}
	if !readyOK && paneMatchesCaptureSurface(pane, cfg) {
		readyOK = true
	}
	exitCode, exited, err := readProcessExitStatus(exitStatusPath)
	if err != nil {
		return Result{RuntimeMs: elapsed, ReadyOK: readyOK, Err: err}
	}
	if exited && exitCode != 0 && operationErr == nil {
		expected := sc.Assert.ExitCode
		if bin.Label == "pig" && sc.Assert.PigExitCode != nil {
			expected = sc.Assert.PigExitCode
		}
		if bin.Label == "pi" && sc.Assert.PiExitCode != nil {
			expected = sc.Assert.PiExitCode
		}
		if expected == nil || *expected != exitCode {
			operationErr = fmt.Errorf("interactive process exited before capture with status %d", exitCode)
		}
	}

	result = Result{
		ChangelogSource: changelogSource,
		Output:          pane,
		Escaped:         escaped,
		ExitCode:        exitCode,
		RuntimeMs:       elapsed,
		ReadyOK:         readyOK,
		Err:             operationErr,
	}
	if result.Err == nil && readyOK && cfg.CaptureStart != "" && !strings.Contains(pane, cfg.CaptureStart) {
		result.Err = fmt.Errorf("capture_start %q absent after scenario steps", cfg.CaptureStart)
	}
	// Coverage-only: Go integration coverage flushes on a clean process exit, but
	// an interactive pig sits at its prompt and is SIGKILLed at teardown, losing
	// its counters. When GOCOVERDIR is set, drive a clean quit after capture so
	// the normal exit path writes coverage. This runs only under a coverage build
	// (GOCOVERDIR unset in ordinary parity runs) and never touches the already
	// captured comparison output above.
	if covDir := os.Getenv("GOCOVERDIR"); covDir != "" && !exited {
		flushCoverageOnCleanExit(ctx, session, exitStatusPath)
	}
	return result
}

// flushCoverageOnCleanExit drives an interactive, coverage-instrumented pig to a
// normal exit so its counters are written before teardown. It sends pig's quit
// chord (two Ctrl-C within the 500ms confirm window, as raw ETX bytes) and waits
// briefly for the launch shell to record the process exit, which happens after
// the coverage exit hook has run. Best-effort and diagnostic-only: a pig that
// ignores the chord is left to the existing SIGKILL teardown, so a failure here
// only reproduces today's missing-coverage behavior: it never fails a scenario.
func flushCoverageOnCleanExit(ctx context.Context, session, exitStatusPath string) {
	_ = sendTmuxKey(ctx, session, "hex:03")
	_ = sendTmuxKey(ctx, session, "hex:03")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, exited, _ := readProcessExitStatus(exitStatusPath); exited {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// expectedExitCode returns the exit status a scenario asserts for label, or
// nil when it asserts none.
func expectedExitCode(sc *Scenario, label string) *int {
	switch {
	case label == "pig" && sc.Assert.PigExitCode != nil:
		return sc.Assert.PigExitCode
	case label == "pi" && sc.Assert.PiExitCode != nil:
		return sc.Assert.PiExitCode
	}
	return sc.Assert.ExitCode
}

// processExitTimeout bounds the wait for an asserted process exit.
func processExitTimeout(t *testing.T) time.Duration {
	limit := 20 * time.Second
	if deadline, ok := t.Deadline(); ok {
		if remaining := time.Until(deadline) / 2; remaining < limit {
			limit = remaining
		}
	}
	return limit
}

// awaitProcessExit waits until the launch shell has recorded the process's
// exit status, polling the status file, and reports a process that did not
// exit within timeout.
func awaitProcessExit(ctx context.Context, path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, exited, err := readProcessExitStatus(path); err != nil || exited {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("interactive process did not exit within %s", timeout)
		case <-ticker.C:
		}
	}
}

// readProcessExitStatus reads the launch shell's published exit status. The
// shell writes "<code>\n" to a temporary file and renames it into place, so
// the status is either absent or complete; a status without its trailing
// newline is treated as not yet published, never as status 0.
func readProcessExitStatus(path string) (int, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read process exit status: %w", err)
	}
	text := string(data)
	if !strings.HasSuffix(text, "\n") {
		return 0, false, nil
	}
	code, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, true, fmt.Errorf("parse process exit status %q: %w", data, err)
	}
	return code, true, nil
}

var invalidSessionPart = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func parityRunID() string {
	runID := strings.TrimSpace(os.Getenv("PIG_PARITY_RUN_ID"))
	if runID == "" {
		runID = strconv.Itoa(os.Getpid())
	}
	return invalidSessionPart.ReplaceAllString(runID, "-")
}

// resolveScenarioCWD resolves a CWD path relative to the scenario source file.
// If cwd is empty, returns "" (no override). If cwd is absolute, returns it as-is.
func resolveScenarioCWD(sourcePath, cwd string) string {
	if cwd == "" {
		return ""
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	return filepath.Join(filepath.Dir(sourcePath), cwd)
}

// resolveExtensionPath resolves an extension path relative to the scenario
// source file, matching the same resolution used in the tmux driver.
func resolveExtensionPath(sourcePath, ext string) string {
	if filepath.IsAbs(ext) {
		return ext
	}
	return filepath.Join(filepath.Dir(sourcePath), ext)
}

func resolveScenarioEnvVars(sourcePath string, vars []string) []string {
	if len(vars) == 0 {
		return nil
	}
	base := filepath.Dir(sourcePath)
	out := make([]string, 0, len(vars))
	for _, kv := range vars {
		key, value, ok := strings.Cut(kv, "=")
		// Run-owned tokens and URLs are not fixture-relative filesystem paths.
		if !ok || value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "{{") || strings.Contains(value, "://") || (!strings.HasPrefix(value, ".") && !strings.Contains(value, "/")) {
			out = append(out, kv)
			continue
		}
		out = append(out, key+"="+filepath.Join(base, value))
	}
	return out
}

func paneMatchesCaptureSurface(pane string, cfg TmuxDriverConfig) bool {
	if cfg.CaptureStart == "" {
		return false
	}
	if !strings.Contains(pane, cfg.CaptureStart) {
		return false
	}
	if cfg.CaptureEnd != "" && !strings.Contains(pane, cfg.CaptureEnd) {
		return false
	}
	if cfg.CaptureEndRegex != "" {
		re, err := regexp.Compile(cfg.CaptureEndRegex)
		if err != nil || !re.MatchString(pane) {
			return false
		}
	}
	return true
}

// waitForCaptureSurface polls the pane, captured as the scenario captures it, until the scenario's capture start and end are both visible or timeout elapses.
func waitForCaptureSurface(ctx context.Context, session string, cfg TmuxDriverConfig, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		out, err := exec.CommandContext(ctx, "tmux", tmuxArgs(capturePaneArgs(session, false, cfg)...)...).Output()
		if err == nil && paneMatchesCaptureSurface(string(out), cfg) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForPaneOrCapture(ctx context.Context, session, pattern, captureStart string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		out, _ := exec.CommandContext(ctx, "tmux", tmuxArgs("capture-pane", "-t", session, "-p")...).Output()
		pane := string(out)
		if paneMatchesReadyPattern(pane, pattern) || captureStart != "" && strings.Contains(pane, captureStart) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func paneMatchesReadyPattern(pane, pattern string) bool {
	if pattern == "(auto)" {
		return paneMatchesAutoReadyPattern(pane)
	}
	if strings.Contains(pane, pattern) {
		return true
	}
	// Upstream pi has two valid ready footers in our parity environments:
	// older builds showed "$0.000 ...", while newer builds render a footer like
	// "0.0%/128k ... model" (or "0.0%/0 ... unknown" in no-model runs). Treat
	// all of these as the same readiness condition for scenarios authored
	// against the upstream footer.
	if pattern == "$0.000" && paneMatchesUpstreamFooterReady(pane) {
		return true
	}
	return false
}

var upstreamFooterReadyRe = regexp.MustCompile(`(?m)(?:\?|\d+(?:\.\d+)?%)/[^\s]+`)

func paneMatchesUpstreamFooterReady(pane string) bool {
	if strings.Contains(pane, "$0.000") {
		return true
	}
	return upstreamFooterReadyRe.MatchString(pane)
}

// paneMatchesAutoReadyPattern recognizes the stable footer or visible input prompt without depending on a startup header that can be quiet or scrolled out of view.
func paneMatchesAutoReadyPattern(pane string) bool {
	if paneMatchesUpstreamFooterReady(pane) {
		return true
	}
	for _, line := range strings.Split(pane, "\n") {
		if strings.TrimSpace(line) == ">" {
			return true
		}
	}
	return false
}

// waitForPaneStable returns once the pane content is unchanged across two
// consecutive samples `interval` apart, or when `limit` elapses. It lets a
// just-rendered TUI settle before the harness dispatches keys, closing the
// window where a signal (ready footer) is visible but the input loop is not yet
// accepting a submitting Enter. Best-effort: a pane that never stabilizes
// (persistent animation) simply proceeds after `limit`.
func waitForPaneStable(ctx context.Context, session string, interval, limit time.Duration) {
	deadline := time.Now().Add(limit)
	prev, _ := exec.CommandContext(ctx, "tmux", tmuxArgs("capture-pane", "-t", session, "-p")...).Output()
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		time.Sleep(interval)
		cur, _ := exec.CommandContext(ctx, "tmux", tmuxArgs("capture-pane", "-t", session, "-p")...).Output()
		if string(cur) == string(prev) {
			return
		}
		prev = cur
	}
}

const tmuxQuiescencePoll = 25 * time.Millisecond

func capturePaneVisible(ctx context.Context, session string) string {
	out, _ := exec.CommandContext(ctx, "tmux", tmuxArgs("capture-pane", "-t", session, "-p")...).Output()
	return string(out)
}

// waitForPaneQuiescent returns true once sample has returned the same value
// continuously for window, measured from the first sample of that value. It
// returns false when timeout elapses first or ctx ends. An animation that
// emits no completion output, such as Armin's rain effect, is complete when
// its visible state stops changing for longer than the animation's own idle
// gap between frames of motion.
func waitForPaneQuiescent(ctx context.Context, sample func() string, window, poll, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	last := sample()
	since := time.Now()
	for {
		if ctx.Err() != nil {
			return false
		}
		if time.Since(since) >= window {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(poll)
		if cur := sample(); cur != last {
			last, since = cur, time.Now()
		}
	}
}

func paneContainsAll(pane string, patterns []string) bool {
	for _, pattern := range patterns {
		if !strings.Contains(pane, pattern) {
			return false
		}
	}
	return true
}

func waitForPaneState(ctx context.Context, session string, includes, excludes []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		out, _ := exec.CommandContext(ctx, "tmux", tmuxArgs("capture-pane", "-t", session, "-p")...).Output()
		pane := string(out)
		matches := paneContainsAll(pane, includes)
		for _, pattern := range excludes {
			if strings.Contains(pane, pattern) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// capturePaneHistory returns the pane's scrollback followed by the displayed text of the terminal transcript, so a wait matches displayed text only.
func capturePaneHistory(ctx context.Context, session, transcriptPath string) string {
	out, _ := exec.CommandContext(ctx, "tmux", tmuxArgs("capture-pane", "-t", session, "-p", "-S", "-")...).Output()
	transcript, _ := os.ReadFile(transcriptPath)
	return string(out) + "\n" + transcriptText(transcript)
}

// transcriptText returns the text a raw terminal transcript displays. Escape-sequence parameters and string payloads are not text: a repaint at row 42 writes ESC[42;1H without displaying "42", and a kitty graphics APC carries base64 data. A cursor movement separates the text written before it from the text written after it, so it becomes a line break; fullscreen rendering positions every row with ESC[row;1H and no line break. Other sequences are removed. tmux pipe-pane is still appending while a wait reads the file, so a sequence cut off at the end is removed as well.
func transcriptText(raw []byte) string {
	var text strings.Builder
	text.Grow(len(raw))
	for len(raw) > 0 {
		esc := bytes.IndexByte(raw, 0x1b)
		if esc < 0 {
			text.Write(raw)
			break
		}
		text.Write(raw[:esc])
		n, moves := escapeSequenceLength(raw[esc:])
		if n == 0 {
			break
		}
		if moves {
			text.WriteByte('\n')
		}
		raw = raw[esc+n:]
	}
	return text.String()
}

// escapeSequenceLength returns the length of the ECMA-48 escape sequence at the start of seq, which begins with ESC, and whether it moves the cursor. It returns 0 for a sequence cut off at the end of seq. An ESC that interrupts a control string ends it.
func escapeSequenceLength(seq []byte) (int, bool) {
	if len(seq) < 2 {
		return 0, false
	}
	switch seq[1] {
	case '[':
		i := 2
		for i < len(seq) && seq[i] >= 0x30 && seq[i] <= 0x3f {
			i++
		}
		for i < len(seq) && seq[i] >= 0x20 && seq[i] <= 0x2f {
			i++
		}
		if i == len(seq) {
			return 0, false
		}
		if seq[i] < 0x40 || seq[i] > 0x7e {
			return i, false
		}
		return i + 1, strings.IndexByte("ABCDEFGHIZadef`r", seq[i]) >= 0
	case ']', 'P', 'X', '^', '_':
		end := bytes.IndexAny(seq[2:], "\x07\x1b")
		switch {
		case end < 0:
			return 0, false
		case seq[2+end] == 0x07:
			return 2 + end + 1, false
		case 2+end+1 == len(seq):
			return 0, false
		case seq[2+end+1] == '\\':
			return 2 + end + 2, false
		default:
			return 2 + end, false
		}
	default:
		i := 1
		for i < len(seq) && seq[i] >= 0x20 && seq[i] <= 0x2f {
			i++
		}
		if i == len(seq) {
			return 0, false
		}
		if seq[i] < 0x30 || seq[i] > 0x7e {
			return i, false
		}
		return i + 1, i == 1 && strings.IndexByte("8DEMc", seq[i]) >= 0
	}
}

func waitForPaneAllSince(ctx context.Context, session, transcriptPath string, patterns []string, baseline string, requireNew bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		pane := capturePaneHistory(ctx, session, transcriptPath)
		if paneSatisfiesWait(pane, patterns, baseline, requireNew) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func paneSatisfiesWait(pane string, patterns []string, baseline string, requireNew bool) bool {
	for _, pattern := range patterns {
		if !strings.Contains(pane, pattern) || (requireNew && strings.Count(pane, pattern) <= strings.Count(baseline, pattern)) {
			return false
		}
	}
	return true
}

// tmux stores history-limit as a signed 32-bit value and allocates history as output arrives, not upfront. Complete-history scenarios select its maximum retention without changing terminal dimensions or another session's limit.
const tmuxCompleteHistoryLimit = 1<<31 - 1

func createTmuxSessionArgs(session, command string, cfg TmuxDriverConfig) []string {
	args := []string{"new-session", "-d", "-s", session, "-x", strconv.Itoa(cfg.Width), "-y", strconv.Itoa(cfg.Height)}
	if !cfg.CaptureHistory {
		return append(args, command)
	}
	// tmux copies the session's history limit into each newly created pane. Set it on a private bootstrap session before creating the application pane; never mutate a shared server default. Both panes are reclaimed by the session's existing cleanup.
	return append(args, "-n", "parity-bootstrap", "sleep 999",
		";", "set-option", "-t", session, "history-limit", strconv.Itoa(tmuxCompleteHistoryLimit),
		";", "new-window", "-t", session+":", "-n", "parity-capture", command)
}

// capturePaneArgs returns the capture-pane arguments for the final capture.
func capturePaneArgs(session string, escaped bool, cfg TmuxDriverConfig) []string {
	args := []string{"capture-pane"}
	if escaped {
		args = append(args, "-e")
	}
	if cfg.CaptureJoinWrapped {
		args = append(args, "-J")
	}
	if cfg.CaptureHistory {
		args = append(args, "-S", "-")
	}
	return append(args, "-t", session, "-p")
}

func capturePanePair(ctx context.Context, session string, cfg TmuxDriverConfig) (string, string, error) {
	plainOut, err := exec.CommandContext(ctx, "tmux", tmuxArgs(capturePaneArgs(session, false, cfg)...)...).Output()
	if err != nil {
		return "", "", err
	}
	escapedOut, err := exec.CommandContext(ctx, "tmux", tmuxArgs(capturePaneArgs(session, true, cfg)...)...).Output()
	if err != nil {
		return "", "", err
	}
	plain := string(plainOut)
	escaped := string(escapedOut)
	if cfg.CaptureStart != "" || cfg.CaptureEnd != "" || cfg.CaptureEndRegex != "" {
		plain = cropCapture(plain, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast)
		escaped = cropCapture(escaped, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast)
	}
	return plain, escaped, nil
}

// sessionRegistry tracks every tmux session created by Run() so we can
// guarantee cleanup even when the test process is killed by SIGINT,
// SIGTERM, or SIGHUP (e.g. `make` cancel, parent-shell Ctrl-C, OS
// shutdown). The happy path (test goroutine exits normally) is still
// served by `defer killSession(session)` in tmuxDriver.Run: this
// registry only matters when the goroutine never gets to run that
// defer. We cannot recover from SIGKILL; that's a known limitation.
//
// The Makefile's `parity` target additionally wraps the `go test`
// invocation in a bash `trap` that cleans the invocation's isolated tmux
// server, covering SIGKILL without disturbing concurrent parity work.
var (
	sessionRegistryMu sync.Mutex
	sessionRegistry   = map[string]struct{}{}
	tmuxServerMu      sync.Mutex
	tmuxKeeperErr     error
	tmuxKeeperStarted bool
	// tmuxServerPID is the isolated server's process once it has been recorded (0 before).
	tmuxServerPID atomic.Int64
	// tmuxLostServers describes each keeper or server that vanished and was started again.
	tmuxLostMu      sync.Mutex
	tmuxLostServers []string
	// tmuxExtendedKeysFormatSupported records whether the isolated server accepted extended-keys-format (tmux 3.5+).
	tmuxExtendedKeysFormatSupported bool
	tmuxSocketName                  = "pig-parity-" + parityRunID()
)

const tmuxKeeperSession = "pig-parity-keeper"

func tmuxArgs(args ...string) []string {
	return append([]string{"-L", tmuxSocketName}, args...)
}

// ensureTmuxServer starts the isolated server with its keeper session and keyboard configuration, and starts it again when the server was lost since the last call: a replacement created implicitly by the next new-session has tmux's defaults and no keeper. A configuration error persists while the keeper does. Callers hold tmuxServerMu.
func ensureTmuxServer() error {
	if tmuxKeeperStarted && tmuxKeeperPresent() {
		return tmuxKeeperErr
	}
	lost, restarting := int(tmuxServerPID.Swap(0)), tmuxKeeperStarted
	if restarting {
		defer func() { noteLostTmuxServer(lost, int(tmuxServerPID.Load())) }()
	}
	tmuxKeeperErr = nil
	tmuxKeeperStarted = false
	// Ignore host tmux configuration and configure the private server before either application starts. Pi checks these options asynchronously (interactive-mode.ts:1214-1259); both sides must see the same supported keyboard protocol, not race a startup warning against scenario input.
	output, err := exec.Command("tmux", tmuxArgs("-f", os.DevNull,
		"new-session", "-d", "-s", tmuxKeeperSession, "sleep 2147483647",
		";", "set-option", "-g", "extended-keys", "on")...).CombinedOutput()
	if err != nil {
		tmuxKeeperErr = fmt.Errorf("start isolated tmux server: %w: %s", err, strings.TrimSpace(string(output)))
		return tmuxKeeperErr
	}
	tmuxKeeperStarted = true
	recordTmuxServerPID(context.Background())
	// extended-keys-format arrived in tmux 3.5. Older tmux (Ubuntu 24.04 ships 3.4) has no such option and always emits its one extended-key format; Pi then reads an empty format and raises no warning (interactive-mode.ts:1214-1259), so both sides still see the same setup.
	output, err = exec.Command("tmux", tmuxArgs("set-option", "-g", "extended-keys-format", "csi-u")...).CombinedOutput()
	switch {
	case err == nil:
		tmuxExtendedKeysFormatSupported = true
	case strings.Contains(string(output), "invalid option: extended-keys-format"):
	default:
		tmuxKeeperErr = fmt.Errorf("configure isolated tmux server: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return tmuxKeeperErr
}

// noteLostTmuxServer records for tmuxServerReport that the keeper vanished: with its server when the restarted keeper runs on another process, alone when the server survived.
func noteLostTmuxServer(lost, current int) {
	if lost == 0 {
		return
	}
	note := fmt.Sprintf("server pid %d was lost and replaced", lost)
	if lost == current {
		note = fmt.Sprintf("keeper session on server pid %d was lost and restarted", lost)
	}
	tmuxLostMu.Lock()
	tmuxLostServers = append(tmuxLostServers, note)
	tmuxLostMu.Unlock()
}

// tmuxKeeperPresent reports whether the keeper session, and so the server configured around it, is still there.
func tmuxKeeperPresent() bool {
	return exec.Command("tmux", tmuxArgs("has-session", "-t", "="+tmuxKeeperSession)...).Run() == nil
}

func registerSession(name string) {
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	sessionRegistry[name] = struct{}{}
}

func unregisterSession(name string) {
	sessionRegistryMu.Lock()
	defer sessionRegistryMu.Unlock()
	delete(sessionRegistry, name)
}

// cleanupAllSessions kills every still-registered tmux session.
// Called from TestMain on signal receipt and (defensively) on normal
// test-process exit. Idempotent.
func cleanupAllSessions() {
	sessionRegistryMu.Lock()
	names := make([]string, 0, len(sessionRegistry))
	for n := range sessionRegistry {
		names = append(names, n)
	}
	sessionRegistry = map[string]struct{}{}
	sessionRegistryMu.Unlock()
	tmuxServerMu.Lock()
	defer tmuxServerMu.Unlock()
	for _, n := range names {
		_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", n)...).Run()
	}
	// Helper processes can inherit the run ID without starting a server. They must not kill the parent's keeper when their TestMain exits.
	if tmuxKeeperStarted {
		_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", tmuxKeeperSession)...).Run()
	}
}

func killSession(session string) {
	// Best-effort. AGENTS.md forbids `tmux kill-server`: we only kill
	// this one named session. Creation and teardown share a lock so cleanup
	// cannot remove a session while another test is creating one.
	tmuxServerMu.Lock()
	_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", session)...).Run()
	tmuxServerMu.Unlock()
	unregisterSession(session)
}

func tmuxEnvPrefix(coverDir string) string {
	// The tmux server can inherit capability signals from an outer Herdr pane (HERDR_* is in hostTerminalEnvKeys).
	// The scenario's tmux pane is the terminal under test, so those outer signals
	// must not authorize images or other intermediary-owned features.
	prefix := "unset " + agentHomeEnvKeys + " HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy ALL_PROXY all_proxy " +
		hostTerminalEnvKeys + " " + hostPresenceEnvKeys + "; " +
		"export COLORTERM=truecolor; "
	if coverDir != "" {
		prefix += "export GOCOVERDIR=" + shellQuote(coverDir) + "; "
	}
	return prefix
}

func sendTmuxKeys(ctx context.Context, session string, keys []string) error {
	for _, key := range coalesceTmuxKeys(keys) {
		if err := sendTmuxKey(ctx, session, key); err != nil {
			return fmt.Errorf("send key %q: %w", key, err)
		}
	}
	return nil
}

func coalesceTmuxKeys(keys []string) []string {
	coalesced := make([]string, 0, len(keys))
	for index := 0; index < len(keys); {
		key := keys[index]
		if isSpecialTmuxKey(key) || strings.HasPrefix(key, "hex:") {
			coalesced = append(coalesced, key)
			index++
			continue
		}

		var literal strings.Builder
		for index < len(keys) && !isSpecialTmuxKey(keys[index]) && !strings.HasPrefix(keys[index], "hex:") {
			literal.WriteString(keys[index])
			index++
		}
		coalesced = append(coalesced, literal.String())
	}
	return coalesced
}

func sendTmuxKey(ctx context.Context, session, key string) error {
	// hex:XX sends raw byte(s) via tmux send-keys -H.
	if after, ok := strings.CutPrefix(key, "hex:"); ok {
		args := []string{"send-keys", "-t", session, "-H"}
		args = append(args, strings.Fields(after)...)
		return exec.CommandContext(ctx, "tmux", tmuxArgs(args...)...).Run()
	}
	if isSpecialTmuxKey(key) {
		return exec.CommandContext(ctx, "tmux", tmuxArgs("send-keys", "-t", session, key)...).Run()
	}
	return exec.CommandContext(ctx, "tmux", tmuxArgs("send-keys", "-t", session, "-l", key)...).Run()
}

func isSpecialTmuxKey(key string) bool {
	switch key {
	case "Enter", "Escape", "Tab", "Space", "BSpace", "Up", "Down", "Left", "Right",
		"Home", "End", "PgUp", "PgDn", "C-c", "C-d", "C-e", "C-o", "C-r", "C-u", "C-k",
		"C-Left", "C-Right", "C-a", "C-l", "C-t", "C-y":
		return true
	default:
		return false
	}
}

func cropCapture(s, start, end, endRegex string, startLast bool) string {
	from := 0
	if start != "" {
		idx := strings.Index(s, start)
		if startLast {
			idx = strings.LastIndex(s, start)
		}
		if idx >= 0 {
			from = idx
		}
	}
	to := len(s)
	if end != "" {
		if idx := strings.Index(s[from:], end); idx >= 0 {
			to = from + idx + len(end)
		}
	} else if endRegex != "" {
		if re, err := regexp.Compile(endRegex); err == nil {
			if loc := re.FindStringIndex(s[from:]); loc != nil {
				to = from + loc[1]
			}
		}
	}
	if from > to {
		return s
	}
	return strings.TrimRight(s[from:to], "\n")
}

// tmuxServerReport states what the isolated tmux server looks like now, so a capture that fails because the whole server vanished is told apart from one session ending: the recorded server process, the socket file, the sessions that remain, and whether the named session is among them.
func tmuxServerReport(ctx context.Context, session string) string {
	var parts []string
	tmuxLostMu.Lock()
	parts = append(parts, tmuxLostServers...)
	tmuxLostMu.Unlock()
	if pid := int(tmuxServerPID.Load()); pid != 0 {
		if tmuxProcessRunning(pid) {
			parts = append(parts, fmt.Sprintf("server pid %d running", pid))
		} else {
			parts = append(parts, fmt.Sprintf("server pid %d exited", pid))
		}
	}
	socket := tmuxSocketPath()
	if _, err := os.Stat(socket); err != nil {
		parts = append(parts, "socket missing: "+socket)
	} else {
		parts = append(parts, "socket present: "+socket)
	}
	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs("list-sessions", "-F", "#{session_name}")...).CombinedOutput()
	if err != nil {
		parts = append(parts, fmt.Sprintf("server unreachable: %v: %s", err, strings.TrimSpace(string(out))))
		return strings.Join(append(parts, "session "+session+" absent"), "; ")
	}
	names := strings.Fields(string(out))
	state := "absent"
	if slices.Contains(names, session) {
		state = "present"
	}
	parts = append(parts, "server alive", "session "+session+" "+state, fmt.Sprintf("%d sessions: %s", len(names), strings.Join(names, ",")))
	return strings.Join(parts, "; ")
}

// tmuxSocketPath is where `tmux -L` places the isolated server's socket.
func tmuxSocketPath() string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), tmuxSocketName)
}

// recordTmuxServerPID remembers the isolated server's process for tmuxServerReport.
func recordTmuxServerPID(ctx context.Context) {
	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs("display-message", "-p", "#{pid}")...).Output()
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
		tmuxServerPID.Store(int64(pid))
	}
}

func tmuxProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
