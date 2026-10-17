package docsdrift

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every environment variable the docs name must be one Pig actually reads.
//
// A variable that does not exist is worse than an undocumented one: the reader
// sets it, sees no change, and has no way to tell a typo from a broken feature.
// This scans the source for the name rather than keeping a second list, so the
// check cannot drift from the code it is checking. It accepts a variable Pig
// sets for a child process as well as one it reads, because both are real to a
// reader.

var (
	docEnvRE    = regexp.MustCompile("`((?:PIG|PI)_[A-Z0-9_]+)(?:=[^`]*)?`")
	sourceEnvRE = regexp.MustCompile(`((?:PIG|PI)_[A-Z0-9_]+)`)
)

func readEnvNames(t *testing.T) map[string]bool {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	names := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".upstream", ".git", "node_modules", "target", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		// Go reads most variables; the image build scripts and Dockerfiles define the build arguments the benchmark pages name.
		if base := filepath.Base(path); !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".sh") && !strings.HasPrefix(base, "Dockerfile") && base != "Makefile" && !strings.HasSuffix(path, ".mk") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range sourceEnvRE.FindAllStringSubmatch(string(data), -1) {
			names[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan the source: %v", err)
	}
	if len(names) < 20 {
		t.Fatalf("found only %d environment variables in the source; the scanner is broken", len(names))
	}
	return names
}

func TestEveryDocumentedEnvironmentVariableExists(t *testing.T) {
	read := readEnvNames(t)
	for _, src := range corpus(t) {
		for _, m := range docEnvRE.FindAllStringSubmatch(src.Body, -1) {
			if !read[m[1]] && m[1] != "PIG_ACP_PIG_COMMAND" { // read by the pig-acp companion of the Pigpen acp Package (packages.md), not by PiG
				t.Errorf("%s documents %s, which appears nowhere in the source", src.Rel, m[1])
			}
		}
	}
}
