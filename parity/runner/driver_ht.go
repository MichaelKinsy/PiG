//go:build parity

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// htDriver launches the binary inside an ht (headless-terminal) session,
// waits for the ready pattern, optionally sends keys, then captures the
// screen.
//
// ht provides a Kitty-graphics-capable PTY via libghostty-vt, enabling
// parity testing of image rendering code paths that are unreachable under
// tmux (where getCapabilities().images returns null).
//
// Prerequisites:
//   - ht binary on PATH (brew install montanaflynn/tap/ht)
//   - The ht daemon auto-starts on first use
//
// Scenarios using this driver should set env overrides to ensure both
// binaries detect Kitty image capability:
//
//	[env]
//	pig = ["TERM_PROGRAM=ghostty", "TERM=xterm-ghostty", "COLORTERM=truecolor"]
//	pi   = ["TERM_PROGRAM=ghostty", "TERM=xterm-ghostty", "COLORTERM=truecolor"]
//
// The driver automatically strips TMUX/TMUX_PANE from the child environment
// so the tmux guard in detectCapabilities/terminal-image.ts does not fire.
type htDriver struct{}

func (htDriver) Name() string { return "headless-terminal" }

func (htDriver) Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) Result {
	t.Helper()

	// TestParity records the known optional dependency as an allowlisted skip.
	// Direct driver callers fail closed instead of silently skipping.
	if _, err := exec.LookPath("ht"); err != nil {
		return Result{Err: fmt.Errorf("headless-terminal driver requires ht on PATH")}
	}

	cfg := sc.Tmux // reuse TmuxDriverConfig for ready/keys/crop

	tc := newTokenContext(t, "ht-"+sc.Name+"-"+bin.Label)

	// Snapshot agent dirs (same as tmux driver).
	preserveAuth, injectAuth := scenarioAuthModes(sc)
	pigEnv, piEnv := snapshotAgentDirs(t, sc.SourcePath, sc.Env.Pig, sc.Env.Pi, preserveAuth, injectAuth)

	// Build env pairs for this binary.
	envPairs := snapshotBinaryEnv(t, sc.SourcePath, bin.Env, preserveAuth, injectAuth)
	if bin.Label == "pig" {
		envPairs = append(envPairs, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, pigEnv))...)
	} else {
		envPairs = append(envPairs, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, piEnv))...)
	}

	// Strip TMUX and TMUX_PANE so capabilities detector takes the
	// Ghostty/Kitty path instead of the tmux guard. Inherit PATH
	// so node/git/etc. are available.
	envPairs = append(envPairs,
		"TMUX=",
		"TMUX_PANE=",
		"PATH="+os.Getenv("PATH"),
	)

	// Build CLI args for the binary.
	var binArgs []string
	if !sc.Env.OverrideBaseArgs {
		binArgs = append(binArgs, bin.Args...)
	}
	if bin.Label == "pig" {
		binArgs = append(binArgs, tc.expandSlice(sc.Env.PigArgs)...)
	} else {
		binArgs = append(binArgs, tc.expandSlice(sc.Env.PiArgs)...)
		for _, ext := range sc.Env.PiExtensions {
			resolved := ext
			if !filepath.IsAbs(ext) {
				resolved = filepath.Join(filepath.Dir(sc.SourcePath), ext)
			}
			binArgs = append(binArgs, "-e", resolved)
		}
	}
	if bin.Label == "pig" {
		for _, ext := range sc.Env.PigExtensions {
			resolved := ext
			if !filepath.IsAbs(ext) {
				resolved = filepath.Join(filepath.Dir(sc.SourcePath), ext)
			}
			binArgs = append(binArgs, "-e", resolved)
		}
	}
	if sc.Model != "" {
		binArgs = append(binArgs, "--model", sc.Model)
	}

	// Determine working directory.
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
	cwd = snap

	// Build session name. Must be unique per binary per run.
	sessionName := fmt.Sprintf("parity-%s-%s-%s", sc.Name, bin.Label, uniqueID())

	// Screen size.
	size := "100x35"
	if cfg.Width > 0 && cfg.Height > 0 {
		size = fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
	}

	// Build ht run args.
	htArgs := []string{"run", "--size", size, "--name", sessionName}
	for _, kv := range envPairs {
		htArgs = append(htArgs, "--env", kv)
	}
	if cwd != "" {
		htArgs = append(htArgs, "--cwd", cwd)
	}
	htArgs = append(htArgs, bin.Path)
	htArgs = append(htArgs, binArgs...)

	// Launch the session.
	t0 := time.Now()
	out, err := htOutput(ctx, htArgs...)
	if err != nil {
		return Result{Err: fmt.Errorf("ht run failed: %w\n%s", err, out)}
	}

	// Parse the session JSON response to verify launch.
	var sessInfo struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &sessInfo); err != nil {
		return Result{Err: fmt.Errorf("ht run: unexpected output: %s", out)}
	}

	// Register cleanup.
	t.Cleanup(func() {
		_ = exec.Command("ht", "stop", sessionName).Run()
		time.Sleep(200 * time.Millisecond)
		_ = exec.Command("ht", "remove", sessionName).Run()
	})

	// Wait for ready pattern (per-binary, like tmux driver).
	readyPattern := cfg.ReadyPatternPig
	if bin.Label == "pi" {
		readyPattern = cfg.ReadyPatternPi
	}
	if readyPattern == "" {
		readyPattern = "Ready"
	}
	readyTimeout := cfg.ReadyTimeoutSeconds
	if readyTimeout <= 0 {
		readyTimeout = 30
	}

	readyDeadline := time.Duration(readyTimeout) * time.Second
	lastView, err := waitForHTReady(ctx, readyPattern, readyDeadline, func() (string, error) {
		view, err := htOutput(ctx, "view", sessionName)
		return string(view), err
	})
	if err != nil {
		return Result{
			Err: fmt.Errorf("ht wait for ready %q failed: %w\nview:\n%s", readyPattern, err, lastView),
		}
	}

	// Send keys if any.
	for _, keySeq := range cfg.Keys {
		expanded := tc.expand(keySeq)
		sendArgs := []string{"send", sessionName, expanded, "--wait-idle", "300ms"}
		if sendOut, err := htOutput(ctx, sendArgs...); err != nil {
			return Result{Err: fmt.Errorf("ht send %q failed: %w\n%s", expanded, err, sendOut)}
		}
	}

	// Post-keys settle.
	if cfg.SettleSeconds > 0 {
		time.Sleep(time.Duration(cfg.SettleSeconds) * time.Second)
	} else {
		time.Sleep(300 * time.Millisecond)
	}

	elapsed := time.Since(t0).Milliseconds()

	// Capture screen content (plain text).
	viewOut, err := htOutput(ctx, "view", sessionName)
	if err != nil {
		return Result{Err: fmt.Errorf("ht view failed: %w\n%s", err, viewOut)}
	}

	// Capture ANSI version for escaped_output_equal comparisons.
	viewAnsi, _ := htOutput(ctx, "view", sessionName, "--format", "ansi")

	raw := string(viewOut)
	escaped := string(viewAnsi)

	// Strip trailing "cursor: R,C" status line that ht appends.
	raw = stripHTCursorLine(raw)
	escaped = stripHTCursorLine(escaped)

	// Apply crop using CaptureStart/CaptureEnd patterns (same as tmux driver).
	plain := raw
	if cfg.CaptureStart != "" || cfg.CaptureEnd != "" || cfg.CaptureEndRegex != "" {
		plain = cropCapture(plain, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast)
		escaped = cropCapture(escaped, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast)
	}

	return Result{
		Output:    strings.TrimRight(plain, "\n"),
		Escaped:   strings.TrimRight(escaped, "\n"),
		RuntimeMs: elapsed,
		ReadyOK:   true,
	}
}

// htOutput runs ht and returns its stdout: the session JSON or the screen the
// scenario compares. ht's stderr is tool noise (warnings, daemon logs) that
// must not enter either, so it is kept only to explain a failure.
func htOutput(ctx context.Context, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, "ht", args...)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return output, fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return output, nil
}

func waitForHTReady(ctx context.Context, pattern string, timeout time.Duration, view func() (string, error)) (string, error) {
	deadline := time.Now().Add(timeout)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		last, lastErr = view()
		if lastErr == nil && paneMatchesReadyPattern(last, pattern) {
			return last, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return last, lastErr
	}
	return last, fmt.Errorf("timed out after %s", timeout)
}

// stripHTCursorLine removes the trailing "cursor: R,C" or "cursor: R,C (hidden)"
// line that ht appends to view output.
func stripHTCursorLine(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasPrefix(last, "cursor:") {
			lines = lines[:len(lines)-1]
		}
	}
	return strings.Join(lines, "\n")
}
