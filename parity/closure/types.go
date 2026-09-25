package closure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type Kind string

const (
	KindSnapshot            Kind = "snapshot"
	KindPin                 Kind = "pin"
	KindFact                Kind = "fact"
	KindHypothesis          Kind = "hypothesis"
	KindProvisionalClaim    Kind = "provisional-claim"
	KindFacet               Kind = "facet"
	KindRule                Kind = "rule"
	KindBehavior            Kind = "behavior"
	KindObligation          Kind = "obligation"
	KindTarget              Kind = "target"
	KindMapping             Kind = "mapping"
	KindReachability        Kind = "reachability"
	KindTest                Kind = "test"
	KindAssertion           Kind = "assertion"
	KindExecutionWitness    Kind = "execution-witness"
	KindEvidenceRequest     Kind = "evidence-request"
	KindEvidenceRun         Kind = "evidence-run"
	KindEvidenceAttestation Kind = "evidence-attestation"
	KindMutant              Kind = "mutant"
	KindMutationRequest     Kind = "mutation-request"
	KindMutationRun         Kind = "mutation-run"
	KindMutationAttestation Kind = "mutation-attestation"
	KindDecision            Kind = "decision"
	KindLease               Kind = "lease"
	KindScenarioRun         Kind = "scenario-run"
)

type Record interface {
	RecordKind() Kind
	RecordID() string
}

type Snapshot struct {
	Kind            Kind     `json:"kind"`
	ID              string   `json:"id"`
	UpstreamCommit  string   `json:"upstreamCommit"`
	TargetCommit    string   `json:"targetCommit"`
	ToolchainHash   string   `json:"toolchainHash"`
	EnvironmentHash string   `json:"environmentHash"`
	ExtractorHashes []string `json:"extractorHashes,omitempty"`
	RulePackHashes  []string `json:"rulePackHashes,omitempty"`
}

func (r *Snapshot) RecordKind() Kind { return r.Kind }
func (r *Snapshot) RecordID() string { return r.ID }

type Pin struct {
	Kind       Kind   `json:"kind"`
	ID         string `json:"id"`
	SnapshotID string `json:"snapshotId"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	SemanticID string `json:"semanticId"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	QuoteHash  string `json:"quoteHash"`
	APIHash    string `json:"apiHash,omitempty"`
	BodyHash   string `json:"bodyHash,omitempty"`
}

func (r *Pin) RecordKind() Kind { return r.Kind }
func (r *Pin) RecordID() string { return r.ID }

type Fact struct {
	Kind       Kind            `json:"kind"`
	ID         string          `json:"id"`
	SnapshotID string          `json:"snapshotId"`
	FactType   string          `json:"factType"`
	SubjectID  string          `json:"subjectId"`
	Resolution string          `json:"resolution"`
	PinIDs     []string        `json:"pinIds"`
	Value      json.RawMessage `json:"value"`
}

func (r *Fact) RecordKind() Kind { return r.Kind }
func (r *Fact) RecordID() string { return r.ID }

type Hypothesis struct {
	Kind           Kind            `json:"kind"`
	ID             string          `json:"id"`
	SnapshotID     string          `json:"snapshotId"`
	HypothesisType string          `json:"hypothesisType"`
	SubjectID      string          `json:"subjectId"`
	PinIDs         []string        `json:"pinIds"`
	Value          json.RawMessage `json:"value"`
}

func (r *Hypothesis) RecordKind() Kind { return r.Kind }
func (r *Hypothesis) RecordID() string { return r.ID }

type ProvisionalClaim struct {
	Kind        Kind   `json:"kind"`
	ID          string `json:"id"`
	SnapshotID  string `json:"snapshotId"`
	SubjectID   string `json:"subjectId"`
	SourcePinID string `json:"sourcePinId"`
	Status      string `json:"status"`
}

func (r *ProvisionalClaim) RecordKind() Kind { return r.Kind }
func (r *ProvisionalClaim) RecordID() string { return r.ID }

type Facet struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (r *Facet) RecordKind() Kind { return r.Kind }
func (r *Facet) RecordID() string { return r.ID }

type Rule struct {
	Kind           Kind   `json:"kind"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	DefinitionHash string `json:"definitionHash"`
}

func (r *Rule) RecordKind() Kind { return r.Kind }
func (r *Rule) RecordID() string { return r.ID }

type Behavior struct {
	Kind         Kind     `json:"kind"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	OriginPinIDs []string `json:"originPinIds"`
	FactIDs      []string `json:"factIds,omitempty"`
	Profile      string   `json:"profile"`
}

func (r *Behavior) RecordKind() Kind { return r.Kind }
func (r *Behavior) RecordID() string { return r.ID }

