package plugins

// pi: packages/chord/src/next-node/plugins/install.ts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/chord/test/next-node/plugins/install.test.ts. Tests of installing a plugin's dependencies (slice 7c §5):
// the npm arguments, run in the plugin's real directory, the command overridable; a failure and an abort reject.

// fakeNpm is a fake npm (a Node script, as upstream's is) that records its directory and arguments, one per line, in
// dir/record.txt, and then exits with code, or, with hang, waits to be stopped.
func fakeNpm(t *testing.T, dir string, code int, hang bool) []string {
	t.Helper()
	script := filepath.Join(dir, "fake-npm.mjs")
	lines := []string{
		`import { writeFileSync } from "node:fs";`,
		`import { join } from "node:path";`,
		`writeFileSync(join(import.meta.dirname, "record.txt"), [process.cwd(), ...process.argv.slice(2)].join("\n") + "\n");`,
	}
	if hang {
		lines = append(lines, "setTimeout(() => {}, 30000);")
	} else {
		if code != 0 {
			lines = append(lines, `console.error("npm said no");`)
		}
		lines = append(lines, fmt.Sprintf("process.exit(%d);", code))
	}
	writeFiles(t, dir, map[string]string{"fake-npm.mjs": strings.Join(lines, "\n") + "\n"})
	return []string{pcNode(t), script}
}

func TestInstallDependenciesRunsNpmInstallWithoutDevDependenciesPeersScriptsAuditOrFundingInThePluginsRealDirectory(t *testing.T) {
	tools := tempRoot(t)
	elsewhere := tempRoot(t)
	root := tempRoot(t)
	dir := writePlugin(t, elsewhere, "deps", "@test/deps", map[string]string{"worker.ts": ""}, map[string]any{
		"dependencies": map[string]any{"a": "1.0.0"},
	})
	testenv.RequireDirectoryLink(t, dir, filepath.Join(root, "deps"))
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallDependencies(discovery.Plugins[0], background, InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}); err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(filepath.Join(tools, "record.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{realOf(t, dir), "install", "--omit=dev", "--legacy-peer-deps", "--ignore-scripts", "--no-audit", "--no-fund"}
	if got := strings.Split(strings.TrimSuffix(string(record), "\n"), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %q, want %q", got, want)
	}
}

func TestInstallDependenciesRejectsWithTheCommandsOutputWhenItFailsAndWhenTheContextAborts(t *testing.T) {
	tools := tempRoot(t)
	root := tempRoot(t)
	writePlugin(t, root, "deps", "@test/deps", map[string]string{}, nil)
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	plugin := discovery.Plugins[0]
	err = InstallDependencies(plugin, background, InstallOptions{NpmCommand: fakeNpm(t, tools, 1, false)})
	if err == nil || !regexp.MustCompile(`^plugin "@test/deps": .* install failed \(exit 1\): npm said no$`).MatchString(err.Error()) {
		t.Fatalf("error = %v", err)
	}
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	time.AfterFunc(50*time.Millisecond, func() { cancel(stop) })
	err = InstallDependencies(plugin, aborted, InstallOptions{NpmCommand: fakeNpm(t, tools, 0, true)})
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason %v", err, stop)
	}
}

func TestInstallDependenciesRefusesAnEmptyCommandAndAnAbortedContextBeforeRunningAnything(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "deps", "@test/deps", map[string]string{}, nil)
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	plugin := discovery.Plugins[0]
	err = InstallDependencies(plugin, background, InstallOptions{NpmCommand: []string{}})
	if err == nil || err.Error() != "installDependencies: npmCommand is empty" {
		t.Fatalf("error = %v", err)
	}
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	cancel(stop)
	tools := tempRoot(t)
	err = InstallDependencies(plugin, aborted, InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)})
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason", err)
	}
	if _, statErr := os.Stat(filepath.Join(tools, "record.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the command ran: %v", statErr)
	}
}

// Not Pi's: Pi's install waits for the `close` of the command, the end of its output, so a process the command started that
// inherited the pipes holds the install for as long as it lives. Here the fake npm starts a sleeping Node that inherits them
// and exits with code 0 (then with code 1): the install ends with the command, within the stop grace of its exit, with the
// result of its exit code and the output it wrote.
func TestInstallDependenciesEndsWithTheCommandThoughAProcessItStartedHoldsItsPipes(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(fmt.Sprintf("exit%d", code), func(t *testing.T) {
			tools := tempRoot(t)
			root := tempRoot(t)
			writePlugin(t, root, "deps", "@test/deps", map[string]string{}, nil)
			discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
			if err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(tools, "fake-npm.mjs")
			writeFiles(t, tools, map[string]string{"fake-npm.mjs": strings.Join([]string{
				`import { spawn } from "node:child_process";`,
				`import { writeFileSync } from "node:fs";`,
				`import { join } from "node:path";`,
				`const child = spawn(process.execPath, ["-e", "setTimeout(() => {}, 60000)"], { stdio: ["ignore", "inherit", "inherit"], detached: true });`,
				`child.unref();`,
				`writeFileSync(join(import.meta.dirname, "held.pid"), String(child.pid));`,
				`console.error("npm output");`,
				fmt.Sprintf("process.exit(%d);", code),
			}, "\n") + "\n"})
			t.Cleanup(func() {
				if raw, err := os.ReadFile(filepath.Join(tools, "held.pid")); err == nil {
					var pid int
					if _, err := fmt.Sscan(string(raw), &pid); err == nil {
						if process, err := os.FindProcess(pid); err == nil {
							_ = process.Kill()
						}
					}
				}
			})
			done := make(chan error, 1)
			go func() {
				done <- InstallDependencies(discovery.Plugins[0], background, InstallOptions{NpmCommand: []string{pcNode(t), script}})
			}()
			select {
			case err := <-done:
				if code == 0 && err != nil {
					t.Fatalf("install = %v, want nil: the command exited with code 0", err)
				}
				if code != 0 && (err == nil || !strings.Contains(err.Error(), "(exit 1): npm output")) {
					t.Fatalf("install = %v, want the failure of exit 1 with the command's output", err)
				}
			case <-time.After(25 * time.Second):
				t.Fatal("the install waited for what holds the command's pipes")
			}
		})
	}
}
