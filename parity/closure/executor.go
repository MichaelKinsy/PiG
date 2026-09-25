package closure

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const maxEvidenceCommandOutput = 16 << 20

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

type evidenceCommandArtifacts struct {
	stdout []byte
	stderr []byte
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.overflow = true
		return originalLength, nil
	}
	if len(data) > remaining {
		buffer.overflow = true
		data = data[:remaining]
	}
	_, _ = buffer.buffer.Write(data)
	return originalLength, nil
}

func ExecuteRequest(ctx context.Context, databasePath, root, requestID, outputPath string) (*EvidenceRun, error) {
	database, err := openStore(databasePath)
	if err != nil {
		return nil, err
	}
	records, err := readRecords(ctx, database)
	closeErr := database.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	request, ok := graph.Records[requestID].(*EvidenceRequest)
	if !ok {
		return nil, fmt.Errorf("evidence request %s not found", requestID)
	}
	snapshot := graph.Records[request.SnapshotID].(*Snapshot)
	if err := requireCanonicalTree(ctx, root, snapshot.TargetCommit); err != nil {
		return nil, err
	}
	toolchainHash, err := CurrentToolchainHash(ctx)
	if err != nil {
		return nil, err
	}
	if toolchainHash != snapshot.ToolchainHash {
		return nil, fmt.Errorf("toolchain differs from evidence snapshot")
	}
	environmentHash, err := CurrentEnvironmentHash(request.Environment)
	if err != nil {
		return nil, err
	}
	if environmentHash != snapshot.EnvironmentHash {
		return nil, fmt.Errorf("environment differs from evidence snapshot")
	}
	outputDirectory, err := createEvidenceOutput(root, outputPath)
	if err != nil {
		return nil, err
	}
	environment, err := evidenceEnvironment(request.Environment)
	if err != nil {
		return nil, err
	}
	cleanupWitnessArtifacts, err := prepareWitnessArtifacts(root, request.Witnesses)
	if err != nil {
		return nil, err
	}
	defer cleanupWitnessArtifacts()
	var executions []EvidenceCommandResult
	var commandArtifacts []evidenceCommandArtifacts
	var artifacts []EvidenceArtifact
	for run := range request.Durability {
		for index, command := range request.Commands {
			commandHash, err := evidenceCommandHash(run, index, command)
			if err != nil {
				return nil, err
			}
			execution, stdout, stderr := executeEvidenceCommand(ctx, root, request.TimeoutSeconds, environment, run, index, command, commandHash)
			executions = append(executions, execution)
			commandArtifacts = append(commandArtifacts, evidenceCommandArtifacts{stdout: stdout, stderr: stderr})
			stdoutArtifact, err := writeEvidenceArtifact(outputDirectory, run, index, "stdout", stdout)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, stdoutArtifact)
			stderrArtifact, err := writeEvidenceArtifact(outputDirectory, run, index, "stderr", stderr)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, stderrArtifact)
		}
	}
	transcript, err := json.Marshal(executions)
	if err != nil {
		return nil, err
	}
	executorHash, err := hashExecutorSource(root)
	if err != nil {
		return nil, err
	}
	result := evidenceResult(request, executions)
	var witnesses []*ExecutionWitness
	if result == "pass" {
		witnesses, err = buildExecutionWitnesses(root, outputDirectory, request, graph, commandArtifacts)
		if err != nil {
			return nil, err
		}
		for _, witness := range witnesses {
			artifacts = append(artifacts, witness.Artifacts...)
		}
	}
	artifacts, err = normalizeEvidenceArtifacts(artifacts)
	if err != nil {
		return nil, err
	}
	witnessIDs := make([]string, 0, len(witnesses))
	subjectPinSet := make(map[string]struct{})
	for _, witness := range witnesses {
		witnessIDs = append(witnessIDs, witness.ID)
		for _, pinID := range witness.SubjectPinIDs {
			subjectPinSet[pinID] = struct{}{}
		}
	}
	slices.Sort(witnessIDs)
	subjectPinIDs := []string{}
	if len(subjectPinSet) > 0 {
		subjectPinIDs = slices.Sorted(maps.Keys(subjectPinSet))
	}
	evidence := &EvidenceRun{
		Kind: KindEvidenceRun, RequestID: request.ID, SnapshotID: request.SnapshotID, Result: result,
		AssertionIDs: slices.Clone(request.AssertionIDs), WitnessIDs: witnessIDs, SubjectPinIDs: subjectPinIDs,
		Executor:     "repository-reviewed-local",
		ExecutorHash: executorHash, Environment: slices.Clone(environment), Commands: executions, Artifacts: artifacts,
		Bindings:      graph.evidenceBindings(request),
		ToolchainHash: snapshot.ToolchainHash, EnvironmentHash: snapshot.EnvironmentHash, Durability: request.Durability,
		TranscriptHash: HashBytes(transcript),
	}
	evidence.ID, err = evidenceContentID(evidence)
	if err != nil {
		return nil, err
	}
	outputRecords := make([]Record, 0, len(witnesses)+1)
	for _, witness := range witnesses {
		outputRecords = append(outputRecords, witness)
	}
	outputRecords = append(outputRecords, evidence)
	if err := writeJSONLAtomic(filepath.Join(outputDirectory, "evidence.jsonl"), outputRecords...); err != nil {
		return nil, err
	}
	return evidence, nil
}

