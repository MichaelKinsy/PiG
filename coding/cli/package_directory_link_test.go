package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The upstream Package cases at package-manager.test.ts:516-517,1673-1677 explicitly use an unprivileged Windows junction, not a privileged directory symlink. Node owns that fixture primitive on each platform, and command failure is fatal rather than a skipped assertion.
func requirePackageDirectoryLink(t *testing.T, target, link string) {
	t.Helper()
	if err := createPackageDirectoryLink(t.Context(), target, link); err != nil {
		t.Fatal(err)
	}
}

func createPackageDirectoryLink(ctx context.Context, target, link string) error {
	const script = `require("node:fs").symlinkSync(process.argv[1], process.argv[2], process.platform === "win32" ? "junction" : "dir")`
	command := exec.CommandContext(ctx, "node", "-e", script, target, link)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("create Package directory link: %w: %s", err, output)
	}
	return nil
}

func TestPackageDirectoryLinkDoesNotSkipAssertions(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target & directory")
	link := filepath.Join(dir, "link & directory")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "marker")
	if err := os.WriteFile(marker, []byte("linked content"), 0o600); err != nil {
		t.Fatal(err)
	}
	completed := false
	t.Run("directory link fixture", func(t *testing.T) {
		requirePackageDirectoryLink(t, target, link)
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		linked, err := os.Stat(link)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(info, linked) {
			t.Fatal("directory link did not resolve to its target")
		}
		data, err := os.ReadFile(filepath.Join(link, "marker"))
		if err != nil || string(data) != "linked content" {
			t.Fatalf("linked content=%q error=%v", data, err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("removing the link removed its target: %v", err)
		}
		completed = true
	})
	if !completed {
		t.Fatal("directory-link fixture skipped its assertions")
	}
}

func TestPackageDirectoryLinkSurfacesFailures(t *testing.T) {
	dir := t.TempDir()
	if err := createPackageDirectoryLink(t.Context(), dir, dir); err == nil {
		t.Fatal("occupied destination did not fail")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	link := filepath.Join(dir, "cancelled")
	if err := createPackageDirectoryLink(ctx, dir, link); err == nil {
		t.Fatal("cancelled fixture setup did not fail")
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("cancelled fixture created a link: %v", err)
	}
}
