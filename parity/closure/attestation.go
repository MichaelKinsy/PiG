package closure

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func LoadEvidenceAttestation(ctx context.Context, root, configured string) ([]Record, error) {
	path, relative, err := trackedEvidencePath(ctx, root, configured)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	records, decodeErr := DecodeJSONL(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, decodeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	var evidence *EvidenceRun
	var witnessIDs []string
	for _, record := range records {
		switch value := record.(type) {
		case *ExecutionWitness:
			witnessIDs = append(witnessIDs, value.ID)
		case *EvidenceRun:
			if evidence != nil {
				return nil, fmt.Errorf("evidence attestation %s contains multiple evidence runs", configured)
			}
			evidence = value
		default:
			return nil, fmt.Errorf("evidence attestation %s contains %s", configured, record.RecordKind())
		}
	}
	if evidence == nil {
		return nil, fmt.Errorf("evidence attestation %s contains no evidence run", configured)
	}
	slices.Sort(witnessIDs)
	if !slices.Equal(witnessIDs, evidence.WitnessIDs) {
		return nil, fmt.Errorf("evidence attestation %s witness set differs from evidence", configured)
	}
	directory := filepath.Dir(path)
	for _, artifact := range evidence.Artifacts {
		artifactPath := filepath.Join(directory, filepath.FromSlash(artifact.Path))
		if _, _, err := trackedEvidencePath(ctx, root, artifactPath); err != nil {
			return nil, fmt.Errorf("evidence artifact %s: %w", artifact.Path, err)
		}
		data, err := os.ReadFile(artifactPath)
		if err != nil {
			return nil, err
		}
		if HashBytes(data) != artifact.Hash {
			return nil, fmt.Errorf("evidence artifact %s hash differs", artifact.Path)
		}
	}
	for _, record := range records {
		if witness, ok := record.(*ExecutionWitness); ok {
			if err := verifyWitnessArtifactClaims(root, directory, witness); err != nil {
				return nil, fmt.Errorf("witness %s: %w", witness.ID, err)
			}
		}
	}
	commit, err := repositoryGitOutput(ctx, root, "log", "-1", "--format=%H", "--", relative)
	if err != nil {
		return nil, err
	}
	attestation := &EvidenceAttestation{
		Kind: KindEvidenceAttestation, EvidenceID: evidence.ID, WitnessIDs: slices.Clone(evidence.WitnessIDs),
		RepositoryCommit: commit, Path: relative, Artifacts: slices.Clone(evidence.Artifacts),
	}
	attestation.ID, err = evidenceAttestationContentID(attestation)
	if err != nil {
		return nil, err
	}
	return append(records, attestation), nil
}

func LoadMutationRun(ctx context.Context, root, configured string) ([]Record, error) {
	path, relative, err := trackedEvidencePath(ctx, root, configured)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	records, decodeErr := decodeStoredJSONL(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, decodeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(records) != 1 {
		return nil, fmt.Errorf("mutation run %s must contain exactly one record", configured)
	}
	run, ok := records[0].(*MutationRun)
	if !ok {
		return nil, fmt.Errorf("mutation run %s contains %s", configured, records[0].RecordKind())
	}
	if expectedID, contentErr := mutationRunContentID(run); contentErr != nil || run.ID != expectedID {
		return nil, fmt.Errorf("mutation run %s is not content-addressed", configured)
	}
	if len(run.Baselines) != len(run.Commands) {
		return nil, fmt.Errorf("mutation run %s baseline count differs from commands", configured)
	}
	directory := filepath.Dir(path)
	artifactData := make(map[string][]byte, len(run.Artifacts))
	for _, artifact := range run.Artifacts {
		artifactPath := filepath.Join(directory, filepath.FromSlash(artifact.Path))
		if _, _, err := trackedEvidencePath(ctx, root, artifactPath); err != nil {
			return nil, fmt.Errorf("mutation artifact %s: %w", artifact.Path, err)
		}
		data, err := os.ReadFile(artifactPath)
		if err != nil {
			return nil, err
		}
		if HashBytes(data) != artifact.Hash {
			return nil, fmt.Errorf("mutation artifact %s hash differs", artifact.Path)
		}
		artifactData[artifact.Path] = data
	}
	for _, result := range run.Results {
		if result.CommandIndex == 0 {
			continue
		}
		index := result.CommandIndex
		if index < 1 || index > len(run.Commands) {
			return nil, fmt.Errorf("mutation result %s command index is out of range", result.MutantID)
		}
		baselineStdout := artifactData[fmt.Sprintf("mutant-%03d-baseline.stdout", index)]
		baselineStderr := artifactData[fmt.Sprintf("mutant-%03d-baseline.stderr", index)]
		stdout := artifactData[fmt.Sprintf("mutant-%03d.stdout", index)]
		stderr := artifactData[fmt.Sprintf("mutant-%03d.stderr", index)]
		patch := artifactData[fmt.Sprintf("mutant-%03d.patch", index)]
		if baselineStdout == nil || baselineStderr == nil || stdout == nil || stderr == nil || patch == nil {
			return nil, fmt.Errorf("mutation result %s lacks complete artifacts", result.MutantID)
		}
		baseline := run.Baselines[index-1]
		command := run.Commands[index-1]
		if baseline.StdoutHash != HashBytes(baselineStdout) || baseline.StderrHash != HashBytes(baselineStderr) || command.StdoutHash != HashBytes(stdout) || command.StderrHash != HashBytes(stderr) {
			return nil, fmt.Errorf("mutation result %s stream hashes differ", result.MutantID)
		}
		if baseline.ExitCode != 0 || baseline.Error != "" || baseline.Overflow || !goTestPassed(baselineStdout, result.BoundTest) {
			return nil, fmt.Errorf("mutation result %s baseline did not execute the bound test", result.MutantID)
		}
		failureHash := mutationFailureHash(stdout)
		if result.FailureTest != "" {
			failureHash, err = goTestFailureHash(stdout, result.FailureTest)
		}
		if err != nil || failureHash != result.FailureHash {
			return nil, fmt.Errorf("mutation result %s failure differs from artifacts", result.MutantID)
		}
		if err := verifyMutationPatch(ctx, root, run.RepositoryCommit, patch, result.MutatedPath, result.MutatedHash); err != nil {
			return nil, fmt.Errorf("mutation result %s patch: %w", result.MutantID, err)
		}
	}
	commit, err := repositoryGitOutput(ctx, root, "log", "-1", "--format=%H", "--", relative)
	if err != nil {
		return nil, err
	}
	attestation := &MutationAttestation{Kind: KindMutationAttestation, RunID: run.ID, RepositoryCommit: commit, Path: relative, Artifacts: slices.Clone(run.Artifacts)}
	attestation.ID, err = mutationAttestationContentID(attestation)
	if err != nil {
		return nil, err
	}
	return []Record{run, attestation}, nil
}

func verifyMutationPatch(ctx context.Context, root, commit string, patch []byte, mutatedPath, mutatedHash string) error {
	checkout, err := createMutationCheckout(ctx, root, commit)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(checkout) }()
	command := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
	command.Dir = checkout
	command.Stdin = strings.NewReader(string(patch))
	if output, applyErr := command.CombinedOutput(); applyErr != nil {
		return fmt.Errorf("apply: %w: %s", applyErr, strings.TrimSpace(string(output)))
	}
	output, err := repositoryGitOutputBytes(ctx, checkout, "diff", "--name-only", "--no-ext-diff", "-z")
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSuffix(output, []byte{0})
	paths := bytes.Split(trimmed, []byte{0})
	if len(paths) != 1 || string(paths[0]) != mutatedPath {
		return fmt.Errorf("patch does not change only %s", mutatedPath)
	}
	data, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(mutatedPath)))
	if err != nil {
		return err
	}
	if HashBytes(data) != mutatedHash {
		return fmt.Errorf("mutated file hash differs")
	}
	return nil
}

