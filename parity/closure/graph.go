package closure

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

type Graph struct {
	Records      map[string]Record
	RecordHashes map[string]string
	Verdicts     map[string]Verdict
}

func Build(records []Record) (*Graph, error) {
	graph := &Graph{
		Records:      make(map[string]Record, len(records)),
		RecordHashes: make(map[string]string, len(records)),
		Verdicts:     make(map[string]Verdict),
	}
	var problems []string
	for _, record := range records {
		if record == nil {
			problems = append(problems, "nil record")
			continue
		}
		id := strings.TrimSpace(record.RecordID())
		if id == "" {
			problems = append(problems, fmt.Sprintf("%s record has empty id", record.RecordKind()))
			continue
		}
		if _, duplicate := graph.Records[id]; duplicate {
			problems = append(problems, "duplicate record "+id)
			continue
		}
		hash, err := ContentHash(record)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		graph.Records[id] = record
		graph.RecordHashes[id] = hash
	}
	for _, id := range sortedRecordIDs(graph.Records) {
		problems = append(problems, graph.validateRecord(graph.Records[id])...)
	}
	problems = append(problems, graph.validateRelationships()...)
	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, fmt.Errorf("closure graph invalid:\n- %s", strings.Join(problems, "\n- "))
	}
	graph.deriveVerdicts()
	return graph, nil
}

func (g *Graph) EffectiveVerdicts() []Verdict {
	ids := make([]string, 0, len(g.Verdicts))
	for id := range g.Verdicts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]Verdict, 0, len(ids))
	for _, id := range ids {
		result = append(result, g.Verdicts[id])
	}
	return result
}

