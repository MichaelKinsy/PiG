package upstreammirror

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func writeMirror(t *testing.T, root string, marker map[string]string, pkgVersion string) {
	t.Helper()
	current := filepath.Join(root, ".upstream", "current")
	if err := os.MkdirAll(filepath.Join(current, "packages", "coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if marker != nil {
		write(filepath.Join(current, ".pig-upstream-source.json"), marker)
	}
	write(filepath.Join(current, "packages", "coding-agent", "package.json"), map[string]string{"version": pkgVersion})
}

func TestCheckAcceptsOnlyTheMirrorOfTheLedgerVersion(t *testing.T) {
	pinned := map[string]string{"version": pigversion.UpstreamVersion, "tag": pigversion.UpstreamRef, "commit": pigversion.UpstreamCommit}
	other := strings.Repeat("3", 40)
	cases := []struct {
		name     string
		version  string
		marker   map[string]string
		pkg      string
		missing  bool
		wantFail string // a substring of the error; "" accepts
	}{
		{name: "the pinned build", version: pigversion.UpstreamVersion, marker: pinned, pkg: pigversion.UpstreamPackageVersion},
		{name: "no mirror", version: pigversion.UpstreamVersion, missing: true, wantFail: "mirror is missing"},
		{name: "another Pi release", version: pigversion.UpstreamVersion, marker: map[string]string{"version": "1.0.3", "tag": "v1.0.3", "commit": other}, pkg: "1.0.3", wantFail: "is Pi 1.0.3"},
		{name: "no provenance marker", version: pigversion.UpstreamVersion, pkg: pigversion.UpstreamPackageVersion, wantFail: "has no provenance"},
		{name: "the pinned version at another commit", version: pigversion.UpstreamVersion, marker: map[string]string{"version": pigversion.UpstreamVersion, "tag": pigversion.UpstreamRef, "commit": other}, pkg: pigversion.UpstreamPackageVersion, wantFail: other},
		{name: "a commit pin's mirror for its version", version: "9.9.9+0123abcd", marker: map[string]string{"version": "9.9.9+0123abcd", "tag": other, "commit": other}, pkg: "9.9.9"},
		{name: "a release mirror for a commit pin's version", version: "9.9.9+0123abcd", marker: map[string]string{"version": "9.9.9", "tag": "v9.9.9", "commit": other}, pkg: "9.9.9", wantFail: "is Pi 9.9.9@"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if !c.missing {
				writeMirror(t, root, c.marker, c.pkg)
			}
			err := Check(root, c.version)
			switch {
			case c.wantFail == "" && err != nil:
				t.Fatalf("Check rejected the mirror: %v", err)
			case c.wantFail != "" && err == nil:
				t.Fatal("Check accepted the mirror")
			case c.wantFail != "" && (!strings.Contains(err.Error(), c.wantFail) || !strings.Contains(err.Error(), "make upstream-mirror")):
				t.Fatalf("Check error %q does not name %q and the fix", err, c.wantFail)
			}
		})
	}
}

// The mirror of the pinned release is read through ReadPicked, which applies the release's recorded Pi fixes to the file it returns:
// each file a fix covers must still be the pinned release's, so a fix applies once and never to a mirror that already holds it.
func TestTheMirrorHoldsThePinnedReleaseWhereTheRecordedFixesApply(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", filepath.Join(root, filepath.FromSlash(BackportsScript)), "--verify-mirror", filepath.Join(root, ".upstream", "current")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, err := ReadPicked(root, "packages/coding-agent/docs/models-configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "Default: true for provider anthropic") || !strings.Contains(string(got), "Default: true for a baseUrl on api.anthropic.com") {
		t.Error("ReadPicked did not return Pi's models-configuration.md with the post-release fixes")
	}
}
