package correspondence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

const maxAgentContentBytes = 4 << 20

const (
	AnalystRole             = "analyst"
	ContractSynthesizerRole = "contract-synthesizer"
	TranslatorRole          = "translator"
	AdversaryRole           = "adversary"
)

type AgentWorkPacket struct {
	ID                 string              `json:"id"`
	Role               string              `json:"role"`
	SnapshotID         string              `json:"snapshotId"`
	AlignmentPacketID  string              `json:"alignmentPacketId"`
	Source             SourceIdentity      `json:"source"`
	Target             SourceIdentity      `json:"target"`
	Questions          []AgentWorkQuestion `json:"questions"`
	UnresolvedEdges    []string            `json:"unresolvedEdges"`
	ObligationIDs      []string            `json:"obligationIds"`
	EvidenceIDs        []string            `json:"evidenceIds"`
	ReadPaths          []string            `json:"readPaths"`
	WritePaths         []string            `json:"writePaths"`
	GeneratedPaths     []string            `json:"generatedPaths"`
	FixturePaths       []string            `json:"fixturePaths"`
	TestPaths          []string            `json:"testPaths,omitempty"`
	SemanticBoundaries []string            `json:"semanticBoundaries"`
	ForbiddenActions   []string            `json:"forbiddenActions"`
	Budget             AgentWorkBudget     `json:"budget"`
	OutputSchema       string              `json:"outputSchema"`
}

type AgentWorkScope struct {
	SnapshotID    string
	ObligationIDs []string
	EvidenceIDs   []string
	FixturePaths  []string
	TestPaths     []string
}

func NewAgentWorkScope(alignment *AlignmentWorkPacket, snapshotID string, evidenceIDs, fixturePaths, testPaths []string) (AgentWorkScope, error) {
	if alignment == nil || snapshotID == "" {
		return AgentWorkScope{}, fmt.Errorf("alignment packet and snapshot identity are required")
	}
	if err := alignment.Validate(); err != nil {
		return AgentWorkScope{}, err
	}
	questions := make([]AgentWorkQuestion, 0, len(alignment.Questions)+len(alignment.Functions))
	for _, question := range alignment.Questions {
		questions = append(questions, AgentWorkQuestion{ID: question.ID, RequiredAnalyses: question.RequiredAnalyses})
	}
	for _, question := range alignment.Functions {
		questions = append(questions, AgentWorkQuestion{ID: question.ID, RequiredAnalyses: question.RequiredAnalyses})
	}
	return AgentWorkScope{
		SnapshotID: snapshotID, ObligationIDs: agentObligationIDs(questions),
		EvidenceIDs: slices.Clone(evidenceIDs), FixturePaths: slices.Clone(fixturePaths), TestPaths: slices.Clone(testPaths),
	}, nil
}

type AgentWorkQuestion struct {
	ID               string              `json:"id"`
	RequiredAnalyses []string            `json:"requiredAnalyses"`
	Citations        []AlignmentCitation `json:"citations"`
}

type AgentWorkBudget struct {
	MaxFindings  int `json:"maxFindings"`
	MaxCitations int `json:"maxCitations"`
}

type AnalystBundle struct {
	ID       string           `json:"id"`
	Role     string           `json:"role"`
	PacketID string           `json:"packetId"`
	Findings []AnalystFinding `json:"findings"`
}

type AnalystFinding struct {
	QuestionID string              `json:"questionId"`
	Proposal   string              `json:"proposal"`
	Rationale  string              `json:"rationale"`
	Analyses   []string            `json:"analyses"`
	Citations  []AlignmentCitation `json:"citations"`
}

type AlignmentBundle struct {
	ID       string                   `json:"id"`
	Role     string                   `json:"role"`
	PacketID string                   `json:"packetId"`
	Findings []AlignmentBundleFinding `json:"findings"`
}

type AlignmentBundleFinding struct {
	QuestionID string              `json:"questionId"`
	Proposal   string              `json:"proposal"`
	Rationale  string              `json:"rationale"`
	Citations  []AlignmentCitation `json:"citations"`
}

type ContractBundle struct {
	ID        string             `json:"id"`
	Role      string             `json:"role"`
	PacketID  string             `json:"packetId"`
	Proposals []ContractProposal `json:"proposals"`
}

