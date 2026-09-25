package closure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

// AppliedPatch records the disposable candidate checkout a confined Translator
// patch was applied to, plus the exact changed paths and their resulting content
// hashes. The caller runs evidence against CheckoutDir and owns discarding it;
// the canonical tree is never modified.
type AppliedPatch struct {
	CheckoutDir  string
	ChangedPaths []string
	ResultHashes map[string]string
}

// ApplyTranslatorPatch integrates a Translator patch one serial step: it rejects
// a stale lease base, confines the patch to the leased work unit, clones the
// canonical commit into a disposable checkout, and applies each full-file
// replacement guarded by its original-content hash. It fails closed if the patch
// touches any path outside the declared edits.
func ApplyTranslatorPatch(ctx context.Context, root, canonicalCommit string, current *Graph, lease Lease, units map[string]WorkUnit, bundle *correspondence.TranslatorBundle) (*AppliedPatch, error) {
	if err := IntegrateLease(current, lease, units); err != nil {
		return nil, err
	}
	unit := units[lease.WorkUnitID]
	if err := ConfinePatchToLease(lease, unit, bundle); err != nil {
		return nil, err
	}
	edits := collectTranslationEdits(bundle)
	if len(edits) == 0 {
		return nil, errors.New("apply patch: bundle has no edits")
	}
	edits, err := dedupTranslationEdits(edits)
	if err != nil {
		return nil, err
	}
	checkout, err := createMutationCheckout(ctx, root, canonicalCommit)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(checkout)
		}
	}()

	resultHashes := make(map[string]string, len(edits))
	changed := make([]string, 0, len(edits))
	for _, edit := range edits {
		hash, applyErr := applyTranslationEdit(checkout, edit)
		if applyErr != nil {
			return nil, applyErr
		}
		resultHashes[edit.Path] = hash
		changed = append(changed, edit.Path)
	}
	slices.Sort(changed)
	changed = slices.Compact(changed)

	command := exec.CommandContext(ctx, "git", "diff", "--name-only", "--no-ext-diff", "-z")
	command.Dir = checkout
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	touched := splitNulPaths(output)
	if !slices.Equal(touched, changed) {
		return nil, fmt.Errorf("apply patch: touched files %v exceed declared edits %v", touched, changed)
	}
	if err := ConfineFactDiffToUnit(current, unit, changed); err != nil {
		return nil, err
	}
	cleanup = false
	return &AppliedPatch{CheckoutDir: checkout, ChangedPaths: changed, ResultHashes: resultHashes}, nil
}

func collectTranslationEdits(bundle *correspondence.TranslatorBundle) []correspondence.TranslationEdit {
	var edits []correspondence.TranslationEdit
	for _, translation := range bundle.Translations {
		edits = append(edits, translation.ProductionEdits...)
		edits = append(edits, translation.TestEdits...)
	}
	return edits
}

// ConfineFactDiffToUnit rejects an applied patch whose semantic footprint exceeds
// the leased work unit. The footprint of a changed path is the set of semantic
// boundaries owned by every behavior whose mapped production target hosts a pin
// at that path; each such boundary must belong to the unit's own boundary set. A
// patch that stays inside its declared write paths can still change a file that
// also hosts another behavior's mapped target, and this guard catches that
// escape where the purely textual path confinement cannot.
func ConfineFactDiffToUnit(graph *Graph, unit WorkUnit, changedPaths []string) error {
	if graph == nil {
		return errors.New("confine fact diff: nil graph")
	}
	allowed := make(map[string]struct{}, len(unit.SemanticBoundaryIDs))
	for _, boundary := range unit.SemanticBoundaryIDs {
		allowed[boundary] = struct{}{}
	}
	for _, path := range changedPaths {
		for _, boundary := range graph.footprintBoundaries(path) {
			if _, ok := allowed[boundary]; !ok {
				return fmt.Errorf("confine fact diff: changed path %s touches semantic boundary %s outside work unit %s", path, boundary, unit.ID)
			}
		}
	}
	return nil
}

// footprintBoundaries returns the deterministic set of semantic boundaries a
// change to path implicates: for every production pin at that path that a mapped
// target hosts, the origin-pin semantic IDs of the behaviors that map it.
func (g *Graph) footprintBoundaries(path string) []string {
	boundaries := newStringSet()
	for _, pinID := range g.recordIDs(KindPin) {
		pin := g.Records[pinID].(*Pin)
		if pin.Repository != "pig" || pin.Path != path {
			continue
		}
		for _, targetID := range g.recordIDs(KindTarget) {
			target := g.Records[targetID].(*Target)
			if !slices.Contains(target.PinIDs, pinID) {
				continue
			}
			for _, mappingID := range g.recordIDs(KindMapping) {
				mapping := g.Records[mappingID].(*Mapping)
				if !slices.Contains(mapping.TargetIDs, targetID) {
					continue
				}
				behavior, ok := g.Records[mapping.BehaviorID].(*Behavior)
				if !ok {
					continue
				}
				for _, originPinID := range behavior.OriginPinIDs {
					if origin, ok := g.Records[originPinID].(*Pin); ok && origin.SemanticID != "" {
						boundaries.add(origin.SemanticID)
					}
				}
			}
		}
	}
	return boundaries.sorted()
}

