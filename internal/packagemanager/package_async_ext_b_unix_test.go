//go:build unix

package packagemanager

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func mkfifo(t *testing.T, path string) *os.File {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// Opened read-write so the test's end stays open between the shell's own opens.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func eventLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// Pi package-manager.ts:60,1424 checkForAvailableUpdates runs its registry and remote checks through runWithConcurrency(checks,
// UPDATE_CHECK_CONCURRENCY = 4) and awaits them all (:1864-1885): at most four checks are in flight, and the call returns only after
// every check has finished, with the updates in configured order.
//
// The fake npm announces each `view` on a FIFO the test reads and then waits for a release token, so the test sees exactly which
// checks are in flight. The absence of a fifth is a bounded wait: a pool wider than four can only start it, never undo it.
func TestCheckForAvailableUpdatesRunsFourChecksAtATimeAndJoinsAll(t *testing.T) {
	cwd, agentDir, sm, pm := resolveFixtureWithHome(t)
	dir := t.TempDir()
	start := mkfifo(t, filepath.Join(dir, "start.fifo"))
	release := mkfifo(t, filepath.Join(dir, "release.fifo"))
	events := filepath.Join(dir, "events")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in *view*) ;; *) exit 0;; esac
echo start >> '%[1]s'
echo x > '%[2]s'
read token < '%[3]s'
echo end >> '%[1]s'
echo '"2.0.0"'
`, events, filepath.Join(dir, "start.fifo"), filepath.Join(dir, "release.fifo"))
	npm := filepath.Join(dir, "npm")
	if err := os.WriteFile(npm, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetNpmCommand([]string{npm}); err != nil {
		t.Fatal(err)
	}
	const packages = 8
	var configured []codingagent.PackageSource
	for i := range packages {
		name := fmt.Sprintf("check-%d", i)
		ref, err := ParseNpmInstallRef("npm:" + name)
		if err != nil {
			t.Fatal(err)
		}
		writeResolveFile(t, filepath.Join(NpmInstallRoot(cwd, agentDir, ref, false), "node_modules", name, "package.json"), `{"name":"`+name+`","version":"1.0.0"}`)
		configured = append(configured, codingagent.PackageSource{Source: "npm:" + name})
	}
	if err := sm.SetPackages(configured); err != nil {
		t.Fatal(err)
	}

	done := make(chan []PackageUpdate, 1)
	go func() { done <- pm.CheckForAvailableUpdates() }()
	reader := bufio.NewReader(start)
	for range 4 {
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}
	fifth := make(chan struct{})
	go func() {
		first := true
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			if first {
				close(fifth)
				first = false
			}
		}
	}()
	select {
	case <-fifth:
		t.Fatal("a fifth registry check started while four were still running")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := release.WriteString(strings.Repeat("go\n", packages)); err != nil {
		t.Fatal(err)
	}
	updates := <-done
	if len(updates) != packages {
		t.Fatalf("updates = %+v, want %d: the call returned before every check finished", updates, packages)
	}
	for i, update := range updates {
		if update.Source != configured[i].Source {
			t.Errorf("update %d = %s, want configured order %s", i, update.Source, configured[i].Source)
		}
	}
	inFlight, peak := 0, 0
	for _, line := range eventLines(t, events) {
		if line == "start" {
			inFlight++
		} else {
			inFlight--
		}
		peak = max(peak, inFlight)
	}
	if peak > 4 {
		t.Errorf("peak concurrent registry checks = %d, want at most 4", peak)
	}
}

// Pi package-manager.ts:1192-1260 updateConfiguredSources awaits npm metadata checks, then starts the user/project npm batches and
// the Git pool together and joins them with Promise.all (:1256): a failing npm batch must not abandon a Git update that is still
// running, and the batch's error is the one reported. Within a checkout ensureGitRef awaits the fetch before it compares and
// resets (:2160-2208).
//
// The Git update is held at its first git process until the progress callback sees the npm batch fail, so the Git work is provably
// still running when the first error exists.
func TestUpdateJoinsTheGitPoolAfterAnNpmBatchFails(t *testing.T) {
	cwd, agentDir, sm, _ := resolveFixtureWithHome(t)
	testenv.IsolateGit(t)
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	npm := filepath.Join(dir, "npm")
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
case "$*" in *view*) echo '"2.0.0"';; *install*) echo "npm exploded" >&2; exit 3;; esac
`, calls)
	if err := os.WriteFile(npm, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetNpmCommand([]string{npm}); err != nil {
		t.Fatal(err)
	}
	ref, err := ParseNpmInstallRef("npm:left-pad")
	if err != nil {
		t.Fatal(err)
	}
	writeResolveFile(t, filepath.Join(NpmInstallRoot(cwd, agentDir, ref, false), "node_modules", "left-pad", "package.json"), `{"name":"left-pad","version":"1.0.0"}`)
	origin := testenv.NewSubdirectoryOrigin(t, "joined")
	gitSource := "git:" + origin.URL
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:left-pad"}, {Source: gitSource}}); err != nil {
		t.Fatal(err)
	}
	checkout, err := GitCheckoutPath(cwd, agentDir, gitSource, false)
	if err != nil {
		t.Fatal(err)
	}
	origin.CloneAt(t, checkout, origin.Commits[0])

	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var events []ProgressEvent
	heldFirst := false
	gate := testenv.NewGitGate(t, func(op testenv.GitOp) <-chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		if op.Dir != testenv.PhysicalPath(checkout) || heldFirst {
			return nil
		}
		heldFirst = true
		return release
	}, nil)
	manager := NewPackageManager(PackageManagerOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: sm})
	manager.SetProgressCallback(func(event ProgressEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		if event.Type == "error" && event.Source == "npm:left-pad" {
			once.Do(func() { close(release) })
		}
	})
	updateErr := manager.Update("")

	if updateErr == nil || !strings.Contains(updateErr.Error(), "left-pad") && !strings.Contains(updateErr.Error(), "exit status 3") {
		t.Fatalf("Update error = %v, want the failed npm batch", updateErr)
	}
	mu.Lock()
	seen := slices.Clone(events)
	mu.Unlock()
	if !slices.ContainsFunc(seen, func(e ProgressEvent) bool { return e.Type == "complete" && e.Source == gitSource }) {
		t.Errorf("Update returned before the Git update finished: %s", describe(seen))
	}
	if head := testenv.RunGit(t, checkout, "rev-parse", "HEAD"); head != origin.Commits[2] {
		t.Errorf("checkout HEAD = %s, want the fetched commit %s", head, origin.Commits[2])
	}
	var verbs []string
	for _, op := range gate.Ops(checkout) {
		verbs = append(verbs, op.Args[0])
	}
	fetch, reset := slices.Index(verbs, "fetch"), slices.Index(verbs, "reset")
	if fetch < 0 || reset < 0 || fetch > reset {
		t.Errorf("git processes in the checkout = %v, want fetch before reset", verbs)
	}
	data, _ := os.ReadFile(calls)
	view, install := strings.Index(string(data), "view"), strings.Index(string(data), "install")
	if view < 0 || install < 0 || view > install {
		t.Errorf("npm calls = %q, want the metadata check before the batch install", data)
	}
	if overlaps := gate.Overlaps(); len(overlaps) != 0 {
		t.Errorf("overlapping git processes: %v", overlaps)
	}
}