type ContractProposal struct {
	QuestionID      string              `json:"questionId"`
	Analysis        string              `json:"analysis"`
	Class           string              `json:"class"`
	Oracle          string              `json:"oracle"`
	TestRequirement string              `json:"testRequirement"`
	WitnessType     string              `json:"witnessType"`
	Citations       []AlignmentCitation `json:"citations"`
}

type TranslatorBundle struct {
	ID           string                `json:"id"`
	Role         string                `json:"role"`
	PacketID     string                `json:"packetId"`
	Translations []TranslationProposal `json:"translations"`
}

type TranslationProposal struct {
	QuestionID        string              `json:"questionId"`
	Analysis          string              `json:"analysis"`
	Rationale         string              `json:"rationale"`
	ProductionEdits   []TranslationEdit   `json:"productionEdits"`
	TestEdits         []TranslationEdit   `json:"testEdits"`
	EvidencePlan      string              `json:"evidencePlan"`
	MutationOperators []string            `json:"mutationOperators"`
	Citations         []AlignmentCitation `json:"citations"`
}

type TranslationEdit struct {
	Path         string `json:"path"`
	OriginalHash string `json:"originalHash"`
	Replacement  string `json:"replacement"`
}

type AdversaryBundle struct {
	ID       string             `json:"id"`
	Role     string             `json:"role"`
	PacketID string             `json:"packetId"`
	Findings []AdversaryFinding `json:"findings"`
}

type AdversaryFinding struct {
	QuestionID string              `json:"questionId"`
	Fault      string              `json:"fault"`
	Rationale  string              `json:"rationale"`
	Analyses   []string            `json:"analyses"`
	Citations  []AlignmentCitation `json:"citations"`
}

type AgentContent interface {
	agentContent()
}

func (*AgentWorkPacket) agentContent()  {}
func (*AnalystBundle) agentContent()    {}
func (*AlignmentBundle) agentContent()  {}
func (*ContractBundle) agentContent()   {}
func (*TranslatorBundle) agentContent() {}
func (*AdversaryBundle) agentContent()  {}

func BuildAgentWorkPacket(alignment *AlignmentWorkPacket, role string, unresolvedEdges []string, scope AgentWorkScope) (*AgentWorkPacket, error) {
	if alignment == nil {
		return nil, fmt.Errorf("alignment packet is required")
	}
	if err := alignment.Validate(); err != nil {
		return nil, err
	}
	outputSchema := ""
	switch role {
	case AnalystRole:
		outputSchema = "analyst-bundle"
	case AlignmentReviewerRole:
		outputSchema = "alignment-bundle"
	case ContractSynthesizerRole:
		outputSchema = "contract-bundle"
	case TranslatorRole:
		outputSchema = "translator-bundle"
	case AdversaryRole:
		outputSchema = "adversary-bundle"
	default:
		return nil, fmt.Errorf("unsupported agent role %q", role)
	}
	questions := make([]AgentWorkQuestion, 0, len(alignment.Questions)+len(alignment.Functions))
	readPathSet := make(map[string]struct{})
	boundarySet := make(map[string]struct{})
	for _, question := range alignment.Questions {
		citations := questionCitations(question)
		questions = append(questions, AgentWorkQuestion{ID: question.ID, RequiredAnalyses: slices.Clone(question.RequiredAnalyses), Citations: citations})
		for _, citation := range citations {
			readPathSet[citation.Path] = struct{}{}
		}
		for _, analysis := range question.RequiredAnalyses {
			boundarySet[analysis] = struct{}{}
		}
	}
	for _, question := range alignment.Functions {
		citations := functionQuestionCitations(question.Source, question.Target)
		questions = append(questions, AgentWorkQuestion{ID: question.ID, RequiredAnalyses: slices.Clone(question.RequiredAnalyses), Citations: citations})
		for _, citation := range citations {
			readPathSet[citation.Path] = struct{}{}
		}
		for _, analysis := range question.RequiredAnalyses {
			boundarySet[analysis] = struct{}{}
		}
	}
	slices.SortFunc(questions, func(left, right AgentWorkQuestion) int { return strings.Compare(left.ID, right.ID) })
	packet := &AgentWorkPacket{
		Role: role, SnapshotID: scope.SnapshotID, AlignmentPacketID: alignment.ID, Source: alignment.Source, Target: alignment.Target,
		Questions: questions, UnresolvedEdges: slices.Clone(unresolvedEdges), ReadPaths: slices.Sorted(maps.Keys(readPathSet)),
		ObligationIDs: slices.Clone(scope.ObligationIDs), EvidenceIDs: slices.Clone(scope.EvidenceIDs),
		WritePaths: []string{}, GeneratedPaths: []string{}, FixturePaths: slices.Clone(scope.FixturePaths), TestPaths: slices.Clone(scope.TestPaths),
		SemanticBoundaries: slices.Sorted(maps.Keys(boundarySet)),
		ForbiddenActions:   forbiddenActions(), Budget: AgentWorkBudget{MaxFindings: len(questions), MaxCitations: len(questions) * 8},
		OutputSchema: outputSchema,
	}
	slices.Sort(packet.UnresolvedEdges)
	slices.Sort(packet.ObligationIDs)
	slices.Sort(packet.EvidenceIDs)
	slices.Sort(packet.FixturePaths)
	slices.Sort(packet.TestPaths)
	id, err := agentContentID("packet", packet)
	if err != nil {
		return nil, err
	}
	packet.ID = id
	if err := packet.Validate(); err != nil {
		return nil, err
	}
	return packet, nil
}