func ExecuteMutationRequest(ctx context.Context, databasePath, root, requestID, outputPath string) (*MutationRun, error) {
	database, err := openStore(databasePath)
	if err != nil {
		return nil, err
	}
	records, err := readRecords(ctx, database)
	closeErr := database.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	request, ok := graph.Records[requestID].(*MutationRequest)
	if !ok {
		return nil, fmt.Errorf("mutation request %s not found", requestID)
	}
	snapshot := graph.Records[request.SnapshotID].(*Snapshot)
	if err := requireCanonicalTree(ctx, root, snapshot.TargetCommit); err != nil {
		return nil, err
	}
	toolchainHash, err := CurrentToolchainHash(ctx)
	if err != nil {
		return nil, err
	}
	if toolchainHash != snapshot.ToolchainHash {
		return nil, fmt.Errorf("toolchain differs from mutation snapshot")
	}
	environmentHash, err := CurrentEnvironmentHash(request.Environment)
	if err != nil {
		return nil, err
	}
	if environmentHash != snapshot.EnvironmentHash {
		return nil, fmt.Errorf("environment differs from mutation snapshot")
	}
	outputDirectory, err := createEvidenceOutput(root, outputPath)
	if err != nil {
		return nil, err
	}
	environment, err := evidenceEnvironment(request.Environment)
	if err != nil {
		return nil, err
	}
	baselines := make([]EvidenceCommandResult, 0, len(request.Commands))
	executions := make([]EvidenceCommandResult, 0, len(request.Commands))
	artifacts := make([]EvidenceArtifact, 0, len(request.Commands)*5)
	results := make([]MutationResult, 0, len(request.MutantIDs))
	commandIndex := 0
	for _, mutantID := range request.MutantIDs {
		mutant := graph.Records[mutantID].(*Mutant)
		result := MutationResult{MutantID: mutant.ID, BoundTest: mutant.ExpectedFailureTest, MutatedPath: mutant.Edit.Path, MutatedHash: mutant.Edit.MutatedHash}
		if mutant.Mode == "automatic" {
			commandIndex++
			baseline, execution, failureHash, failureTest, mutationArtifacts, executeErr := executeAutomaticMutant(ctx, root, snapshot.TargetCommit, outputDirectory, request.TimeoutSeconds, environment, commandIndex-1, request.Commands[commandIndex-1], mutant)
			if executeErr != nil {
				return nil, executeErr
			}
			baselines = append(baselines, baseline)
			executions = append(executions, execution)
			artifacts = append(artifacts, mutationArtifacts...)
			result.CommandIndex = commandIndex
			result.FailureHash = failureHash
			result.FailureTest = failureTest
		} else {
			decision := graph.scopedDecision(mutant.ID, "manual-mutation")
			if decision == nil || decision.ValidThroughCommit != snapshot.TargetCommit || len(decision.FailureHashes) != 1 {
				return nil, fmt.Errorf("manual mutant %s lacks an exact reviewed decision", mutant.ID)
			}
			result.DecisionID = decision.ID
			result.FailureHash = decision.FailureHashes[0]
		}
		results = append(results, result)
	}
	executorHash, err := hashExecutorSource(root)
	if err != nil {
		return nil, err
	}
	artifacts, err = normalizeEvidenceArtifacts(artifacts)
	if err != nil {
		return nil, err
	}
	repositoryCommit, err := repositoryGitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	run := &MutationRun{
		Kind: KindMutationRun, RequestID: request.ID, SnapshotID: request.SnapshotID, RepositoryCommit: repositoryCommit,
		Executor: "repository-reviewed-local", ExecutorHash: executorHash, Environment: slices.Clone(environment),
		Baselines: baselines, Commands: executions, Results: results, Artifacts: artifacts, Support: graph.mutationBindings(request),
		ToolchainHash: snapshot.ToolchainHash, EnvironmentHash: snapshot.EnvironmentHash,
	}
	run.ID, err = mutationRunContentID(run)
	if err != nil {
		return nil, err
	}
	if err := writeJSONLAtomic(filepath.Join(outputDirectory, "mutation.jsonl"), run); err != nil {
		return nil, err
	}
	return run, nil
}