// Pi package-manager.ts:1449-1458 resolve installs each missing source inside its own `installMissing` closure: the prompt, the
// install and the failure report of one source finish before the next source is asked, in settings order, and a failed install does
// not stop the next source.
func TestResolveAsksOnMissingOneSourceAtATimeAfterTheInstallOfThePreviousOne(t *testing.T) {
	_, _, sm, pm := resolveFixtureWithHome(t)
	dir := t.TempDir()
	installs := filepath.Join(dir, "installs")
	npm := filepath.Join(dir, "npm")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *install*) echo install >> '%s'; exit 1;; esac\n", installs)
	if err := os.WriteFile(npm, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetNpmCommand([]string{npm}); err != nil {
		t.Fatal(err)
	}
	sources := []string{"npm:first-one", "npm:second-two", "npm:third-three"}
	var configured []codingagent.PackageSource
	for _, source := range sources {
		configured = append(configured, codingagent.PackageSource{Source: source})
	}
	if err := sm.SetPackages(configured); err != nil {
		t.Fatal(err)
	}
	var asked []string
	var installsSeen []int
	resolved, err := pm.Resolve(func(source string) (MissingSourceAction, error) {
		asked = append(asked, source)
		installsSeen = append(installsSeen, len(eventLines(t, installs)))
		return MissingSourceInstall, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, sources) {
		t.Errorf("asked = %v, want settings order %v", asked, sources)
	}
	if !slices.Equal(installsSeen, []int{0, 1, 2}) {
		t.Errorf("installs finished when each source was asked = %v, want [0 1 2]: the next prompt must wait for the previous install", installsSeen)
	}
	if len(resolved.InstallFailures) != len(sources) {
		t.Errorf("install failures = %+v, want one per source", resolved.InstallFailures)
	}
}
