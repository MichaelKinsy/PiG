package closure

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

func initializeIntegratorRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/integrate\n\ngo 1.26\n",
		"prod.go":      "package prod\n\nfunc Value() int { return 1 }\n",
		"prod_test.go": "package prod\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 2 {\n\t\tt.Fatal(\"want 2\")\n\t}\n}\n",
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitOutput(t, root, "init")
	gitOutput(t, root, "config", "user.email", "integrator@example.invalid")
	gitOutput(t, root, "config", "user.name", "Integrator Test")
	gitOutput(t, root, "add", "go.mod", "prod.go", "prod_test.go")
	gitOutput(t, root, "commit", "-m", "fixture")
	commit := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	return root, commit
}

func integratorUnitAndLease(root string) (WorkUnit, map[string]WorkUnit, Lease, *Graph) {
	unit := WorkUnit{
		ID: "work-unit:prod", SnapshotID: "snapshot:integrate", BehaviorID: "behavior:prod",
		WritePaths: []string{"prod.go"}, TestPaths: []string{"prod_test.go"},
	}
	units := map[string]WorkUnit{unit.ID: unit}
	graph := &Graph{}
	base, _ := WorkUnitBaseFingerprint(graph, unit)
	lease := Lease{ID: "lease:prod", WorkUnitID: unit.ID, SnapshotID: unit.SnapshotID, Holder: "worker-1", BaseFingerprint: base}
	return unit, units, lease, graph
}

func fileHash(t *testing.T, root, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return HashBytes(data)
}

// prodPatch replaces prod.go so Value() returns 2, aligning it with the test.
func prodPatch(originalHash string) *correspondence.TranslatorBundle {
	return &correspondence.TranslatorBundle{
		Role: correspondence.TranslatorRole, PacketID: "packet:prod",
		Translations: []correspondence.TranslationProposal{{
			QuestionID: "q",
			ProductionEdits: []correspondence.TranslationEdit{{
				Path: "prod.go", OriginalHash: originalHash, Replacement: "package prod\n\nfunc Value() int { return 2 }\n",
			}},
		}},
	}
}

