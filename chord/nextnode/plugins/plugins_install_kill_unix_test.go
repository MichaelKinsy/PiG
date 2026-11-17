//go:build unix

package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// pig divergence (D267), not Pi's: the WaitDelay of the shared `run` also counts from an abort. The context ends, the command is sent SIGTERM, and a command that
// ignores it is killed once the stop grace (five seconds) has passed; the install returns the abort's reason then, not when the command chooses to end. Pi sends
// SIGTERM and never kills. The fake npm ignores SIGTERM and runs until killed.
func TestInstallDependenciesKillsACommandThatIgnoresTheAbortsSigtermAfterTheStopGrace(t *testing.T) {
	tools := tempRoot(t)
	root := tempRoot(t)
	writePlugin(t, root, "deps", "@test/deps", map[string]string{}, nil)
	discovery, err := model.DiscoverPlugins(plainRoots(root), model.DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(tools, "fake-npm.mjs")
	writeFiles(t, tools, map[string]string{"fake-npm.mjs": strings.Join([]string{
		`import { writeFileSync } from "node:fs";`,
		`import { join } from "node:path";`,
		`process.on("SIGTERM", () => writeFileSync(join(import.meta.dirname, "term"), "ignored"));`,
		`writeFileSync(join(import.meta.dirname, "pid"), String(process.pid));`,
		`setInterval(() => {}, 1000);`,
	}, "\n") + "\n"})
	t.Cleanup(func() {
		if raw, err := os.ReadFile(filepath.Join(tools, "pid")); err == nil {
			var pid int
			if _, err := fmt.Sscan(string(raw), &pid); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	done := make(chan error, 1)
	go func() {
		done <- InstallDependencies(discovery.Plugins[0], aborted, InstallOptions{NpmCommand: []string{pcNode(t), script}})
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(tools, "pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake npm did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The SIGTERM handler is installed before the pid file is written.
	cancelled := time.Now()
	cancel(stop)
	select {
	case err := <-done:
		if !errors.Is(err, stop) {
			t.Fatalf("install = %v, want the abort's reason %v", err, stop)
		}
		if waited := time.Since(cancelled); waited < defaultStopGrace {
			t.Fatalf("the install returned %v after the abort, before the stop grace %v: a command that ignores SIGTERM was not given the grace", waited, defaultStopGrace)
		}
	case <-time.After(defaultStopGrace + 20*time.Second):
		t.Fatal("the install did not return after the stop grace: a command that ignores SIGTERM was not killed")
	}
	if raw, err := os.ReadFile(filepath.Join(tools, "term")); err != nil || string(raw) != "ignored" {
		t.Fatalf("the command was not sent SIGTERM first: %q, %v", raw, err)
	}
	raw, err := os.ReadFile(filepath.Join(tools, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscan(string(raw), &pid); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the command (pid %d) is still alive after the install returned", pid)
		}
	}
}
