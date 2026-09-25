package runtimecell

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// publishForIdentityTest publishes a real entry through PublishArtifact, so
// every case starts from production-valid ready metadata and changes one
// thing.
func publishForIdentityTest(t *testing.T, digest string) (string, EntryIdentity) {
	t.Helper()
	finalDir := filepath.Join(t.TempDir(), "cells", "go", digest)
	identity := EntryIdentity{InputDigest: digest, Artifact: "runner", Language: "go"}
	if _, err := PublishArtifact(context.Background(), finalDir, identity.Artifact, digest, identity.Language, func(scratch string) (string, error) {
		out := filepath.Join(scratch, "runner")
		return out, os.WriteFile(out, []byte("artifact"), 0o755)
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := validCellEntry(finalDir, identity); !ok {
		t.Fatal("freshly published entry is not valid for its own identity")
	}
	return finalDir, identity
}

func rewriteReady(t *testing.T, dir string, change func(*cellEntryMeta)) {
	t.Helper()
	meta, ok := readReadyMetadata(dir)
	if !ok {
		t.Fatal("published entry has no valid ready metadata")
	}
	change(&meta)
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cellReadyFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A cached artifact is adopted only when its ready metadata declares the input
// digest, artifact, language, and target the caller expects, and the artifact
// is a regular file inside the entry. Anything else is stale or foreign.
func TestPublishedEntryRejectsForeignIdentity(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string, want *EntryIdentity){
		"input digest": func(t *testing.T, dir string, _ *EntryIdentity) {
			rewriteReady(t, dir, func(m *cellEntryMeta) { m.InputDigest = "different-input-digest" })
		},
		"expected digest": func(_ *testing.T, _ string, want *EntryIdentity) { want.InputDigest = "another-cell" },
		"language":        func(_ *testing.T, _ string, want *EntryIdentity) { want.Language = "rust" },
		"artifact name":   func(_ *testing.T, _ string, want *EntryIdentity) { want.Artifact = "bin" },
		"target": func(t *testing.T, dir string, _ *EntryIdentity) {
			rewriteReady(t, dir, func(m *cellEntryMeta) { m.Target = "plan9/mips" })
		},
		"artifact digest": func(t *testing.T, dir string, _ *EntryIdentity) {
			rewriteReady(t, dir, func(m *cellEntryMeta) { m.ArtifactDigest = "" })
		},
		"artifact outside entry": func(t *testing.T, dir string, want *EntryIdentity) {
			outside := filepath.Join(filepath.Dir(dir), "outside")
			if err := os.WriteFile(outside, []byte("artifact"), 0o755); err != nil {
				t.Fatal(err)
			}
			rewriteReady(t, dir, func(m *cellEntryMeta) { m.Artifact = "../outside" })
			want.Artifact = "../outside"
		},
		"symlinked artifact": func(t *testing.T, dir string, _ *EntryIdentity) {
			foreign := filepath.Join(t.TempDir(), "foreign")
			if err := os.WriteFile(foreign, []byte("artifact"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "runner")); err != nil {
				t.Fatal(err)
			}
			testenv.Symlink(t, foreign, filepath.Join(dir, "runner"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir, want := publishForIdentityTest(t, "expected-input-digest")
			mutate(t, dir, &want)
			if art, ok := validCellEntry(dir, want); ok {
				t.Fatalf("adopted %s despite a mismatched %s", art, name)
			}
		})
	}
}

// PublishArtifact must rebuild, not reuse, an entry whose metadata declares a
// different input identity.
func TestPublishArtifactRebuildsForeignEntry(t *testing.T) {
	dir, _ := publishForIdentityTest(t, "expected-input-digest")
	rewriteReady(t, dir, func(m *cellEntryMeta) { m.InputDigest = "different-input-digest" })
	built := false
	entry, err := PublishArtifact(context.Background(), dir, "runner", "expected-input-digest", "go", func(scratch string) (string, error) {
		built = true
		out := filepath.Join(scratch, "runner")
		return out, os.WriteFile(out, []byte("fresh"), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !built || entry.Reused {
		t.Fatalf("foreign entry was reused: built=%v entry=%+v", built, entry)
	}
}
