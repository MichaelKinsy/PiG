// Package porter exposes the single deterministic process contract used by the
// direct Porter adapter and the Pig Porter extension.
package porter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/parity/closure"
	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

const (
	maxRequestBytes  = 1 << 20
	maxResponseBytes = 16 << 20
)

const (
	OperationStatus            = "status"
	OperationMappingReview     = "mapping-review"
	OperationWhyOpen           = "why-open"
	OperationFrontier          = "frontier"
	OperationExplain           = "explain"
	OperationVerify            = "verify"
	OperationInventory         = "inventory"
	OperationPlan              = "plan"
	OperationWorkPacket        = "work-packet"
	OperationPrompt            = "prompt"
	OperationSubmitBundle      = "submit-bundle"
	OperationRequestEvidence   = "request-evidence"
	OperationRequestMutation   = "request-mutation"
	OperationMutationReadiness = "mutation-readiness"
	OperationPlanUnits         = "plan-units"
	OperationGrantLease        = "grant-lease"
	OperationIntegrate         = "integrate"
	OperationCampaignStep      = "campaign-step"
	OperationReport            = "report"
	OperationDispositionPlan   = "disposition-plan"
)

// Request is the current unversioned Porter process request. Root and database
// identify the same closure snapshot for every adapter; an empty Root means
// the adapter's current working directory.
type Request struct {
	Operation       string `json:"operation"`
	Root            string `json:"root,omitempty"`
	Database        string `json:"database,omitempty"`
	RecordID        string `json:"recordId,omitempty"`
	UpstreamVersion string `json:"upstreamVersion,omitempty"`
	TargetCommit    string `json:"targetCommit,omitempty"`
	Node            string `json:"node,omitempty"`
	Role            string `json:"role,omitempty"`
	SnapshotID      string `json:"snapshotId,omitempty"`
	PacketPath      string `json:"packetPath,omitempty"`
	BundlePath      string `json:"bundlePath,omitempty"`
	OutputDirectory string `json:"outputDirectory,omitempty"`
	Dataset         string `json:"dataset,omitempty"`
	WorkUnitID      string `json:"workUnitId,omitempty"`
	Holder          string `json:"holder,omitempty"`
}

// Response is the deterministic result returned by every Porter adapter.
type Response struct {
	Operation string `json:"operation"`
	Output    string `json:"output"`
}

// Plan is the deterministic correspondence work plan shared by direct and
// extension adapters. It describes candidates and blockers; it does not
// accept mappings or derive closure verdicts.
type Plan struct {
	UpstreamVersion   string                        `json:"upstreamVersion"`
	TargetCommit      string                        `json:"targetCommit"`
	RuleID            string                        `json:"ruleId"`
	Source            correspondence.SourceIdentity `json:"source"`
	Target            correspondence.SourceIdentity `json:"target"`
	Status            string                        `json:"status"`
	MappingCandidates []correspondence.MappingFact  `json:"mappingCandidates"`
	Findings          []correspondence.Finding      `json:"findings"`
	QuestionIDs       []string                      `json:"questionIds"`
	UnresolvedEdges   []string                      `json:"unresolvedEdges"`
}

type requestField struct {
	name  string
	value string
}