func (g *Graph) validateRecord(record Record) []string {
	var problems []string
	id := record.RecordID()
	if record.RecordKind() == "" {
		problems = append(problems, id+" has empty kind")
	}
	if prefix := expectedIDPrefix(record.RecordKind()); prefix == "" {
		problems = append(problems, fmt.Sprintf("%s has unknown kind %q", id, record.RecordKind()))
	} else if !strings.HasPrefix(id, prefix) {
		problems = append(problems, fmt.Sprintf("%s id must start with %q", id, prefix))
	}

	switch value := record.(type) {
	case *Snapshot:
		if !validCommit(value.UpstreamCommit) || !validCommit(value.TargetCommit) {
			problems = append(problems, id+" has invalid commit")
		}
		problems = append(problems, validateHash(id, "toolchainHash", value.ToolchainHash)...)
		problems = append(problems, validateHash(id, "environmentHash", value.EnvironmentHash)...)
		problems = append(problems, validateHashList(id, "extractorHashes", value.ExtractorHashes)...)
		problems = append(problems, validateHashList(id, "rulePackHashes", value.RulePackHashes)...)
	case *Pin:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		if value.Repository == "" || value.Path == "" || value.SemanticID == "" {
			problems = append(problems, id+" requires repository, path, and semanticId")
		}
		if !validCommit(value.Commit) || value.StartLine < 1 || value.EndLine < value.StartLine {
			problems = append(problems, id+" has invalid commit or line range")
		}
		problems = append(problems, validateHash(id, "quoteHash", value.QuoteHash)...)
		problems = append(problems, validateOptionalHash(id, "apiHash", value.APIHash)...)
		problems = append(problems, validateOptionalHash(id, "bodyHash", value.BodyHash)...)
	case *Fact:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expectMany(id, value.PinIDs, KindPin)...)
		if value.FactType == "" || value.SubjectID == "" || !oneOf(value.Resolution, "resolved", "bounded", "open", "observed") {
			problems = append(problems, id+" has invalid factType, subjectId, or resolution")
		}
		if !json.Valid(value.Value) {
			problems = append(problems, id+" has invalid fact value JSON")
		}
	case *Hypothesis:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expectMany(id, value.PinIDs, KindPin)...)
		if value.HypothesisType == "" || value.SubjectID == "" || len(value.PinIDs) == 0 || !json.Valid(value.Value) {
			problems = append(problems, id+" requires hypothesisType, subjectId, pins, and valid value JSON")
		}
		problems = append(problems, g.validateAgentHypothesis(value)...)
	case *ProvisionalClaim:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expect(id, value.SourcePinID, KindPin)...)
		if value.SubjectID == "" || value.Status == "" || value.Status == "pending" {
			problems = append(problems, id+" requires a non-pending subject status")
		}
	case *Facet:
		if !oneOf(value.Name, "result", "error", "cancel", "order", "concurrency", "dispatch", "wire", "compat", "state", "input", "render", "layout", "persistence", "restoration", "history", "lifetime", "shutdown", "boundedness", "realization", "resource") {
			problems = append(problems, id+" has unclassified facet "+value.Name)
		}
	case *Rule:
		if value.Name == "" {
			problems = append(problems, id+" has empty rule name")
		}
		problems = append(problems, validateHash(id, "definitionHash", value.DefinitionHash)...)
	case *Behavior:
		problems = append(problems, g.expectMany(id, value.OriginPinIDs, KindPin)...)
		problems = append(problems, g.expectMany(id, value.FactIDs, KindFact)...)
		if value.Name == "" || value.Profile == "" || len(value.OriginPinIDs) == 0 {
			problems = append(problems, id+" requires name, profile, and origin pins")
		}
	case *Obligation:
		problems = append(problems, g.expect(id, value.BehaviorID, KindBehavior)...)
		problems = append(problems, g.expect(id, value.FacetID, KindFacet)...)
		problems = append(problems, g.expect(id, value.RuleID, KindRule)...)
		problems = append(problems, g.expectMany(id, value.OriginPinIDs, KindPin)...)
	case *Target:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expectMany(id, value.PinIDs, KindPin)...)
		problems = append(problems, g.expectMany(id, value.FactIDs, KindFact)...)
		if value.Language == "" || value.Symbol == "" || len(value.PinIDs) == 0 {
			problems = append(problems, id+" requires language, symbol, and pins")
		}
	case *Mapping:
		problems = append(problems, g.expect(id, value.BehaviorID, KindBehavior)...)
		problems = append(problems, g.expectMany(id, value.TargetIDs, KindTarget)...)
		if !oneOf(value.Status, "hypothesis", "decided", "derived") || len(value.TargetIDs) == 0 {
			problems = append(problems, id+" has invalid status or no targets")
		}
		switch value.Status {
		case "decided":
			problems = append(problems, g.expect(id, value.DecisionID, KindDecision)...)
			if value.RuleID != "" || len(value.FactIDs) != 0 {
				problems = append(problems, id+" decided mapping cannot cite derivation inputs")
			}
		case "derived":
			problems = append(problems, g.expect(id, value.RuleID, KindRule)...)
			problems = append(problems, g.expectMany(id, value.FactIDs, KindFact)...)
			if value.DecisionID != "" || len(value.FactIDs) == 0 {
				problems = append(problems, id+" derived mapping requires facts and cannot cite a decision")
			}
		case "hypothesis":
			if value.DecisionID != "" || value.RuleID != "" || len(value.FactIDs) != 0 {
				problems = append(problems, id+" hypothesis cannot cite acceptance inputs")
			}
		}
	case *Reachability:
		problems = append(problems, g.expect(id, value.BehaviorID, KindBehavior)...)
		problems = append(problems, g.expect(id, value.TargetID, KindTarget)...)
		problems = append(problems, g.expectMany(id, value.RootPinIDs, KindPin)...)
		if !oneOf(value.Class, "prod-reachable", "exported", "test-only", "dead", "unknown") || value.Method == "" {
			problems = append(problems, id+" has invalid class or empty method")
		}
	case *Test:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expect(id, value.PinID, KindPin)...)
		problems = append(problems, g.expectMany(id, value.FixturePinIDs, KindPin)...)
		if value.FixturePinIDs == nil {
			problems = append(problems, id+" fixturePinIds must be an array")
		}
		if !oneOf(value.MutationPolicy, "", "required") {
			problems = append(problems, id+" has invalid mutationPolicy")
		}
		problems = append(problems, validateHash(id, "definitionHash", value.DefinitionHash)...)
		if value.Execution != nil {
			exec := value.Execution
			if exec.Package == "" || exec.RunName == "" {
				problems = append(problems, id+" execution requires package and runName")
			}
			if !oneOf(exec.Comparator, "exit-zero", "output-equal") {
				problems = append(problems, id+" execution has invalid comparator")
			}
			if !sortedUnique(exec.BuildTags) {
				problems = append(problems, id+" execution buildTags must be sorted and unique")
			}
		}
	case *Assertion:
		problems = append(problems, g.expect(id, value.TestID, KindTest)...)
		problems = append(problems, g.expect(id, value.BehaviorID, KindBehavior)...)
		problems = append(problems, g.expect(id, value.FacetID, KindFacet)...)
		if !oneOf(value.Class, "A0", "A1", "A2", "A3") || value.Oracle == "" {
			problems = append(problems, id+" has invalid class or empty oracle")
		}
	case *ExecutionWitness:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expect(id, value.RequestID, KindEvidenceRequest)...)
		problems = append(problems, g.expectMany(id, value.TargetIDs, KindTarget)...)
		problems = append(problems, g.expectMany(id, value.SubjectPinIDs, KindPin)...)
		if !oneOf(value.WitnessType, "go-covered-range", "typescript-covered-range", "provider-capture", "event-delivery", "state-transition", "subprocess-invocation", "extension-realization", "terminal-trace", "persistence-roundtrip", "cancellation-trace", "resource-trace") || len(value.TargetIDs) == 0 {
			problems = append(problems, id+" has invalid witnessType or no targets")
		}
		if expectedID, err := executionWitnessContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
		if value.TargetIDs == nil || value.SubjectPinIDs == nil || value.Artifacts == nil || value.CoveredRanges == nil || value.ProviderCaptures == nil {
			problems = append(problems, id+" witness collections must be arrays")
		}
		problems = append(problems, validateArtifacts(id, value.Artifacts)...)
		if value.WitnessType == "go-covered-range" && (len(value.CoveredRanges) == 0 || len(value.ProviderCaptures) != 0) {
			problems = append(problems, id+" has invalid Go coverage observations")
		}
		if value.WitnessType == "provider-capture" && (len(value.ProviderCaptures) == 0 || len(value.CoveredRanges) != 0) {
			problems = append(problems, id+" has invalid provider capture observations")
		}
		if isTraceWitness(value.WitnessType) && (len(value.Artifacts) != 1 || len(value.CoveredRanges) != 0 || len(value.ProviderCaptures) != 0) {
			problems = append(problems, id+" has invalid typed trace observations")
		}
		for _, covered := range value.CoveredRanges {
			problems = append(problems, g.expect(id, covered.TargetID, KindTarget)...)
			problems = append(problems, g.expect(id, covered.PinID, KindPin)...)
			if covered.Path == "" || covered.StartLine < 1 || covered.EndLine < covered.StartLine || covered.Count < 1 {
				problems = append(problems, id+" has invalid covered range")
			}
		}
		if len(value.CoveredRanges) > 0 && allCoveredRangesScratch(value.CoveredRanges) {
			problems = append(problems, id+" covers only worker scratch")
		}
		for _, capture := range value.ProviderCaptures {
			problems = append(problems, g.expect(id, capture.TargetID, KindTarget)...)
			problems = append(problems, validateHash(id, "providerCaptures.oracleArtifactHash", capture.OracleArtifactHash)...)
			problems = append(problems, validateHash(id, "providerCaptures.targetArtifactHash", capture.TargetArtifactHash)...)
		}
	case *EvidenceRequest:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expectMany(id, value.ObligationIDs, KindObligation)...)
		problems = append(problems, g.expectMany(id, value.AssertionIDs, KindAssertion)...)
		if len(value.ObligationIDs) == 0 || len(value.Commands) == 0 || value.Durability < 1 || value.TimeoutSeconds < 1 {
			problems = append(problems, id+" requires obligations, commands, positive durability, and timeout")
		}
		if !oneOf(value.Comparator, "exit-zero", "output-equal") {
			problems = append(problems, id+" has invalid comparator")
		}
		if value.Environment == nil || value.Witnesses == nil {
			problems = append(problems, id+" environment and witnesses must be arrays")
		}
		if !sortedUnique(value.Environment) {
			problems = append(problems, id+" environment must be sorted and unique")
		}
		for index, command := range value.Commands {
			if strings.TrimSpace(command.Name) == "" {
				problems = append(problems, fmt.Sprintf("%s command %d has empty name", id, index))
			}
			for _, argument := range command.Args {
				if strings.ContainsRune(argument, '\x00') {
					problems = append(problems, fmt.Sprintf("%s command %d has NUL argument", id, index))
				}
			}
		}
		for index, witness := range value.Witnesses {
			if !oneOf(witness.WitnessType, "go-covered-range", "provider-capture", "event-delivery", "state-transition", "subprocess-invocation", "extension-realization", "terminal-trace", "persistence-roundtrip", "cancellation-trace", "resource-trace") || len(witness.TargetIDs) == 0 || len(witness.SubjectPinIDs) == 0 || len(witness.CommandIndexes) == 0 {
				problems = append(problems, fmt.Sprintf("%s witness %d is incomplete", id, index))
			}
			problems = append(problems, g.expectMany(id, witness.TargetIDs, KindTarget)...)
			problems = append(problems, g.expectMany(id, witness.SubjectPinIDs, KindPin)...)
			if !slices.IsSorted(witness.CommandIndexes) || len(slices.Compact(slices.Clone(witness.CommandIndexes))) != len(witness.CommandIndexes) {
				problems = append(problems, fmt.Sprintf("%s witness %d command indexes must be sorted and unique", id, index))
			}
			for _, commandIndex := range witness.CommandIndexes {
				if commandIndex < 1 || commandIndex > len(value.Commands) {
					problems = append(problems, fmt.Sprintf("%s witness %d command index is out of range", id, index))
				}
			}
			if witness.WitnessType == "go-covered-range" && witness.ArtifactPath == "" {
				problems = append(problems, fmt.Sprintf("%s witness %d has no coverage artifact path", id, index))
			}
			if witness.WitnessType == "provider-capture" && (witness.ArtifactPath != "" || len(witness.CommandIndexes) != 2) {
				problems = append(problems, fmt.Sprintf("%s witness %d has invalid provider capture commands", id, index))
			}
			if isTraceWitness(witness.WitnessType) && (witness.ArtifactPath == "" || len(witness.CommandIndexes) != 1) {
				problems = append(problems, fmt.Sprintf("%s witness %d has invalid typed trace command", id, index))
			}
		}
	case *EvidenceRun:
		problems = append(problems, g.expect(id, value.RequestID, KindEvidenceRequest)...)
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expectMany(id, value.AssertionIDs, KindAssertion)...)
		problems = append(problems, g.expectMany(id, value.WitnessIDs, KindExecutionWitness)...)
		problems = append(problems, g.expectMany(id, value.SubjectPinIDs, KindPin)...)
		if !oneOf(value.Result, "pass", "fail") || value.Executor == "" {
			problems = append(problems, id+" has invalid result or empty executor")
		}
		if expectedID, err := evidenceContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
		if value.AssertionIDs == nil || value.WitnessIDs == nil || value.SubjectPinIDs == nil || value.Environment == nil || value.Commands == nil || value.Artifacts == nil || value.Bindings == nil {
			problems = append(problems, id+" evidence collections must be arrays")
		}
		problems = append(problems, validateArtifacts(id, value.Artifacts)...)
		obligationIDs := make([]string, 0, len(value.Bindings))
		for _, group := range value.Bindings {
			problems = append(problems, g.expect(id, group.ObligationID, KindObligation)...)
			obligationIDs = append(obligationIDs, group.ObligationID)
			recordIDs := make([]string, 0, len(group.Support))
			for _, binding := range group.Support {
				if _, exists := g.Records[binding.RecordID]; !exists {
					problems = append(problems, fmt.Sprintf("%s has unresolved binding %q", id, binding.RecordID))
				}
				recordIDs = append(recordIDs, binding.RecordID)
				problems = append(problems, validateHash(id, "bindings.support.contentHash", binding.ContentHash)...)
			}
			if !sortedUnique(recordIDs) {
				problems = append(problems, id+" binding support must be sorted and unique")
			}
		}
		if !sortedUnique(obligationIDs) {
			problems = append(problems, id+" binding obligations must be sorted and unique")
		}
		problems = append(problems, validateHash(id, "executorHash", value.ExecutorHash)...)
		if !sortedUnique(value.Environment) {
			problems = append(problems, id+" environment must be sorted and unique")
		}
		for index, command := range value.Commands {
			problems = append(problems, validateHash(id, fmt.Sprintf("commands[%d].commandHash", index), command.CommandHash)...)
			problems = append(problems, validateHash(id, fmt.Sprintf("commands[%d].stdoutHash", index), command.StdoutHash)...)
			problems = append(problems, validateHash(id, fmt.Sprintf("commands[%d].stderrHash", index), command.StderrHash)...)
		}
		problems = append(problems, validateHash(id, "toolchainHash", value.ToolchainHash)...)
		problems = append(problems, validateHash(id, "environmentHash", value.EnvironmentHash)...)
		if value.Durability < 1 {
			problems = append(problems, id+" has invalid durability")
		}
		problems = append(problems, validateHash(id, "transcriptHash", value.TranscriptHash)...)
	case *EvidenceAttestation:
		problems = append(problems, g.expect(id, value.EvidenceID, KindEvidenceRun)...)
		problems = append(problems, g.expectMany(id, value.WitnessIDs, KindExecutionWitness)...)
		if expectedID, err := evidenceAttestationContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
		if !validCommit(value.RepositoryCommit) || value.Path == "" || value.WitnessIDs == nil || value.Artifacts == nil {
			problems = append(problems, id+" has invalid repository provenance or collections")
		}
		problems = append(problems, validateArtifacts(id, value.Artifacts)...)
	case *Mutant:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expect(id, value.ObligationID, KindObligation)...)
		problems = append(problems, g.expect(id, value.TestID, KindTest)...)
		problems = append(problems, g.expect(id, value.TargetID, KindTarget)...)
		problems = append(problems, g.expect(id, value.PinID, KindPin)...)
		problems = append(problems, validateHash(id, "expectedFailureHash", value.ExpectedFailureHash)...)
		if err := validateMutantLocal(value); err != nil {
			problems = append(problems, id+" "+err.Error())
		}
		if expectedID, err := mutantContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
	case *MutationRequest:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expect(id, value.ObligationID, KindObligation)...)
		problems = append(problems, g.expect(id, value.TestID, KindTest)...)
		problems = append(problems, g.expectMany(id, value.MutantIDs, KindMutant)...)
		if len(value.MutantIDs) == 0 || value.Environment == nil || value.Commands == nil || value.TimeoutSeconds < 1 || !sortedUnique(value.Environment) {
			problems = append(problems, id+" has invalid mutants, environment, commands, or timeout")
		}
		for index, command := range value.Commands {
			if err := validateMutationCommand(command); err != nil {
				problems = append(problems, fmt.Sprintf("%s command %d: %v", id, index, err))
			}
		}
		if expectedID, err := mutationRequestContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
	case *MutationRun:
		problems = append(problems, g.expect(id, value.RequestID, KindMutationRequest)...)
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		if !validCommit(value.RepositoryCommit) {
			problems = append(problems, id+" has invalid repository provenance")
		}
		problems = append(problems, validateHash(id, "executorHash", value.ExecutorHash)...)
		problems = append(problems, validateHash(id, "toolchainHash", value.ToolchainHash)...)
		problems = append(problems, validateHash(id, "environmentHash", value.EnvironmentHash)...)
		if value.Executor == "" || value.Environment == nil || value.Baselines == nil || value.Commands == nil || value.Results == nil || value.Artifacts == nil || value.Support == nil || !sortedUnique(value.Environment) {
			problems = append(problems, id+" has invalid executor or collections")
		}
		problems = append(problems, validateArtifacts(id, value.Artifacts)...)
		for index, command := range value.Baselines {
			problems = append(problems, validateHash(id, fmt.Sprintf("baselines[%d].commandHash", index), command.CommandHash)...)
			problems = append(problems, validateHash(id, fmt.Sprintf("baselines[%d].stdoutHash", index), command.StdoutHash)...)
			problems = append(problems, validateHash(id, fmt.Sprintf("baselines[%d].stderrHash", index), command.StderrHash)...)
		}
		for index, command := range value.Commands {
			problems = append(problems, validateHash(id, fmt.Sprintf("commands[%d].commandHash", index), command.CommandHash)...)
			problems = append(problems, validateHash(id, fmt.Sprintf("commands[%d].stdoutHash", index), command.StdoutHash)...)
			problems = append(problems, validateHash(id, fmt.Sprintf("commands[%d].stderrHash", index), command.StderrHash)...)
		}
		resultIDs := make([]string, 0, len(value.Results))
		for _, result := range value.Results {
			problems = append(problems, g.expect(id, result.MutantID, KindMutant)...)
			problems = append(problems, validateHash(id, "results.failureHash", result.FailureHash)...)
			problems = append(problems, validateHash(id, "results.mutatedHash", result.MutatedHash)...)
			resultIDs = append(resultIDs, result.MutantID)
			if result.DecisionID != "" {
				problems = append(problems, g.expect(id, result.DecisionID, KindDecision)...)
			}
		}
		if !sortedUnique(resultIDs) {
			problems = append(problems, id+" results must be sorted and unique")
		}
		supportIDs := make([]string, 0, len(value.Support))
		for _, binding := range value.Support {
			if _, ok := g.Records[binding.RecordID]; !ok {
				problems = append(problems, fmt.Sprintf("%s has unresolved support %q", id, binding.RecordID))
			}
			problems = append(problems, validateHash(id, "support.contentHash", binding.ContentHash)...)
			supportIDs = append(supportIDs, binding.RecordID)
		}
		if !sortedUnique(supportIDs) {
			problems = append(problems, id+" support must be sorted and unique")
		}
		if expectedID, err := mutationRunContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
	case *MutationAttestation:
		problems = append(problems, g.expect(id, value.RunID, KindMutationRun)...)
		if !validCommit(value.RepositoryCommit) || value.Path == "" || value.Artifacts == nil {
			problems = append(problems, id+" has invalid repository provenance or artifacts")
		}
		problems = append(problems, validateArtifacts(id, value.Artifacts)...)
		if expectedID, err := mutationAttestationContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
	case *Decision:
		problems = append(problems, g.expectAnyMany(id, value.ScopeIDs)...)
		problems = append(problems, validateHashList(id, "failureHashes", value.FailureHashes)...)
		if value.ValidThroughCommit != "" && !validCommit(value.ValidThroughCommit) {
			problems = append(problems, id+" has invalid validThroughCommit")
		}
		if !oneOf(value.DecisionType, "mapping", "contract-restatement", "accept-divergence", "waive", "defer", "designed-out", "adjudicate", "manual-mutation", "dismiss-agent-finding", "confirm-agent-finding") || value.Rationale == "" || value.Authority == "" || len(value.ScopeIDs) == 0 {
			problems = append(problems, id+" has invalid type or missing rationale, authority, or scope")
		}
	case *Lease:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		if value.WorkUnitID == "" || value.Holder == "" {
			problems = append(problems, id+" requires workUnitId and holder")
		}
		problems = append(problems, validateHash(id, "baseFingerprint", value.BaseFingerprint)...)
		if !oneOf(string(value.State), string(LeaseActive), string(LeaseIntegrated)) {
			problems = append(problems, id+" has invalid lease state")
		}
	case *ScenarioRun:
		problems = append(problems, g.expect(id, value.SnapshotID, KindSnapshot)...)
		problems = append(problems, g.expect(id, value.ScenarioFactID, KindFact)...)
		if fact, ok := g.Records[value.ScenarioFactID].(*Fact); ok && fact.FactType != "denominator:scenario" {
			problems = append(problems, id+" must reference a scenario denominator fact")
		}
		if !oneOf(value.Outcome, ScenarioOutcomePass, ScenarioOutcomeFail) {
			problems = append(problems, id+" has invalid outcome")
		}
		if value.Runs < 1 {
			problems = append(problems, id+" requires at least one run")
		}
		if value.Comparator == "" || strings.ContainsAny(value.Comparator, "|\t\r\n") {
			problems = append(problems, id+" has invalid comparator")
		}
		problems = append(problems, validateHash(id, "commandHash", value.CommandHash)...)
		if value.Artifacts == nil {
			problems = append(problems, id+" requires artifacts")
		}
		problems = append(problems, validateArtifacts(id, value.Artifacts)...)
		if expectedID, err := scenarioRunContentID(value); err != nil || id != expectedID {
			problems = append(problems, id+" is not content-addressed")
		}
	default:
		problems = append(problems, fmt.Sprintf("%s has unsupported Go record type %T", id, record))
	}
	return problems
}

