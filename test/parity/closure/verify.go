package closure

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
)

// VerifyRequestIntoStore autonomously produces attested evidence for a
// verification-only obligation set and folds it into the store, returning the
// evidence run and the re-derived verdict of every obligation the request
// covers.
//
// It runs the bound EvidenceRequest against the canonical tree, commits and
// attests the resulting evidence, appends the attestation records to the store,
// and rebuilds it so the derived verdicts reflect the new proof. This is the
// non-authoring path: no translator patch is proposed, so it applies only to
// behaviors pig already matches. It cannot fabricate proof: the returned
// verdicts are re-derived from the attested records, so a failing run yields a
// contradicted or open verdict, never proven.
func VerifyRequestIntoStore(ctx context.Context, root, storePath, requestID, evidenceDir, message string) (*EvidenceRun, []Verdict, error) {
	graph, err := loadStoreGraph(ctx, storePath)
	if err != nil {
		return nil, nil, err
	}
	request, ok := graph.Records[requestID].(*EvidenceRequest)
	if !ok {
		return nil, nil, fmt.Errorf("evidence request %s not found in store", requestID)
	}
	if err := prepareWitnessArtifactDirs(root, request); err != nil {
		return nil, nil, err
	}
	run, err := ExecuteRequest(ctx, storePath, root, requestID, evidenceDir)
	if err != nil {
		return nil, nil, err
	}
	attested, err := AttestEvidenceRun(ctx, root, evidenceDir, message)
	if err != nil {
		return nil, nil, err
	}
	combined := make([]Record, 0, len(graph.Records)+len(attested))
	for _, id := range sortedRecordIDs(graph.Records) {
		combined = append(combined, graph.Records[id])
	}
	combined = append(combined, attested...)
	rebuilt, err := Build(combined)
	if err != nil {
		return nil, nil, err
	}
	if err := RebuildStore(ctx, storePath, rebuilt); err != nil {
		return nil, nil, err
	}
	obligationIDs := slices.Clone(request.ObligationIDs)
	slices.Sort(obligationIDs)
	verdicts := make([]Verdict, 0, len(obligationIDs))
	for _, obligationID := range obligationIDs {
		verdicts = append(verdicts, rebuilt.Verdicts[obligationID])
	}
	return run, verdicts, nil
}

// prepareWitnessArtifactDirs creates the parent directories of witness artifact
// paths before evidence commands run, so a command writing a coverage profile
// under tmp/closure/evidence-work/ succeeds. requireCanonicalTree runs before
// command execution and rejects untracked files, but tmp/ is gitignored, so
// creating these directories does not dirty the canonical tree.
func prepareWitnessArtifactDirs(root string, request *EvidenceRequest) error {
	for _, witness := range request.Witnesses {
		if witness.ArtifactPath == "" {
			continue
		}
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(witness.ArtifactPath)))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("prepare witness artifact %s: %w", witness.ArtifactPath, err)
		}
	}
	return nil
}

// AttestEvidenceRun commits the evidence output directory produced by
// ExecuteRequest and loads its attestation, returning the attestation records
// (evidence run, witnesses, attestation) ready to fold into a store. Committing
// is required and not incidental: LoadEvidenceAttestation only trusts evidence
// whose artifacts are tracked and unchanged in the repository, so attestation is
// exactly the act of committing the captured evidence.
func AttestEvidenceRun(ctx context.Context, root, evidenceDir, message string) ([]Record, error) {
	if _, err := repositoryGitOutput(ctx, root, "add", "--", evidenceDir); err != nil {
		return nil, err
	}
	if _, err := repositoryGitOutput(ctx, root, "commit", "-m", message, "--", evidenceDir); err != nil {
		return nil, err
	}
	return LoadEvidenceAttestation(ctx, root, path.Join(evidenceDir, "evidence.jsonl"))
}