type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

func executeAutomaticMutant(ctx context.Context, root, commit, outputDirectory string, timeoutSeconds int, environment []string, index int, command EvidenceCommand, mutant *Mutant) (EvidenceCommandResult, EvidenceCommandResult, string, string, []EvidenceArtifact, error) {
	checkout, err := createMutationCheckout(ctx, root, commit)
	if err != nil {
		return EvidenceCommandResult{}, EvidenceCommandResult{}, "", "", nil, err
	}
	defer func() { _ = os.RemoveAll(checkout) }()
	commandHash, err := evidenceCommandHash(0, index, command)
	if err != nil {
		return EvidenceCommandResult{}, EvidenceCommandResult{}, "", "", nil, err
	}
	baseline, baselineStdout, baselineStderr := executeEvidenceCommand(ctx, checkout, timeoutSeconds, environment, 0, index, command, commandHash)
	if baseline.ExitCode != 0 || baseline.Error != "" || baseline.Overflow || !goTestPassed(baselineStdout, mutant.ExpectedFailureTest) {
		return EvidenceCommandResult{}, EvidenceCommandResult{}, "", "", nil, fmt.Errorf("mutant %s baseline did not pass", mutant.ID)
	}
	patch, err := applyMutationEdit(checkout, mutant.Edit)
	if err != nil {
		return EvidenceCommandResult{}, EvidenceCommandResult{}, "", "", nil, fmt.Errorf("mutant %s: %w", mutant.ID, err)
	}
	execution, stdout, stderr := executeEvidenceCommand(ctx, checkout, timeoutSeconds, environment, 0, index, command, commandHash)
	failureHash := mutationFailureHash(stdout)
	failureTest := ""
	if execution.ExitCode != 0 && execution.Error == "" && !execution.Overflow {
		if observedHash, observeErr := goTestFailureHash(stdout, mutant.ExpectedFailureTest); observeErr == nil {
			failureHash = observedHash
			failureTest = mutant.ExpectedFailureTest
		}
	}
	streams := []struct {
		name string
		data []byte
	}{
		{fmt.Sprintf("mutant-%03d-baseline.stdout", index+1), baselineStdout},
		{fmt.Sprintf("mutant-%03d-baseline.stderr", index+1), baselineStderr},
		{fmt.Sprintf("mutant-%03d.stdout", index+1), stdout},
		{fmt.Sprintf("mutant-%03d.stderr", index+1), stderr},
		{fmt.Sprintf("mutant-%03d.patch", index+1), patch},
	}
	artifacts := make([]EvidenceArtifact, 0, len(streams))
	for _, stream := range streams {
		if err := os.WriteFile(filepath.Join(outputDirectory, stream.name), stream.data, 0o600); err != nil {
			return EvidenceCommandResult{}, EvidenceCommandResult{}, "", "", nil, err
		}
		artifacts = append(artifacts, EvidenceArtifact{Path: stream.name, Hash: HashBytes(stream.data)})
	}
	return baseline, execution, failureHash, failureTest, artifacts, nil
}

