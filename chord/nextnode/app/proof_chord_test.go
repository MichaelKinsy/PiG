package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Proof of the kit's private-directory and private-store steps (layout.ts privateDirectory and privateStore) and of the worker
// that runs them (worker.ts startWorker): directories 0700 and stores 0600, also when they existed looser before.

func TestPrivateDirectoryMakesAMissingDirectoryAndTightensAnExistingOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not enforced on Windows")
	}
	root := t.TempDir()
	missing := filepath.Join(root, "a", "b")
	if err := PrivateDirectory(missing); err != nil {
		t.Fatalf("PrivateDirectory(missing): %v", err)
	}
	if mode := modeOf(t, missing); mode != 0o700 {
		t.Fatalf("a made directory has mode %o, want 700", mode)
	}
	loose := filepath.Join(root, "loose")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(loose); err != nil {
		t.Fatalf("PrivateDirectory(existing): %v", err)
	}
	if mode := modeOf(t, loose); mode != 0o700 {
		t.Fatalf("a directory that existed looser has mode %o, want 700", mode)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(filepath.Join(file, "inside")); err == nil {
		t.Fatal("a directory below a file was made")
	}
}

func TestPrivateStoreCreatesTheFileAt0600AndTightensItsJournalFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not enforced on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "store.sqlite")
	if err := PrivateStore(path); err != nil {
		t.Fatalf("PrivateStore(missing): %v", err)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("a made store has mode %o, want 600", mode)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(name, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := PrivateStore(path); err != nil {
		t.Fatalf("PrivateStore(existing): %v", err)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if mode := modeOf(t, name); mode != 0o600 {
			t.Fatalf("%s has mode %o after PrivateStore, want 600", filepath.Base(name), mode)
		}
	}
	if err := PrivateStore(filepath.Join(dir, "missing", "store.sqlite")); err == nil {
		t.Fatal("a store in a directory that does not exist was made")
	}
}

func TestStartWorkerMakesItsDirectoryAndStoresPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not enforced on Windows")
	}
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	defer p1.Release()
	member(t, p1, "start") // the worker (this test binary run again, StartWorker) is up
	dir := filepath.Join(s.dataDir, "workers", "project", "project%7Cp1")
	if mode := modeOf(t, dir); mode != 0o700 {
		t.Fatalf("the worker's directory has mode %o, want 700", mode)
	}
	if mode := modeOf(t, filepath.Join(dir, archiveStore)); mode != 0o600 {
		t.Fatalf("the worker's plugin store has mode %o, want 600", mode)
	}
}
