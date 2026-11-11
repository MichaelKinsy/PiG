package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The abort contract of archive.ts (async-contracts row): unpackData checks the context at every entry and a failure removes the
// staging directory; packStore writes to a staging file and renames it last, so an ended context leaves no target either.

func TestUnpackDataStopsAtTheEntryAfterTheContextEndsLeavingNothing(t *testing.T) {
	base := t.TempDir()
	if err := PackData(background, hostDirectory(t, base), archiveManifest, serverPart(t, base), kitServerPart(t, base), filepath.Join(base, "out.tar")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(background)
	cancel()
	_, _, _, err := UnpackStaging(ctx, filepath.Join(base, "out.tar"), filepath.Join(base, "in"), func(ArchiveManifest) error { return nil }, ImportLimits{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := dirNames(t, base); !slices.Equal(got, []string{"host", "in", "kit-server.jsonl", "out.tar", "server.jsonl"}) {
		t.Fatalf("base = %v: a staging directory or the target was left", got)
	}
}

func TestPackDataWithAnEndedContextLeavesNeitherATargetNorAStagingDirectory(t *testing.T) {
	base := t.TempDir()
	dir := hostDirectory(t, base)
	ctx, cancel := context.WithCancel(background)
	cancel()
	err := PackData(ctx, dir, archiveManifest, serverPart(t, base), kitServerPart(t, base), filepath.Join(base, "out.tar"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(filepath.Join(base, "out.tar")); !os.IsNotExist(statErr) {
		t.Fatalf("the target exists after an ended context: %v", statErr)
	}
	if got := dirNames(t, base); !slices.Equal(got, []string{"host", "kit-server.jsonl", "server.jsonl"}) {
		t.Fatalf("base = %v", got)
	}
}
