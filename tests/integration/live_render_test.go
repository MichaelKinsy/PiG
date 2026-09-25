//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestLiveRenderingAdvancesWithoutInput drives the release-shaped binary through
// tmux. After each initial prompt (and the queued steering prompt in the serial
// tool case), the test only captures the pane: no keypress can accidentally
// invalidate a cached component and make a frozen timer appear healthy.
func TestLiveRenderingAdvancesWithoutInput(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run("tool-"+mode, func(t *testing.T) {
			h := startLiveRenderHarness(t, mode, "{}")
			h.send("Run: tui live tool pause\r")
			h.expectContains(10*time.Second, "LIVE-TOOL-08", "Elapsed", "Working")

			// Queue steering while the tool is paused, then provide no more input.
			h.send("please also check the logs\r")
			h.expectContains(3*time.Second, "Steering: please also check the logs")
			samples := captureLiveToolSamples(h.capture, 4*time.Second)
			assertSpinnerChanges(t, samples, 3)
			assertElapsedAdvances(t, samples, 3*time.Second)

			// The command resumes its stream after the pause without a keypress.
			pane := h.expectContains(5*time.Second, "LIVE-TOOL-24")
			if !strings.Contains(pane, "Steering: please also check the logs") {
				t.Fatalf("queued steering disappeared while output streamed:\n%s", pane)
			}
		})
	}

	t.Run("parallel-tools", func(t *testing.T) {
		cwd := t.TempDir()
		paths := []string{
			filepath.Join(cwd, ".pig-live-parallel-a"),
			filepath.Join(cwd, ".pig-live-parallel-b"),
		}
		for _, path := range paths {
			if output, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
				t.Fatalf("mkfifo: %v: %s", err, output)
			}
		}
		opened := make(chan error, len(paths))
		for i, path := range paths {
			go func() {
				file, err := os.OpenFile(path, os.O_WRONLY, 0)
				opened <- err
				if err != nil {
					return
				}
				defer func() { _ = file.Close() }()
				_, _ = fmt.Fprintf(file, "LIVE-PAR-%c\n", 'A'+i)
				time.Sleep(5 * time.Second)
			}()
		}

		h := startLiveRenderHarnessAt(t, "regular", "{}", cwd, nil)
		h.send("Run: tui live parallel tools\r")
		h.expectContains(10*time.Second, ".pig-live-parallel-a", ".pig-live-parallel-b")
		for range paths {
			select {
			case err := <-opened:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("both FIFO writers did not open; parallel read calls did not start together")
			}
		}
		samples := captureLiveToolSamples(h.capture, 3500*time.Millisecond)
		assertSpinnerChanges(t, samples, 3)
		h.expectContains(5*time.Second, "LIVE-PARALLEL-DONE")
	})

	t.Run("compaction", func(t *testing.T) {
		root, err := findRepoRoot()
		if err != nil {
			t.Fatal(err)
		}
		fixture := filepath.Join(root, "parity", "scenarios", "compaction", "testdata", "compact-session")
		cwd := t.TempDir()
		if err := os.CopyFS(cwd, os.DirFS(fixture)); err != nil {
			t.Fatalf("copy compact session: %v", err)
		}
		h := startLiveRenderHarnessAt(t, "regular", `{"compaction":{"keepRecentTokens":100}}`, cwd, []string{"--session-dir", filepath.Join(cwd, "sessions"), "-c"}, "TEST_FAUX_SLOW_COMPACTION=1")
		h.send("Run: tui live tool\r")
		h.expectContains(10*time.Second, "LIVE-TOOL-DONE")
		if !pollUntil(5*time.Second, func() bool {
			pane := h.capture()
			return strings.Contains(pane, "LIVE-TOOL-DONE") &&
				!strings.Contains(pane, "Working") && !strings.Contains(pane, "Elapsed")
		}) {
			t.Fatalf("tool turn did not settle before compaction:\n%s", h.capture())
		}
		h.send("/compact\r")
		h.expectContains(5*time.Second, "Compacting context... (escape to cancel)")
		samples := captureLiveSamples(h.capture, 7, 120*time.Millisecond)
		assertSpinnerChanges(t, samples, 2)
	})

	t.Run("retry-countdown", func(t *testing.T) {
		settings := `{"retry":{"enabled":true,"maxRetries":1,"baseDelayMs":3000}}`
		h := startLiveRenderHarness(t, "regular", settings)
		h.send("Trigger: retryable provider error\r")
		h.expectContains(5*time.Second, "Retrying (1/1) in 3s")
		samples := captureLiveSamples(h.capture, 15, 250*time.Millisecond)
		assertSpinnerChanges(t, samples, 2)
		assertStringsAppearInOrder(t, strings.Join(samples, "\n"), []string{
			"Retrying (1/1) in 3s",
			"Retrying (1/1) in 2s",
			"Retrying (1/1) in 1s",
		})
		h.expectContains(3*time.Second, "retry-ok")
	})
}

