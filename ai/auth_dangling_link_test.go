package ai

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// #11655's setup before the shared file exists: the agent directory's auth.json is a symlink to a file that has not been created. Pi's ensureFileExists is writeFileSync(path, "{}"), which follows the link and creates the target, so the lock (which follows the link) finds the real file. Red without the link-following create: every access fails with `lstat …/shared/auth.json: no such file or directory` and nothing is written; the base wrote the target.
func TestAuthStorageCreatesTheTargetOfADanglingSymlinkedAuthFile(t *testing.T) {
	root := t.TempDir()
	shared, agent := filepath.Join(root, "shared"), filepath.Join(root, "agent")
	for _, dir := range []string{shared, agent} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target, link := filepath.Join(shared, "auth.json"), filepath.Join(agent, "auth.json")
	testenv.Symlink(t, filepath.Join("..", "shared", "auth.json"), link)
	storage, err := NewAuthStorage(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Set("anthropic", Credential{Type: CredentialAPIKey, Key: "stored"}); err != nil {
		t.Fatalf("Set through a dangling symlink = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), "stored") {
		t.Fatalf("target = %q, %v; want the credential written to the link's target", data, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("auth.json stopped being a symlink: %v", err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm()&0o077 != 0 && runtime.GOOS != "windows" {
		t.Fatalf("target mode = %v, %v; want owner-only", info.Mode(), err)
	}
}

// The same for models.json's store: FileModelsStore creates the target of a dangling link before it locks.
func TestFileModelsStoreCreatesTheTargetOfADanglingSymlinkedFile(t *testing.T) {
	root := t.TempDir()
	shared, agent := filepath.Join(root, "shared"), filepath.Join(root, "agent")
	for _, dir := range []string{shared, agent} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target, link := filepath.Join(shared, "models-store.json"), filepath.Join(agent, "models-store.json")
	testenv.Symlink(t, target, link)
	store := NewFileModelsStore(link)
	if err := store.Write(t.Context(), "probe", ModelsStoreEntry{Models: []AnyModel{}}); err != nil {
		t.Fatalf("Write through a dangling symlink = %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || !strings.Contains(string(data), "probe") {
		t.Fatalf("target = %q, %v; want the entry written to the link's target", data, err)
	}
}