func createMutationCheckout(ctx context.Context, root, commit string) (string, error) {
	checkout, err := os.MkdirTemp("", "pig-mutation-*")
	if err != nil {
		return "", err
	}
	if err := os.Remove(checkout); err != nil {
		return "", err
	}
	// Evidence hashes the committed bytes, so the snapshot must not take a
	// line-ending conversion from the host (Git for Windows sets
	// core.autocrlf=true system-wide). Repository attributes still apply.
	command := exec.CommandContext(ctx, "git", "clone", "--quiet", "--shared", "--no-checkout", "--config", "core.autocrlf=false", root, checkout)
	if output, cloneErr := command.CombinedOutput(); cloneErr != nil {
		_ = os.RemoveAll(checkout)
		return "", fmt.Errorf("create mutation checkout: %w: %s", cloneErr, strings.TrimSpace(string(output)))
	}
	command = exec.CommandContext(ctx, "git", "checkout", "--quiet", "--detach", commit)
	command.Dir = checkout
	if output, checkoutErr := command.CombinedOutput(); checkoutErr != nil {
		_ = os.RemoveAll(checkout)
		return "", fmt.Errorf("checkout mutation snapshot: %w: %s", checkoutErr, strings.TrimSpace(string(output)))
	}
	return checkout, nil
}

func applyMutationEdit(root string, edit MutationEdit) ([]byte, error) {
	path := filepath.Join(root, filepath.FromSlash(edit.Path))
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("mutation target is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if HashBytes(data) != edit.OriginalHash || bytes.Count(data, []byte(edit.Before)) != 1 {
		return nil, fmt.Errorf("mutation target differs from its exact original content")
	}
	mutated := bytes.Replace(data, []byte(edit.Before), []byte(edit.After), 1)
	if HashBytes(mutated) != edit.MutatedHash {
		return nil, fmt.Errorf("mutation replacement differs from its expected hash")
	}
	if err := os.WriteFile(path, mutated, info.Mode().Perm()); err != nil {
		return nil, err
	}
	command := exec.Command("git", "diff", "--name-only", "--no-ext-diff", "-z")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	if string(bytes.TrimSuffix(output, []byte{0})) != edit.Path {
		return nil, fmt.Errorf("mutation changed files outside %s", edit.Path)
	}
	command = exec.Command("git", "diff", "--binary", "--no-ext-diff", "--", edit.Path)
	command.Dir = root
	patch, err := command.Output()
	if err != nil {
		return nil, err
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("mutation produced no patch")
	}
	return patch, nil
}

// goTestFailureHash derives the failure identity from go test -json events on
// stdout alone. Stderr carries tool noise (telemetry, download and cache
// warnings) that varies by host, so it is recorded as an artifact but never
// decides whether a mutant was killed.
func goTestFailureHash(stdout []byte, expectedTest string) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	var outputs []string
	found := false
	for {
		var event goTestEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("decode go test event: %w", err)
		}
		if event.Action == "fail" && event.Test != "" {
			if event.Test != expectedTest {
				return "", fmt.Errorf("unrelated test %s failed", event.Test)
			}
			found = true
		}
		if event.Test == expectedTest && event.Output != "" && !strings.HasPrefix(event.Output, "=== RUN") && !strings.HasPrefix(event.Output, "--- FAIL") {
			outputs = append(outputs, event.Output)
		}
	}
	if !found {
		return "", fmt.Errorf("expected test %s did not fail", expectedTest)
	}
	return ExpectedGoTestFailureHash(expectedTest, outputs)
}

func goTestPassed(stdout []byte, expectedTest string) bool {
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	found := false
	for {
		var event goTestEvent
		if err := decoder.Decode(&event); err != nil {
			return err == io.EOF && found
		}
		if event.Action == "fail" {
			return false
		}
		if event.Action == "pass" && event.Test == expectedTest {
			found = true
		}
	}
}

// ExpectedGoTestFailureHash derives the exact structured failure identity used
// by automatic Go mutants from the bound test's expected output events.
func ExpectedGoTestFailureHash(expectedTest string, outputs []string) (string, error) {
	material, err := json.Marshal(struct {
		Test   string   `json:"test"`
		Output []string `json:"output"`
	}{Test: expectedTest, Output: outputs})
	if err != nil {
		return "", err
	}
	return HashBytes(material), nil
}

// mutationFailureHash identifies a surviving mutant's run by its stdout; like
// goTestFailureHash it ignores stderr noise.
func mutationFailureHash(stdout []byte) string {
	return mutationStdoutFailureHash(HashBytes(stdout))
}

func mutationStdoutFailureHash(stdoutHash string) string {
	return HashBytes([]byte("mutation-survivor\x00" + stdoutHash))
}

func evidenceContentID(evidence *EvidenceRun) (string, error) {
	copy := *evidence
	copy.ID = ""
	data, err := json.Marshal(&copy)
	if err != nil {
		return "", err
	}
	return "evidence:" + strings.TrimPrefix(HashBytes(data), "sha256:"), nil
}