func TestApplyTranslatorPatchAppliesInIsolationAndLeavesCanonicalTreeClean(t *testing.T) {
	root, commit := initializeIntegratorRepository(t)
	_, units, lease, graph := integratorUnitAndLease(root)
	bundle := prodPatch(fileHash(t, root, "prod.go"))

	applied, err := ApplyTranslatorPatch(t.Context(), root, commit, graph, lease, units, bundle)
	if err != nil {
		t.Fatalf("ApplyTranslatorPatch() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(applied.CheckoutDir) })

	if got := []string{"prod.go"}; len(applied.ChangedPaths) != 1 || applied.ChangedPaths[0] != got[0] {
		t.Fatalf("changed paths = %v", applied.ChangedPaths)
	}
	// The isolated checkout carries the fix.
	if data, _ := os.ReadFile(filepath.Join(applied.CheckoutDir, "prod.go")); !strings.Contains(string(data), "return 2") {
		t.Fatalf("checkout prod.go not patched: %s", data)
	}
	// The canonical tree is untouched.
	if data, _ := os.ReadFile(filepath.Join(root, "prod.go")); !strings.Contains(string(data), "return 1") {
		t.Fatalf("canonical prod.go was modified: %s", data)
	}
	if status := strings.TrimSpace(gitOutput(t, root, "status", "--porcelain")); status != "" {
		t.Fatalf("canonical tree dirty: %s", status)
	}
}

func TestApplyTranslatorPatchRejectsStaleOriginalHash(t *testing.T) {
	root, commit := initializeIntegratorRepository(t)
	_, units, lease, graph := integratorUnitAndLease(root)
	bundle := prodPatch(HashBytes([]byte("not the real original")))
	if _, err := ApplyTranslatorPatch(t.Context(), root, commit, graph, lease, units, bundle); err == nil || !strings.Contains(err.Error(), "differs from its declared original") {
		t.Fatalf("stale original error = %v", err)
	}
	if status := strings.TrimSpace(gitOutput(t, root, "status", "--porcelain")); status != "" {
		t.Fatalf("canonical tree dirty after rejected apply: %s", status)
	}
}

func TestApplyTranslatorPatchRejectsOutOfLeaseEdit(t *testing.T) {
	root, commit := initializeIntegratorRepository(t)
	_, units, lease, graph := integratorUnitAndLease(root)
	bundle := prodPatch(fileHash(t, root, "prod.go"))
	// Add a production edit outside the leased write set.
	bundle.Translations[0].ProductionEdits = append(bundle.Translations[0].ProductionEdits, correspondence.TranslationEdit{
		Path: "go.mod", OriginalHash: fileHash(t, root, "go.mod"), Replacement: "module x\n",
	})
	if _, err := ApplyTranslatorPatch(t.Context(), root, commit, graph, lease, units, bundle); err == nil || !strings.Contains(err.Error(), "go.mod is outside") {
		t.Fatalf("out-of-lease edit error = %v", err)
	}
}

func TestApplyTranslatorPatchRejectsStaleLease(t *testing.T) {
	root, commit := initializeIntegratorRepository(t)
	unit, units, lease, graph := integratorUnitAndLease(root)
	lease.BaseFingerprint = HashBytes([]byte("some other base"))
	_ = unit
	bundle := prodPatch(fileHash(t, root, "prod.go"))
	if _, err := ApplyTranslatorPatch(t.Context(), root, commit, graph, lease, units, bundle); err == nil || !strings.Contains(err.Error(), "base is stale") {
		t.Fatalf("stale lease error = %v", err)
	}
}

func boundTestCommand() EvidenceCommand {
	return EvidenceCommand{Name: "go", Args: []string{"test", "-json", "-run", "^TestValue$", "./..."}}
}

func TestProveCandidatePatchRequiresRedThenGreen(t *testing.T) {
	root, commit := initializeIntegratorRepository(t)
	_, units, lease, graph := integratorUnitAndLease(root)
	bundle := prodPatch(fileHash(t, root, "prod.go"))
	applied, err := ApplyTranslatorPatch(t.Context(), root, commit, graph, lease, units, bundle)
	if err != nil {
		t.Fatalf("ApplyTranslatorPatch() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(applied.CheckoutDir) })

	proof, err := ProveCandidatePatch(t.Context(), root, commit, applied, boundTestCommand(), "TestValue", 120)
	if err != nil {
		t.Fatalf("ProveCandidatePatch() = %v", err)
	}
	if proof.Baseline.ExitCode == 0 {
		t.Fatalf("baseline exit code = 0, want failure on canonical")
	}
	if proof.Patched.ExitCode != 0 {
		t.Fatalf("patched exit code = %d, want pass", proof.Patched.ExitCode)
	}
}

func TestProveCandidatePatchRejectsAlreadyGreenBound(t *testing.T) {
	// A fixture whose bound test already passes on canonical: the patch is not
	// load-bearing and must be rejected.
	root := t.TempDir()
	for path, content := range map[string]string{
		"go.mod":       "module example.com/green\n\ngo 1.26\n",
		"prod.go":      "package prod\n\nfunc Value() int { return 2 }\n",
		"prod_test.go": "package prod\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 2 {\n\t\tt.Fatal(\"want 2\")\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitOutput(t, root, "init")
	gitOutput(t, root, "config", "user.email", "g@example.invalid")
	gitOutput(t, root, "config", "user.name", "G")
	gitOutput(t, root, "add", "go.mod", "prod.go", "prod_test.go")
	gitOutput(t, root, "commit", "-m", "fixture")
	commit := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))

	unit := WorkUnit{ID: "work-unit:prod", SnapshotID: "snapshot:g", BehaviorID: "behavior:prod", WritePaths: []string{"prod.go"}, TestPaths: []string{"prod_test.go"}}
	units := map[string]WorkUnit{unit.ID: unit}
	graph := &Graph{}
	base, _ := WorkUnitBaseFingerprint(graph, unit)
	lease := Lease{ID: "lease:prod", WorkUnitID: unit.ID, SnapshotID: unit.SnapshotID, Holder: "w", BaseFingerprint: base}
	bundle := &correspondence.TranslatorBundle{Role: correspondence.TranslatorRole, PacketID: "p", Translations: []correspondence.TranslationProposal{{
		QuestionID: "q", ProductionEdits: []correspondence.TranslationEdit{{Path: "prod.go", OriginalHash: fileHash(t, root, "prod.go"), Replacement: "package prod\n\nfunc Value() int { return 3 }\n"}},
	}}}
	applied, err := ApplyTranslatorPatch(t.Context(), root, commit, graph, lease, units, bundle)
	if err != nil {
		t.Fatalf("ApplyTranslatorPatch() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(applied.CheckoutDir) })

	if _, err := ProveCandidatePatch(t.Context(), root, commit, applied, boundTestCommand(), "TestValue", 120); err == nil || !strings.Contains(err.Error(), "not load-bearing") {
		t.Fatalf("already-green proof error = %v, want not load-bearing", err)
	}
}

func TestProveCandidatePatchRejectsNonGreenPatch(t *testing.T) {
	root, commit := initializeIntegratorRepository(t)
	_, units, lease, graph := integratorUnitAndLease(root)
	// Patch edits prod.go but leaves Value() returning 1, so the bound test stays red.
	bundle := &correspondence.TranslatorBundle{Role: correspondence.TranslatorRole, PacketID: "p", Translations: []correspondence.TranslationProposal{{
		QuestionID: "q", ProductionEdits: []correspondence.TranslationEdit{{Path: "prod.go", OriginalHash: fileHash(t, root, "prod.go"), Replacement: "package prod\n\n// unchanged behavior\nfunc Value() int { return 1 }\n"}},
	}}}
	applied, err := ApplyTranslatorPatch(t.Context(), root, commit, graph, lease, units, bundle)
	if err != nil {
		t.Fatalf("ApplyTranslatorPatch() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(applied.CheckoutDir) })

	if _, err := ProveCandidatePatch(t.Context(), root, commit, applied, boundTestCommand(), "TestValue", 120); err == nil || !strings.Contains(err.Error(), "did not turn bound test") {
		t.Fatalf("non-green patch proof error = %v, want did-not-turn-green", err)
	}
}

// initializeThreeBundleRepository builds a Go module with three independent
// functions, each with a bound test that fails until the function is fixed.
func initializeThreeBundleRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/three\n\ngo 1.26\n",
		"a.go":      "package prod\n\nfunc A() int { return 1 }\n",
		"b.go":      "package prod\n\nfunc B() int { return 1 }\n",
		"c.go":      "package prod\n\nfunc C() int { return 1 }\n",
		"a_test.go": "package prod\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 2 {\n\t\tt.Fatal(\"want 2\")\n\t}\n}\n",
		"b_test.go": "package prod\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tif B() != 2 {\n\t\tt.Fatal(\"want 2\")\n\t}\n}\n",
		"c_test.go": "package prod\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {\n\tif C() != 2 {\n\t\tt.Fatal(\"want 2\")\n\t}\n}\n",
	}
	names := make([]string, 0, len(files))
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, path)
	}
	gitOutput(t, root, "init")
	gitOutput(t, root, "config", "user.email", "three@example.invalid")
	gitOutput(t, root, "config", "user.name", "Three")
	gitOutput(t, root, append([]string{"add"}, names...)...)
	gitOutput(t, root, "commit", "-m", "fixture")
	return root, strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
}

func TestThreeConcurrentBundlesIntegrateWithoutHiddenOverlap(t *testing.T) {
	root, commit := initializeThreeBundleRepository(t)
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "a", "wire:a", "a.go")...)
	records = append(records, behaviorBundle(snapshot, "b", "wire:b", "b.go")...)
	records = append(records, behaviorBundle(snapshot, "c", "wire:c", "c.go")...)
	before, units := planUnits(t, records)

	type work struct {
		fn, boundTest, path, fixed string
	}
	items := []work{
		{"a", "TestA", "a.go", "package prod\n\nfunc A() int { return 2 }\n"},
		{"b", "TestB", "b.go", "package prod\n\nfunc B() int { return 2 }\n"},
		{"c", "TestC", "c.go", "package prod\n\nfunc C() int { return 2 }\n"},
	}

	// Grant three concurrent leases; disjoint work must all be admitted.
	var active []Lease
	leaseByFn := make(map[string]Lease, 3)
	unitByFn := make(map[string]WorkUnit, 3)
	for _, item := range items {
		unit := unitFor(t, units, "behavior:"+item.fn)
		lease, err := GrantLease(before, active, units, unit.ID, "worker-"+item.fn)
		if err != nil {
			t.Fatalf("GrantLease(%s) = %v", item.fn, err)
		}
		active = append(active, lease)
		leaseByFn[item.fn] = lease
		unitByFn[item.fn] = unit
	}

	// Each bundle applies in isolation and proves load-bearing red -> green.
	for _, item := range items {
		bundle := &correspondence.TranslatorBundle{Role: correspondence.TranslatorRole, PacketID: "packet:" + item.fn, Translations: []correspondence.TranslationProposal{{
			QuestionID: "q", ProductionEdits: []correspondence.TranslationEdit{{Path: item.path, OriginalHash: fileHash(t, root, item.path), Replacement: item.fixed}},
		}}}
		applied, err := ApplyTranslatorPatch(t.Context(), root, commit, before, leaseByFn[item.fn], units, bundle)
		if err != nil {
			t.Fatalf("ApplyTranslatorPatch(%s) = %v", item.fn, err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(applied.CheckoutDir) })
		command := EvidenceCommand{Name: "go", Args: []string{"test", "-json", "-run", "^" + item.boundTest + "$", "./..."}}
		if _, err := ProveCandidatePatch(t.Context(), root, commit, applied, command, item.boundTest, 120); err != nil {
			t.Fatalf("ProveCandidatePatch(%s) = %v", item.fn, err)
		}
	}

	// Serial integration: integrating one bundle advances only its own support,
	// so the other two leases are never staled and receive no delta briefs.
	// (Support accrual is simulated at the verdict layer; the evidence->verdict
	// wiring is a later slice. This test proves the concurrency/overlap safety.)
	for _, integrated := range items {
		if err := IntegrateLease(before, leaseByFn[integrated.fn], units); err != nil {
			t.Fatalf("IntegrateLease(%s) on matching base = %v", integrated.fn, err)
		}
		after, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		verdict := after.Verdicts["obligation:"+integrated.fn]
		verdict.SupportHashes = append(slices.Clone(verdict.SupportHashes), HashBytes([]byte(integrated.fn+"-integrated")))
		after.Verdicts["obligation:"+integrated.fn] = verdict

		var others []Lease
		for _, item := range items {
			if item.fn != integrated.fn {
				others = append(others, leaseByFn[item.fn])
			}
		}
		briefs, err := DeltaBriefs(before, after, others, units)
		if err != nil {
			t.Fatal(err)
		}
		if len(briefs) != 0 {
			t.Fatalf("integrating %s produced hidden-overlap delta briefs to disjoint leases: %#v", integrated.fn, briefs)
		}
	}
}