type Obligation struct {
	Kind         Kind     `json:"kind"`
	ID           string   `json:"id"`
	BehaviorID   string   `json:"behaviorId"`
	FacetID      string   `json:"facetId"`
	RuleID       string   `json:"ruleId"`
	OriginPinIDs []string `json:"originPinIds"`
}

func (r *Obligation) RecordKind() Kind { return r.Kind }
func (r *Obligation) RecordID() string { return r.ID }

type Target struct {
	Kind       Kind     `json:"kind"`
	ID         string   `json:"id"`
	SnapshotID string   `json:"snapshotId"`
	PinIDs     []string `json:"pinIds"`
	FactIDs    []string `json:"factIds,omitempty"`
	Language   string   `json:"language"`
	Symbol     string   `json:"symbol"`
}

func (r *Target) RecordKind() Kind { return r.Kind }
func (r *Target) RecordID() string { return r.ID }

type Mapping struct {
	Kind       Kind     `json:"kind"`
	ID         string   `json:"id"`
	BehaviorID string   `json:"behaviorId"`
	TargetIDs  []string `json:"targetIds"`
	Status     string   `json:"status"`
	DecisionID string   `json:"decisionId,omitempty"`
	RuleID     string   `json:"ruleId,omitempty"`
	FactIDs    []string `json:"factIds,omitempty"`
}

func (r *Mapping) RecordKind() Kind { return r.Kind }
func (r *Mapping) RecordID() string { return r.ID }

type Reachability struct {
	Kind        Kind     `json:"kind"`
	ID          string   `json:"id"`
	BehaviorID  string   `json:"behaviorId"`
	TargetID    string   `json:"targetId"`
	Class       string   `json:"class"`
	Method      string   `json:"method"`
	RootPinIDs  []string `json:"rootPinIds"`
	Uncertainty string   `json:"uncertainty,omitempty"`
}

func (r *Reachability) RecordKind() Kind { return r.Kind }
func (r *Reachability) RecordID() string { return r.ID }

// TestExecution records how to run a test mechanically, so an autonomous
// verifier can synthesize an EvidenceRequest from the graph rather than from an
// authored command switch (provider-wire derives these today from a hardcoded
// table). The recipe is Go-specific for the common case: run one test function
// in a package with a coverage profile. Heterogeneous recipes (differentials,
// multi-command traces) stay authored and leave Execution empty; a synthesizer
// fails closed for them rather than guessing.
type TestExecution struct {
	Package    string   `json:"package"`
	RunName    string   `json:"runName"`
	BuildTags  []string `json:"buildTags,omitempty"`
	Comparator string   `json:"comparator"`
}

type Test struct {
	Kind           Kind           `json:"kind"`
	ID             string         `json:"id"`
	SnapshotID     string         `json:"snapshotId"`
	PinID          string         `json:"pinId"`
	FixturePinIDs  []string       `json:"fixturePinIds"`
	DefinitionHash string         `json:"definitionHash"`
	MutationPolicy string         `json:"mutationPolicy,omitempty"`
	Execution      *TestExecution `json:"execution,omitempty"`
}

func (r *Test) RecordKind() Kind { return r.Kind }
func (r *Test) RecordID() string { return r.ID }

type Assertion struct {
	Kind       Kind   `json:"kind"`
	ID         string `json:"id"`
	TestID     string `json:"testId"`
	Class      string `json:"class"`
	BehaviorID string `json:"behaviorId"`
	FacetID    string `json:"facetId"`
	Oracle     string `json:"oracle"`
}

func (r *Assertion) RecordKind() Kind { return r.Kind }
func (r *Assertion) RecordID() string { return r.ID }

type ExecutionWitness struct {
	Kind             Kind                      `json:"kind"`
	ID               string                    `json:"id"`
	SnapshotID       string                    `json:"snapshotId"`
	RequestID        string                    `json:"requestId"`
	WitnessType      string                    `json:"witnessType"`
	TargetIDs        []string                  `json:"targetIds"`
	SubjectPinIDs    []string                  `json:"subjectPinIds"`
	Artifacts        []EvidenceArtifact        `json:"artifacts"`
	CoveredRanges    []EvidenceCoveredRange    `json:"coveredRanges"`
	ProviderCaptures []EvidenceProviderCapture `json:"providerCaptures"`
}

func (r *ExecutionWitness) RecordKind() Kind { return r.Kind }
func (r *ExecutionWitness) RecordID() string { return r.ID }