func executionWitnessContentID(witness *ExecutionWitness) (string, error) {
	copy := *witness
	copy.ID = ""
	data, err := json.Marshal(&copy)
	if err != nil {
		return "", err
	}
	return "witness:" + strings.TrimPrefix(HashBytes(data), "sha256:"), nil
}

func evidenceAttestationContentID(attestation *EvidenceAttestation) (string, error) {
	copy := *attestation
	copy.ID = ""
	data, err := json.Marshal(&copy)
	if err != nil {
		return "", err
	}
	return "attestation:" + strings.TrimPrefix(HashBytes(data), "sha256:"), nil
}

func prepareWitnessArtifacts(root string, plans []EvidenceWitnessRequest) (func(), error) {
	var paths []string
	for _, plan := range plans {
		if plan.ArtifactPath == "" {
			continue
		}
		path, err := confinedOutputPath(root, plan.ArtifactPath)
		if err != nil {
			return func() {}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return func() {}, err
		}
		if _, err := os.Lstat(path); err == nil {
			return func() {}, fmt.Errorf("witness artifact already exists: %s", plan.ArtifactPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return func() {}, err
		}
		paths = append(paths, path)
	}
	return func() {
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}, nil
}

func buildExecutionWitnesses(root, outputDirectory string, request *EvidenceRequest, graph *Graph, commands []evidenceCommandArtifacts) ([]*ExecutionWitness, error) {
	witnesses := make([]*ExecutionWitness, 0, len(request.Witnesses))
	for index, plan := range request.Witnesses {
		witness := &ExecutionWitness{
			Kind: KindExecutionWitness, SnapshotID: request.SnapshotID, RequestID: request.ID,
			WitnessType: plan.WitnessType, TargetIDs: slices.Clone(plan.TargetIDs), SubjectPinIDs: slices.Clone(plan.SubjectPinIDs),
			Artifacts: []EvidenceArtifact{}, CoveredRanges: []EvidenceCoveredRange{}, ProviderCaptures: []EvidenceProviderCapture{},
		}
		var err error
		switch plan.WitnessType {
		case "provider-capture":
			err = populateProviderCaptureWitness(witness, plan, commands)
		case "go-covered-range":
			err = populateGoCoverageWitness(root, outputDirectory, index, witness, plan, graph)
		case "event-delivery", "state-transition", "subprocess-invocation", "extension-realization", "terminal-trace", "persistence-roundtrip", "cancellation-trace", "resource-trace":
			err = populateTraceWitness(root, outputDirectory, index, witness, plan)
		default:
			err = fmt.Errorf("unsupported executor witness type %s", plan.WitnessType)
		}
		if err != nil {
			return nil, err
		}
		witness.ID, err = executionWitnessContentID(witness)
		if err != nil {
			return nil, err
		}
		witnesses = append(witnesses, witness)
	}
	return witnesses, nil
}

func populateTraceWitness(root, outputDirectory string, index int, witness *ExecutionWitness, plan EvidenceWitnessRequest) error {
	if len(plan.CommandIndexes) != 1 || plan.ArtifactPath == "" {
		return fmt.Errorf("typed trace requires one command index and an artifact path")
	}
	path, err := confinedOutputPath(root, plan.ArtifactPath)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open typed trace %s: %w", plan.ArtifactPath, err)
	}
	reader := &io.LimitedReader{R: file, N: maxEvidenceCommandOutput + 1}
	data, readErr := io.ReadAll(reader)
	closeErr := file.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > maxEvidenceCommandOutput {
		return fmt.Errorf("typed trace %s exceeds %d bytes", plan.ArtifactPath, maxEvidenceCommandOutput)
	}
	artifact, err := decodeTraceArtifact(data)
	if err != nil {
		return err
	}
	if artifact.WitnessType != plan.WitnessType {
		return fmt.Errorf("typed trace witness type %s differs from %s", artifact.WitnessType, plan.WitnessType)
	}
	targets := make([]string, len(artifact.Captures))
	for captureIndex, capture := range artifact.Captures {
		targets[captureIndex] = capture.TargetID
	}
	if !slices.Equal(targets, plan.TargetIDs) {
		return fmt.Errorf("typed trace targets %q differ from requested %q", targets, plan.TargetIDs)
	}
	artifactName := fmt.Sprintf("witness-%03d.trace.json", index+1)
	if err := os.WriteFile(filepath.Join(outputDirectory, artifactName), data, 0o600); err != nil {
		return err
	}
	witness.Artifacts = []EvidenceArtifact{{Path: artifactName, Hash: HashBytes(data)}}
	return nil
}

