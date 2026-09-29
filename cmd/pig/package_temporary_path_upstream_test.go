package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/source"
)

// packages/coding-agent/test/package-manager.test.ts:1285 checks path construction independently of resolution, installation, or offline policy.
func TestTemporaryNpmPathUnderAgentRootUpstream(t *testing.T) {
	f := newPackageResourceFixture(t)
	parsed, err := source.Parse("npm:left-pad", source.Options{Bare: source.BareReject})
	if err != nil || parsed.Kind != source.KindNPM {
		t.Fatalf("parse npm source: %+v, %v", parsed, err)
	}
	root, err := temporaryPackagePath(f.agent, "npm", "")
	if err != nil {
		t.Fatal(err)
	}
	install := npmPackagePath(root, parsed)
	if !strings.HasSuffix(filepath.ToSlash(install), "node_modules/left-pad") {
		t.Fatalf("path=%q", install)
	}
	tempRoot := filepath.Join(f.agent, "tmp", "extensions")
	rel, err := filepath.Rel(tempRoot, install)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("path escaped temp root: %s, %v", install, err)
	}
	if strings.HasPrefix(install, filepath.Join(os.TempDir(), "pi-extensions")) {
		t.Fatalf("global temporary path=%s", install)
	}
	info, err := os.Stat(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("temp mode=%o", info.Mode().Perm())
	}
}