type EvidenceRequest struct {
	Kind           Kind                     `json:"kind"`
	ID             string                   `json:"id"`
	SnapshotID     string                   `json:"snapshotId"`
	ObligationIDs  []string                 `json:"obligationIds"`
	AssertionIDs   []string                 `json:"assertionIds"`
	Commands       []EvidenceCommand        `json:"commands"`
	Environment    []string                 `json:"environment"`
	Witnesses      []EvidenceWitnessRequest `json:"witnesses"`
	Durability     int                      `json:"durability"`
	Comparator     string                   `json:"comparator"`
	TimeoutSeconds int                      `json:"timeoutSeconds"`
}

func (r *EvidenceRequest) RecordKind() Kind { return r.Kind }
func (r *EvidenceRequest) RecordID() string { return r.ID }

type EvidenceCommand struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

type EvidenceWitnessRequest struct {
	WitnessType    string   `json:"witnessType"`
	TargetIDs      []string `json:"targetIds"`
	SubjectPinIDs  []string `json:"subjectPinIds"`
	CommandIndexes []int    `json:"commandIndexes"`
	ArtifactPath   string   `json:"artifactPath,omitempty"`
}

type EvidenceArtifact struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type EvidenceBinding struct {
	RecordID    string `json:"recordId"`
	ContentHash string `json:"contentHash"`
}

type EvidenceObligationBindings struct {
	ObligationID string            `json:"obligationId"`
	Support      []EvidenceBinding `json:"support"`
}

type EvidenceCoveredRange struct {
	TargetID  string `json:"targetId"`
	PinID     string `json:"pinId"`
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Count     int    `json:"count"`
}

type EvidenceProviderCapture struct {
	TargetID           string `json:"targetId"`
	OracleArtifactHash string `json:"oracleArtifactHash"`
	TargetArtifactHash string `json:"targetArtifactHash"`
}

type EvidenceTraceArtifact struct {
	WitnessType string                         `json:"witnessType"`
	Captures    []EvidenceTraceArtifactCapture `json:"captures"`
}

type EvidenceTraceArtifactCapture struct {
	TargetID string               `json:"targetId"`
	Events   []EvidenceTraceEvent `json:"events"`
}

type EvidenceTraceEvent struct {
	Ordinal   int    `json:"ordinal"`
	Kind      string `json:"kind"`
	Subject   string `json:"subject"`
	ValueHash string `json:"valueHash"`
}

type EvidenceRun struct {
	Kind            Kind                         `json:"kind"`
	ID              string                       `json:"id"`
	RequestID       string                       `json:"requestId"`
	SnapshotID      string                       `json:"snapshotId"`
	Result          string                       `json:"result"`
	AssertionIDs    []string                     `json:"assertionIds"`
	WitnessIDs      []string                     `json:"witnessIds"`
	SubjectPinIDs   []string                     `json:"subjectPinIds"`
	Executor        string                       `json:"executor"`
	ExecutorHash    string                       `json:"executorHash"`
	Environment     []string                     `json:"environment"`
	Commands        []EvidenceCommandResult      `json:"commands"`
	Artifacts       []EvidenceArtifact           `json:"artifacts"`
	Bindings        []EvidenceObligationBindings `json:"bindings"`
	ToolchainHash   string                       `json:"toolchainHash"`
	EnvironmentHash string                       `json:"environmentHash"`
	Durability      int                          `json:"durability"`
	TranscriptHash  string                       `json:"transcriptHash"`
}

func (r *EvidenceRun) RecordKind() Kind { return r.Kind }
func (r *EvidenceRun) RecordID() string { return r.ID }

type EvidenceAttestation struct {
	Kind             Kind               `json:"kind"`
	ID               string             `json:"id"`
	EvidenceID       string             `json:"evidenceId"`
	WitnessIDs       []string           `json:"witnessIds"`
	RepositoryCommit string             `json:"repositoryCommit"`
	Path             string             `json:"path"`
	Artifacts        []EvidenceArtifact `json:"artifacts"`
}

func (r *EvidenceAttestation) RecordKind() Kind { return r.Kind }
func (r *EvidenceAttestation) RecordID() string { return r.ID }

type EvidenceCommandResult struct {
	Run         int    `json:"run"`
	Index       int    `json:"index"`
	CommandHash string `json:"commandHash"`
	StdoutHash  string `json:"stdoutHash"`
	StderrHash  string `json:"stderrHash"`
	ExitCode    int    `json:"exitCode"`
	Error       string `json:"error,omitempty"`
	Overflow    bool   `json:"overflow,omitempty"`
}

