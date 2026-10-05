package runtimecell

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoBuildCgoEnvFollowsGoExternalLinkRule(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, want string }{
		{"android", "amd64", "CGO_ENABLED=1"},
		{"android", "386", "CGO_ENABLED=1"},
		{"android", "arm", "CGO_ENABLED=1"},
		{"android", "arm64", "CGO_ENABLED=0"},
		{"linux", "amd64", "CGO_ENABLED=0"},
		{"linux", "arm64", "CGO_ENABLED=0"},
		{"darwin", "arm64", "CGO_ENABLED=0"},
		{"windows", "amd64", "CGO_ENABLED=0"},
	} {
		if got := GoBuildCgoEnv(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("GoBuildCgoEnv(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

// The toolchain is the denominator: for every Android port it lists, `go build -n` of an executable accepts the entry GoBuildCgoEnv returns, and a port that gets cgo fails without it. `-n` plans the build without a C compiler.
func TestGoBuildCgoEnvMatchesTheGoToolchain(t *testing.T) {
	goCmd, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go is required to check the cgo rule against the toolchain: %v", err)
	}
	ports, err := exec.Command(goCmd, "tool", "dist", "list").Output()
	if err != nil {
		t.Fatalf("go tool dist list: %v", err)
	}
	dir := t.TempDir()
	for name, content := range map[string]string{"go.mod": "module probe\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan := func(goos, goarch, cgo string) ([]byte, error) {
		cmd := exec.Command(goCmd, "build", "-n", "-o", filepath.Join(dir, "probe"), ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, cgo, "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
		return cmd.CombinedOutput()
	}
	checked := 0
	for port := range strings.FieldsSeq(string(ports)) {
		goos, goarch, _ := strings.Cut(port, "/")
		if goos != "android" {
			continue
		}
		checked++
		env := GoBuildCgoEnv(goos, goarch)
		if out, err := plan(goos, goarch, env); err != nil {
			t.Errorf("%s with %s: %v\n%s", port, env, err, out)
		}
		if env == "CGO_ENABLED=1" {
			if out, err := plan(goos, goarch, "CGO_ENABLED=0"); err == nil {
				t.Errorf("%s builds with CGO_ENABLED=0, so GoBuildCgoEnv need not enable cgo for it\n%s", port, out)
			}
		}
	}
	if checked == 0 {
		t.Fatal("go tool dist list names no android port")
	}
}
