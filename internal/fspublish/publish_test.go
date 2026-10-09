package fspublish

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPublishMakesTheStageVisible(t *testing.T) {
	dir := t.TempDir()
	stage, target := filepath.Join(dir, ".stage"), filepath.Join(dir, "target")
	writeFile(t, stage, "new")
	if err := Publish(stage, target); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != "new" {
		t.Fatalf("target = %q, want %q", got, "new")
	}
}

func TestPublishNeverReplacesAnExistingTarget(t *testing.T) {
	dir := t.TempDir()
	stage, target := filepath.Join(dir, ".stage"), filepath.Join(dir, "target")
	writeFile(t, stage, "new")
	writeFile(t, target, "old")
	if err := Publish(stage, target); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Publish over an existing target = %v, want an exist error", err)
	}
	if got := readFile(t, target); got != "old" {
		t.Fatalf("target = %q after a refused publish, want %q", got, "old")
	}
}
