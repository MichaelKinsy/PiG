package piglet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBundledPigletClosurePathAndSizeLimits(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, path := range []string{"../outside", "/absolute", `C:/absolute`, `skills\review`, ".", "skills/../other", ".git/config", "~/private"} {
		t.Run(path, func(t *testing.T) {
			closure := pigletClosure{root: root, prefix: "alpha.source/commit", files: map[string][]byte{}, modes: map[string]os.FileMode{}}
			if _, err := closure.copy(path); err == nil {
				t.Fatalf("unsafe path %q accepted", path)
			}
		})
	}
	file, err := root.Create("large")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((32 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	closure := pigletClosure{root: root, prefix: "alpha.source/commit", files: map[string][]byte{}, modes: map[string]os.FileMode{}}
	if _, err := closure.copy("large"); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversize=%v", err)
	}
	if len(closure.files) != 0 {
		t.Fatal("oversize staged data")
	}
}

func TestBundledPigletRejectsAliasedPaths(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("name: alpha\npackages: {base: &base local:./resources}\nskills: [{name: review, origins: [*base]}]\n"), &node); err != nil {
		t.Fatal(err)
	}
	if err := rejectBundledYAMLAliases(&node); err == nil {
		t.Fatal("aliased Resource path can escape rewrite")
	}
}

func TestBundledPigletClosureBoundsEmptyDirectories(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.Mkdir("resource", 0o755); err != nil {
		t.Fatal(err)
	}
	// The documented entry limit counts the selected root and empty directories, not just files.
	for i := range 4096 {
		if err := root.Mkdir(fmt.Sprintf("resource/dir-%04d", i), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	closure := pigletClosure{root: root, prefix: "alpha.source/commit", files: map[string][]byte{}, modes: map[string]os.FileMode{}}
	if _, err := closure.copy("resource"); err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Fatalf("empty directory stress=%v", err)
	}
}

func BenchmarkPigletClosure(b *testing.B) {
	dir := b.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "resource"), 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 16 {
		if err := os.WriteFile(filepath.Join(dir, "resource", fmt.Sprintf("file-%02d", i)), []byte(strings.Repeat("x", 64<<10)), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	b.ReportAllocs()
	b.SetBytes(1 << 20)
	for b.Loop() {
		closure := pigletClosure{root: root, prefix: "alpha.source/commit", files: map[string][]byte{}, modes: map[string]os.FileMode{}}
		if _, err := closure.copy("resource"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPigletClosurePreservesExecutableMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	closure := pigletClosure{root: root, prefix: "alpha.source/commit", files: map[string][]byte{}, modes: map[string]os.FileMode{}}
	relative, err := closure.copy("tool")
	if err != nil {
		t.Fatal(err)
	}
	if closure.modes[relative].Perm()&0o111 != info.Mode().Perm()&0o111 {
		t.Fatal("executable mode lost")
	}
}