type Mutant struct {
	Kind                Kind         `json:"kind"`
	ID                  string       `json:"id"`
	SnapshotID          string       `json:"snapshotId"`
	ObligationID        string       `json:"obligationId"`
	TestID              string       `json:"testId"`
	TargetID            string       `json:"targetId"`
	PinID               string       `json:"pinId"`
	Facet               string       `json:"facet"`
	Mode                string       `json:"mode"`
	Operator            string       `json:"operator"`
	Edit                MutationEdit `json:"edit"`
	ExpectedFailureTest string       `json:"expectedFailureTest,omitempty"`
	ExpectedFailureHash string       `json:"expectedFailureHash"`
}

func (r *Mutant) RecordKind() Kind { return r.Kind }
func (r *Mutant) RecordID() string { return r.ID }

type MutationEdit struct {
	Path         string `json:"path"`
	OriginalHash string `json:"originalHash"`
	Before       string `json:"before"`
	After        string `json:"after"`
	MutatedHash  string `json:"mutatedHash"`
}

type MutationRequest struct {
	Kind           Kind              `json:"kind"`
	ID             string            `json:"id"`
	SnapshotID     string            `json:"snapshotId"`
	ObligationID   string            `json:"obligationId"`
	TestID         string            `json:"testId"`
	MutantIDs      []string          `json:"mutantIds"`
	Commands       []EvidenceCommand `json:"commands"`
	Environment    []string          `json:"environment"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
}

func (r *MutationRequest) RecordKind() Kind { return r.Kind }
func (r *MutationRequest) RecordID() string { return r.ID }

type MutationResult struct {
	MutantID     string `json:"mutantId"`
	CommandIndex int    `json:"commandIndex,omitempty"`
	FailureHash  string `json:"failureHash"`
	BoundTest    string `json:"boundTest,omitempty"`
	FailureTest  string `json:"failureTest,omitempty"`
	MutatedPath  string `json:"mutatedPath"`
	MutatedHash  string `json:"mutatedHash"`
	DecisionID   string `json:"decisionId,omitempty"`
}

type MutationRun struct {
	Kind             Kind                    `json:"kind"`
	ID               string                  `json:"id"`
	RequestID        string                  `json:"requestId"`
	SnapshotID       string                  `json:"snapshotId"`
	RepositoryCommit string                  `json:"repositoryCommit"`
	Executor         string                  `json:"executor"`
	ExecutorHash     string                  `json:"executorHash"`
	Environment      []string                `json:"environment"`
	Baselines        []EvidenceCommandResult `json:"baselines"`
	Commands         []EvidenceCommandResult `json:"commands"`
	Results          []MutationResult        `json:"results"`
	Artifacts        []EvidenceArtifact      `json:"artifacts"`
	Support          []EvidenceBinding       `json:"support"`
	ToolchainHash    string                  `json:"toolchainHash"`
	EnvironmentHash  string                  `json:"environmentHash"`
}

func (r *MutationRun) RecordKind() Kind { return r.Kind }
func (r *MutationRun) RecordID() string { return r.ID }

type MutationAttestation struct {
	Kind             Kind               `json:"kind"`
	ID               string             `json:"id"`
	RunID            string             `json:"runId"`
	RepositoryCommit string             `json:"repositoryCommit"`
	Path             string             `json:"path"`
	Artifacts        []EvidenceArtifact `json:"artifacts"`
}

func (r *MutationAttestation) RecordKind() Kind { return r.Kind }
func (r *MutationAttestation) RecordID() string { return r.ID }

type Decision struct {
	Kind               Kind     `json:"kind"`
	ID                 string   `json:"id"`
	DecisionType       string   `json:"decisionType"`
	ScopeIDs           []string `json:"scopeIds"`
	Rationale          string   `json:"rationale"`
	Authority          string   `json:"authority"`
	ReviewWhen         string   `json:"reviewWhen,omitempty"`
	ValidThroughCommit string   `json:"validThroughCommit,omitempty"`
	FailureHashes      []string `json:"failureHashes,omitempty"`
}

func (r *Decision) RecordKind() Kind { return r.Kind }
func (r *Decision) RecordID() string { return r.ID }

type VerdictState string

const (
	VerdictOpen         VerdictState = "open"
	VerdictProvisional  VerdictState = "provisional"
	VerdictProven       VerdictState = "proven"
	VerdictWaived       VerdictState = "waived"
	VerdictContradicted VerdictState = "contradicted"
)

type Verdict struct {
	ObligationID  string       `json:"obligationId"`
	State         VerdictState `json:"state"`
	SupportHashes []string     `json:"supportHashes"`
	Reason        string       `json:"reason"`
}

func HashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ContentHash(record Record) (string, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode %s %s: %w", record.RecordKind(), record.RecordID(), err)
	}
	return HashBytes(encoded), nil
}

func ValidHash(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func sortedUnique(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	return len(values) == 0 || len(slices.Compact(values)) == len(values)
}
