package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports apps/p2/test/p2/host-dirs.test.ts: p2's host directories (per-host stores): the server's and each session's
// directory with its store and files, made private (0700, store 0600), every outbox off while p2 does not sync (also on
// a store that had one), the old layout refused.

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func sessionDirectory(t *testing.T, data, id string) string {
	t.Helper()
	dir, err := SessionDirectory(data, id)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHostDirectoriesAreLaidOutPrivateWithTheOutboxOff(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()
	if got, want := ServerDirectory(data), filepath.Join(data, "server"); got != want {
		t.Errorf("ServerDirectory = %q", got)
	}
	if got, want := sessionDirectory(t, data, "a1b2"), filepath.Join(data, "sessions", "a1b2"); got != want {
		t.Errorf("SessionDirectory = %q", got)
	}
	if got, want := HostFiles(sessionDirectory(t, data, "a1b2")), filepath.Join(data, "sessions", "a1b2", "files"); got != want {
		t.Errorf("HostFiles = %q", got)
	}
	for _, id := range []string{"", "..", "a/b", "../x", ".hidden"} {
		if _, err := SessionDirectory(data, id); err == nil {
			t.Errorf("SessionDirectory accepted %q", id)
		}
	}
	server, err := OpenHostStore(ctx, ServerDirectory(data), false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := OpenHostStore(ctx, sessionDirectory(t, data, "s1"), false)
	if err != nil {
		t.Fatal(err)
	}
	if server.Outbox() != nil || session.Outbox() != nil {
		t.Error("an outbox is on")
	}
	if err := server.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if mode(t, ServerDirectory(data)) != 0o700 || mode(t, sessionDirectory(t, data, "s1")) != 0o700 {
		t.Error("a host directory is not 0700")
	}
	if mode(t, HostStore(sessionDirectory(t, data, "s1"))) != 0o600 || mode(t, HostStore(ServerDirectory(data))) != 0o600 {
		t.Error("a store is not 0600")
	}
}

func TestAssertMigratedRefusesTheOldLayout(t *testing.T) {
	data := t.TempDir()
	if err := AssertMigrated(data); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "sessions", "s1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AssertMigrated(data); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(data, "sessions", "s2.sqlite")
	if err := os.WriteFile(old, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AssertMigrated(data); err == nil || !strings.Contains(err.Error(), "migrate-data") {
		t.Errorf("sessions/<id>.sqlite: %v", err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "server.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AssertMigrated(data); err == nil || !strings.Contains(err.Error(), "migrate-data") {
		t.Errorf("server.sqlite: %v", err)
	}
}

func TestOpenHostStoreTurnsTheOutboxOffOfAStoreThatHadOne(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()
	dir := sessionDirectory(t, data, "s1")
	before, err := OpenHostStore(ctx, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if before.Outbox() == nil {
		t.Fatal("outbox true opened without one")
	}
	if err := before.Close(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := OpenHostStore(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Outbox() != nil {
		t.Error("the outbox stayed on")
	}
	if err := after.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
