package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanCandidatesFindsNamedVersionFieldsAndSkipsIdentityFreeFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "shape.go")
	data := `package shape

const CurrentAPIVersion = "pig.dev/" + "v1"

type Document struct {
	Version int ` + "`json:\"version\"`" + `
	Schema int ` + "`json:\"schemaVersion,omitempty\"`" + `
	APIVersion string
	Name string ` + "`json:\"name\"`" + `
}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates, err := scanCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 4 {
		t.Fatalf("candidates = %+v", candidates)
	}
	ids := candidates[0].ID + candidates[1].ID + candidates[2].ID + candidates[3].ID
	for _, want := range []string{"Document.Version", "Document.Schema", "Document.APIVersion:go:-", "const.CurrentAPIVersion"} {
		if !strings.Contains(ids, want) {
			t.Fatalf("candidate IDs missing %q: %+v", want, candidates)
		}
	}
}

func TestScanRemovedMarkersRejectsFormerPigFormats(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "piglet.yaml"), []byte("version: 1\nname: old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.go"), []byte(`package old
const manifest = "pig.dev/`+`v1"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := scanRemovedMarkers(root, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version: 1", "pig.dev/" + "v1"} {
		if !containsProblem(problems, want) {
			t.Fatalf("problems missing %q: %v", want, problems)
		}
	}
}

func TestScanRemovedMarkersAcceptsIdentityVersions(t *testing.T) {
	root := t.TempDir()
	data := []byte("release:\n  version: 1.2.0\nruntimeVersion: go1.26\n")
	if err := os.WriteFile(filepath.Join(root, "release.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := scanRemovedMarkers(root, []string{"."})
	if err != nil || len(problems) != 0 {
		t.Fatalf("problems = %v, error = %v", problems, err)
	}
}

func TestValidateFailsClosedOnMissingStaleAndStrictPigFormat(t *testing.T) {
	current := candidate{
		ID: "shape.go#Document.Version:json:version", Path: "shape.go",
		Owner: "Document", Field: "Version", WireName: "version",
	}
	if problems := validate([]candidate{current}, manifest{}, false); !containsProblem(problems, "unclassified version-like field") {
		t.Fatalf("missing disposition problems = %v", problems)
	}

	reviewed := manifest{Fields: []fieldDisposition{{
		ID: current.ID, Path: current.Path, Owner: current.Owner,
		Field: current.Field, WireName: current.WireName,
		Classification: "pig-format", Rationale: "remove before strict format-policy acceptance",
	}}}
	if problems := validate([]candidate{current}, reviewed, false); len(problems) != 0 {
		t.Fatalf("non-strict tracked debt problems = %v", problems)
	}
	if problems := validate([]candidate{current}, reviewed, true); !containsProblem(problems, "remains a Pig-owned format discriminator") {
		t.Fatalf("strict problems = %v", problems)
	}

	reviewed.Fields[0].ID = "stale"
	if problems := validate([]candidate{current}, reviewed, false); !containsProblem(problems, "stale disposition") || !containsProblem(problems, "unclassified version-like field") {
		t.Fatalf("stale problems = %v", problems)
	}
}

func TestValidateRequiresClassificationRationaleAndExactIdentity(t *testing.T) {
	current := candidate{
		ID: "shape.go#Document.Version:json:version", Path: "shape.go",
		Owner: "Document", Field: "Version", WireName: "version",
	}
	reviewed := manifest{Fields: []fieldDisposition{{
		ID: current.ID, Path: current.Path, Owner: "Wrong",
		Field: current.Field, WireName: current.WireName,
		Classification: "pending",
	}}}
	problems := validate([]candidate{current}, reviewed, false)
	for _, want := range []string{"candidate identity drift", "invalid classification", "has no rationale"} {
		if !containsProblem(problems, want) {
			t.Fatalf("problems missing %q: %v", want, problems)
		}
	}
}

func containsProblem(problems []string, want string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return true
		}
	}
	return false
}