// dedupTranslationEdits collapses byte-identical edits to the same generated path
// to a single edit and fails a genuine collision. A campaign bundle may carry the
// same regenerated output from more than one translation; identical copies are
// deduplicated, while two edits that disagree on original or replacement content
// are a real conflict and are rejected.
func dedupTranslationEdits(edits []correspondence.TranslationEdit) ([]correspondence.TranslationEdit, error) {
	byPath := make(map[string]correspondence.TranslationEdit, len(edits))
	order := make([]string, 0, len(edits))
	for _, edit := range edits {
		existing, seen := byPath[edit.Path]
		if !seen {
			byPath[edit.Path] = edit
			order = append(order, edit.Path)
			continue
		}
		if existing.OriginalHash != edit.OriginalHash || existing.Replacement != edit.Replacement {
			return nil, fmt.Errorf("dedup edits: conflicting edits to generated path %s", edit.Path)
		}
	}
	slices.Sort(order)
	deduped := make([]correspondence.TranslationEdit, 0, len(order))
	for _, path := range order {
		deduped = append(deduped, byPath[path])
	}
	return deduped, nil
}

// applyTranslationEdit rewrites one file with the edit's full replacement after
// proving the on-disk content matches the edit's declared original hash, so a
// patch built against a different base is rejected rather than silently applied.
func applyTranslationEdit(root string, edit correspondence.TranslationEdit) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(edit.Path))
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("apply patch: %s is not a regular file", edit.Path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if HashBytes(data) != edit.OriginalHash {
		return "", fmt.Errorf("apply patch: %s differs from its declared original content", edit.Path)
	}
	replacement := []byte(edit.Replacement)
	if bytes.Equal(replacement, data) {
		return "", fmt.Errorf("apply patch: %s replacement is identical to the original", edit.Path)
	}
	if err := os.WriteFile(path, replacement, info.Mode().Perm()); err != nil {
		return "", err
	}
	return HashBytes(replacement), nil
}

// CandidatePatchProof is the red→green evidence that a Translator patch is
// load-bearing: the bound test fails on the unpatched canonical checkout and
// passes on the patched candidate. It is the pre-integration gate that turns an
// applied patch into a proven one before serial integration runs the attested
// canonical evidence.
type CandidatePatchProof struct {
	BoundTest string
	Command   EvidenceCommand
	Baseline  EvidenceCommandResult
	Patched   EvidenceCommandResult
}

// ProveCandidatePatch runs the bound test command on a clean checkout of the
// canonical commit and on the patched candidate, and requires that the test
// fails on canonical and passes on the candidate. A patch whose bound test
// already passes on canonical is rejected as not load-bearing, and a patch that
// does not turn the test green is rejected. A harness error, timeout, or
// overflow on either run fails closed.
func ProveCandidatePatch(ctx context.Context, root, canonicalCommit string, applied *AppliedPatch, command EvidenceCommand, boundTest string, timeoutSeconds int) (*CandidatePatchProof, error) {
	if applied == nil {
		return nil, errors.New("candidate proof: nil applied patch")
	}
	if strings.TrimSpace(boundTest) == "" {
		return nil, errors.New("candidate proof: empty bound test")
	}
	environment, err := evidenceEnvironment(nil)
	if err != nil {
		return nil, err
	}
	commandHash, err := evidenceCommandHash(0, 0, command)
	if err != nil {
		return nil, err
	}

	baselineCheckout, err := createMutationCheckout(ctx, root, canonicalCommit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(baselineCheckout) }()
	baseline, baselineStdout, _ := executeEvidenceCommand(ctx, baselineCheckout, timeoutSeconds, environment, 0, 0, command, commandHash)
	if baseline.Error != "" || baseline.Overflow {
		return nil, fmt.Errorf("candidate proof: baseline harness error on %s", boundTest)
	}
	if goTestPassed(baselineStdout, boundTest) {
		return nil, fmt.Errorf("candidate proof: bound test %s already passes on canonical; patch is not load-bearing", boundTest)
	}

	patched, patchedStdout, _ := executeEvidenceCommand(ctx, applied.CheckoutDir, timeoutSeconds, environment, 0, 0, command, commandHash)
	if patched.Error != "" || patched.Overflow {
		return nil, fmt.Errorf("candidate proof: patched harness error on %s", boundTest)
	}
	if !goTestPassed(patchedStdout, boundTest) {
		return nil, fmt.Errorf("candidate proof: patch did not turn bound test %s green", boundTest)
	}
	return &CandidatePatchProof{BoundTest: boundTest, Command: command, Baseline: baseline, Patched: patched}, nil
}

func splitNulPaths(output []byte) []string {
	trimmed := bytes.TrimSuffix(output, []byte{0})
	if len(trimmed) == 0 {
		return nil
	}
	parts := strings.Split(string(trimmed), "\x00")
	slices.Sort(parts)
	return slices.Compact(parts)
}
