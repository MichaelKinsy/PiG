//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi startup-ui.ts:32-47,122-140 excludes non-official distributions before the other gates. These keep the original inputs; PiG's own gate (D88) shows setup on an interactive start in the default agent directory exactly when settings.json does not exist, regardless of PI_EXPERIMENTAL; a custom agent directory skips it as in Pi (startup-ui.ts:144-146).
func TestFirstTimeSetupOriginalCasesAsFork(t *testing.T) {
	if codingagent.PackageName == "@earendil-works/pi-coding-agent" && codingagent.AppName == "pi" && codingagent.CONFIG_DIR_NAME == ".pi" {
		t.Fatal("this test requires the actual non-official PiG distribution")
	}
	binary := buildPigBinaryForSignalTest(t)
	var observed struct {
		Cases   []int  `json:"cases"`
		Results []bool `json:"results"`
	}
	for _, tc := range []struct {
		name                           string
		line                           int
		experimental, custom, existing bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/first-time-setup.test.ts:36
		{"returns true when experimental, default agent dir, and no settings.json (official); false for fork", 36, true, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/first-time-setup.test.ts:40
		{"returns false when experimental features are disabled", 40, false, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/first-time-setup.test.ts:46
		{"returns false when a custom agent dir is set", 46, true, true, false},
		// .upstream/v0.87.1/packages/coding-agent/test/first-time-setup.test.ts:52
		{"returns false when settings.json already exists", 52, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shown := firstTimeStartupShowsSetup(t, binary, tc.experimental, tc.custom, tc.existing)
			if want := !tc.existing && !tc.custom; shown != want {
				t.Fatalf("PiG startup displayed setup = %v with settings.json present = %v and a custom agent dir = %v; PiG's gate (D88) shows it exactly when settings.json is missing in the default agent dir", shown, tc.existing, tc.custom)
			}
			observed.Cases = append(observed.Cases, tc.line)
			observed.Results = append(observed.Results, shown)
		})
	}
	data, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("FIRST_TIME_FORK %s\n", data)
}

// ConfigRoot prefers PIG_HOME, then XDG_CONFIG_HOME, over HOME (paths.go ConfigRoot). An ambient XDG_CONFIG_HOME must neither receive the binary's config writes nor make the settings.json the case prepares differ from the one startup reads.
func TestFirstTimeSetupStartupIgnoresAmbientConfigRoot(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t) // Built before the override so the Go toolchain's own XDG use stays out of the assertion.
	ambient := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", ambient)
	if firstTimeStartupShowsSetup(t, binary, true, false, true) {
		t.Fatal("PiG startup displayed setup; real Pi with fork metadata returns false")
	}
	entries, err := os.ReadDir(ambient)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("startup wrote into the ambient XDG_CONFIG_HOME: %v", entries)
	}
}

// A completed interactive Provider turn proves startup did not wait for setup input. The trace is only a readiness barrier, not the acceptance assertion.
func firstTimeStartupShowsSetup(t *testing.T, binary string, experimental, custom, existing bool) bool {
	t.Helper()
	root := t.TempDir()
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "project")
	agentDir := filepath.Join(home, codingagent.CONFIG_DIR_NAME, "agent")
	if custom {
		agentDir = filepath.Join(root, "custom-agent")
	}
	for _, dir := range []string{home, cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settings := filepath.Join(agentDir, "settings.json")
	if existing {
		if err := os.WriteFile(settings, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if _, err := os.Stat(settings); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no settings.json: %v", err)
	}
	env := slices.DeleteFunc(os.Environ(), func(value string) bool {
		name, _, _ := strings.Cut(value, "=")
		return slices.Contains([]string{"HOME", "PIG_HOME", "XDG_CONFIG_HOME", "PI_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PI_PACKAGE_DIR", "PIG_USE_PI_DIRS", "PI_EXPERIMENTAL", "PIG_TEST_FAUX", "PIG_TEST_FAUX_SCENARIO", "PIG_STARTUP_TRACE", "PIG_OFFLINE", "PI_OFFLINE"}, name)
	})
	env = append(env, "HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_STARTUP_TRACE=1", "TERM=xterm-256color")
	if experimental {
		env = append(env, "PI_EXPERIMENTAL=1")
	}
	if custom {
		env = append(env, codingagent.ENV_AGENT_DIR+"="+agentDir)
	}
	master, slave := openPTY(t, 40, 160)
	defer master.Close()
	defer slave.Close()
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	// The reply is found as an output line, which the regular renderer writes; fullscreen is the default since Pi 1.0.0 (settings-manager.ts:1348-1350) and positions rows instead.
	cmd := exec.CommandContext(ctx, binary, "--offline", "--no-extensions", "--no-session", "--model", "test-faux/faux-1", "--tui-mode", "regular")
	cmd.Dir, cmd.Env = cwd, env
	output, diagnostics := &ptyOutput{}, &ptyOutput{}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, diagnostics
	captureCtx, cancelCapture := context.WithCancel(ctx)
	captured := make(chan struct{})
	fd := int(master.Fd())
	go func() {
		defer close(captured)
		buffer := make([]byte, 4096)
		for captureCtx.Err() == nil {
			ready, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 20)
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if err != nil {
				return
			}
			if ready == 0 {
				continue
			}
			n, err := unix.Read(fd, buffer)
			if n > 0 {
				_, _ = output.Write(buffer[:n])
			}
			if err != nil || n == 0 {
				return
			}
		}
	}()
	defer func() { cancelCapture(); <-captured }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	done := make(chan error, 1)
	go func() { defer close(done); done <- cmd.Wait() }()
	defer func() { cancel(); <-done }()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	sent := false
	for {
		text := string(tools.StripANSI(output.since(0)))
		stderr := string(diagnostics.since(0))
		for _, marker := range []string{"Pick a theme.", "Opt-in to anonymous usage data sharing?", "skip setup", ", the minimal coding agent."} {
			if strings.Contains(text+stderr, marker) {
				return true
			}
		}
		if !sent && strings.Contains(stderr, "interactive-ready") {
			if _, err := master.Write([]byte("What is 20+22?\r")); err != nil {
				t.Fatal(err)
			}
			sent = true
		}
		if sent {
			for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' }) {
				if strings.TrimSpace(line) == "42" {
					return false
				}
			}
		}
		select {
		case err := <-done:
			t.Fatalf("startup exited before an interactive answer: %v\nstdout=%s\nstderr=%s", err, text, stderr)
		case <-ctx.Done():
			t.Fatalf("startup did not complete an interactive answer without setup input: %v\nstdout=%s\nstderr=%s", ctx.Err(), text, stderr)
		case <-tick.C:
		}
	}
}