func startLiveRenderHarness(t *testing.T, mode, settings string, extraEnv ...string) *harness {
	t.Helper()
	return startLiveRenderHarnessAt(t, mode, settings, "", nil, extraEnv...)
}

func startLiveRenderHarnessAt(t *testing.T, mode, settings, cwd string, extraArgs []string, extraEnv ...string) *harness {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if mode == "fullscreen" {
		if settings == "{}" {
			settings = `{"tuiMode":"fullscreen"}`
		} else {
			settings = strings.TrimSuffix(settings, "}") + `,"tuiMode":"fullscreen"}`
		}
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t)
	env := append([]string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1"}, extraEnv...)
	args := []string{"--model", "test-faux/faux-1", "--no-extensions"}
	if extraArgs == nil {
		args = append(args, "--no-session")
	} else {
		args = append(args, extraArgs...)
	}
	h.startArgsAt(args, cwd, env...)
	h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")
	return h
}

func captureLiveToolSamples(capture func() string, window time.Duration) []string {
	// 500ms pane snapshots can repeatedly land on two phases of a ten-frame spinner. Use a denser cadence coprime to Loader's 800ms cycle and a delayed 1000ms cycle, within the same observation window.
	const interval = 137 * time.Millisecond
	return captureLiveSamples(capture, 1+int(window/interval), interval)
}

func TestLiveToolSamplingDoesNotAliasAnimatingSpinner(t *testing.T) {
	// Loader uses 80ms. Also exercise slower delivered frames: the sampling contract must not depend on ideal renderer/tmux scheduling under load.
	for _, interval := range []time.Duration{80 * time.Millisecond, 100 * time.Millisecond, 160 * time.Millisecond} {
		for _, window := range []time.Duration{4 * time.Second, 3500 * time.Millisecond} {
			t.Run(fmt.Sprintf("frame=%s/window=%s", interval, window), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					const frames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
					for phase := time.Duration(0); phase < interval; phase += time.Millisecond {
						start := time.Now().Add(-phase)
						samples := captureLiveToolSamples(func() string {
							index := int(time.Since(start)/interval) % len([]rune(frames))
							return string([]rune(frames)[index]) + " Working"
						}, window)
						assertSpinnerChanges(t, samples, 3)
					}
				})
			})
		}
	}
}

func captureLiveSamples(capture func() string, count int, interval time.Duration) []string {
	samples := make([]string, count)
	for i := range count {
		if i > 0 {
			time.Sleep(interval)
		}
		samples[i] = capture()
	}
	return samples
}

func assertSpinnerChanges(t *testing.T, samples []string, minDistinct int) {
	t.Helper()
	spinner := regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] (?:Working|Compacting context|Retrying)`)
	distinct := map[string]bool{}
	for _, sample := range samples {
		if frame := spinner.FindString(sample); frame != "" {
			distinct[frame] = true
		}
	}
	if len(distinct) < minDistinct {
		t.Fatalf("spinner produced %d distinct frames, want at least %d; samples:\n%s", len(distinct), minDistinct, strings.Join(samples, "\n--- sample ---\n"))
	}
}

func assertElapsedAdvances(t *testing.T, samples []string, minimum time.Duration) {
	t.Helper()
	elapsed := regexp.MustCompile(`Elapsed ([0-9]+\.[0-9]+)s`)
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		match := elapsed.FindStringSubmatch(sample)
		if len(match) != 2 {
			t.Fatalf("running tool sample has no elapsed footer:\n%s", sample)
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if delta := values[len(values)-1] - values[0]; delta < minimum.Seconds() {
		t.Fatalf("elapsed footer advanced %.1fs, want at least %.1fs: %v", delta, minimum.Seconds(), values)
	}
	for i := 2; i < len(values); i += 2 {
		if values[i] <= values[i-2] {
			t.Fatalf("elapsed footer did not advance within one second: %v", values)
		}
	}
}

func assertStringsAppearInOrder(t *testing.T, output string, values []string) {
	t.Helper()
	rest := output
	for _, value := range values {
		_, after, ok := strings.Cut(rest, value)
		if !ok {
			t.Fatalf("%q did not appear in order; output:\n%s", value, output)
		}
		rest = after
	}
}
