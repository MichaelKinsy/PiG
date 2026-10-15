package pioracle

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// A Pi oracle script compares the version it was handed with the installed package's package.json. Under a commit pin the display
// version (1.1.0+42a3497d) is never that string, so a test that passes it fails against the right oracle and passes only by
// accident against the wrong one.
func TestNodeOracleScriptsGetThePackageVersion(t *testing.T) {
	root := repoRoot(t)
	call := regexp.MustCompile(`"node",[^\n]*\b(?:pigversion|coding)\.UpstreamVersion\b`)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", ".upstream", "tmp", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if call.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these tests pass the display version to a node oracle script; pass pigversion.UpstreamPackageVersion:\n%s", strings.Join(offenders, "\n"))
	}
}

// A recording probe stamps the pin's display version into its fixture, which the Go test compares with the pin. A literal
// package version (1.1.0) is shared by the release and every commit pin on it, so such a stamp cannot tell fixtures apart.
func TestObservationProbesStampThePinDisplayVersion(t *testing.T) {
	root := repoRoot(t)
	stamp := regexp.MustCompile(`\b(?:piVersion|pi)["']?\s*:\s*["']\d+\.\d+\.\d+["']`)
	var offenders []string
	for _, dir := range []string{"coding/testdata", "ai/testdata"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".mjs") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				if stamp.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, rel+":"+strconv.Itoa(i+1))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("probes stamp a literal Pi version; use process.env.PIG_UPSTREAM_VERSION:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// An oracle script's version guard compares the installed Pi with the pinned package version, which it imports from
// test/parity/testdata/pi-version.mjs (.upstream/pin). A literal (the 1.1.0 guards Pi 1.2.0's pin move found in 117
// scripts) goes stale at every pin move and fails each guard; this finds any script that spells the pinned version out.
func TestOracleScriptsReadThePinnedPackageVersion(t *testing.T) {
	root := repoRoot(t)
	literal := regexp.MustCompile(`["'` + "`" + `]` + regexp.QuoteMeta(pigversion.UpstreamPackageVersion) + `["'` + "`" + `]`)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", ".upstream", "tmp", "build", "pi-dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".mjs") && !strings.HasSuffix(path, ".cjs") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if literal.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these oracle scripts spell the pinned Pi version %s; import PI_PACKAGE_VERSION from test/parity/testdata/pi-version.mjs:\n  %s", pigversion.UpstreamPackageVersion, strings.Join(offenders, "\n  "))
	}
}
