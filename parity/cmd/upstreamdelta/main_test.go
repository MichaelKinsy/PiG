package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestComputeDelta(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "from")
	to := filepath.Join(root, "to")
	for _, versionRoot := range []string{from, to} {
		for _, trackedRoot := range trackedRoots {
			if err := os.MkdirAll(filepath.Join(versionRoot, trackedRoot), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeTestFile(t, from, "packages/ai/src/modified.ts", "old")
	writeTestFile(t, to, "packages/ai/src/modified.ts", "new")
	writeTestFile(t, from, "packages/agent/src/removed.ts", "removed")
	writeTestFile(t, to, "packages/tui/src/added.tsx", "added")
	writeTestFile(t, from, "packages/coding-agent/src/ignored.test.ts", "old")
	writeTestFile(t, to, "packages/coding-agent/src/ignored.test.ts", "new")

	got, err := computeDelta(from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []sourceDelta{
		{Path: "packages/agent/src/removed.ts", Change: "removed"},
		{Path: "packages/ai/src/modified.ts", Change: "modified"},
		{Path: "packages/tui/src/added.tsx", Change: "added"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("computeDelta() = %#v, want %#v", got, want)
	}
}

func TestValidateManifestRejectsSilentGapsAndPendingWork(t *testing.T) {
	deltas := []sourceDelta{
		{Path: "packages/ai/src/added.ts", Change: "added"},
		{Path: "packages/ai/src/changed.ts", Change: "modified"},
	}
	manifest := syncManifest{
		From: "1.0.0",
		To:   "1.1.0",
		Files: []fileAudit{
			{Path: "packages/ai/src/added.ts", Change: "added", Disposition: "pending"},
		},
	}
	problems := validateManifest(manifest, deltas, t.TempDir())
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"packages/ai/src/added.ts remains pending", "missing file audit packages/ai/src/changed.ts"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("problems %q do not contain %q", joined, want)
		}
	}
}

func TestValidateManifestAcceptsDurableEvidenceAndExplicitExclusion(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "parity/scenarios/api.toml", "name = 'api'\ncovers = ['packages/ai/src/ported.ts']")
	deltas := []sourceDelta{
		{Path: "packages/ai/src/ported.ts", Change: "modified"},
		{Path: "packages/ai/src/types-only.ts", Change: "added"},
	}
	manifest := syncManifest{
		From: "1.0.0",
		To:   "1.1.0",
		Files: []fileAudit{
			{Path: "packages/ai/src/ported.ts", Change: "modified", Disposition: "ported", Evidence: []string{"scenario:parity/scenarios/api.toml"}, Rationale: "The scenario asserts the changed API response."},
			{Path: "packages/ai/src/types-only.ts", Change: "added", Disposition: "designed-out", Rationale: "TypeScript-only compile-time helper"},
		},
	}
	if problems := validateManifest(manifest, deltas, repo); len(problems) != 0 {
		t.Fatalf("validateManifest() problems = %v", problems)
	}
}

func TestValidateManifestRejectsPortedEvidenceWithoutDeltaRationale(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "parity/scenarios/api.toml", "name = 'api'\ncovers = ['packages/ai/src/ported.ts']")
	manifest := syncManifest{
		From: "1.0.0",
		To:   "1.1.0",
		Files: []fileAudit{{
			Path:        "packages/ai/src/ported.ts",
			Change:      "modified",
			Disposition: "ported",
			Evidence:    []string{"scenario:parity/scenarios/api.toml"},
		}},
	}
	problems := strings.Join(validateManifest(manifest, []sourceDelta{{Path: "packages/ai/src/ported.ts", Change: "modified"}}, repo), "\n")
	if !strings.Contains(problems, "requires a delta-specific evidence rationale") {
		t.Fatalf("validateManifest() problems = %q", problems)
	}
}

func TestValidateManifestRejectsScenarioThatDoesNotCoverAuditedPath(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "parity/scenarios/api.toml", "name = 'api'\ncovers = ['packages/ai/src/other.ts']")
	deltas := []sourceDelta{{Path: "packages/ai/src/ported.ts", Change: "modified"}}
	manifest := syncManifest{
		From: "1.0.0",
		To:   "1.1.0",
		Files: []fileAudit{{
			Path:        "packages/ai/src/ported.ts",
			Change:      "modified",
			Disposition: "ported",
			Evidence:    []string{"scenario:parity/scenarios/api.toml"},
		}},
	}
	problems := strings.Join(validateManifest(manifest, deltas, repo), "\n")
	if !strings.Contains(problems, "does not cover the audited path") {
		t.Fatalf("validateManifest() problems = %q", problems)
	}
}

func writeTestFile(t *testing.T, root, relativePath, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
