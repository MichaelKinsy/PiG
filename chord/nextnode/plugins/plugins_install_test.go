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