func decodeTraceArtifact(data []byte) (*EvidenceTraceArtifact, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var artifact EvidenceTraceArtifact
	if err := decoder.Decode(&artifact); err != nil {
		return nil, fmt.Errorf("decode typed trace: %w", err)
	}
	var suffix json.RawMessage
	if err := decoder.Decode(&suffix); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode typed trace: multiple JSON values")
		}
		return nil, fmt.Errorf("decode typed trace suffix: %w", err)
	}
	if !isTraceWitness(artifact.WitnessType) || len(artifact.Captures) == 0 {
		return nil, fmt.Errorf("typed trace has invalid witness type or no captures")
	}
	if !slices.IsSortedFunc(artifact.Captures, func(left, right EvidenceTraceArtifactCapture) int {
		return strings.Compare(left.TargetID, right.TargetID)
	}) {
		return nil, fmt.Errorf("typed trace captures are not sorted")
	}
	seen := make(map[string]struct{}, len(artifact.Captures))
	for _, capture := range artifact.Captures {
		if capture.TargetID == "" || len(capture.Events) == 0 {
			return nil, fmt.Errorf("typed trace has incomplete capture")
		}
		if _, exists := seen[capture.TargetID]; exists {
			return nil, fmt.Errorf("typed trace duplicates target %s", capture.TargetID)
		}
		seen[capture.TargetID] = struct{}{}
		for index, event := range capture.Events {
			if event.Ordinal != index+1 || event.Kind == "" || event.Subject == "" || !ValidHash(event.ValueHash) {
				return nil, fmt.Errorf("typed trace target %s has invalid event %d", capture.TargetID, index+1)
			}
		}
	}
	return &artifact, nil
}

func populateProviderCaptureWitness(witness *ExecutionWitness, plan EvidenceWitnessRequest, commands []evidenceCommandArtifacts) error {
	if len(plan.CommandIndexes) != 2 {
		return fmt.Errorf("provider capture requires oracle and target command indexes")
	}
	oracleIndex := plan.CommandIndexes[0] - 1
	targetIndex := plan.CommandIndexes[1] - 1
	if oracleIndex < 0 || oracleIndex >= len(commands) || targetIndex < 0 || targetIndex >= len(commands) {
		return fmt.Errorf("provider capture command index is out of range")
	}
	oracleHash := HashBytes(commands[oracleIndex].stdout)
	targetHash := HashBytes(commands[targetIndex].stdout)
	if oracleHash != targetHash {
		return fmt.Errorf("provider capture output differs from oracle")
	}
	witness.Artifacts = []EvidenceArtifact{
		{Path: evidenceStreamArtifactPath(0, oracleIndex, "stdout"), Hash: oracleHash},
		{Path: evidenceStreamArtifactPath(0, targetIndex, "stdout"), Hash: targetHash},
	}
	for _, targetID := range plan.TargetIDs {
		witness.ProviderCaptures = append(witness.ProviderCaptures, EvidenceProviderCapture{
			TargetID: targetID, OracleArtifactHash: oracleHash, TargetArtifactHash: targetHash,
		})
	}
	return nil
}

