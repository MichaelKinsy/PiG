package pigdocs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestListReturnsEmbeddedDocs(t *testing.T) {
	names := List()
	if len(names) == 0 {
		t.Fatal("List() returned no docs; embed glob may be wrong")
	}
	wantSome := map[string]bool{
		"README.md":      false,
		"extensions.md":  false,
		"commands.md":    false,
		"providers.md":   false,
		"models.md":      false,
		"piglets.md":     false,
		"config.md":      false,
		"divergences.md": false,
	}
	for _, n := range names {
		if _, ok := wantSome[n]; ok {
			wantSome[n] = true
		}
	}
	for n, seen := range wantSome {
		if !seen {
			t.Errorf("missing embedded doc: %s", n)
		}
	}
}

func TestExtensionsDocPreservesAsyncTranslationContract(t *testing.T) {
	body, err := Read("extensions.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("TypeScript async is a contract, not a goroutine instruction"),
		[]byte("already dispatches every inbound handler on its own goroutine"),
		[]byte("request-scoped `sdk.Context`"),
		[]byte("await Promise.all"),
		[]byte("Never use a naked fire-and-forget goroutine"),
	} {
		if !bytes.Contains(body, required) {
			t.Errorf("extensions.md missing async guidance %q", required)
		}
	}
}

func TestReadRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"", "../etc/passwd", "/etc/passwd", "foo/bar.md", "..\\evil.md"} {
		if _, err := Read(bad); err == nil {
			t.Errorf("Read(%q) should reject", bad)
		}
	}
}

func TestSyncWritesAllDocsWithMarker(t *testing.T) {
	root := t.TempDir()
	dir := DocsDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	// A managed page that is not in the embedded set must be pruned. Use a name
	// that no embedded doc uses so the assertion tests pruning, not overwrite.
	stale := filepath.Join(dir, "removed-page.md")
	if err := os.WriteFile(stale, []byte("stale managed page"), 0o644); err != nil {
		t.Fatalf("write stale page: %v", err)
	}
	written, err := Sync(root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("Sync wrote no files")
	}
	for _, name := range written {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale managed page was not pruned: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	digest, err := contentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(bytes.TrimSpace(marker)); got != digest {
		t.Fatalf("marker = %q, want %q", got, digest)
	}
}

func TestEnsureSyncedReplacesOlderProfileDocs(t *testing.T) {
	root := t.TempDir()
	dir := DocsDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, markerFile), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.md"), []byte("pig --profile old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSynced(root); err != nil {
		t.Fatalf("EnsureSynced: %v", err)
	}
	install, err := os.ReadFile(filepath.Join(dir, "install.md"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(install, []byte("pig --profile")) || !bytes.Contains(install, []byte("pig --piglet")) {
		t.Fatalf("install docs were not upgraded to Piglet commands:\n%s", install)
	}
	version, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes.TrimSpace(version)) != digest {
		t.Fatalf("marker = %q, want %q", version, digest)
	}
}

func TestEnsureSyncedIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSynced(root); err != nil {
		t.Fatalf("first EnsureSynced: %v", err)
	}
	// Mutate a synced file: EnsureSynced must not rewrite it because
	// the marker is current.
	target := filepath.Join(DocsDir(root), "README.md")
	if err := os.WriteFile(target, []byte("user edit"), 0o644); err != nil {
		t.Fatalf("override: %v", err)
	}
	if err := EnsureSynced(root); err != nil {
		t.Fatalf("second EnsureSynced: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "user edit" {
		t.Fatalf("EnsureSynced overwrote up-to-date docs; got %q", got)
	}
}

func TestRunCommand_PathAndList(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := RunCommand([]string{"docs", "path"}, &stdout, &stderr); code != 0 {
		t.Fatalf("path: code=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("docs")) {
		t.Fatalf("path output should contain docs dir: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunCommand([]string{"docs", "list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list: code=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("extensions.md")) {
		t.Fatalf("list missing extensions.md: %q", stdout.String())
	}
}

func TestRunCommand_DefaultSyncs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	var stdout, stderr bytes.Buffer
	if code := RunCommand([]string{"docs"}, &stdout, &stderr); code != 0 {
		t.Fatalf("docs: code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "README.md")); err != nil {
		t.Fatalf("README not synced: %v", err)
	}
}

func TestRunCommand_RejectsForeignArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunCommand([]string{"login"}, &stdout, &stderr); code != -1 {
		t.Fatalf("non-docs args should return -1, got %d", code)
	}
}