func DecodeAgentWorkPacket(reader io.Reader) (*AgentWorkPacket, error) {
	var packet AgentWorkPacket
	if err := decodeAgentJSON(reader, &packet, "agent work packet"); err != nil {
		return nil, err
	}
	if err := packet.Validate(); err != nil {
		return nil, err
	}
	return &packet, nil
}

func (packet *AgentWorkPacket) Validate() error {
	if packet == nil || !strings.HasPrefix(packet.SnapshotID, "snapshot:") || packet.AlignmentPacketID == "" || packet.OutputSchema == "" || len(packet.Questions) == 0 || len(packet.ObligationIDs) == 0 || packet.EvidenceIDs == nil || packet.WritePaths == nil || len(packet.WritePaths) != 0 || packet.GeneratedPaths == nil || len(packet.GeneratedPaths) != 0 || packet.FixturePaths == nil {
		return fmt.Errorf("agent work packet has incomplete identity or is not read-only")
	}
	if packet.Source.Language != LanguageTypeScript || packet.Source.Revision == "" || packet.Target.Language != LanguageGo || packet.Target.Revision == "" {
		return fmt.Errorf("agent work packet has invalid source or target identity")
	}
	if !oneOfString(packet.Role, AnalystRole, AlignmentReviewerRole, ContractSynthesizerRole, TranslatorRole, AdversaryRole) || packet.Budget.MaxFindings < 1 || packet.Budget.MaxCitations < packet.Budget.MaxFindings {
		return fmt.Errorf("agent work packet has invalid role or budget")
	}
	if packet.Role == TranslatorRole && len(packet.TestPaths) == 0 {
		return fmt.Errorf("translator work packet has no regression-test paths")
	}
	wantSchema := map[string]string{AnalystRole: "analyst-bundle", AlignmentReviewerRole: "alignment-bundle", ContractSynthesizerRole: "contract-bundle", TranslatorRole: "translator-bundle", AdversaryRole: "adversary-bundle"}[packet.Role]
	if packet.OutputSchema != wantSchema || !slices.Equal(packet.ForbiddenActions, forbiddenActions()) || !sortedUniqueStrings(packet.UnresolvedEdges) || !sortedUniqueStrings(packet.ObligationIDs) || !sortedUniqueStrings(packet.EvidenceIDs) || !sortedUniqueStrings(packet.ReadPaths) || !sortedUniqueStrings(packet.FixturePaths) || !sortedUniqueStrings(packet.TestPaths) || !sortedUniqueStrings(packet.SemanticBoundaries) {
		return fmt.Errorf("agent work packet authority or sets are invalid")
	}
	if !slices.IsSortedFunc(packet.Questions, func(left, right AgentWorkQuestion) int { return strings.Compare(left.ID, right.ID) }) {
		return fmt.Errorf("agent work packet questions are not sorted")
	}
	seen := make(map[string]struct{}, len(packet.Questions))
	for _, question := range packet.Questions {
		if question.ID == "" || !sortedUniqueStrings(question.RequiredAnalyses) || len(question.Citations) < 2 {
			return fmt.Errorf("agent work packet question %q is incomplete", question.ID)
		}
		if _, exists := seen[question.ID]; exists {
			return fmt.Errorf("agent work packet duplicates question %s", question.ID)
		}
		seen[question.ID] = struct{}{}
	}
	for _, unresolved := range packet.UnresolvedEdges {
		if _, exists := seen[unresolved]; !exists {
			return fmt.Errorf("agent work packet unresolved edge %s is outside scope", unresolved)
		}
	}
	if !slices.Equal(packet.ObligationIDs, agentObligationIDs(packet.Questions)) {
		return fmt.Errorf("agent work packet obligation scope differs from questions")
	}
	for _, evidenceID := range packet.EvidenceIDs {
		if !strings.HasPrefix(evidenceID, "evidence:") {
			return fmt.Errorf("agent work packet has invalid evidence identity %s", evidenceID)
		}
	}
	paths := append(slices.Clone(packet.ReadPaths), packet.FixturePaths...)
	paths = append(paths, packet.TestPaths...)
	for _, path := range paths {
		clean := filepath.ToSlash(filepath.Clean(path))
		if path == "" || path != strings.TrimSpace(path) || strings.ContainsRune(path, '\x00') || filepath.IsAbs(path) || !filepath.IsLocal(path) || clean != path {
			return fmt.Errorf("agent work packet has escaping path %s", path)
		}
	}
	wantID, err := agentContentID("packet", packet)
	if err != nil {
		return err
	}
	if packet.ID != wantID {
		return fmt.Errorf("agent work packet ID does not match its content")
	}
	return nil
}