func populateGoCoverageWitness(root, outputDirectory string, index int, witness *ExecutionWitness, plan EvidenceWitnessRequest, graph *Graph) error {
	path, err := confinedOutputPath(root, plan.ArtifactPath)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open witness coverage %s: %w", plan.ArtifactPath, err)
	}
	blocks, parseErr := ParseGoCoverage(root, file)
	closeErr := file.Close()
	if parseErr != nil {
		return parseErr
	}
	if closeErr != nil {
		return closeErr
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	artifactPath := fmt.Sprintf("witness-%03d.coverprofile.gz", index+1)
	if err := os.WriteFile(filepath.Join(outputDirectory, artifactPath), compressed.Bytes(), 0o600); err != nil {
		return err
	}
	witness.Artifacts = []EvidenceArtifact{{Path: artifactPath, Hash: HashBytes(compressed.Bytes())}}
	for _, targetID := range plan.TargetIDs {
		target, ok := graph.Records[targetID].(*Target)
		if !ok {
			return fmt.Errorf("coverage witness target %s not found", targetID)
		}
		for _, pinID := range target.PinIDs {
			pin := graph.Records[pinID].(*Pin)
			count := 0
			for _, block := range blocks {
				if block.Path == pin.Path && block.Count > 0 && block.StartLine <= pin.EndLine && block.EndLine >= pin.StartLine {
					count += block.Count
				}
			}
			if count == 0 {
				return fmt.Errorf("coverage witness did not execute target %s pin %s", targetID, pinID)
			}
			witness.CoveredRanges = append(witness.CoveredRanges, EvidenceCoveredRange{
				TargetID: targetID, PinID: pinID, Path: pin.Path, StartLine: pin.StartLine, EndLine: pin.EndLine, Count: count,
			})
		}
	}
	slices.SortFunc(witness.CoveredRanges, func(left, right EvidenceCoveredRange) int {
		if target := strings.Compare(left.TargetID, right.TargetID); target != 0 {
			return target
		}
		return strings.Compare(left.PinID, right.PinID)
	})
	return nil
}

func confinedOutputPath(root, configured string) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(absoluteRoot, path)
	}
	path = filepath.Clean(path)
	relative, err := filepath.Rel(absoluteRoot, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("witness artifact escapes repository root")
	}
	return path, nil
}

func normalizeEvidenceArtifacts(artifacts []EvidenceArtifact) ([]EvidenceArtifact, error) {
	artifacts = slices.Clone(artifacts)
	slices.SortFunc(artifacts, compareEvidenceArtifacts)
	result := artifacts[:0]
	for _, artifact := range artifacts {
		if len(result) == 0 || result[len(result)-1].Path != artifact.Path {
			result = append(result, artifact)
			continue
		}
		if result[len(result)-1].Hash != artifact.Hash {
			return nil, fmt.Errorf("evidence artifact %s has conflicting hashes", artifact.Path)
		}
	}
	return result, nil
}

func compareEvidenceArtifacts(left, right EvidenceArtifact) int {
	if path := strings.Compare(left.Path, right.Path); path != 0 {
		return path
	}
	return strings.Compare(left.Hash, right.Hash)
}

func evidenceStreamArtifactPath(run, index int, stream string) string {
	return fmt.Sprintf("run-%03d-command-%03d.%s", run+1, index+1, stream)
}

func evidenceCommandHash(run, index int, command EvidenceCommand) (string, error) {
	data, err := json.Marshal(struct {
		Run     int             `json:"run"`
		Index   int             `json:"index"`
		Command EvidenceCommand `json:"command"`
	}{Run: run + 1, Index: index + 1, Command: command})
	if err != nil {
		return "", err
	}
	return HashBytes(data), nil
}

func executeEvidenceCommand(parent context.Context, root string, timeoutSeconds int, environment []string, run, index int, command EvidenceCommand, commandHash string) (EvidenceCommandResult, []byte, []byte) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Dir = root
	process.Env = environment
	stdout := &boundedBuffer{limit: maxEvidenceCommandOutput}
	stderr := &boundedBuffer{limit: maxEvidenceCommandOutput}
	process.Stdout = stdout
	process.Stderr = stderr
	err := process.Run()
	exitCode := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		exitCode = -1
	}
	errorMessage := ""
	if ctx.Err() != nil {
		errorMessage = ctx.Err().Error()
	} else if err != nil && exitCode == -1 {
		errorMessage = err.Error()
	}
	stdoutData := slices.Clone(stdout.buffer.Bytes())
	stderrData := slices.Clone(stderr.buffer.Bytes())
	return EvidenceCommandResult{
		Run: run + 1, Index: index + 1, CommandHash: commandHash,
		StdoutHash: HashBytes(stdoutData), StderrHash: HashBytes(stderrData), ExitCode: exitCode,
		Error: errorMessage, Overflow: stdout.overflow || stderr.overflow,
	}, stdoutData, stderrData
}