// DecodeRequest decodes one strict JSON request from reader.
func DecodeRequest(reader io.Reader) (Request, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
	if err != nil {
		return Request{}, fmt.Errorf("read Porter request: %w", err)
	}
	if len(data) > maxRequestBytes {
		return Request{}, fmt.Errorf("Porter request exceeds %d bytes", maxRequestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("decode Porter request: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Request{}, fmt.Errorf("Porter request contains trailing JSON")
		}
		return Request{}, fmt.Errorf("decode Porter request trailing data: %w", err)
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

// Validate checks the closed operation set and operation-specific identity.
func (request Request) Validate() error {
	if err := validatePathField("Porter root", request.Root); err != nil {
		return err
	}
	if err := validatePathField("Porter database", request.Database); err != nil {
		return err
	}
	if request.Database != "" && (filepath.IsAbs(request.Database) || !filepath.IsLocal(request.Database)) {
		return fmt.Errorf("Porter database must be repository-relative")
	}
	if request.WorkUnitID != "" && request.Operation != OperationGrantLease {
		return fmt.Errorf("Porter operation %q does not accept workUnitId", request.Operation)
	}
	if request.Holder != "" && request.Operation != OperationGrantLease && request.Operation != OperationCampaignStep {
		return fmt.Errorf("Porter operation %q does not accept holder", request.Operation)
	}
	switch request.Operation {
	case OperationDispositionPlan:
		if request.RecordID != "" {
			return fmt.Errorf("Porter operation %q does not accept recordId", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"database", request.Database}, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationStatus, OperationMappingReview, OperationWhyOpen, OperationVerify, OperationMutationReadiness, OperationPlanUnits:
		if request.RecordID != "" {
			return fmt.Errorf("Porter operation %q does not accept recordId", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationFrontier, OperationExplain:
		if request.RecordID == "" {
			return fmt.Errorf("Porter operation %q requires recordId", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationRequestEvidence:
		if !strings.HasPrefix(request.RecordID, "request:") {
			return fmt.Errorf("Porter operation %q requires an evidence request recordId", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationRequestMutation:
		if !strings.HasPrefix(request.RecordID, "mutation-request:") {
			return fmt.Errorf("Porter operation %q requires a mutation request recordId", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationInventory, OperationPlan:
		if err := request.validateCorrespondenceIdentity(); err != nil {
			return err
		}
		if err := rejectFields(request.Operation, requestField{"database", request.Database}, requestField{"recordId", request.RecordID}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationWorkPacket, OperationPrompt:
		if err := request.validateCorrespondenceIdentity(); err != nil {
			return err
		}
		if !validRole(request.Role) {
			return fmt.Errorf("Porter operation %q requires a supported role", request.Operation)
		}
		if request.Role == correspondence.TranslatorRole && request.Database == "" {
			return fmt.Errorf("Porter operation %q requires database for translator scope", request.Operation)
		}
		if !strings.HasPrefix(request.SnapshotID, "snapshot:") || request.SnapshotID == "snapshot:" {
			return fmt.Errorf("Porter operation %q requires snapshotId", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"recordId", request.RecordID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationSubmitBundle:
		if request.PacketPath == "" || request.BundlePath == "" {
			return fmt.Errorf("Porter operation %q requires packetPath and bundlePath", request.Operation)
		}
		if filepath.IsAbs(request.PacketPath) || !filepath.IsLocal(request.PacketPath) || filepath.IsAbs(request.BundlePath) || !filepath.IsLocal(request.BundlePath) {
			return fmt.Errorf("Porter packetPath and bundlePath must be repository-relative")
		}
		if request.OutputDirectory != "" && (filepath.IsAbs(request.OutputDirectory) || !filepath.IsLocal(request.OutputDirectory)) {
			return fmt.Errorf("Porter outputDirectory must be repository-relative")
		}
		if err := rejectFields(request.Operation, requestField{"database", request.Database}, requestField{"recordId", request.RecordID}, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationReport:
		if !slices.Contains(closure.ReportDatasets(), request.Dataset) {
			return fmt.Errorf("Porter report dataset %q is unsupported", request.Dataset)
		}
		if err := rejectFields(request.Operation, requestField{"recordId", request.RecordID}, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}); err != nil {
			return err
		}
	case OperationGrantLease:
		if request.WorkUnitID == "" || request.Holder == "" {
			return fmt.Errorf("Porter operation %q requires workUnitId and holder", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"recordId", request.RecordID}, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationIntegrate:
		if !strings.HasPrefix(request.RecordID, "lease:") || request.RecordID == "lease:" {
			return fmt.Errorf("Porter operation %q requires a lease recordId", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"upstreamVersion", request.UpstreamVersion}, requestField{"targetCommit", request.TargetCommit}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	case OperationCampaignStep:
		if err := request.validateCorrespondenceIdentity(); err != nil {
			return err
		}
		if request.Holder == "" {
			return fmt.Errorf("Porter operation %q requires holder", request.Operation)
		}
		if err := rejectFields(request.Operation, requestField{"recordId", request.RecordID}, requestField{"node", request.Node}, requestField{"role", request.Role}, requestField{"snapshotId", request.SnapshotID}, requestField{"packetPath", request.PacketPath}, requestField{"bundlePath", request.BundlePath}, requestField{"outputDirectory", request.OutputDirectory}, requestField{"dataset", request.Dataset}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Porter operation %q is unsupported", request.Operation)
	}
	for name, value := range map[string]string{
		"recordId": request.RecordID, "upstreamVersion": request.UpstreamVersion,
		"targetCommit": request.TargetCommit, "node": request.Node, "role": request.Role,
		"snapshotId": request.SnapshotID, "packetPath": request.PacketPath,
		"bundlePath": request.BundlePath, "outputDirectory": request.OutputDirectory,
		"dataset": request.Dataset, "workUnitId": request.WorkUnitID, "holder": request.Holder,
	} {
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("Porter %s contains NUL", name)
		}
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("Porter %s must not have surrounding whitespace", name)
		}
	}
	return nil
}

func rejectFields(operation string, fields ...requestField) error {
	for _, field := range fields {
		if field.value != "" {
			return fmt.Errorf("Porter operation %q does not accept %s", operation, field.name)
		}
	}
	return nil
}

func (request Request) validateCorrespondenceIdentity() error {
	if request.UpstreamVersion != coding.UpstreamVersion {
		return fmt.Errorf("Porter upstreamVersion %q does not match pinned %s", request.UpstreamVersion, coding.UpstreamVersion)
	}
	if !validCommit(request.TargetCommit) {
		return fmt.Errorf("Porter targetCommit must be a 40-character lowercase commit")
	}
	return nil
}

func validatePathField(name, value string) error {
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s contains NUL", name)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must not have surrounding whitespace", name)
	}
	return nil
}

func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, digit := range value {
		if !strings.ContainsRune("0123456789abcdef", digit) {
			return false
		}
	}
	return true
}

func validRole(role string) bool {
	return slices.Contains([]string{
		correspondence.AnalystRole,
		correspondence.AlignmentReviewerRole,
		correspondence.ContractSynthesizerRole,
		correspondence.TranslatorRole,
		correspondence.AdversaryRole,
	}, role)
}

// Execute runs one request against the disposable closure store or the
// correspondence compiler without changing canonical records. Both direct
// and extension adapters call this function.
func Execute(ctx context.Context, request Request) (Response, error) {
	if err := request.Validate(); err != nil {
		return Response{}, err
	}
	root := request.Root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return Response{}, fmt.Errorf("resolve Porter root: %w", err)
		}
	}
	var output []byte
	var err error
	switch request.Operation {
	case OperationStatus, OperationMappingReview, OperationWhyOpen, OperationFrontier, OperationExplain, OperationVerify, OperationRequestEvidence, OperationRequestMutation, OperationMutationReadiness, OperationReport, OperationPlanUnits, OperationGrantLease, OperationIntegrate, OperationCampaignStep:
		output, err = executeClosure(ctx, root, request)
	case OperationInventory:
		output, err = executeInventory(ctx, root, request)
	case OperationPlan:
		output, err = executePlan(ctx, root, request)
	case OperationWorkPacket:
		output, err = executeWorkPacket(ctx, root, request)
	case OperationPrompt:
		output, err = executePrompt(ctx, root, request)
	case OperationSubmitBundle:
		output, err = executeSubmitBundle(root, request)
	case OperationDispositionPlan:
		output, err = executeDispositionPlan(root)
	}
	if err != nil {
		return Response{}, err
	}
	if len(output) > maxResponseBytes {
		return Response{}, fmt.Errorf("Porter response exceeds %d bytes", maxResponseBytes)
	}
	return Response{Operation: request.Operation, Output: string(output)}, nil
}

func executeClosure(ctx context.Context, root string, request Request) ([]byte, error) {
	database := request.Database
	if database == "" {
		database = "tmp/closure/graph.db"
	}
	storePath, err := closure.SafeStorePath(root, database)
	if err != nil {
		return nil, err
	}
	switch request.Operation {
	case OperationStatus:
		return closure.ReadStatus(ctx, storePath)
	case OperationMappingReview:
		return closure.MappingReview(ctx, storePath)
	case OperationWhyOpen:
		return closure.WhyOpen(ctx, storePath)
	case OperationFrontier:
		return closure.Frontier(ctx, storePath, request.RecordID)
	case OperationExplain:
		return closure.Explain(ctx, storePath, request.RecordID)
	case OperationRequestEvidence:
		return closure.ReadEvidenceRequest(ctx, storePath, request.RecordID)
	case OperationRequestMutation:
		return closure.ReadMutationRequest(ctx, storePath, request.RecordID)
	case OperationMutationReadiness:
		return closure.MutationReadiness(ctx, storePath)
	case OperationPlanUnits:
		return closure.PlanUnits(ctx, storePath)
	case OperationGrantLease:
		return closure.GrantLeaseInStore(ctx, storePath, request.WorkUnitID, request.Holder)
	case OperationIntegrate:
		return closure.IntegrateLeaseInStore(ctx, storePath, request.RecordID)
	case OperationCampaignStep:
		manifest := closure.CampaignManifest{UpstreamVersion: request.UpstreamVersion, TargetCommit: request.TargetCommit, Holder: request.Holder}
		result, err := closure.CampaignStep(ctx, storePath, manifest)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case OperationReport:
		return closure.ReadReport(ctx, storePath, request.Dataset)
	case OperationVerify:
		if err := closure.VerifyStore(ctx, storePath); err != nil {
			return nil, err
		}
		return []byte("closure store: verified\n"), nil
	default:
		return nil, fmt.Errorf("unsupported closure operation %q", request.Operation)
	}
}

func executeInventory(ctx context.Context, root string, request Request) ([]byte, error) {
	source, target, err := extractCorrespondence(ctx, root, request)
	if err != nil {
		return nil, err
	}
	return marshalOutput(struct {
		Source *correspondence.Inventory `json:"source"`
		Target *correspondence.Inventory `json:"target"`
	}{Source: source, Target: target})
}

func executePlan(ctx context.Context, root string, request Request) ([]byte, error) {
	_, _, report, packet, err := buildCorrespondencePlan(ctx, root, request)
	if err != nil {
		return nil, err
	}
	plan := Plan{
		UpstreamVersion:   request.UpstreamVersion,
		TargetCommit:      request.TargetCommit,
		RuleID:            report.RuleID,
		Source:            report.Source,
		Target:            report.Target,
		Status:            "ready",
		MappingCandidates: slices.Clone(report.Mappings),
		Findings:          slices.Clone(report.Findings),
		QuestionIDs:       []string{},
		UnresolvedEdges:   []string{},
	}
	if len(report.Findings) > 0 {
		plan.Status = "blocked"
	} else {
		plan.QuestionIDs = correspondenceQuestionIDs(packet)
		plan.UnresolvedEdges, err = correspondenceUnresolvedEdges(packet, report)
		if err != nil {
			return nil, err
		}
		if len(plan.UnresolvedEdges) > 0 {
			plan.Status = "open"
		}
	}
	return marshalOutput(plan)
}

func executeWorkPacket(ctx context.Context, root string, request Request) ([]byte, error) {
	_, _, report, packet, err := buildCorrespondencePlan(ctx, root, request)
	if err != nil {
		return nil, err
	}
	if len(report.Findings) > 0 {
		return nil, fmt.Errorf("correspondence comparison found %d gap(s)", len(report.Findings))
	}
	unresolved, err := correspondenceUnresolvedEdges(packet, report)
	if err != nil {
		return nil, err
	}
	scope, err := porterAgentWorkScope(ctx, root, request, packet)
	if err != nil {
		return nil, err
	}
	workPacket, err := correspondence.BuildAgentWorkPacket(packet, request.Role, unresolved, scope)
	if err != nil {
		return nil, err
	}
	return marshalOutput(workPacket)
}

func executePrompt(ctx context.Context, root string, request Request) ([]byte, error) {
	_, _, report, packet, err := buildCorrespondencePlan(ctx, root, request)
	if err != nil {
		return nil, err
	}
	if len(report.Findings) > 0 {
		return nil, fmt.Errorf("correspondence comparison found %d gap(s)", len(report.Findings))
	}
	unresolved, err := correspondenceUnresolvedEdges(packet, report)
	if err != nil {
		return nil, err
	}
	scope, err := porterAgentWorkScope(ctx, root, request, packet)
	if err != nil {
		return nil, err
	}
	workPacket, err := correspondence.BuildAgentWorkPacket(packet, request.Role, unresolved, scope)
	if err != nil {
		return nil, err
	}
	prompt, err := correspondence.BuildAgentPrompt(workPacket)
	if err != nil {
		return nil, err
	}
	return marshalOutput(prompt)
}

func porterAgentWorkScope(ctx context.Context, root string, request Request, packet *correspondence.AlignmentWorkPacket) (correspondence.AgentWorkScope, error) {
	scope, err := correspondence.NewAgentWorkScope(packet, request.SnapshotID, []string{}, []string{}, []string{})
	if err != nil {
		return correspondence.AgentWorkScope{}, err
	}
	if request.Database == "" {
		return scope, nil
	}
	storePath, err := closure.SafeStorePath(root, request.Database)
	if err != nil {
		return correspondence.AgentWorkScope{}, err
	}
	canonical, err := closure.ReadAgentWorkScope(ctx, storePath, request.SnapshotID, scope.ObligationIDs)
	if err != nil {
		return correspondence.AgentWorkScope{}, err
	}
	return correspondence.NewAgentWorkScope(packet, request.SnapshotID, canonical.EvidenceIDs, canonical.FixturePaths, canonical.TestPaths)
}

func executeSubmitBundle(root string, request Request) ([]byte, error) {
	outputDirectory := request.OutputDirectory
	if outputDirectory == "" {
		outputDirectory = "parity/closuredata/proposals"
	}
	result, err := correspondence.SubmitAgentBundle(root, request.PacketPath, request.BundlePath, outputDirectory)
	if err != nil {
		return nil, err
	}
	return marshalOutput(result)
}

func buildCorrespondencePlan(ctx context.Context, root string, request Request) (*correspondence.Inventory, *correspondence.Inventory, *correspondence.Report, *correspondence.AlignmentWorkPacket, error) {
	source, target, err := extractCorrespondence(ctx, root, request)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	report, err := correspondence.Compare(source, target, correspondence.CompactionSettingsRules())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(report.Findings) > 0 {
		return source, target, report, nil, nil
	}
	packet, err := correspondence.BuildSettingsAlignmentPacket(source, target, report)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return source, target, report, packet, nil
}

func extractCorrespondence(ctx context.Context, root string, request Request) (*correspondence.Inventory, *correspondence.Inventory, error) {
	nodePath, err := resolveNode(root, request.Node)
	if err != nil {
		return nil, nil, err
	}
	source, err := correspondence.ExtractTypeScript(ctx, nodePath,
		filepath.Join(root, "parity/interface-extractor/src/extract-correspondence.mjs"),
		filepath.Join(root, ".upstream/current"), request.UpstreamVersion)
	if err != nil {
		return nil, nil, err
	}
	target, err := correspondence.ExtractGo(ctx, root, request.TargetCommit)
	if err != nil {
		return nil, nil, err
	}
	return source, target, nil
}

func resolveNode(root, configured string) (string, error) {
	if configured != "" {
		if strings.ContainsRune(configured, '\x00') {
			return "", fmt.Errorf("Node executable contains NUL")
		}
		if filepath.Base(configured) == configured {
			path, err := exec.LookPath(configured)
			if err != nil {
				return "", fmt.Errorf("resolve Node executable: %w", err)
			}
			return path, nil
		}
		path := configured
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("inspect Node executable: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("Node executable is not a regular file")
		}
		return path, nil
	}
	path, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("resolve Node executable: %w", err)
	}
	return path, nil
}

func correspondenceQuestionIDs(packet *correspondence.AlignmentWorkPacket) []string {
	if packet == nil {
		return []string{}
	}
	ids := make([]string, 0, len(packet.Questions)+len(packet.Functions))
	for _, question := range packet.Questions {
		ids = append(ids, question.ID)
	}
	for _, question := range packet.Functions {
		ids = append(ids, question.ID)
	}
	slices.Sort(ids)
	return ids
}

func correspondenceUnresolvedEdges(packet *correspondence.AlignmentWorkPacket, report *correspondence.Report) ([]string, error) {
	if packet == nil || report == nil {
		return nil, fmt.Errorf("correspondence plan packet and report are required")
	}
	mapped := make(map[string]struct{}, len(report.Mappings))
	for _, mapping := range report.Mappings {
		mapped[mapping.SourceID] = struct{}{}
	}
	unresolved, err := correspondence.RuntimeEffectCandidates(packet)
	if err != nil {
		return nil, err
	}
	for _, question := range packet.Functions {
		if _, ok := mapped[question.Source.ID]; !ok {
			unresolved = append(unresolved, question.ID)
		}
	}
	slices.Sort(unresolved)
	return slices.Compact(unresolved), nil
}

func marshalOutput(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Porter output: %w", err)
	}
	return append(encoded, '\n'), nil
}