func agentObligationIDs(questions []AgentWorkQuestion) []string {
	var ids []string
	for _, question := range questions {
		behavior := ""
		if setting, ok := strings.CutPrefix(question.ID, "alignment:settings:"); ok {
			behavior = "setting:" + setting
		} else if separator := strings.LastIndexByte(question.ID, ':'); separator >= 0 {
			behavior = "function:" + question.ID[separator+1:]
		}
		for _, analysis := range question.RequiredAnalyses {
			ids = append(ids, "obligation:correspondence:"+behavior+":"+analysis)
		}
	}
	slices.Sort(ids)
	return ids
}

func BindAnalystBundle(packet *AgentWorkPacket, findings []AnalystFinding) (*AnalystBundle, error) {
	bundle := &AnalystBundle{Role: AnalystRole, PacketID: packet.ID, Findings: slices.Clone(findings)}
	var err error
	bundle.ID, err = agentContentID("bundle", bundle)
	if err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return bundle, nil
}

func DecodeAnalystBundle(reader io.Reader, packet *AgentWorkPacket) (*AnalystBundle, error) {
	var bundle AnalystBundle
	if err := decodeAgentJSON(reader, &bundle, "analyst bundle"); err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (bundle *AnalystBundle) Validate(packet *AgentWorkPacket) error {
	if bundle == nil || packet == nil || packet.Role != AnalystRole || bundle.Role != AnalystRole || bundle.PacketID != packet.ID || len(bundle.Findings) == 0 || len(bundle.Findings) > packet.Budget.MaxFindings {
		return fmt.Errorf("analyst bundle has incomplete identity or findings")
	}
	if !slices.IsSortedFunc(bundle.Findings, func(left, right AnalystFinding) int { return strings.Compare(left.QuestionID, right.QuestionID) }) {
		return fmt.Errorf("analyst bundle findings are not sorted")
	}
	seen := make(map[string]struct{}, len(bundle.Findings))
	citationCount := 0
	for _, finding := range bundle.Findings {
		if !oneOfString(finding.Proposal, "gap", "match-candidate", "ambiguous", "blocked") || finding.Rationale == "" {
			return fmt.Errorf("analyst finding %q is invalid", finding.QuestionID)
		}
		if err := validateAgentFinding(packet, finding.QuestionID, finding.Analyses, finding.Citations); err != nil {
			return err
		}
		if _, exists := seen[finding.QuestionID]; exists {
			return fmt.Errorf("analyst bundle duplicates question %s", finding.QuestionID)
		}
		seen[finding.QuestionID] = struct{}{}
		citationCount += len(finding.Citations)
	}
	if citationCount > packet.Budget.MaxCitations {
		return fmt.Errorf("analyst bundle exceeds citation budget")
	}
	wantID, err := agentContentID("bundle", bundle)
	if err != nil {
		return err
	}
	if bundle.ID != wantID {
		return fmt.Errorf("analyst bundle ID does not match its content")
	}
	return nil
}

func BindAlignmentBundle(packet *AgentWorkPacket, findings []AlignmentBundleFinding) (*AlignmentBundle, error) {
	bundle := &AlignmentBundle{Role: AlignmentReviewerRole, PacketID: packet.ID, Findings: slices.Clone(findings)}
	var err error
	bundle.ID, err = agentContentID("bundle", bundle)
	if err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return bundle, nil
}

func DecodeAlignmentBundle(reader io.Reader, packet *AgentWorkPacket) (*AlignmentBundle, error) {
	var bundle AlignmentBundle
	if err := decodeAgentJSON(reader, &bundle, "alignment bundle"); err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (bundle *AlignmentBundle) Validate(packet *AgentWorkPacket) error {
	if bundle == nil || packet == nil || packet.Role != AlignmentReviewerRole || bundle.Role != AlignmentReviewerRole || bundle.PacketID != packet.ID || len(bundle.Findings) != len(packet.Questions) {
		return fmt.Errorf("alignment bundle has incomplete identity or findings")
	}
	if !slices.IsSortedFunc(bundle.Findings, func(left, right AlignmentBundleFinding) int {
		return strings.Compare(left.QuestionID, right.QuestionID)
	}) {
		return fmt.Errorf("alignment bundle findings are not sorted")
	}
	seen := make(map[string]struct{}, len(bundle.Findings))
	citationCount := 0
	for _, finding := range bundle.Findings {
		if !oneOfString(finding.Proposal, "aligned", "mismatch", "ambiguous", "blocked") || finding.Rationale == "" {
			return fmt.Errorf("alignment finding %q is invalid", finding.QuestionID)
		}
		question, ok := agentQuestion(packet, finding.QuestionID)
		if !ok {
			return fmt.Errorf("alignment finding %q is outside scope", finding.QuestionID)
		}
		if err := validateAgentFinding(packet, finding.QuestionID, question.RequiredAnalyses, finding.Citations); err != nil {
			return err
		}
		if _, exists := seen[finding.QuestionID]; exists {
			return fmt.Errorf("alignment bundle duplicates question %s", finding.QuestionID)
		}
		seen[finding.QuestionID] = struct{}{}
		citationCount += len(finding.Citations)
	}
	if citationCount > packet.Budget.MaxCitations {
		return fmt.Errorf("alignment bundle exceeds citation budget")
	}
	wantID, err := agentContentID("bundle", bundle)
	if err != nil {
		return err
	}
	if bundle.ID != wantID {
		return fmt.Errorf("alignment bundle ID does not match its content")
	}
	return nil
}

func agentQuestion(packet *AgentWorkPacket, questionID string) (AgentWorkQuestion, bool) {
	for _, question := range packet.Questions {
		if question.ID == questionID {
			return question, true
		}
	}
	return AgentWorkQuestion{}, false
}

func BindContractBundle(packet *AgentWorkPacket, proposals []ContractProposal) (*ContractBundle, error) {
	bundle := &ContractBundle{Role: ContractSynthesizerRole, PacketID: packet.ID, Proposals: slices.Clone(proposals)}
	var err error
	bundle.ID, err = agentContentID("bundle", bundle)
	if err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return bundle, nil
}

func DecodeContractBundle(reader io.Reader, packet *AgentWorkPacket) (*ContractBundle, error) {
	var bundle ContractBundle
	if err := decodeAgentJSON(reader, &bundle, "contract bundle"); err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (bundle *ContractBundle) Validate(packet *AgentWorkPacket) error {
	if bundle == nil || packet == nil || packet.Role != ContractSynthesizerRole || bundle.Role != ContractSynthesizerRole || bundle.PacketID != packet.ID || len(bundle.Proposals) == 0 || len(bundle.Proposals) > packet.Budget.MaxFindings {
		return fmt.Errorf("contract bundle has incomplete identity or proposals")
	}
	if !slices.IsSortedFunc(bundle.Proposals, func(left, right ContractProposal) int {
		return strings.Compare(left.QuestionID+"\x00"+left.Analysis, right.QuestionID+"\x00"+right.Analysis)
	}) {
		return fmt.Errorf("contract bundle proposals are not sorted")
	}
	seen := make(map[string]struct{}, len(bundle.Proposals))
	citationCount := 0
	for _, proposal := range bundle.Proposals {
		if !oneOfString(proposal.Class, "A2", "A3") || proposal.Oracle == "" || proposal.TestRequirement == "" || !oneOfString(proposal.WitnessType, "event-delivery", "state-transition", "terminal-trace", "persistence-roundtrip", "cancellation-trace", "extension-realization", "resource-trace") || !analysisWitnessAllowed(proposal.Analysis, proposal.WitnessType) {
			return fmt.Errorf("contract proposal %q is invalid", proposal.QuestionID)
		}
		if err := validateAgentFinding(packet, proposal.QuestionID, []string{proposal.Analysis}, proposal.Citations); err != nil {
			return err
		}
		key := proposal.QuestionID + "\x00" + proposal.Analysis
		if _, exists := seen[key]; exists {
			return fmt.Errorf("contract bundle duplicates %s %s", proposal.QuestionID, proposal.Analysis)
		}
		seen[key] = struct{}{}
		citationCount += len(proposal.Citations)
	}
	if citationCount > packet.Budget.MaxCitations {
		return fmt.Errorf("contract bundle exceeds citation budget")
	}
	wantID, err := agentContentID("bundle", bundle)
	if err != nil {
		return err
	}
	if bundle.ID != wantID {
		return fmt.Errorf("contract bundle ID does not match its content")
	}
	return nil
}

func analysisWitnessAllowed(analysis, witnessType string) bool {
	switch analysis {
	case "cancellation":
		return witnessType == "cancellation-trace"
	case "dispatch", "callback-effects", "runtime-effects", "setter-dispatch", "submenu-dispatch":
		return witnessType == "event-delivery"
	case "capability-gate", "selector-inputs":
		return witnessType == "terminal-trace"
	case "external-edit-preservation", "field-granularity", "persistence":
		return witnessType == "persistence-roundtrip"
	case "production-reachability":
		return witnessType == "extension-realization"
	case "argument-flow", "array-replacement", "call-order", "current-state", "error-retention", "error-state", "error-surfacing", "load-order", "lock-serialization", "nested-merge", "omitted-value", "precedence", "project-state-reset", "trust-boundary", "value-domain", "write-drain":
		return witnessType == "state-transition"
	default:
		return false
	}
}

func BindTranslatorBundle(packet *AgentWorkPacket, translations []TranslationProposal) (*TranslatorBundle, error) {
	bundle := &TranslatorBundle{Role: TranslatorRole, PacketID: packet.ID, Translations: slices.Clone(translations)}
	var err error
	bundle.ID, err = agentContentID("bundle", bundle)
	if err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return bundle, nil
}

func DecodeTranslatorBundle(reader io.Reader, packet *AgentWorkPacket) (*TranslatorBundle, error) {
	var bundle TranslatorBundle
	if err := decodeAgentJSON(reader, &bundle, "translator bundle"); err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (bundle *TranslatorBundle) Validate(packet *AgentWorkPacket) error {
	if bundle == nil || packet == nil || packet.Role != TranslatorRole || bundle.Role != TranslatorRole || bundle.PacketID != packet.ID || len(bundle.Translations) == 0 || len(bundle.Translations) > packet.Budget.MaxFindings {
		return fmt.Errorf("translator bundle has incomplete identity or translations")
	}
	if !slices.IsSortedFunc(bundle.Translations, func(left, right TranslationProposal) int {
		return strings.Compare(left.QuestionID+"\x00"+left.Analysis, right.QuestionID+"\x00"+right.Analysis)
	}) {
		return fmt.Errorf("translator bundle translations are not sorted")
	}
	testWritable := make(map[string]struct{}, len(packet.TestPaths))
	for _, path := range packet.TestPaths {
		testWritable[path] = struct{}{}
	}
	seen := make(map[string]struct{}, len(bundle.Translations))
	citationCount := 0
	for _, translation := range bundle.Translations {
		if translation.Rationale == "" || len(translation.ProductionEdits) == 0 || len(translation.TestEdits) == 0 || translation.EvidencePlan == "" || len(translation.MutationOperators) == 0 || !sortedUniqueStrings(translation.MutationOperators) {
			return fmt.Errorf("translator proposal %q is incomplete", translation.QuestionID)
		}
		productionWritable := make(map[string]struct{})
		for _, citation := range translation.Citations {
			if citation.Side == "target" {
				productionWritable[citation.Path] = struct{}{}
			}
		}
		editPaths := make(map[string]struct{}, len(translation.ProductionEdits)+len(translation.TestEdits))
		for _, edit := range translation.ProductionEdits {
			if _, ok := productionWritable[edit.Path]; !ok {
				return fmt.Errorf("translator proposal %s writes outside its production paths", translation.QuestionID)
			}
			if _, duplicate := editPaths[edit.Path]; duplicate || !validTranslationEdit(edit) {
				return fmt.Errorf("translator proposal %s has an invalid or duplicate edit", translation.QuestionID)
			}
			editPaths[edit.Path] = struct{}{}
		}
		for _, edit := range translation.TestEdits {
			if _, ok := testWritable[edit.Path]; !ok {
				return fmt.Errorf("translator proposal %s writes outside its test paths", translation.QuestionID)
			}
			if _, duplicate := editPaths[edit.Path]; duplicate || !validTranslationEdit(edit) {
				return fmt.Errorf("translator proposal %s has an invalid or duplicate edit", translation.QuestionID)
			}
			editPaths[edit.Path] = struct{}{}
		}
		for _, operator := range translation.MutationOperators {
			if !mutationOperatorAllowed(translation.Analysis, operator) {
				return fmt.Errorf("translator proposal %s mutation %s does not challenge %s", translation.QuestionID, operator, translation.Analysis)
			}
		}
		if err := validateAgentFinding(packet, translation.QuestionID, []string{translation.Analysis}, translation.Citations); err != nil {
			return err
		}
		key := translation.QuestionID + "\x00" + translation.Analysis
		if _, exists := seen[key]; exists {
			return fmt.Errorf("translator bundle duplicates %s %s", translation.QuestionID, translation.Analysis)
		}
		seen[key] = struct{}{}
		citationCount += len(translation.Citations)
	}
	if citationCount > packet.Budget.MaxCitations {
		return fmt.Errorf("translator bundle exceeds citation budget")
	}
	wantID, err := agentContentID("bundle", bundle)
	if err != nil {
		return err
	}
	if bundle.ID != wantID {
		return fmt.Errorf("translator bundle ID does not match its content")
	}
	return nil
}

func validTranslationEdit(edit TranslationEdit) bool {
	clean := filepath.ToSlash(filepath.Clean(edit.Path))
	return edit.Path != "" && edit.Path == strings.TrimSpace(edit.Path) && !strings.ContainsRune(edit.Path, '\x00') && !filepath.IsAbs(edit.Path) && filepath.IsLocal(edit.Path) && clean == edit.Path && validAgentHash(edit.OriginalHash) && edit.Replacement != "" && !strings.ContainsRune(edit.Replacement, '\x00')
}

func validAgentHash(value string) bool {
	digest, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(digest) != 64 {
		return false
	}
	for _, character := range digest {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func mutationOperatorAllowed(analysis, operator string) bool {
	switch analysis {
	case "cancellation":
		return oneOfString(operator, "remove-cancel", "allow-post-cancel-effect", "leak-owner")
	case "call-order", "load-order", "precedence", "write-drain":
		return oneOfString(operator, "swap-order", "drop-step", "duplicate-step")
	case "argument-flow", "array-replacement", "current-state", "field-granularity", "nested-merge", "omitted-value", "project-state-reset", "value-domain":
		return oneOfString(operator, "change-value", "drop-branch", "invert-guard")
	case "dispatch", "callback-effects", "runtime-effects", "setter-dispatch", "submenu-dispatch", "selector-inputs":
		return oneOfString(operator, "drop-dispatch", "wrong-recipient", "invert-guard")
	case "error-retention", "error-state", "error-surfacing":
		return oneOfString(operator, "drop-error", "change-error", "swallow-error")
	case "external-edit-preservation", "persistence":
		return oneOfString(operator, "drop-write", "change-roundtrip", "overwrite-external-edit")
	case "capability-gate", "lock-serialization", "production-reachability", "trust-boundary":
		return oneOfString(operator, "invert-guard", "remove-lock", "bypass-boundary")
	default:
		return false
	}
}

func BindAdversaryBundle(packet *AgentWorkPacket, findings []AdversaryFinding) (*AdversaryBundle, error) {
	bundle := &AdversaryBundle{Role: AdversaryRole, PacketID: packet.ID, Findings: slices.Clone(findings)}
	var err error
	bundle.ID, err = agentContentID("bundle", bundle)
	if err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return bundle, nil
}

func DecodeAdversaryBundle(reader io.Reader, packet *AgentWorkPacket) (*AdversaryBundle, error) {
	var bundle AdversaryBundle
	if err := decodeAgentJSON(reader, &bundle, "adversary bundle"); err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (bundle *AdversaryBundle) Validate(packet *AgentWorkPacket) error {
	if bundle == nil || packet == nil || packet.Role != AdversaryRole || bundle.Role != AdversaryRole || bundle.PacketID != packet.ID || len(bundle.Findings) == 0 || len(bundle.Findings) > packet.Budget.MaxFindings {
		return fmt.Errorf("adversary bundle has incomplete identity or findings")
	}
	if !slices.IsSortedFunc(bundle.Findings, func(left, right AdversaryFinding) int {
		return strings.Compare(left.QuestionID+"\x00"+left.Fault, right.QuestionID+"\x00"+right.Fault)
	}) {
		return fmt.Errorf("adversary bundle findings are not sorted")
	}
	seen := make(map[string]struct{}, len(bundle.Findings))
	citationCount := 0
	for _, finding := range bundle.Findings {
		if !oneOfString(finding.Fault, "mismatch", "uncovered-case", "stale-support", "ambiguous", "blocked") || finding.Rationale == "" {
			return fmt.Errorf("adversary finding %q is invalid", finding.QuestionID)
		}
		if err := validateAgentFinding(packet, finding.QuestionID, finding.Analyses, finding.Citations); err != nil {
			return err
		}
		key := finding.QuestionID + "\x00" + finding.Fault
		if _, exists := seen[key]; exists {
			return fmt.Errorf("adversary bundle duplicates %s %s", finding.QuestionID, finding.Fault)
		}
		seen[key] = struct{}{}
		citationCount += len(finding.Citations)
	}
	if citationCount > packet.Budget.MaxCitations {
		return fmt.Errorf("adversary bundle exceeds citation budget")
	}
	wantID, err := agentContentID("bundle", bundle)
	if err != nil {
		return err
	}
	if bundle.ID != wantID {
		return fmt.Errorf("adversary bundle ID does not match its content")
	}
	return nil
}

func decodeAgentJSON[T any](reader io.Reader, destination *T, name string) error {
	data, err := io.ReadAll(io.LimitReader(reader, maxAgentContentBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if len(data) > maxAgentContentBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maxAgentContentBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return requireJSONEOF(decoder)
}

func validateAgentFinding(packet *AgentWorkPacket, questionID string, analyses []string, citations []AlignmentCitation) error {
	var question *AgentWorkQuestion
	for index := range packet.Questions {
		if packet.Questions[index].ID == questionID {
			question = &packet.Questions[index]
			break
		}
	}
	if question == nil || len(analyses) == 0 || !sortedUniqueStrings(analyses) || len(citations) < 2 || len(citations) > packet.Budget.MaxCitations {
		return fmt.Errorf("agent finding %q has incomplete scope", questionID)
	}
	for _, analysis := range analyses {
		if !slices.Contains(question.RequiredAnalyses, analysis) {
			return fmt.Errorf("agent finding %s analysis %s is outside scope", questionID, analysis)
		}
	}
	sides := make(map[string]struct{}, 2)
	for _, citation := range citations {
		if !slices.Contains(question.Citations, citation) {
			return fmt.Errorf("agent finding %s has an unbound citation", questionID)
		}
		sides[citation.Side] = struct{}{}
	}
	if len(sides) != 2 {
		return fmt.Errorf("agent finding %s must cite source and target", questionID)
	}
	return nil
}

func agentContentID(prefix string, value AgentContent) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		return "", err
	}
	delete(object, "id")
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return prefix + ":" + strings.TrimPrefix(hashString(string(canonical)), "sha256:"), nil
}

func forbiddenActions() []string {
	return []string{
		"accept-mapping", "adjudicate-contradiction", "approve-divergence", "attest-evidence",
		"derive-verdict", "integrate-patch", "sign-evidence", "waive-behavior",
	}
}