func CurrentToolchainHash(ctx context.Context) (string, error) {
	commands := [][]string{{"go", "version"}, {"node", "--version"}, {"python3", "--version"}, {"rustc", "--version"}}
	versions := make([]string, 0, len(commands))
	for _, command := range commands {
		// Each tool prints its version on stdout. Stderr carries host noise
		// (go's telemetry warnings), so it must not enter the snapshot identity.
		output, err := exec.CommandContext(ctx, command[0], command[1:]...).Output()
		if err != nil {
			return "", fmt.Errorf("resolve toolchain %s: %w", command[0], err)
		}
		versions = append(versions, strings.TrimSpace(string(output)))
	}
	data, err := json.Marshal(versions)
	if err != nil {
		return "", err
	}
	return HashBytes(data), nil
}

func CurrentEnvironmentHash(configured []string) (string, error) {
	requirements := slices.Clone(configured)
	if !sortedUnique(requirements) {
		return "", fmt.Errorf("evidence environment requirements must be sorted and unique")
	}
	for _, entry := range requirements {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
			return "", fmt.Errorf("invalid evidence environment entry %q", entry)
		}
	}
	data, err := json.Marshal(struct {
		GOOS         string   `json:"goos"`
		GOARCH       string   `json:"goarch"`
		Fixed        []string `json:"fixed"`
		Requirements []string `json:"requirements"`
	}{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Fixed: []string{"CI=1", "NO_COLOR=1", "TERM=dumb"}, Requirements: requirements,
	})
	if err != nil {
		return "", err
	}
	return HashBytes(data), nil
}

func requireCanonicalTree(ctx context.Context, root, targetCommit string) error {
	command := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("resolve integration commit: %w", err)
	}
	if strings.TrimSpace(string(output)) != targetCommit {
		return fmt.Errorf("integration commit differs from evidence snapshot")
	}
	command = exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=all")
	command.Dir = root
	output, err = command.Output()
	if err != nil {
		return fmt.Errorf("inspect integration tree: %w", err)
	}
	if len(bytes.TrimSpace(output)) != 0 {
		return fmt.Errorf("integration tree has tracked or untracked changes")
	}
	return nil
}

func createEvidenceOutput(root, configured string) (string, error) {
	if configured == "" {
		return "", fmt.Errorf("evidence output path is empty")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absoluteOutput := configured
	if !filepath.IsAbs(absoluteOutput) {
		absoluteOutput = filepath.Join(absoluteRoot, configured)
	}
	absoluteOutput = filepath.Clean(absoluteOutput)
	relative, err := filepath.Rel(absoluteRoot, absoluteOutput)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("evidence output escapes repository root")
	}
	if err := os.Mkdir(absoluteOutput, 0o700); err != nil {
		return "", fmt.Errorf("create evidence output: %w", err)
	}
	return absoluteOutput, nil
}

func evidenceEnvironment(configured []string) ([]string, error) {
	allowed := map[string]bool{
		"APPDATA": true, "GOCACHE": true, "GOMODCACHE": true, "GOPATH": true, "HOME": true,
		"LOCALAPPDATA": true, "PATH": true, "SYSTEMROOT": true, "TEMP": true, "TMP": true,
		"TMPDIR": true, "USERPROFILE": true,
	}
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && allowed[key] {
			values[key] = value
		}
	}
	values["CI"] = "1"
	values["NO_COLOR"] = "1"
	values["TERM"] = "dumb"
	for _, entry := range configured {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("invalid evidence environment entry %q", entry)
		}
		values[key] = value
	}
	keys := slices.Sorted(maps.Keys(values))
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment, nil
}

func writeEvidenceArtifact(directory string, run, index int, stream string, data []byte) (EvidenceArtifact, error) {
	name := evidenceStreamArtifactPath(run, index, stream)
	if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
		return EvidenceArtifact{}, err
	}
	return EvidenceArtifact{Path: name, Hash: HashBytes(data)}, nil
}

func hashExecutorSource(root string) (string, error) {
	paths := []string{
		"parity/closure/attestation.go",
		"parity/closure/coverage.go",
		"parity/closure/decode.go",
		"parity/closure/executor.go",
		"parity/closure/graph.go",
		"parity/closure/mutation.go",
		"parity/closure/types.go",
		"parity/cmd/closure/main.go",
	}
	var material bytes.Buffer
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return "", err
		}
		material.WriteString(path)
		material.WriteByte(0)
		material.Write(data)
		material.WriteByte(0)
	}
	return HashBytes(material.Bytes()), nil
}

func writeJSONLAtomic(path string, records ...Record) error {
	var data []byte
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		data = append(data, encoded...)
		data = append(data, '\n')
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".evidence-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
