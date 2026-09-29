package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPigBinaryDoesNotReplaceConfigRoot(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	if binary == filepath.Clean(os.Getenv("PIG_HOME")) {
		t.Fatal("the shared executable occupies the config root")
	}
	if err := os.MkdirAll(os.Getenv("PIG_HOME"), 0o700); err != nil {
		t.Fatalf("building Pig made its config root unusable: %v", err)
	}
}

func TestPigBinaryBuildsHavePackageLifetime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*testing.T) string
	}{
		{"normal", buildPigBinaryForSignalTest},
		{"release", buildReleasePigBinary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first string
			t.Run("first-consumer", func(t *testing.T) { first = tc.build(t) })
			if _, err := os.Stat(first); err != nil {
				t.Fatalf("binary must outlive its first consumer: %v", err)
			}
			t.Run("next-consumer", func(t *testing.T) {
				t.Setenv("PATH", t.TempDir())
				if got := tc.build(t); got != first {
					t.Fatalf("binary rebuilt: %q, want %q", got, first)
				}
			})
		})
	}
}