func (g *Graph) expect(owner, reference string, kind Kind) []string {
	if reference == "" {
		return []string{owner + " has empty reference"}
	}
	record, exists := g.Records[reference]
	if !exists {
		return []string{fmt.Sprintf("%s has unresolved reference %q", owner, reference)}
	}
	if record.RecordKind() != kind {
		return []string{fmt.Sprintf("%s reference %q has kind %s, want %s", owner, reference, record.RecordKind(), kind)}
	}
	return nil
}

func (g *Graph) expectMany(owner string, references []string, kind Kind) []string {
	var problems []string
	if !sortedUnique(references) {
		problems = append(problems, owner+" references must be sorted and unique")
	}
	for _, reference := range references {
		problems = append(problems, g.expect(owner, reference, kind)...)
	}
	return problems
}

func (g *Graph) expectAnyMany(owner string, references []string) []string {
	var problems []string
	if !sortedUnique(references) {
		problems = append(problems, owner+" references must be sorted and unique")
	}
	for _, reference := range references {
		if _, exists := g.Records[reference]; !exists {
			problems = append(problems, fmt.Sprintf("%s has unresolved reference %q", owner, reference))
		}
	}
	return problems
}

func (g *Graph) validateAgentHypothesis(hypothesis *Hypothesis) []string {
	if hypothesis.HypothesisType != adversaryProposalType && !strings.HasPrefix(hypothesis.HypothesisType, "agent:") {
		return nil
	}
	var value agentProposalValue
	decoder := json.NewDecoder(strings.NewReader(string(hypothesis.Value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || value.SubmissionID == "" || value.PacketID == "" || value.QuestionID == "" || value.ProposalType == "" || value.Rationale == "" || len(value.Analyses) == 0 || len(value.Citations) < 2 || len(value.Payload) == 0 || !json.Valid(value.Payload) {
		return []string{hypothesis.ID + " has invalid agent proposal value"}
	}
	wantType := "agent:" + value.Role + "-proposal"
	if !oneOf(value.Role, correspondence.AnalystRole, correspondence.AlignmentReviewerRole, correspondence.ContractSynthesizerRole, correspondence.TranslatorRole, correspondence.AdversaryRole) || hypothesis.HypothesisType != wantType {
		return []string{hypothesis.ID + " proposal role does not match its hypothesis type"}
	}
	if !sortedUnique(value.Analyses) {
		return []string{hypothesis.ID + " analyses must be sorted and unique"}
	}
	behaviorID, err := agentQuestionBehaviorID(value.QuestionID)
	if err != nil || behaviorID != hypothesis.SubjectID {
		return []string{hypothesis.ID + " question does not bind its subject behavior"}
	}
	pins, err := proposalCitationPins(g.Records, hypothesis.SnapshotID, value.Citations)
	if err != nil || !slices.Equal(pins, hypothesis.PinIDs) {
		return []string{hypothesis.ID + " citations do not bind its pins"}
	}
	for _, analysis := range value.Analyses {
		obligationID := strings.Replace(hypothesis.SubjectID, "behavior:", "obligation:", 1) + ":" + analysis
		if _, ok := g.Records[obligationID].(*Obligation); !ok {
			return []string{fmt.Sprintf("%s analysis %s has no obligation", hypothesis.ID, analysis)}
		}
	}
	material := strings.Join([]string{value.SubmissionID, value.QuestionID, value.ProposalType}, "\x00")
	if want := "hypothesis:agent:" + strings.TrimPrefix(HashBytes([]byte(material)), "sha256:"); hypothesis.ID != want {
		return []string{hypothesis.ID + " is not content-addressed to its submission proposal"}
	}
	return nil
}

func (g *Graph) validateRelationships() []string {
	var problems []string
	acceptedMappings := make(map[string]string)
	agentFindingDecisions := make(map[string]string)
	mutationAttestations := make(map[string]string)
	for _, id := range sortedRecordIDs(g.Records) {
		switch value := g.Records[id].(type) {
		case *Pin:
			snapshot, ok := g.Records[value.SnapshotID].(*Snapshot)
			if !ok {
				continue
			}
			wantCommit := snapshot.TargetCommit
			if value.Repository == "upstream" {
				wantCommit = snapshot.UpstreamCommit
			}
			if value.Commit != wantCommit {
				problems = append(problems, fmt.Sprintf("%s commit does not match snapshot %s", id, value.SnapshotID))
			}
		case *Target:
			for _, pinID := range value.PinIDs {
				pin, ok := g.Records[pinID].(*Pin)
				if ok && pin.SnapshotID != value.SnapshotID {
					problems = append(problems, fmt.Sprintf("%s pin %s belongs to snapshot %s", id, pinID, pin.SnapshotID))
				}
			}
		case *Mapping:
			if value.Status != "decided" && value.Status != "derived" {
				continue
			}
			if prior := acceptedMappings[value.BehaviorID]; prior != "" {
				problems = append(problems, fmt.Sprintf("%s and %s are competing accepted mappings for %s", prior, id, value.BehaviorID))
			}
			acceptedMappings[value.BehaviorID] = id
			if value.Status == "decided" {
				decision, ok := g.Records[value.DecisionID].(*Decision)
				if ok && (decision.DecisionType != "mapping" || !slices.Contains(decision.ScopeIDs, value.BehaviorID)) {
					problems = append(problems, fmt.Sprintf("%s decision %s does not decide mapping for %s", id, value.DecisionID, value.BehaviorID))
				}
				continue
			}
			problems = append(problems, g.validateDerivedMapping(value)...)
		case *Reachability:
			target, ok := g.Records[value.TargetID].(*Target)
			if !ok {
				continue
			}
			for _, rootPinID := range value.RootPinIDs {
				pin, pinOK := g.Records[rootPinID].(*Pin)
				if pinOK && pin.SnapshotID != target.SnapshotID {
					problems = append(problems, fmt.Sprintf("%s root pin %s differs from target snapshot", id, rootPinID))
				}
			}
		case *Test:
			pinIDs := append([]string{value.PinID}, value.FixturePinIDs...)
			for _, pinID := range pinIDs {
				pin, ok := g.Records[pinID].(*Pin)
				if ok && pin.SnapshotID != value.SnapshotID {
					problems = append(problems, fmt.Sprintf("%s pin %s differs from test snapshot", id, pinID))
				}
			}
		case *EvidenceRequest:
			for _, assertionID := range value.AssertionIDs {
				assertion, ok := g.Records[assertionID].(*Assertion)
				if ok && !g.assertionMatchesAnyObligation(assertion, value.ObligationIDs) {
					problems = append(problems, fmt.Sprintf("%s assertion %s binds no requested obligation", id, assertionID))
				}
			}
			for _, witness := range value.Witnesses {
				for _, targetID := range witness.TargetIDs {
					target, targetOK := g.Records[targetID].(*Target)
					if targetOK && target.SnapshotID != value.SnapshotID {
						problems = append(problems, fmt.Sprintf("%s witness target %s differs from request snapshot", id, targetID))
					}
				}
				for _, pinID := range witness.SubjectPinIDs {
					pin, pinOK := g.Records[pinID].(*Pin)
					if pinOK && pin.SnapshotID != value.SnapshotID {
						problems = append(problems, fmt.Sprintf("%s witness pin %s differs from request snapshot", id, pinID))
					}
				}
			}
		case *ExecutionWitness:
			request, ok := g.Records[value.RequestID].(*EvidenceRequest)
			if !ok {
				continue
			}
			if value.SnapshotID != request.SnapshotID || !witnessRequested(value, request.Witnesses) {
				problems = append(problems, fmt.Sprintf("%s differs from request %s", id, value.RequestID))
			}
			for _, covered := range value.CoveredRanges {
				target, targetOK := g.Records[covered.TargetID].(*Target)
				pin, pinOK := g.Records[covered.PinID].(*Pin)
				if !targetOK || !pinOK {
					continue
				}
				if !slices.Contains(target.PinIDs, covered.PinID) || covered.Path != pin.Path || covered.StartLine != pin.StartLine || covered.EndLine != pin.EndLine {
					problems = append(problems, fmt.Sprintf("%s covered range differs from target pin %s", id, covered.PinID))
				}
			}
			for _, capture := range value.ProviderCaptures {
				if !slices.Contains(value.TargetIDs, capture.TargetID) {
					problems = append(problems, fmt.Sprintf("%s provider capture target %s is not witnessed", id, capture.TargetID))
				}
			}
		case *EvidenceRun:
			request, ok := g.Records[value.RequestID].(*EvidenceRequest)
			if !ok {
				continue
			}
			if value.SnapshotID != request.SnapshotID {
				problems = append(problems, fmt.Sprintf("%s snapshot differs from request %s", id, value.RequestID))
			}
			if value.Durability != request.Durability || len(value.Commands) != request.Durability*len(request.Commands) {
				problems = append(problems, fmt.Sprintf("%s execution count differs from request %s", id, value.RequestID))
			}
			if len(request.Commands) > 0 {
				for index, command := range value.Commands {
					run := index / len(request.Commands)
					commandIndex := index % len(request.Commands)
					expectedHash, hashErr := evidenceCommandHash(run, commandIndex, request.Commands[commandIndex])
					if hashErr != nil || command.Run != run+1 || command.Index != commandIndex+1 || command.CommandHash != expectedHash {
						problems = append(problems, fmt.Sprintf("%s command %d differs from request %s", id, index, value.RequestID))
					}
				}
			}
			transcript, transcriptErr := json.Marshal(value.Commands)
			if transcriptErr != nil || value.TranscriptHash != HashBytes(transcript) {
				problems = append(problems, fmt.Sprintf("%s transcript hash does not match commands", id))
			}
			if expectedResult := evidenceResult(request, value.Commands); value.Result != expectedResult {
				problems = append(problems, fmt.Sprintf("%s result %s does not match execution %s", id, value.Result, expectedResult))
			}
			for _, assertionID := range value.AssertionIDs {
				if !slices.Contains(request.AssertionIDs, assertionID) {
					problems = append(problems, fmt.Sprintf("%s assertion %s was not requested", id, assertionID))
				}
			}
			for _, pinID := range value.SubjectPinIDs {
				pin, pinOK := g.Records[pinID].(*Pin)
				if pinOK && pin.SnapshotID != value.SnapshotID {
					problems = append(problems, fmt.Sprintf("%s subject pin %s differs from evidence snapshot", id, pinID))
				}
			}
			var witnessPinIDs []string
			for _, witnessID := range value.WitnessIDs {
				witness, witnessOK := g.Records[witnessID].(*ExecutionWitness)
				if !witnessOK {
					continue
				}
				if witness.RequestID != value.RequestID || witness.SnapshotID != value.SnapshotID {
					problems = append(problems, fmt.Sprintf("%s witness %s differs from evidence request", id, witnessID))
				}
				witnessPinIDs = append(witnessPinIDs, witness.SubjectPinIDs...)
			}
			slices.Sort(witnessPinIDs)
			witnessPinIDs = slices.Compact(witnessPinIDs)
			if !slices.Equal(value.SubjectPinIDs, witnessPinIDs) {
				problems = append(problems, fmt.Sprintf("%s subject pins differ from witnesses", id))
			}
			if value.Result == "pass" && len(value.WitnessIDs) != len(request.Witnesses) {
				problems = append(problems, fmt.Sprintf("%s witness count differs from request %s", id, value.RequestID))
			}
			if !g.evidenceArtifactsMatch(value) {
				problems = append(problems, fmt.Sprintf("%s artifacts differ from command and witness hashes", id))
			}
		case *EvidenceAttestation:
			evidence, ok := g.Records[value.EvidenceID].(*EvidenceRun)
			if !ok {
				continue
			}
			if !slices.Equal(value.WitnessIDs, evidence.WitnessIDs) || !slices.Equal(value.Artifacts, evidence.Artifacts) {
				problems = append(problems, fmt.Sprintf("%s differs from evidence %s", id, value.EvidenceID))
			}
		case *Mutant:
			obligation, obligationOK := g.Records[value.ObligationID].(*Obligation)
			test, testOK := g.Records[value.TestID].(*Test)
			target, targetOK := g.Records[value.TargetID].(*Target)
			pin, pinOK := g.Records[value.PinID].(*Pin)
			if !obligationOK || !testOK || !targetOK || !pinOK {
				continue
			}
			facet, facetOK := g.Records[obligation.FacetID].(*Facet)
			mapping := g.acceptedMapping(obligation.BehaviorID)
			if test.SnapshotID != value.SnapshotID || target.SnapshotID != value.SnapshotID || pin.SnapshotID != value.SnapshotID || value.Edit.Path != pin.Path || !facetOK || facet.Name != value.Facet || !slices.Contains(target.PinIDs, value.PinID) || mapping == nil || !slices.Contains(mapping.TargetIDs, value.TargetID) || !g.testAssertsObligation(value.TestID, obligation) {
				problems = append(problems, id+" does not bind its snapshot, facet, target pin, and test assertion")
			}
		case *MutationRequest:
			automatic := 0
			for _, mutantID := range value.MutantIDs {
				mutant, ok := g.Records[mutantID].(*Mutant)
				if !ok {
					continue
				}
				if mutant.SnapshotID != value.SnapshotID || mutant.ObligationID != value.ObligationID || mutant.TestID != value.TestID {
					problems = append(problems, fmt.Sprintf("%s mutant %s differs from request scope", id, mutantID))
				}
				if mutant.Mode == "automatic" {
					automatic++
				}
			}
			if len(value.Commands) != automatic {
				problems = append(problems, id+" must contain one command per automatic mutant")
			}
		case *MutationRun:
			request, ok := g.Records[value.RequestID].(*MutationRequest)
			if !ok {
				continue
			}
			snapshot, snapshotOK := g.Records[value.SnapshotID].(*Snapshot)
			if value.SnapshotID != request.SnapshotID || !snapshotOK || value.RepositoryCommit != snapshot.TargetCommit || len(value.Results) != len(request.MutantIDs) || len(value.Commands) != len(request.Commands) || len(value.Baselines) != len(request.Commands) {
				problems = append(problems, id+" differs from mutation request")
			}
			for index := range min(len(value.Commands), len(value.Baselines), len(request.Commands)) {
				command := value.Commands[index]
				expectedHash, hashErr := evidenceCommandHash(0, index, request.Commands[index])
				baseline := value.Baselines[index]
				if hashErr != nil || command.Run != 1 || command.Index != index+1 || command.CommandHash != expectedHash || baseline.Run != 1 || baseline.Index != index+1 || baseline.CommandHash != expectedHash {
					problems = append(problems, fmt.Sprintf("%s command %d differs from mutation request", id, index))
				}
			}
			if !slices.Equal(resultMutantIDs(value.Results), request.MutantIDs) {
				problems = append(problems, id+" results differ from requested mutants")
			}
			commandOwners := make(map[int]string)
			for _, result := range value.Results {
				mutant, mutantOK := g.Records[result.MutantID].(*Mutant)
				if !mutantOK {
					continue
				}
				if mutant.Mode == "automatic" {
					if result.CommandIndex < 1 || result.CommandIndex > len(value.Commands) || result.DecisionID != "" || result.BoundTest != mutant.ExpectedFailureTest || result.MutatedPath != mutant.Edit.Path || result.MutatedHash != mutant.Edit.MutatedHash {
						problems = append(problems, fmt.Sprintf("%s automatic mutant %s has invalid result", id, mutant.ID))
					}
					if prior := commandOwners[result.CommandIndex]; prior != "" {
						problems = append(problems, fmt.Sprintf("%s mutants %s and %s share one command", id, prior, mutant.ID))
					}
					commandOwners[result.CommandIndex] = mutant.ID
					continue
				}
				decision, decisionOK := g.Records[result.DecisionID].(*Decision)
				if result.CommandIndex != 0 || result.BoundTest != "" || result.FailureTest != "" || result.MutatedPath != mutant.Edit.Path || result.MutatedHash != mutant.Edit.MutatedHash || !decisionOK || !g.decisionCurrent(decision) || decision.ValidThroughCommit != snapshot.TargetCommit || decision.DecisionType != "manual-mutation" || !slices.Contains(decision.ScopeIDs, mutant.ID) || !slices.Equal(decision.FailureHashes, []string{result.FailureHash}) {
					problems = append(problems, fmt.Sprintf("%s manual mutant %s lacks an exact reviewed decision", id, mutant.ID))
				}
			}
		case *MutationAttestation:
			run, ok := g.Records[value.RunID].(*MutationRun)
			if ok && !slices.Equal(value.Artifacts, run.Artifacts) {
				problems = append(problems, fmt.Sprintf("%s artifacts differ from mutation run %s", id, value.RunID))
			}
			if prior := mutationAttestations[value.RunID]; prior != "" {
				problems = append(problems, fmt.Sprintf("%s and %s both attest mutation run %s", prior, id, value.RunID))
			}
			mutationAttestations[value.RunID] = id
		case *Decision:
			if value.DecisionType != "dismiss-agent-finding" && value.DecisionType != "confirm-agent-finding" {
				continue
			}
			if len(value.FailureHashes) != 0 {
				problems = append(problems, id+" agent-finding decision cannot contain failure hashes")
			}
			for _, scopeID := range value.ScopeIDs {
				finding, ok := g.Records[scopeID].(*Hypothesis)
				if !ok || finding.HypothesisType != adversaryProposalType {
					problems = append(problems, fmt.Sprintf("%s scope %s is not an adversary finding", id, scopeID))
					continue
				}
				if prior := agentFindingDecisions[scopeID]; prior != "" {
					problems = append(problems, fmt.Sprintf("%s and %s both disposition adversary finding %s", prior, id, scopeID))
				}
				agentFindingDecisions[scopeID] = id
			}
		}
	}
	return problems
}

type directCorrespondenceFact struct {
	RuleID     string `json:"ruleId"`
	SourceID   string `json:"sourceId"`
	TargetID   string `json:"targetId"`
	SourceHash string `json:"sourceHash"`
	TargetHash string `json:"targetHash"`
}

func (g *Graph) validateDerivedMapping(mapping *Mapping) []string {
	behavior, behaviorOK := g.Records[mapping.BehaviorID].(*Behavior)
	if !behaviorOK {
		return nil
	}
	targets := make(map[string]*Target, len(mapping.TargetIDs))
	for _, targetID := range mapping.TargetIDs {
		if target, ok := g.Records[targetID].(*Target); ok {
			targets[targetID] = target
		}
	}
	coveredTargets := make(map[string]struct{}, len(targets))
	var problems []string
	for _, factID := range mapping.FactIDs {
		fact, ok := g.Records[factID].(*Fact)
		if !ok {
			continue
		}
		if fact.FactType != "correspondence:direct" || fact.Resolution != "resolved" || fact.SubjectID != mapping.BehaviorID {
			problems = append(problems, fmt.Sprintf("%s fact %s is not resolved direct correspondence for %s", mapping.ID, factID, mapping.BehaviorID))
			continue
		}
		var value directCorrespondenceFact
		decoder := json.NewDecoder(strings.NewReader(string(fact.Value)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil || value.RuleID != mapping.RuleID || value.SourceID == "" || value.TargetID == "" || !ValidHash(value.SourceHash) || !ValidHash(value.TargetHash) {
			problems = append(problems, fmt.Sprintf("%s fact %s has invalid correspondence value", mapping.ID, factID))
			continue
		}
		if !g.factBindsSemanticPin(fact.PinIDs, behavior.OriginPinIDs, value.SourceID, value.SourceHash) {
			problems = append(problems, fmt.Sprintf("%s fact %s does not bind its source pin", mapping.ID, factID))
			continue
		}
		var matchedTargetIDs []string
		for targetID, target := range targets {
			if g.factBindsSemanticPin(fact.PinIDs, target.PinIDs, value.TargetID, value.TargetHash) {
				matchedTargetIDs = append(matchedTargetIDs, targetID)
			}
		}
		if len(matchedTargetIDs) != 1 {
			problems = append(problems, fmt.Sprintf("%s fact %s does not bind exactly one mapped target pin", mapping.ID, factID))
			continue
		}
		coveredTargets[matchedTargetIDs[0]] = struct{}{}
	}
	for _, targetID := range mapping.TargetIDs {
		if _, covered := coveredTargets[targetID]; !covered {
			problems = append(problems, fmt.Sprintf("%s has no direct correspondence fact for target %s", mapping.ID, targetID))
		}
	}
	return problems
}

func (g *Graph) factBindsSemanticPin(factPinIDs, subjectPinIDs []string, semanticID, hash string) bool {
	for _, pinID := range factPinIDs {
		if !slices.Contains(subjectPinIDs, pinID) {
			continue
		}
		pin, ok := g.Records[pinID].(*Pin)
		if ok && pin.SemanticID == semanticID && pin.QuoteHash == hash {
			return true
		}
	}
	return false
}

func (g *Graph) evidenceBindings(request *EvidenceRequest) []EvidenceObligationBindings {
	bindings := make([]EvidenceObligationBindings, 0, len(request.ObligationIDs))
	for _, obligationID := range request.ObligationIDs {
		bindings = append(bindings, EvidenceObligationBindings{
			ObligationID: obligationID,
			Support:      g.evidenceBindingsForObligation(request, obligationID),
		})
	}
	return bindings
}

func (g *Graph) evidenceBindingsForObligation(request *EvidenceRequest, obligationID string) []EvidenceBinding {
	ids := []string{obligationID}
	if obligation, ok := g.Records[obligationID].(*Obligation); ok {
		ids = append(ids, obligation.BehaviorID, obligation.FacetID, obligation.RuleID)
		ids = append(ids, obligation.OriginPinIDs...)
		behavior := g.Records[obligation.BehaviorID].(*Behavior)
		ids = append(ids, behavior.OriginPinIDs...)
		ids = append(ids, behavior.FactIDs...)
		if mapping := g.acceptedMapping(obligation.BehaviorID); mapping != nil {
			ids = append(ids, mapping.ID)
			ids = append(ids, g.mappingAcceptanceIDs(mapping)...)
			for _, targetID := range mapping.TargetIDs {
				ids = append(ids, targetID)
				target := g.Records[targetID].(*Target)
				ids = append(ids, target.PinIDs...)
				ids = append(ids, target.FactIDs...)
				if reachability := g.productionReachability(obligation.BehaviorID, targetID); reachability != nil {
					ids = append(ids, reachability.ID)
					ids = append(ids, reachability.RootPinIDs...)
				}
			}
		}
		ids = append(ids, g.agentFindingSupportIDs(obligation.BehaviorID)...)
		for _, assertionID := range request.AssertionIDs {
			assertion := g.Records[assertionID].(*Assertion)
			if assertion.BehaviorID != obligation.BehaviorID || assertion.FacetID != obligation.FacetID {
				continue
			}
			ids = append(ids, assertionID, assertion.TestID)
			test := g.Records[assertion.TestID].(*Test)
			ids = append(ids, test.PinID)
			ids = append(ids, test.FixturePinIDs...)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	bindings := make([]EvidenceBinding, 0, len(ids)+1)
	for _, id := range ids {
		if hash := g.RecordHashes[id]; hash != "" {
			bindings = append(bindings, EvidenceBinding{RecordID: id, ContentHash: hash})
		}
	}
	if snapshot, ok := g.Records[request.SnapshotID].(*Snapshot); ok {
		policy, err := json.Marshal(struct {
			ExtractorHashes []string `json:"extractorHashes"`
			RulePackHashes  []string `json:"rulePackHashes"`
		}{ExtractorHashes: snapshot.ExtractorHashes, RulePackHashes: snapshot.RulePackHashes})
		if err == nil {
			bindings = append(bindings, EvidenceBinding{RecordID: snapshot.ID, ContentHash: HashBytes(policy)})
		}
	}
	slices.SortFunc(bindings, func(left, right EvidenceBinding) int { return strings.Compare(left.RecordID, right.RecordID) })
	return bindings
}

func witnessRequested(witness *ExecutionWitness, requests []EvidenceWitnessRequest) bool {
	for _, request := range requests {
		if witness.WitnessType == request.WitnessType && slices.Equal(witness.TargetIDs, request.TargetIDs) && slices.Equal(witness.SubjectPinIDs, request.SubjectPinIDs) {
			return true
		}
	}
	return false
}

func (g *Graph) evidenceArtifactsMatch(evidence *EvidenceRun) bool {
	var expected []EvidenceArtifact
	for _, command := range evidence.Commands {
		expected = append(expected,
			EvidenceArtifact{Path: evidenceStreamArtifactPath(command.Run-1, command.Index-1, "stdout"), Hash: command.StdoutHash},
			EvidenceArtifact{Path: evidenceStreamArtifactPath(command.Run-1, command.Index-1, "stderr"), Hash: command.StderrHash},
		)
	}
	for _, witnessID := range evidence.WitnessIDs {
		if witness, ok := g.Records[witnessID].(*ExecutionWitness); ok {
			expected = append(expected, witness.Artifacts...)
		}
	}
	normalized, err := normalizeEvidenceArtifacts(expected)
	return err == nil && slices.Equal(evidence.Artifacts, normalized)
}

func evidenceResult(request *EvidenceRequest, commands []EvidenceCommandResult) string {
	for _, command := range commands {
		if command.ExitCode != 0 || command.Error != "" || command.Overflow {
			return "fail"
		}
	}
	if request.Comparator == "output-equal" {
		for run := range request.Durability {
			start := run * len(request.Commands)
			if start >= len(commands) {
				return "fail"
			}
			want := commands[start].StdoutHash
			for index := 1; index < len(request.Commands); index++ {
				if start+index >= len(commands) || commands[start+index].StdoutHash != want {
					return "fail"
				}
			}
		}
	}
	return "pass"
}

func (g *Graph) assertionMatchesAnyObligation(assertion *Assertion, obligationIDs []string) bool {
	for _, obligationID := range obligationIDs {
		obligation, ok := g.Records[obligationID].(*Obligation)
		if ok && obligation.BehaviorID == assertion.BehaviorID && obligation.FacetID == assertion.FacetID {
			return true
		}
	}
	return false
}

func expectedIDPrefix(kind Kind) string {
	switch kind {
	case KindSnapshot:
		return "snapshot:"
	case KindPin:
		return "pin:"
	case KindFact:
		return "fact:"
	case KindHypothesis:
		return "hypothesis:"
	case KindProvisionalClaim:
		return "provisional:"
	case KindFacet:
		return "facet:"
	case KindRule:
		return "rule:"
	case KindBehavior:
		return "behavior:"
	case KindObligation:
		return "obligation:"
	case KindTarget:
		return "target:"
	case KindMapping:
		return "mapping:"
	case KindReachability:
		return "reachability:"
	case KindTest:
		return "test:"
	case KindAssertion:
		return "assertion:"
	case KindExecutionWitness:
		return "witness:"
	case KindEvidenceRequest:
		return "request:"
	case KindEvidenceRun:
		return "evidence:"
	case KindEvidenceAttestation:
		return "attestation:"
	case KindMutant:
		return "mutant:"
	case KindMutationRequest:
		return "mutation-request:"
	case KindMutationRun:
		return "mutation-run:"
	case KindMutationAttestation:
		return "mutation-attestation:"
	case KindDecision:
		return "decision:"
	case KindLease:
		return "lease:"
	case KindScenarioRun:
		return scenarioRunPrefix
	default:
		return ""
	}
}

// isWorkerScratchPath reports whether a repository-relative path lives in a
// disposable worker-scratch area. Evidence attributed only to these paths proves
// nothing about production and is inadmissible.
func isWorkerScratchPath(path string) bool {
	clean := filepath.ToSlash(path)
	for _, root := range []string{"tmp", "pig/tmp"} {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return true
		}
	}
	return false
}

func allCoveredRangesScratch(ranges []EvidenceCoveredRange) bool {
	for _, covered := range ranges {
		if !isWorkerScratchPath(covered.Path) {
			return false
		}
	}
	return true
}

func validateArtifacts(id string, artifacts []EvidenceArtifact) []string {
	var problems []string
	paths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Path == "" || filepath.IsAbs(artifact.Path) || artifact.Path == ".." || strings.HasPrefix(filepath.ToSlash(artifact.Path), "../") {
			problems = append(problems, id+" has invalid artifact path")
		}
		problems = append(problems, validateHash(id, "artifacts.hash", artifact.Hash)...)
		paths = append(paths, artifact.Path)
	}
	if !slices.IsSorted(paths) || len(slices.Compact(slices.Clone(paths))) != len(paths) {
		problems = append(problems, id+" artifact paths must be sorted and unique")
	}
	return problems
}

func validateHash(id, field, value string) []string {
	if !ValidHash(value) {
		return []string{fmt.Sprintf("%s has invalid %s %q", id, field, value)}
	}
	return nil
}

func validateOptionalHash(id, field, value string) []string {
	if value == "" {
		return nil
	}
	return validateHash(id, field, value)
}

func validateHashList(id, field string, values []string) []string {
	var problems []string
	if !sortedUnique(values) {
		problems = append(problems, fmt.Sprintf("%s %s must be sorted and unique", id, field))
	}
	for _, value := range values {
		problems = append(problems, validateHash(id, field, value)...)
	}
	return problems
}

func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func oneOf[T comparable](value T, allowed ...T) bool {
	return slices.Contains(allowed, value)
}

func isTraceWitness(witnessType string) bool {
	return oneOf(witnessType, "event-delivery", "state-transition", "subprocess-invocation", "extension-realization", "terminal-trace", "persistence-roundtrip", "cancellation-trace", "resource-trace")
}

func sortedRecordIDs(records map[string]Record) []string {
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
