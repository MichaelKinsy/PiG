package packagemanager

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's package-manager.ts:1171-1181 updates every Git source as its own task (GIT_UPDATE_CONCURRENCY 4) and getGitInstallPath (:2160)
// names the checkout by host and path only, so Pi races two sources of one repository: both fetch into one FETCH_HEAD and both run
// `git reset --hard`, which fails on .git/index.lock. PiG runs the packages of one checkout one at a time in settings order, as
// Pi's non-failing runs do (the last package decides the checked-out commit), and keeps the other checkouts concurrent.
//
// The test needs no sleep: the first git process in the shared checkout waits until an independent repository's clone, which is
// not ordered with it, has finished. By then the pool has taken every task that can run beside the first package, so a second
// package of the shared checkout that has started is one that runs concurrently with it.
func TestUpdateRunsThePackagesOfOneCheckoutOneAtATime(t *testing.T) {
	for _, tc := range []struct {
		name         string
		refA, refB   string
		wantUpdateOK bool
	}{
		{name: "different refs", refA: "v1", refB: "v2", wantUpdateOK: true},
		{name: "same ref", refA: "v2", refB: "v2", wantUpdateOK: true},
		{name: "the first package fails", refA: "no-such-ref", refB: "v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, sm := trustedSettings(t)
			testenv.IsolateGit(t)
			shared := testenv.NewSubdirectoryOrigin(t, "shared")
			other := testenv.NewSubdirectoryOrigin(t, "other")
			sourceA := "git:" + shared.URL + "@" + tc.refA + "#subdirectory=a"
			sourceB := "git:" + shared.URL + "@" + tc.refB + "#subdirectory=b"
			sourceC := "git:" + other.URL
			if err := sm.SetPackages([]codingagent.PackageSource{{Source: sourceA}, {Source: sourceB}, {Source: sourceC}}); err != nil {
				t.Fatal(err)
			}
			checkout, err := GitCheckoutPath(cwd, sm.AgentDir(), sourceA, false)
			if err != nil {
				t.Fatal(err)
			}
			checkoutC, err := GitCheckoutPath(cwd, sm.AgentDir(), sourceC, false)
			if err != nil {
				t.Fatal(err)
			}
			shared.CloneAt(t, checkout, shared.Commits[0])

			var mu sync.Mutex
			var events []ProgressEvent
			var seenAtClone []ProgressEvent
			var opsAtClone []testenv.GitOp
			release := make(chan struct{})
			heldFirst := false
			var gate *testenv.GitGate
			gate = testenv.NewGitGate(t, func(op testenv.GitOp) <-chan struct{} {
				mu.Lock()
				defer mu.Unlock()
				if op.Dir != testenv.PhysicalPath(checkout) || heldFirst {
					return nil
				}
				heldFirst = true
				return release
			}, func(op testenv.GitOp) {
				if op.Dir != testenv.PhysicalPath(checkoutC) || op.Args[0] != "clone" {
					return
				}
				mu.Lock()
				seenAtClone = slices.Clone(events)
				opsAtClone = gate.Ops(checkout)
				mu.Unlock()
				close(release)
			})

			manager := NewPackageManager(PackageManagerOptions{CWD: cwd, AgentDir: sm.AgentDir(), SettingsManager: sm})
			manager.SetProgressCallback(func(event ProgressEvent) {
				mu.Lock()
				events = append(events, event)
				mu.Unlock()
			})
			updateErr := manager.Update("")

			if started(seenAtClone, sourceB) {
				t.Errorf("the second package of the checkout started while the first still held it: %s", describe(seenAtClone))
			}
			if len(opsAtClone) > 1 {
				t.Errorf("git processes in the shared checkout while the first package held it = %v, want at most the held one", opsAtClone)
			}
			if overlaps := gate.Overlaps(); len(overlaps) != 0 {
				t.Errorf("git processes started inside a checkout another process was still using: %v", overlaps)
			}
			if (updateErr == nil) != tc.wantUpdateOK {
				t.Fatalf("Update error = %v, want success %v", updateErr, tc.wantUpdateOK)
			}
			if !tc.wantUpdateOK && !strings.Contains(updateErr.Error(), "fetch origin "+tc.refA+" failed") {
				t.Errorf("Update error = %v, want the first package's fetch failure", updateErr)
			}
			// The last package in settings order decides the commit: Pi's fetches share one FETCH_HEAD.
			if head := testenv.RunGit(t, checkout, "rev-parse", "HEAD"); head != shared.Commits[2] {
				t.Errorf("checkout HEAD = %s, want the commit %s of %s", head, shared.Commits[2], tc.refB)
			}
			mu.Lock()
			defer mu.Unlock()
			indexOf := func(typ, source string) int {
				return slices.IndexFunc(events, func(e ProgressEvent) bool { return e.Type == typ && e.Source == source })
			}
			finishA := indexOf("complete", sourceA)
			if !tc.wantUpdateOK {
				finishA = indexOf("error", sourceA)
			}
			if startB := indexOf("start", sourceB); finishA < 0 || startB < finishA {
				t.Errorf("package B started at %d before package A finished at %d: %s", startB, finishA, describe(events))
			}
			if finishB := indexOf("complete", sourceB); finishB < 0 {
				t.Errorf("package B never completed: %s", describe(events))
			}
			if indexOf("complete", sourceC) < 0 {
				t.Errorf("the independent repository never completed: %s", describe(events))
			}
		})
	}
}

func started(events []ProgressEvent, source string) bool {
	return slices.ContainsFunc(events, func(e ProgressEvent) bool { return e.Type == "start" && e.Source == source })
}

func describe(events []ProgressEvent) string {
	lines := make([]string, len(events))
	for i, e := range events {
		lines[i] = e.Type + " " + e.Source
	}
	return "\n" + strings.Join(lines, "\n")
}
