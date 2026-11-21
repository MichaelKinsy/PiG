package packagemanager

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi package-manager.ts:1281-1296 getLocalGitUpdateTarget runs `git remote set-head origin -a` with `.catch(() => {})`: a remote that
// cannot be reached must not fail the update target, which falls back to the origin/HEAD the clone already has and fetches that branch.
func TestLocalGitUpdateTargetSurvivesAFailingSetHead(t *testing.T) {
	testenv.IsolateGit(t)
	origin := testenv.NewSubdirectoryOrigin(t, "sethead")
	checkout := filepath.Join(t.TempDir(), "checkout")
	origin.CloneAt(t, checkout, origin.Commits[2])
	testenv.RunGit(t, checkout, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	target, err := GetLocalGitUpdateTarget(checkout)
	if err != nil {
		t.Fatalf("a failing `remote set-head` failed the update target: %v", err)
	}
	if target.ref != "origin/HEAD" || len(target.fetchArgs) == 0 || target.fetchArgs[0] != "fetch" {
		t.Fatalf("target = %+v, want origin/HEAD fetched from the existing remote-tracking branch", target)
	}
}

// Pi package-manager.ts:2209-2230 refreshTemporaryGitSource (called by resolveExtensionSources and awaited there) awaits withProgress("pull") (:973): the start event precedes the first git
// process, the terminal event follows the awaited update, and a failed refresh is swallowed and keeps the cached checkout.
func TestRefreshTemporaryGitSourceReportsPullAroundTheAwaitedUpdate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		breakOrg bool
		terminal string
	}{{"refresh succeeds", false, "complete"}, {"refresh fails and keeps the checkout", true, "error"}} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.IsolateGit(t)
			t.Setenv("PI_OFFLINE", "")
			t.Setenv("PIG_OFFLINE", "")
			// ShortTempDir keeps the temporary git checkout and its .git/objects/pack/*.keep path under Windows MAX_PATH.
			sm := codingagent.NewSettingsManager(testenv.ShortTempDir(t, "pm-"), testenv.ShortTempDir(t, "pm-"))
			origin := testenv.NewSubdirectoryOrigin(t, "temporary")
			source := "git:" + origin.URL
			ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
			if err != nil {
				t.Fatal(err)
			}
			checkout, err := TemporaryGitCheckoutPath(sm.AgentDir(), ref)
			if err != nil {
				t.Fatal(err)
			}
			origin.CloneAt(t, checkout, origin.Commits[0])
			if tc.breakOrg {
				testenv.RunGit(t, checkout, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
			}
			var mu sync.Mutex
			var events []ProgressEvent
			var startedBeforeFirstGit *bool
			testenv.NewGitGate(t, func(op testenv.GitOp) <-chan struct{} {
				mu.Lock()
				defer mu.Unlock()
				if op.Dir == testenv.PhysicalPath(checkout) && startedBeforeFirstGit == nil {
					seen := started(events, source)
					startedBeforeFirstGit = &seen
				}
				return nil
			}, nil)
			if _, err := resolveTemporaryGitSource(sm, source, func(e ProgressEvent) {
				mu.Lock()
				events = append(events, e)
				mu.Unlock()
			}); err != nil {
				t.Errorf("a refresh failure must keep the cached checkout without failing the resolution: %v", err)
			}
			// The gate's hold callback takes mu for every git process, so it is not held while git runs below.
			mu.Lock()
			seen := slices.Clone(events)
			before := startedBeforeFirstGit
			mu.Unlock()
			if before == nil || !*before {
				t.Errorf("the first git process ran before the pull start event: %s", describe(seen))
			}
			var types []string
			for _, e := range seen {
				if e.Source == source && e.Action == "pull" {
					types = append(types, e.Type)
				}
			}
			if !slices.Equal(types, []string{"start", tc.terminal}) {
				t.Errorf("pull events = %v, want [start %s]: %s", types, tc.terminal, describe(seen))
			}
			head := testenv.RunGit(t, checkout, "rev-parse", "HEAD")
			want := origin.Commits[0]
			if !tc.breakOrg {
				want = origin.Commits[2]
			}
			if head != want {
				t.Errorf("checkout HEAD = %s, want %s", head, want)
			}
		})
	}
}
