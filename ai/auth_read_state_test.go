package ai

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// countAuthFileReads replaces readAuthFile for the test and returns the live
// read count.
func countAuthFileReads(t *testing.T) *int {
	t.Helper()
	reads := 0
	previous := readAuthFile
	readAuthFile = func(path string) ([]byte, error) {
		reads++
		return previous(path)
	}
	t.Cleanup(func() { readAuthFile = previous })
	return &reads
}

func seedAuthFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed auth.json: %v", err)
	}
	return path
}

// Upstream auth-storage.ts readLatestData compares getFileRevision with the
// revision of its last read and reuses the parsed data while they match. A
// model catalog resolves provider env once per model, so rereading auth.json on
// every lookup turned one catalog publication into hundreds of file reads.
func TestAuthStorageReusesSnapshotWhileRevisionUnchanged(t *testing.T) {
	path := seedAuthFile(t, `{"custom":{"type":"api_key","key":"k","env":{"REGION":"eu"}},"github-copilot":{"type":"oauth","refresh":"r","access":"a","expires":1}}`)
	reads := countAuthFileReads(t)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for range 200 {
		env, err := store.GetProviderEnv("custom")
		if err != nil || env["REGION"] != "eu" {
			t.Fatalf("GetProviderEnv = %v, %v", env, err)
		}
	}
	if _, ok, err := store.Get("custom"); err != nil || !ok {
		t.Fatalf("Get = %v, %v", ok, err)
	}
	if _, ok, err := store.GetRaw("github-copilot"); err != nil || !ok {
		t.Fatalf("GetRaw = %v, %v", ok, err)
	}
	if status := store.GetAuthStatus("github-copilot"); !status.Configured {
		t.Fatalf("GetAuthStatus = %+v", status)
	}
	if creds, err := store.Load(); err != nil || len(creds) != 2 {
		t.Fatalf("Load = %v, %v", creds, err)
	}
	if *reads != 1 {
		t.Fatalf("auth.json reads = %d, want 1 while its revision is unchanged", *reads)
	}
}

func TestAuthStorageRereadsWhenRevisionChanges(t *testing.T) {
	path := seedAuthFile(t, `{"custom":{"type":"api_key","key":"old-key"}}`)
	reads := countAuthFileReads(t)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if cred, _, _ := store.Get("custom"); cred.Key != "old-key" {
		t.Fatalf("initial key = %q", cred.Key)
	}

	// Rewrite in place with the same size, then move the modification time, as
	// another Pi or PiG process editing auth.json would.
	if err := os.WriteFile(path, []byte(`{"custom":{"type":"api_key","key":"new-key"}}`), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if cred, _, _ := store.Get("custom"); cred.Key != "new-key" {
		t.Fatalf("key after external write = %q, want new-key", cred.Key)
	}

	other, err := NewAuthStorage(path)
	if err != nil {
		t.Fatalf("new other: %v", err)
	}
	if err := other.Set("custom", Credential{Type: CredentialAPIKey, Key: "set-by-other"}); err != nil {
		t.Fatalf("other set: %v", err)
	}
	if cred, _, _ := store.Get("custom"); cred.Key != "set-by-other" {
		t.Fatalf("key after another store's write = %q", cred.Key)
	}
	if err := store.Set("custom", Credential{Type: CredentialAPIKey, Key: "set-by-self"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if cred, _, _ := store.Get("custom"); cred.Key != "set-by-self" {
		t.Fatalf("key after own write = %q", cred.Key)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok, err := store.Get("custom"); err != nil || ok {
		t.Fatalf("Get after removal = %v, %v; want absent", ok, err)
	}
	if *reads < 5 {
		t.Fatalf("auth.json reads = %d; every revision change must reread", *reads)
	}
}

func TestAuthStorageSnapshotIsNotAliasedByCallers(t *testing.T) {
	path := seedAuthFile(t, `{"custom":{"type":"api_key","key":"k","env":{"REGION":"eu"},"gatewayConfig":{"a":1}}}`)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	env, _ := store.GetProviderEnv("custom")
	env["REGION"] = "mutated"
	creds, _ := store.Load()
	creds["custom"].Env["REGION"] = "mutated"
	creds["custom"].GatewayConfig[0] = 'X'
	delete(creds, "custom")
	raw, _, _ := store.GetRaw("custom")
	raw.Env["REGION"] = "mutated"

	cred, ok, err := store.GetRaw("custom")
	if err != nil || !ok {
		t.Fatalf("GetRaw = %v, %v", ok, err)
	}
	if cred.Env["REGION"] != "eu" || string(cred.GatewayConfig) != `{"a":1}` {
		t.Fatalf("snapshot aliased by callers: env=%v gateway=%s", cred.Env, cred.GatewayConfig)
	}
}

func BenchmarkAuthStorageGetProviderEnv(b *testing.B) {
	path := filepath.Join(b.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"custom":{"type":"api_key","key":"k","env":{"REGION":"eu"}},"github-copilot":{"type":"oauth","refresh":"r","access":"a","expires":1}}`), 0o600); err != nil {
		b.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := store.GetProviderEnv("custom"); err != nil {
			b.Fatal(err)
		}
	}
}

// Replacing auth.json atomically with a same-size file that keeps the old
// modification time (a restore or a copy that preserves times) is still a new
// revision: upstream getFileRevision includes the file identity and change
// time, so the replacement's credential is read.
func TestAuthStorageRereadsAtomicReplacementWithPreservedMtime(t *testing.T) {
	path := seedAuthFile(t, `{"custom":{"type":"api_key","key":"old-key"}}`)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if cred, _, _ := store.Get("custom"); cred.Key != "old-key" {
		t.Fatalf("initial key = %q", cred.Key)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(path), "auth.json.new")
	if err := os.WriteFile(replacement, []byte(`{"custom":{"type":"api_key","key":"new-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if cred, _, _ := store.Get("custom"); cred.Key != "new-key" {
		t.Fatalf("key after atomic replacement with preserved mtime = %q, want new-key", cred.Key)
	}
}
