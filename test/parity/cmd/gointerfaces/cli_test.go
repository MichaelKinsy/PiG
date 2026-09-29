package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the command, not just its flag declaration: an omitted -out must update the inventory a contributor commits, while -out - remains pipeable.
func TestGeneratorOutputAndCanonicalPlatform(t *testing.T) {
	root := t.TempDir()
	// go build -o writes the exact name, and Windows starts only a file with a PATHEXT extension.
	binary := filepath.Join(root, "gointerfaces")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build generator: %v\n%s", err, output)
	}
	for name, body := range map[string]string{
		"go.mod":        "module example.test/generator\n\ngo 1.26.0\n",
		"api.go":        "package fixture\nimport \"unsafe\"\nvar Word [unsafe.Sizeof(uintptr(0))]byte\n",
		"api_tag.go":    "//go:build generator_local_tag\n\npackage fixture\nfunc LocalTagExport() {}\n",
		"api_linux.go":  "package fixture\nfunc LinuxExport() {}\n",
		"api_darwin.go": "package fixture\nfunc DarwinExport() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "test/parity/interfaces"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(platform string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS="+platform, "GOARCH=386", "CGO_ENABLED=1", "GOFLAGS=-tags=generator_local_tag")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("generator %v: %v\n%s", args, err, stderr.String())
		}
		return output
	}
	t.Run("default writes committed path", func(t *testing.T) {
		output := run("linux", ".")
		if len(output) != 0 {
			t.Errorf("default wrote %d bytes to stdout instead of the committed path", len(output))
		}
		data, err := os.ReadFile(filepath.Join(root, "test/parity/interfaces/pig-go.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "LinuxExport") {
			t.Fatal("default inventory does not contain the fixture export")
		}
		if bytes.Contains(data, []byte("LocalTagExport")) || !bytes.Contains(data, []byte(`"type": "[8]byte"`)) {
			t.Fatal("local build tags or target architecture changed the inventory")
		}
	})
	t.Run("explicit stdout and path agree", func(t *testing.T) {
		stdout := run("linux", "-out", "-", ".")
		if output := run("linux", "-out", "custom.json", "."); len(output) != 0 {
			t.Fatalf("explicit path wrote stdout: %s", output)
		}
		data, err := os.ReadFile(filepath.Join(root, "custom.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, stdout) || !strings.Contains(string(stdout), "LinuxExport") {
			t.Fatal("explicit stdout and file inventories differ or omit the export")
		}
	})
	t.Run("host platform does not change inventory", func(t *testing.T) {
		linux := run("linux", "-out", "-", ".")
		darwin := run("darwin", "-out", "-", ".")
		if !bytes.Equal(linux, darwin) {
			t.Fatal("GOOS changed the committed inventory")
		}
	})
}
