package pigletbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// writeFakePigArtifact writes an executable at dir/name that prints a
// version line when the smoke verification runs it: a shell script, or on
// Windows, which starts only files with an executable extension, a small
// program built as name.exe. It returns the artifact's path.
func writeFakePigArtifact(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho pig-test\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module fakepig\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"pig-test\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".exe")
	build := exec.Command("go", "build", "-o", path, ".")
	build.Dir = src
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake artifact: %v\n%s", err, output)
	}
	return path
}

// A native build's default output is pig-<name>, with .exe for a Windows
// target; an explicit Windows output without an extension is refused before
// the build, since Windows could not start it.
func TestNativeArtifactPath(t *testing.T) {
	windows := Target{OS: "windows", Arch: "amd64"}
	linux := Target{OS: "linux", Arch: "amd64"}
	for _, tc := range []struct {
		out    string
		target Target
		want   string
	}{
		{"", linux, "pig-review"},
		{"", windows, "pig-review.exe"},
		{"pig-small", linux, "pig-small"},
		{"pig-small.exe", windows, "pig-small.exe"},
	} {
		got, err := nativeArtifactPath(tc.out, "review", tc.target)
		if err != nil {
			t.Errorf("nativeArtifactPath(%q, %s): %v", tc.out, tc.target, err)
			continue
		}
		if !filepath.IsAbs(got) || filepath.Base(got) != tc.want {
			t.Errorf("nativeArtifactPath(%q, %s) = %q, want an absolute path to %s", tc.out, tc.target, got, tc.want)
		}
	}
	if _, err := nativeArtifactPath("pig-small", "review", windows); err == nil {
		t.Error("nativeArtifactPath accepted a Windows output without an executable extension")
	}
}