func verifyWitnessArtifactClaims(root, directory string, witness *ExecutionWitness) error {
	switch witness.WitnessType {
	case "provider-capture":
		hashes := make(map[string]struct{}, len(witness.Artifacts))
		for _, artifact := range witness.Artifacts {
			hashes[artifact.Hash] = struct{}{}
		}
		for _, capture := range witness.ProviderCaptures {
			if capture.OracleArtifactHash != capture.TargetArtifactHash {
				return fmt.Errorf("provider capture differs from oracle")
			}
			if _, ok := hashes[capture.OracleArtifactHash]; !ok {
				return fmt.Errorf("provider capture hash has no artifact")
			}
		}
	case "go-covered-range":
		if len(witness.Artifacts) != 1 {
			return fmt.Errorf("Go coverage witness requires one artifact")
		}
		path := filepath.Join(directory, filepath.FromSlash(witness.Artifacts[0].Path))
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		reader, err := gzip.NewReader(file)
		if err != nil {
			_ = file.Close()
			return err
		}
		blocks, parseErr := ParseGoCoverage(root, reader)
		closeErr := reader.Close()
		fileCloseErr := file.Close()
		if parseErr != nil {
			return parseErr
		}
		if closeErr != nil {
			return closeErr
		}
		if fileCloseErr != nil {
			return fileCloseErr
		}
		for _, covered := range witness.CoveredRanges {
			count := 0
			for _, block := range blocks {
				if block.Path == covered.Path && block.Count > 0 && block.StartLine <= covered.EndLine && block.EndLine >= covered.StartLine {
					count += block.Count
				}
			}
			if count != covered.Count {
				return fmt.Errorf("covered range %s:%d-%d count %d differs from artifact %d", covered.Path, covered.StartLine, covered.EndLine, covered.Count, count)
			}
		}
	case "event-delivery", "state-transition", "subprocess-invocation", "extension-realization", "terminal-trace", "persistence-roundtrip", "cancellation-trace", "resource-trace":
		if len(witness.Artifacts) != 1 {
			return fmt.Errorf("typed trace witness requires one artifact")
		}
		data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(witness.Artifacts[0].Path)))
		if err != nil {
			return err
		}
		artifact, err := decodeTraceArtifact(data)
		if err != nil {
			return err
		}
		if artifact.WitnessType != witness.WitnessType || len(artifact.Captures) != len(witness.TargetIDs) {
			return fmt.Errorf("typed trace artifact differs from witness")
		}
		for index, capture := range artifact.Captures {
			if capture.TargetID != witness.TargetIDs[index] {
				return fmt.Errorf("typed trace capture %s differs from witness", capture.TargetID)
			}
		}
	default:
		return fmt.Errorf("unsupported attestation witness type %s", witness.WitnessType)
	}
	return nil
}

func trackedEvidencePath(ctx context.Context, root, configured string) (string, string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	realRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", "", err
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(absoluteRoot, path)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(realRoot, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("tracked evidence path escapes repository root")
	}
	relative = filepath.ToSlash(relative)
	if _, err := repositoryGitOutput(ctx, root, "ls-files", "--error-unmatch", "--", relative); err != nil {
		return "", "", fmt.Errorf("tracked evidence path %s is not committed", relative)
	}
	for _, args := range [][]string{{"diff", "--quiet", "HEAD", "--", relative}, {"diff", "--cached", "--quiet", "HEAD", "--", relative}} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = root
		if err := command.Run(); err != nil {
			return "", "", fmt.Errorf("tracked evidence path %s differs from HEAD", relative)
		}
	}
	return realPath, relative, nil
}

func repositoryGitOutputBytes(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

func repositoryGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}
