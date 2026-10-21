package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// `pig update --extensions` over two Git Packages of one repository (different subdirectories and tags) and one other repository.
// Pi races the two (package-manager.ts:1171-1181 starts a task per source and :2160 names one checkout for both), and Pigpen's CI
// failed with `fatal: Unable to create '.../.git/index.lock': File exists`, then `git reset --hard FETCH_HEAD^{commit} failed with
// code 128`. PiG finishes one package's update before it starts the next one of that checkout, in settings order.
//
// The first git process in the shared checkout waits until the other repository's clone has finished, which is not ordered with
// it. By then the pool has taken every task that can run beside the first package, so an "Updating" line for the second package
// would mean it runs concurrently with the first.
func TestUpdateExtensionsRunsThePackagesOfOneCheckoutOneAtATime(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refA, refB string
	}{
		{"different tags", "v1", "v2"},
		{"the same tag", "v2", "v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageResourceFixture(t)
			// A managed checkout sits at <agent>/git/<host>/<origin path>, and on Windows git cannot create the sample hooks of a clone whose path passes 260 characters, so the agent directory is short instead of named for this test.
			f.agent = testenv.ShortTempDir(t, "a")
			t.Setenv("PIG_CODING_AGENT_DIR", f.agent)
			f.settings = codingagent.NewSettingsManager(f.cwd, f.agent)
			testenv.IsolateGit(t)
			shared := testenv.NewSubdirectoryOrigin(t, "shared")
			other := testenv.NewSubdirectoryOrigin(t, "other")
			sourceA := "git:" + shared.URL + "@" + tc.refA + "#subdirectory=a"
			sourceB := "git:" + shared.URL + "@" + tc.refB + "#subdirectory=b"
			sourceC := "git:" + other.URL
			if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: sourceA}, {Source: sourceB}, {Source: sourceC}}); err != nil {
				t.Fatal(err)
			}
			checkout, err := packagemanager.GitCheckoutPath(f.cwd, f.settings.AgentDir(), sourceA, false)
			if err != nil {
				t.Fatal(err)
			}
			checkoutC, err := packagemanager.GitCheckoutPath(f.cwd, f.settings.AgentDir(), sourceC, false)
			if err != nil {
				t.Fatal(err)
			}
			shared.CloneAt(t, checkout, shared.Commits[0])
			t.Chdir(f.cwd)

			stdout, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdout.Close() }()
			var mu sync.Mutex
			var printedAtClone string
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
				printed, err := os.ReadFile(stdout.Name())
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				printedAtClone = string(printed)
				opsAtClone = gate.Ops(checkout)
				mu.Unlock()
				close(release)
			})

			oldStdout := os.Stdout
			os.Stdout = stdout
			code := runPackageCommand([]string{"update", "--extensions", "--no-approve"})
			os.Stdout = oldStdout

			mu.Lock()
			printed, ops := printedAtClone, opsAtClone
			mu.Unlock()
			if !strings.Contains(printed, "Updating "+sourceA+"...") || !strings.Contains(printed, "Updating "+sourceC+"...") {
				t.Errorf("stdout when the other repository finished = %q, want the first package and the other repository started", printed)
			}
			if strings.Contains(printed, "Updating "+sourceB+"...") {
				t.Errorf("the second package of the checkout started while the first still held it: %q", printed)
			}
			if len(ops) > 1 {
				t.Errorf("git processes in the shared checkout while the first package held it = %v, want at most the held one", ops)
			}
			if overlaps := gate.Overlaps(); len(overlaps) != 0 {
				t.Errorf("git processes started inside a checkout another process was still using: %v", overlaps)
			}
			if code != 0 {
				t.Fatalf("pig update --extensions exit code = %d, want 0", code)
			}
			if head := testenv.RunGit(t, checkout, "rev-parse", "HEAD"); head != shared.Commits[2] {
				t.Errorf("checkout HEAD = %s, want %s: the last package in settings order decides the commit", head, shared.Commits[2])
			}
			fetches := []string{}
			for _, op := range gate.Ops(checkout) {
				if op.Args[0] == "fetch" {
					fetches = append(fetches, op.Args[len(op.Args)-1])
				}
			}
			if want := []string{tc.refA, tc.refB}; !slices.Equal(fetches, want) {
				t.Errorf("fetches of the shared checkout = %v, want %v in settings order", fetches, want)
			}
		})
	}
}
