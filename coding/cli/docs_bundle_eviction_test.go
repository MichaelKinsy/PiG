//go:build !pig_strip_docs

package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/contentdirs"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// The system prompt names the pages of this build's documentation bundle, and the session reads them with its tools for as long as it runs. Another PiG build
// that shares the config root evicts the bundles beyond the four most recently used when it writes its own (contentdirs.Evict), and the use time a session records
// is its start. A session that outlives four newer builds' use found its pages gone, and nothing wrote them again. The bundle a running session started with stays.
func TestARunningSessionsDocsBundleSurvivesAnotherBuildsEviction(t *testing.T) {
	for _, test := range []struct {
		name string
		// warm writes the bundle before the session starts, so startup finds it and leases it; cold leaves it to the deferred write.
		warm bool
	}{{"warm start", true}, {"cold start", false}} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			t.Cleanup(func() { configroot.SetDocsWriter(nil); configroot.SetDocsPresent(true) })
			t.Cleanup(releaseDocsLeases)
			if test.warm {
				if err := pigdocs.EnsureSynced(home); err != nil {
					t.Fatal(err)
				}
			}
			prepareDocsBundle()
			writeDocsBundle()
			bundle := pigdocs.DocsDir(codingagent.ConfigRoot())
			if _, err := os.Stat(filepath.Join(bundle, "README.md")); err != nil {
				t.Fatalf("the session has no docs bundle: %v", err)
			}
			// The session started long ago; four other builds have been used since.
			long := time.Now().Add(-72 * time.Hour)
			if err := os.Chtimes(bundle, long, long); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Dir(bundle)
			for i, hash := range []string{"1111111111111111", "2222222222222222", "3333333333333333", "4444444444444444"} {
				other := filepath.Join(parent, "bundle-"+hash)
				if err := os.MkdirAll(other, 0o755); err != nil {
					t.Fatal(err)
				}
				used := time.Now().Add(-time.Duration(i+1) * time.Hour)
				if err := os.Chtimes(other, used, used); err != nil {
					t.Fatal(err)
				}
			}
			// Another build starts and writes its bundle: the eviction it runs spares its own and the four most recently used.
			if _, err := contentdirs.Evict(parent, "bundle", filepath.Join(parent, "bundle-ffffffffffffffff")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(bundle, "README.md")); err != nil {
				t.Fatalf("another build's eviction removed the docs bundle a running session's prompt names: %v", err)
			}
		})
	}
}
